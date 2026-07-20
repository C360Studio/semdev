## Context

semdev's arc already consumes only *normalized* facts (`run.issue.ref`,
`run.change.approved`, `human.opt.signal`) — never a host payload. But the
conversation *plumbing* beneath those facts is fused to GitHub in three places,
all living inside the `forge-io` code-host capability:

- **POST** — `parkpost.Commenter.CreateComment(ctx, owner, repo, number, body)`
  (`internal/intake/parkpost.go`), resolved from `run.issue.ref` via `SplitRef`.
- **READ/normalize** — `NormalizeComment(githubwebhook.CommentEvent)`
  (`internal/intake/normalize.go`) → `CommentSignal`, fed by a durable JetStream
  consumer on `github.event.>` (the semdev-owned webhook receiver's output).
- **THREAD COORDINATE** — `CommentSignal.IssueRef = "owner/repo#number"`, a
  GitHub-flavored handle that doubles as the code-host work coordinate.

`forge-io` conflates two separable adapters onto GitHub's single object (comments
live *on* the issue/PR): a **code-host** adapter (clone, PR, issue intake, issue
content) and a **conversation-channel** adapter (post/read messages on a thread).
This change splits the conversation half into its own capability behind a port so
GitHub stays v1 while other channels can compose later — without re-baking the
GitHub shape into every future message lane.

Today's live approval path: webhook → `github.event.comment` → durable consumer →
`handleCommentEvent` → `NormalizeComment` → `Authorize` → `approval-adapter` stamps
`run.change.approved`. `human.opt.signal` is the *declared* reply fact (writer
`comment-adapter`, cap `forge-io`), reserved for `ask_human` replies. `parkpost`
posts `run.awaiting.human` messages as comments. Foundation (self-target clone +
`semdev launch`) is orthogonal and untouched.

## Goals / Non-Goals

**Goals:**
- A channel-neutral `ConversationChannel` port with an opaque `ThreadRef`; GitHub
  the sole v1 impl. Adding a channel later touches only a new impl, no arc rule.
- Dissolve all three coupling points: internal message flow carries a neutral
  `Message`, never `githubwebhook.CommentEvent`; posting and thread resolution go
  through the port.
- Pull-first: a poll-based READ is the default transport; the webhook is an
  optional accelerator, never required.
- Byte-identical arc behavior: the same facts, the same rules, the same journeys —
  only the adapter beneath changes (the seam's whole point).

**Non-Goals:**
- NL intent classification (Phase 2 `nl-conversation-intent`) — the `/semdev approve`
  exact-command match is carried over UNCHANGED.
- Any non-GitHub impl (Slack/email/Discord) — later.
- The `workRef ↔ threadRef` binding fact — GitHub is the identity case
  (`threadRef == workRef`), so no new predicate; the binding lands with the first
  channel that separates them.
- Running poll AND webhook concurrently (cross-transport dedup) — see D6.

## Decisions

### D1 — `ThreadRef` is an opaque, host-neutral string the impl resolves
`type ThreadRef string`. The arc/component never parses it; only the impl does.
For GitHub v1, `ThreadRef` is the host-neutral `owner/repo#number` — i.e.
`ResolveThread(workRef) == ThreadRef(workRef)` (**identity**), because comments
live on the work object. A future Slack impl resolves a work coordinate to a
`slack:CHANNEL/ts` thread (non-identity → the binding fact, out of scope here).
*Alternative rejected:* a structured `{host, owner, repo, number}` — leaks the
GitHub shape back into the port; the whole point is opacity.

### D2 — `Message` is the neutral, attributed, ordered, dedup-able unit
```
type Message struct {
    ID     string    // channel-native id — the cursor/dedup key (GitHub comment id)
    Author string    // channel-native author handle (github login)
    Body   string
    At     time.Time // channel-native timestamp — the ordering key
}
```
Generalizes `CommentSignal` (`Event{Actor,Owner,Repo,AuthoredText}` + `IssueRef` +
`Attributable`). Attribution (sender == author) is computed by the impl and carried
as the normalized actor identity; the admission `Event` (actor/repo scope) is built
from `Message.Author` + the thread's resolved code-host scope, so the SHARED
`Authorize` gate is unchanged.

### D3 — the `ConversationChannel` port
```
type ConversationChannel interface {
    Post(ctx, thread ThreadRef, body string) error
    Read(ctx, thread ThreadRef, cursor string) (msgs []Message, next string, err error)
    ResolveThread(ctx, workRef string) (ThreadRef, error)
}
```
`Read` returns messages strictly after `cursor` and the new cursor (last message
id). Post/Read fail closed; a transport blip on Post is retried bounded (never
blocks the durable park fact), exactly as `parkpost` does today.

### D4 — package placement: `internal/forge/conversation`
Beside `internal/forge/github` and `internal/forge/clone`. Holds the port, the
neutral `Message`/`ThreadRef`, the GitHub impl (over `github.Client` —
`CreateComment` for Post, `ListComments` for Read), and the poll transport. The
GitHub webhook flatten stays in `internal/forge/githubwebhook` but is consumed
*inside* the GitHub impl (or the optional receiver), never on the neutral path.
*G1 note:* no new primitive — this re-homes existing adapters behind a Go port and
reuses the existing `provisionsandbox.Sources`-style seam pattern; the framework
alignment is "one component owns the fact, adapters plug in behind it."

### D5 — one writer, impls behind it (the G5 pivot)
`human.opt.signal`'s writer moves `comment-adapter` → `conversation-channel` (the
*component*), and its capability tag `forge-io` → `conversation-channel`. The GitHub
impl is not a writer — the component is. This is what makes N channels G5-legal:
one writer per fact, adapters are internal. Named in the `conversation-channel`
spec delta (G9). `run.change.approved` (writer `approval-adapter`) is untouched —
the approval adapter still owns it; it just reads a `Message` instead of a
`CommentSignal`.

