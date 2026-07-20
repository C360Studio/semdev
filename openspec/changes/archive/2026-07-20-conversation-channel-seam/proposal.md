## Why

The conversation half of the arc speaks GitHub in three hardcoded places —
`CreateComment(owner,repo,number,body)`, `NormalizeComment(githubwebhook.CommentEvent)`,
and the `owner/repo#number` thread coordinate — fused into the `forge-io` code-host
seam. Every human-message lane we add re-hardcodes that GitHub shape. Before those
lanes multiply (Phase 2 NL intent, Phase 3 draft-PR review), carve a channel-neutral
conversation seam so GitHub stays v1 while other channels compose later. This change
is the **pure carve** — a refactor behind stable facts, byte-identical arc behavior;
the pull-first poll transport is split into its own follow-on change (see below).

## What Changes

- **New `conversation-channel` capability**: a `ConversationChannel` port with an
  opaque `ThreadRef` and two verbs — `Post(thread, message)` and
  `ResolveThread(workRef) → thread`. A normalized, channel-neutral `Message`
  (`{ID, Author, Body, At}`) is what the conversation component processes; the arc
  consumes only the message facts, never a channel payload.
- **GitHub issue/PR comments is the SOLE v1 impl**, re-homed behind the port. The
  existing `parkpost.Commenter.CreateComment` becomes `Post`; the existing
  `NormalizeComment(githubwebhook.CommentEvent)` is CONTAINED inside the GitHub impl
  (it produces a neutral `Message`), so the arc/component boundary sees `Message`,
  not `CommentEvent` — dissolving coupling point 2 at the seam.
- **Transport is UNCHANGED**: the existing webhook-fed path (the receiver flattens
  `issue_comment` → `github.event.comment` on the GITHUB stream; a durable consumer
  drives approval + park) stays exactly as today — only re-homed into the new
  component and normalized to `Message`. **No poll, no cursor, no transport XOR** in
  this change.
- **Extract a `conversation-channel` component**: it owns comment post + the
  approval-from-message + park-post, behind the port; `issue-intake` NARROWS to the
  code-host issue front door (issue events → run mint). Both share one admission
  `Authorize` core (a pure function, referenced by both, not a writer).
- **`human.opt.signal`'s writer is reassigned** to a single channel-neutral writer
  (`conversation-adapter`) and its capability tag to `conversation-channel` — one
  writer per fact, impls behind it (G5). NOTE: `human.opt.signal` has **no writer
  and no reader today** (the ask_human-reply/resume lane is unimplemented); this
  change only reassigns the *declared* vocab writer, it does not wire the lane.
- **Relocate `forge-io`'s "Human communication rides the seam" requirement** into
  `conversation-channel`; `forge-io` narrows to code-host (issue intake, PR delivery,
  source clone, issue content).
- **NOT in scope**: the pull-first poll transport (its own change — see below); NL
  intent classification (Phase 2 — the `/semdev approve` command match is carried
  over UNCHANGED); non-GitHub impls; the `workRef↔threadRef` binding fact (GitHub =
  identity, none needed); any new arc behavior. **No arc rule changes.**

### Deferred to a follow-on change: `pull-first-transport`

The pull-first poll READ (poll `ListComments` on an interval, a read cursor, the
poll-vs-webhook deployment ownership, poll-path authorization by `Message.Author`)
is split out. The pre-impl review showed it is net-new behavior with its own failure
modes (receiver ownership, cursor durability, poll-path auth) that must not ride this
carve's byte-identical guard — exactly the roadmap's pull-first split-tripwire. It
adds the port's `Read(thread, cursor)` verb + the poller, red-first, on top of this
seam.

## Capabilities

### New Capabilities

- `conversation-channel` — the channel-agnostic conversation seam: the
  `ConversationChannel` port (Post / ResolveThread), the opaque `ThreadRef`, the
  neutral `Message`, the single-writer message-fact family (`human.opt.signal`
  relocated here), and the GitHub issue/PR-comment v1 implementation over the
  existing webhook-fed transport.

### Modified Capabilities

- `forge-io` — relocate the **"Human communication rides the seam"** requirement to
  `conversation-channel` and narrow forge-io to code-host concerns (intake, delivery,
  source, issue-content). The **"Intake is gated to authorized, opted-in actors"**
  requirement's conversation-steering scenario moves with it; the admission
  `Authorize` gate stays one shared implementation referenced by both.

## Impact

- **Code**: a new `internal/forge/conversation` package (port + neutral types + the
  GitHub impl over `github.Client`). A new `conversation-channel` component
  (comment post/approval/park behind the port). `issue-intake` narrows; likely a
  shared `internal/intake/admission` sub-package for `Authorize` / `SplitRef` /
  `RunResolver` / `PermissionChecker` that both components import. `internal/launch`
  imports (`intake.SplitRef` / `CoordinatorTask` / `NewRunResolver` / `FrontDoorSubject`)
  update if those move.
- **Vocab**: `human.opt.signal` writer `comment-adapter` → `conversation-adapter`,
  capability `forge-io` → `conversation-channel` (a single-writer reassignment named
  in the delta, G5/G9). NO new predicate. `run.change.approved` stays written by
  `approval-adapter`; its capability tag moves to `conversation-channel` to match
  where its production is now spec'd (G10 census coherence).
- **No arc-rule changes**: rules still read `human.opt.signal` / `run.change.approved`;
  only the adapter beneath them changes. The webhook/park/approval journeys pass
  byte-for-byte (the regression guard).
- **Foundation unchanged**: self-target provisioning + `semdev launch` (live-proven).
