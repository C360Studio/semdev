## Why

The conversation half of the arc speaks GitHub in three hardcoded places —
`CreateComment(owner,repo,number,body)`, `NormalizeComment(githubwebhook.CommentEvent)`,
and the `owner/repo#number` thread coordinate — fused into the `forge-io` code-host
seam. Every human-message lane we add (ask_human, park-notice, approval,
delivery-status) re-hardcodes that GitHub shape. Before those lanes multiply
(Phase 2 NL intent, Phase 3 draft-PR review), carve a channel-neutral conversation
seam so GitHub stays v1 while Slack/email/etc. compose later — a pure debt-stop,
no new arc behavior.

## What Changes

- **New `conversation-channel` capability**: a `ConversationChannel` port with an
  opaque `ThreadRef` and three verbs — `Post(thread, message)`,
  `Read(thread, cursor) → []Message` (author-attributed, ordered, deduped), and
  `ResolveThread(workRef) → thread`. The arc consumes ONE normalized message-fact
  family, never a host payload.
- **GitHub issue/PR comments is the SOLE v1 impl**, re-homed behind the port: the
  existing `parkpost.Commenter`, `NormalizeComment`, and `CommentSignal` become the
  GitHub `ConversationChannel` implementation.
- **Pull-first poll READ is the default transport** — poll the thread on an interval
  (`github.Client.ListComments`) + a per-thread last-seen cursor + dedup → normalized
  message facts. The webhook stays an OPTIONAL latency accelerator, never required.
  (Kept IN this change unless the cursor/cost/dedup/fan-out story balloons — then it
  splits into its own `pull-first-transport` change ahead of the seam.)
- **Dissolve the 3 GitHub-shaped coupling points**: `Commenter.CreateComment(owner,repo,number,body)`
  (internal/intake/parkpost.go) → `Post(thread, message)`; `NormalizeComment(githubwebhook.CommentEvent)`
  (normalize.go) → the GitHub impl's `Read`/normalize; `CommentSignal.IssueRef="owner/repo#number"`
  → an opaque `ThreadRef` the adapter resolves.
- **`human.opt.signal`'s writer becomes the one `conversation-channel` component**
  (impls plug in BEHIND it), NOT competing per-channel writers — this is what makes
  multiple channels G5-legal (one writer per fact; GitHub/Slack are internal impls,
  not writers). Today's writer `comment-adapter` is reassigned to `conversation-channel`.
- **Relocate `forge-io`'s "Human communication rides the seam" requirement** into
  `conversation-channel`; `forge-io` narrows to purely code-host (issue intake, PR
  delivery, source clone, issue-content). The shared admission `Authorize` gate stays
  referenced by both.
- **NOT in scope** (later phases): NL intent classification (Phase 2 `nl-conversation-intent`
  — the `/semdev approve` command match stays as-is here); non-GitHub channel impls;
  the `workRef↔threadRef` binding fact (GitHub = identity, so none is needed yet); any
  new arc behavior. **No arc rule changes** — rules still read `human.opt.signal` /
  `run.change.approved`; only the adapter beneath them changes (the seam's whole point).

## Capabilities

### New Capabilities

- `conversation-channel` — the channel-agnostic conversation seam: the
  `ConversationChannel` port (Post / Read / ResolveThread), the opaque `ThreadRef`,
  the normalized message-fact family (`human.opt.signal` relocated here), the
  pull-first poll transport, and the GitHub issue/PR-comment v1 implementation.

### Modified Capabilities

- `forge-io` — relocate the **"Human communication rides the seam"** requirement to
  `conversation-channel` and narrow forge-io to code-host concerns (intake, delivery,
  source, issue-content). The **"Intake is gated to authorized, opted-in actors"**
  requirement's conversation-steering scenario ("only the authorizing requester steers
  a run's human gate") moves with it; the admission `Authorize` gate itself stays
  shared (referenced by both capabilities, one implementation).

## Impact

- **Code**: a new `internal/forge/conversation` (or `internal/conversation`) package
  for the port + the GitHub impl + the poll transport. `internal/intake/parkpost.go`
  (`Commenter` → `ConversationChannel.Post`), `normalize.go` (`NormalizeComment` → the
  GitHub impl's read/normalize), `approval.go` (`CommentSignal` → normalized `Message`;
  the `/semdev approve` command match is UNCHANGED). Boot wiring registers the
  conversation-channel component + selects the pull-first poller.
- **Vocab**: `human.opt.signal` relocates its capability tag `forge-io` →
  `conversation-channel` and its writer `comment-adapter` → `conversation-channel`
  (a single-writer reassignment named in the delta, G5/G9). NO new predicate (GitHub
  is the identity thread; the binding fact is deferred to the first non-GitHub channel).
- **Config**: a minimal poll-interval / cursor surface on the conversation-channel
  component (kept small; the pull-first split-tripwire governs whether it grows).
- **Foundation unchanged**: self-target provisioning + `semdev launch` (live-proven,
  M0-completion) stay; `run.change.approved` (writer `approval-adapter`) is untouched.
- **Tests**: the webhook/park/approval journeys keep passing byte-for-byte (the arc
  reads the same facts); a new conversation-channel unit + poll-transport pins prove
  the port and the cursor/dedup.
