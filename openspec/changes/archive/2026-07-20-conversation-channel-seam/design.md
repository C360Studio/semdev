## Context

semdev's arc already consumes only *normalized* facts (`run.issue.ref`,
`run.change.approved`, `human.opt.signal`) — never a host payload. But the
conversation *plumbing* beneath those facts is fused to GitHub in three places,
all inside the `forge-io` code-host capability:

- **POST** — `parkpost.Commenter.CreateComment(ctx, owner, repo, number, body)`
  (`internal/intake/parkpost.go`), resolved from `run.issue.ref` via `SplitRef`.
- **READ/normalize** — `NormalizeComment(githubwebhook.CommentEvent)` → `CommentSignal`
  (`internal/intake/normalize.go`), fed by a durable JetStream consumer on
  `github.event.>` (the semdev-owned webhook receiver's output).
- **THREAD COORDINATE** — `CommentSignal.IssueRef = "owner/repo#number"`.

`forge-io` conflates a **code-host** adapter (clone, PR, issue intake, issue content)
with a **conversation-channel** adapter (post/read messages on a thread) because
GitHub fuses them onto one object. This change splits the conversation half into its
own capability behind a port so GitHub stays v1 while other channels compose later.

Two hard facts from the pre-impl review shape the scope:
- The webhook **receiver is not comment-specific**: `component.go` `handleWebhook`
  flattens BOTH `issues`→`github.event.issue` AND `issue_comment`→`github.event.comment`
  onto one stream; GitHub delivers all event types to one URL. So "webhook receiver"
  and "comment transport" are not the same toggle.
- `human.opt.signal` has **no writer and no reader today** — it is a declared vocab
  entry only (the ask_human-reply/resume lane is unimplemented). The live human lane
  is the approval path: webhook → `github.event.comment` → consumer → `Authorize` →
  `approval-adapter` stamps `run.change.approved` → `run-lifecycle/02` resumes.

These two facts are why the **poll transport is split out** (below): it cannot ride
this carve's byte-identical guard.

## Goals / Non-Goals

**Goals:**
- A channel-neutral `ConversationChannel` port (`Post`, `ResolveThread`) with an
  opaque `ThreadRef` and a neutral `Message`; GitHub the sole v1 impl.
- Dissolve the three coupling points AT THE SEAM: the arc/component boundary handles
  a neutral `Message`, never `githubwebhook.CommentEvent`; posting and thread
  resolution go through the port.
- Extract a `conversation-channel` component; narrow `issue-intake` to code-host.
- **Byte-identical arc behavior AND byte-identical transport**: same facts, same
  rules, same webhook-fed path, same journeys — only the code structure beneath moves.

**Non-Goals (this change):**
- The **pull-first poll transport** — split into `pull-first-transport` (see below).
  This change keeps the EXISTING webhook-fed transport unchanged. The port therefore
  exposes NO `Read(cursor)` verb yet (no latent code); `pull-first-transport` adds it.
- NL intent classification (Phase 2) — the `/semdev approve` exact-command match is
  carried over UNCHANGED.
- Non-GitHub impls; the `workRef↔threadRef` binding fact (GitHub = identity).
- Wiring the `human.opt.signal` write/resume lane (it stays unimplemented; only the
  declared writer is reassigned).

## Decisions

### D1 — `ThreadRef` is an opaque, host-neutral string the impl resolves
`type ThreadRef string`; only the impl parses it. For GitHub v1, `ThreadRef` is the
host-neutral `owner/repo#number`, so `ResolveThread(workRef) == ThreadRef(workRef)`
(**identity** — comments live on the work object). A future Slack impl resolves to a
`slack:channel/ts` thread (non-identity → the binding fact, out of scope).

### D2 — `Message` is the neutral unit; attribution is contained in the impl
```
type Message struct { ID, Author, Body string; At time.Time }
```
Generalizes `CommentSignal`. On the webhook path, the GitHub impl's normalize keeps
today's `sender == author` attribution guard (a webhook carries two identities) and
emits the attributed `Message`. The admission `Event{Actor, Owner, Repo, …}` for the
shared `Authorize` gate is built from the attributed `Message` + the thread's resolved
code-host scope — so `Authorize` is unchanged. (The poll path has a single author and
no separate sender — that transport's authorization principal is `Message.Author`
directly; specified in `pull-first-transport`, not here.)

### D3 — the `ConversationChannel` port (this change: two verbs)
```
type ConversationChannel interface {
    Post(ctx, thread ThreadRef, body string) error
    ResolveThread(ctx, workRef string) (ThreadRef, error)
}
```
`Post` fails closed; a transport blip is retried bounded and NEVER blocks the durable
park fact (exactly as `parkpost` does today). The `Read(thread, cursor) → []Message`
verb is ADDED by `pull-first-transport`; adding it now would be latent code (the
webhook transport is push-fed, it never calls Read).

### D4 — package placement: `internal/forge/conversation`
Beside `internal/forge/github` and `internal/forge/clone`. Holds the port, the neutral
`Message`/`ThreadRef`, and the GitHub impl (`Post`→`CreateComment`; the webhook
`CommentEvent`→`Message` normalize CONTAINED here). *G1 note:* no new primitive — this
re-homes existing adapters behind a Go port; the framework alignment is "one component
owns the fact, adapters plug in behind it."

### D5 — one writer, impls behind it (the G5 pivot); the writer names the code
`human.opt.signal`'s writer moves `comment-adapter` → **`conversation-adapter`** (the
writer names the stamping adapter, matching the `admission-check`/`route-mirror`
convention — L-2), and its capability tag `forge-io` → `conversation-channel`. Channel
impls are NOT writers — the one adapter is. This makes N channels G5-legal.
**`human.opt.signal` is a paper reassignment** (no current writer/reader — M-3), so
there is no two-writer hazard. `run.change.approved` stays written by `approval-adapter`
(read by `run-lifecycle/02`), but its **capability tag moves `forge-io` →
`conversation-channel`** to match where its production is now spec'd (G10 census — M-2).