### D6 — pull-first poll is default; webhook is an optional, mutually-exclusive accelerator
Default transport: the conversation-channel component polls each ACTIVE run's
thread via `Read(thread, cursor)` on an interval, dedups by `Message.ID`, and feeds
new messages to the same processing (approval command match + `human.opt.signal`).
Cursor = last-seen `Message.ID` per thread. The webhook receiver stays available
but is **config-selected XOR** the poller — NOT both at once — so there is no
cross-transport double-delivery to reconcile (that reconciliation is exactly the
pull-first split-tripwire; keeping them exclusive keeps the transport IN this
change). *Alternative rejected:* both concurrently with dedup — pulls the
cross-transport-dedup tripwire in and balloons the change.

### D7 — `forge-io` narrows; the conversation requirement relocates
`forge-io` spec delta: **REMOVE** "Human communication rides the seam" and the
"only the authorizing requester steers a run's human gate" scenario. `forge-io`
keeps code-host only: issue intake, PR delivery, source clone, issue content. The
`conversation-channel` spec **ADDS** the relocated requirement, restated
channel-neutrally (Post/Read/ResolveThread, `ThreadRef`, the message-fact family,
pull-first transport, GitHub v1 impl). The admission `Authorize` gate is referenced
by both capabilities but has ONE implementation (shared, unchanged).

### D8 — component boundary: extract a `conversation-channel` component
The `issue-intake` component today owns both webhook halves + comment approval +
park-post. Extract the conversation half (comment read/post, approval-from-message,
park-post) into a `conversation-channel` component/package; `issue-intake` stays
the code-host issue front door (issue events → run mint). The two components share
the `Authorize` core. *This is the cleanest map of the two-adapter vision; see Open
Questions for the lighter alternative if the extraction proves too invasive for one
change.*

## Risks / Trade-offs

- [The neutral-`Message` carve touches the approval + park + receiver paths at once]
  → land it behind the SAME facts with the webhook/park/approval journeys as the
  regression guard (they must pass byte-for-byte); add conversation-channel unit
  pins for Post/Read/cursor/dedup before rewiring.
- [Poll cost/latency — an LLM-free poll per active thread per interval] → Phase 1
  has no NL read, so a poll is a cheap `ListComments` + an id compare; gate on
  new-ids-only. If cursor durability or cost-gating grows beyond a last-seen marker,
  that is the **split-tripwire** → carve `pull-first-transport` out ahead of the seam.
- [Cursor loss on restart → reprocessing a comment] → dedup by `Message.ID` is
  idempotent (re-approving is a no-op; a repeated `human.opt.signal` is the same
  fact); a lost cursor at worst re-reads a bounded window, never double-acts.
- [Two components now vs one] → more surface, but it is the vision's boundary; the
  lighter alternative (one component, port behind it) is the fallback (Open Q).

## Migration Plan

1. Add `internal/forge/conversation`: port + neutral types + GitHub impl + poll
   transport, with unit pins (Post→CreateComment, Read→ListComments+cursor+dedup,
   ResolveThread identity, no-argv token reuse).
2. Reassign the `human.opt.signal` writer/cap in `internal/vocab` + the G5 writer
   census pin.
3. Rewire `parkpost` (Commenter → `Post`), `approval` (CommentSignal → `Message`,
   fed by the poller), and the component boundary (D8) — arc facts unchanged.
4. Relocate the spec requirement (forge-io REMOVE, conversation-channel ADD).
5. Green the existing webhook/park/approval journeys byte-for-byte (regression
   guard) + a new poll-transport journey (a `/semdev approve` delivered by POLL,
   no webhook) proving pull-first end-to-end. No paid tokens.
Rollback: the change is a refactor behind stable facts; revert restores the
webhook-fed path.

## Open Questions (RESOLVED — user, 2026-07-20)

- **OQ1 — component split → RESOLVED: EXTRACT (D8).** A dedicated
  `conversation-channel` component; `issue-intake` narrows to the pure code-host
  issue front door. The vision's clean two-adapter boundary, accepting the
  consumer/config/registry churn — it is the debt-stop this phase exists for.
- **OQ2 — cursor durability → RESOLVED: a per-thread fact on the run entity**
  (`conversation.thread.cursor`, no new store; G1). If fan-out over many threads
  later needs its own index, that is the split-tripwire.
- **OQ3 — webhook receiver → RESOLVED: KEEP it in-tree**, config-selected XOR the
  poller (D6); the poll transport is the default.
