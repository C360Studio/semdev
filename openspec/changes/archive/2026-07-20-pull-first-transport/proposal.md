## Why

The `conversation-channel-seam` carve landed the `Channel` port with `Post` +
`ResolveThread`, but kept the inbound transport **webhook-fed** (the receiver
flattens comments onto the GITHUB stream; a durable consumer drives approval). That
leaves the `/semdev approve` gate reachable ONLY where a GitHub webhook can reach the
process. But semdev is **PULL-FIRST / webhook-optional** (user decision): most
instances run on a dev box or behind NAT, unreachable by a webhook. Today such a
deployment can release the change gate only through a CLI stand-in — the human cannot
approve ON the issue thread where they live.

This change adds the port's **`Read`** verb + a **poller** so a deployment with no
webhook receiver drives the approval lane by POLLING each active run's thread on an
interval. The webhook stays as an optional latency accelerator; the portable default
is outbound-only (poll). It is the transport layer only — the `/semdev approve`
command match is carried over UNCHANGED (NL intent is Phase 2).

## What Changes

- **Add `Read(ctx, thread, cursor) → ([]Message, cursor)` to the `Channel` port** —
  the third verb the carve deliberately deferred. The GitHub impl reads via
  `github.Client.ListComments` (already exists), normalizes each comment to a neutral
  `Message`, and returns those AFTER the cursor. Post/ResolveThread are unchanged.
- **A poller in the conversation-channel component**: on a configured interval it
  enumerates the runs AWAITING APPROVAL (`agent.run.phase == awaiting_approval`, a
  read-only graph observation — G2), `Read`s each run's thread, and feeds fresh
  `Message`s to the SAME approval core the webhook consumer uses (the `/semdev
  approve` command match + `admission.Authorize`). Once a run is approved it leaves
  the phase and the poller stops polling it.
- **An IN-MEMORY cursor** per thread (last-seen `Message.ID`) — NOT a domain-graph
  fact (review B-2: cursor-as-fact is a B1/B9 bespoke-state smell). On restart the
  cursor is empty and the poller re-reads from the top; re-processing an already-seen
  approve is a no-op (the adapter's `alreadyApproved` guard), so correctness comes
  from idempotency, not cursor durability.
- **Poll-vs-webhook deployment ownership (review B-1)**: a config toggle — a
  deployment runs EITHER the webhook-fed comment consumer OR the poller for the
  comment lane, never both, so a comment is never double-processed. The receiver
  serves issues AND comments (it is not comment-specific); poll mode = receiver off
  (`http_port 0`) + the poller on. Both modes drive the identical approval core.
- **Poll-path authorization is `Message.Author` (review H-1)**: a poll read carries
  ONE identity (the comment's author), with no separate webhook sender — so the
  neutral `Message.Author` is the authorization principal. The carve already made
  `Message.Author` the principal on the webhook path (sender==author guarded), so the
  approval core is unchanged; the poll path simply has no attribution guard to run.
- **`TestBridgeProofApprovalByPollNoWebhook`** — the red-first journey: a deployment
  with `http_port 0` + poll enabled, a run minted (front-door publish), parked at
  approval; the poller reads `/semdev approve` from the thread and releases the gate,
  with NO webhook and NO stand-in write.

- **NOT in scope**: NL intent classification (Phase 2 — the exact `/semdev approve`
  command match is carried over UNCHANGED); the draft-PR review surface / what the
  human sees to approve (Phase 3); non-GitHub `Read` impls; issue DISCOVERY by poll
  (issues enter via the already-live pull-first `semdev launch`; the poller drives the
  CONVERSATION on active runs, not intake); the `ask_human`-reply / `human.opt.signal`
  resume lane (the carve's RESERVED forward contract — still not delivered).

## Capabilities

### Modified Capabilities

- `conversation-channel` — add the port's `Read` verb, the poll transport (a poller
  reading each awaiting-approval run's thread with an in-memory cursor), and the
  deployment-level poll-vs-webhook ownership model, so the approval gate is operable
  from the thread with no inbound webhook. GitHub `ListComments` is the v1 `Read`.

## Impact

- **Code**: `Read` on the `Channel` interface + the GitHub impl over
  `github.Client.ListComments` (normalize comment → `Message`). A poller in
  `internal/conversationchannel` (interval loop, in-memory per-thread cursor, the
  awaiting-approval run enumeration). The approval adapter's `handleCommentEvent`
  refactored so BOTH the webhook consumer and the poller feed one `handleMessage`
  core. A config toggle (`poll` block: enabled + interval) that selects poll mode.
- **Graph**: one new READ — enumerate runs at `agent.run.phase == awaiting_approval`
  and read their `run.issue.ref`. A read-only observation, never a lifecycle write (G2).
- **Vocab**: NO new predicate; NO new writer (approval stays `approval-adapter`, G5).
  The cursor is in-memory, not a fact (B-2).
- **Regression guard**: the webhook/park/approval journeys stay green (webhook mode
  unchanged); the new by-poll journey is added red-first.
- **Foundation unchanged**: the `Channel` port's Post/ResolveThread, the carve's
  component split, `semdev launch` (pull-first mint, already live).