### D6 — the transport is UNCHANGED (webhook-fed); the split is deferred
This change keeps the existing webhook receiver + the `github.event.comment` durable
consumer exactly as today, re-homed into the `conversation-channel` component and
normalized to `Message`. There is NO poll, NO cursor, NO transport XOR here. The
pull-first poll transport — and the receiver-ownership question it raises (B-1: the
receiver serves issues AND comments) — is `pull-first-transport`'s to solve, with a
deployment-level ownership model and full fail-closed proof.

### D7 — `forge-io` narrows; the conversation requirement relocates
`forge-io` delta: **REMOVE** "Human communication rides the seam"; **MODIFY** "Intake
is gated to authorized, opted-in actors" to drop the steering scenario. `conversation-channel`
**ADDs** the relocated requirement, restated channel-neutrally. The relocated steering
authorization says **"an authorized actor"** (push-capable collaborator or allowlisted)
— NOT "the run's authorized requester," which the code does not enforce (`approval.go`
`Authorize` checks allowlist/push-capability only; the stronger claim was a pre-existing
forge-io over-claim — M-1; do not re-entrench it). The shared `Authorize` gate is one
pure-function implementation referenced by both caps (not a G5 writer concern).

### D8 — component extraction; the shared admission surface
Extract a `conversation-channel` component owning: comment `Post`, approval-from-`Message`
(the `/semdev approve` command match UNCHANGED), and park-post (the `user.response.>`
USER-stream consumer moves here too). `issue-intake` keeps the code-host issue front
door (`github.event.issue` → run mint) + the webhook receiver (which still flattens
BOTH event types — B-1: comment events continue to reach the conversation component's
consumer, unchanged). **Shared surface to extract into `internal/intake/admission`**
(both components import): `Authorize`, `Config`, `PermissionChecker`, `SplitRef`,
`RunResolver`/`ResolveRunByRef`, `natsEntityFetcher`, and the `Event` type.
`internal/launch` imports (`SplitRef`, `CoordinatorTask`, `NewRunResolver`,
`FrontDoorSubject`, `Intake`) update to the new homes. Both binaries must register the
new component (the half-wired-in-one-binary class).

## Risks / Trade-offs

- [The carve touches approval + park + the component boundary at once] → land it behind
  the SAME facts with the webhook/park/approval journeys as the byte-for-byte regression
  guard; add conversation unit pins (Post→CreateComment, ResolveThread identity,
  CommentEvent→Message normalize) before rewiring.
- [`internal/intake` restructure ripples into `internal/launch`] → the shared
  `admission` sub-package (D8) keeps the launch imports stable; a parity pin proves both
  binaries still register every component.
- [Capability-tag moves (`human.opt.signal`, `run.change.approved`)] → a G10 census pin
  keeps the vocab/architecture mapping honest.

## Migration Plan

1. Add `internal/forge/conversation`: port (Post/ResolveThread) + neutral types + GitHub
   impl (Post→CreateComment; CommentEvent→Message normalize contained), with unit pins.
2. Extract `internal/intake/admission` (shared Authorize/SplitRef/RunResolver/…); update
   `internal/launch` imports; parity pin.
3. Reassign `human.opt.signal` writer/cap + move `run.change.approved`'s cap tag in
   `internal/vocab`; G5 writer census + G10 mapping pins.
4. Extract the `conversation-channel` component (comment post + approval-from-Message +
   park-post + the USER-stream consumer); narrow `issue-intake`; boot registers both;
   the arc facts unchanged.
5. Relocate the spec requirement (forge-io REMOVE/MODIFY, conversation-channel ADD).
6. Green the webhook/park/approval journeys byte-for-byte (the regression guard); docs
   (G10). No new journey is needed (no new behavior).
Rollback: a refactor behind stable facts; revert restores the pre-carve structure.

## Operational notes (from the group-4 adversarial review)

- **Paired admission config (both reviewers, MEDIUM M1)**: the carve splits the
  admission knobs (`allowlist`, `repo`, `opt_in_command`) across `issue-intake` and
  `conversation-channel`. They MUST stay in sync — if `issue-intake` admits an actor
  the `conversation-channel` allowlist omits (allowlist-only mode), the run mints,
  parks at the approval gate, and `/semdev approve` is rejected → the run parks
  forever. Mitigated for commit by a loud PAIRED-INVARIANT note in both components'
  config `_comment` fields (and the e2e `patchIntakeJourneyConfig` patches both).
  **Named follow-on**: a boot-time cross-component consistency check that warns when
  the two components' admission knobs diverge (or a shared config fragment both
  reference). Not done here — it is net-new boot behavior, out of the pure carve.
- **Durable cutover on a long-lived deployment (LOW)**: `issue-intake`'s
  `github_events` consumer narrows its FilterSubject `github.event.>` →
  `github.event.issue`, and the `user_responses` consumer moves to
  `conversation-channel`. JetStream forbids changing a durable's FilterSubject in
  place, so on an UPGRADE over a persistent GITHUB/USER stream the old durables must
  be deleted (or the KV/streams reset) — `Start` fails LOUD otherwise (fail-closed,
  never silent). Moot for the journeys + live runs (they `task nats:reset`).

## Open Questions (RESOLVED)

- **OQ1 — component split → EXTRACT** (user): a dedicated `conversation-channel`
  component; `issue-intake` narrows. The shared admission surface goes to
  `internal/intake/admission` (D8).
- **OQ2 — poll cursor → MOOT here**: the poll transport (and its cursor) is deferred to
  `pull-first-transport`. When it lands, the cursor is **in-memory component state**,
  rebuilt on restart by a bounded idempotent re-read (dedup by `Message.ID`), NOT a
  domain-graph fact (pre-impl review B-2: cursor-as-fact is a B1/B9 bespoke-state smell,
  and the design's own idempotency argument shows it is not needed for correctness).
  No `conversation.thread.cursor` predicate is registered.
- **OQ3 — webhook receiver → KEEP** (unchanged in this carve); `pull-first-transport`
  decides poll-vs-webhook ownership at the deployment level (B-1).
