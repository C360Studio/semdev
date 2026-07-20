# Tasks: pull-first-transport

Discipline (project law): red-first pin with every behavior (G6); WEBHOOK mode stays
BYTE-IDENTICAL (the webhook/park/approval journeys are the regression guard — this
adds a second transport, it does not change the first); adversarial review (go +
semstreams reviewers) before each group commit; no paid token. Scope guardrails: NO
NL intent (Phase 2 — the `/semdev approve` command match is unchanged), NO draft-PR
review surface (Phase 3), NO non-GitHub `Read`, NO issue-discovery-by-poll, NO
`ask_human`-reply lane (the carve's RESERVED forward contract).

## 1. The `Read` verb + `Cursor` on the port + GitHub impl (design D1, D7)

- [ ] 1.1 RED: `TestChannelReadVerb` — the port has EXACTLY three verbs now (Post,
  ResolveThread, Read); `Read(thread, cursor)` returns `([]Message, Cursor, error)`;
  `Cursor` is an opaque string (empty = from the top). The exported-surface
  host-neutrality pin still holds (no `githubwebhook` type on the Read signature).
- [ ] 1.2 RED: `TestGitHubChannelReadFiltersByCursor` — the GitHub `Read` maps each
  `github.Comment` → neutral `Message{ID=itoa(comment id), Author, Body, At}`, returns
  those with `id > cursor` NUMERICALLY (parse to int64 — pin a digit-width-boundary
  case: cursor "9", comments 9,10,11 → returns 10,11; review M1) + the max id as the
  next cursor; an empty cursor returns ALL; a NO-NEW-COMMENTS read returns the INPUT
  cursor unchanged (not max-of-empty → no re-read storm; review M2); no attribution
  guard (a polled comment has one author = the principal).
- [ ] 1.3 Implement `Read` on the `Channel` interface + the GitHub impl over
  `github.Client.ListComments` (reuse `SplitRef` for owner/repo/number). Post +
  ResolveThread + `NormalizeInboundComment` unchanged.

## 2. One approval core, two transports (design D3; H-1)

- [ ] 2.1 RED: `TestApprovalCoreSharedByWebhookAndPoll` — extract `handleMessage(ctx,
  msg, thread)` and prove the WEBHOOK path (`handleCommentEvent` → normalize →
  handleMessage) is byte-identical to today (the migrated approval pins stay green),
  AND that feeding a neutral `Message` DIRECTLY (the poll path) through `handleMessage`
  authorizes `Message.Author` and lands the same `run.change.approved`.
- [ ] 2.2 Refactor the approval adapter: `handleCommentEvent(payload)` →
  `NormalizeInboundComment` → `handleMessage`; the poller calls `handleMessage`
  directly. Decode-error ACK + all grp2-review carry-forwards preserved.
- [ ] 2.3 PIN the accepted edit divergence (review M3): the poll path honors an
  approve in a comment's CURRENT body (an edited-in `/semdev approve`, attributed to
  its author); the webhook path fires only on `created`. Not byte-identical on edits —
  documented in the spec, not claimed away. (No security hole: `Authorize` gates the
  comment's author either way.)

## 3. The poller + config + XOR ownership (design D2, D4, D5, D6)

- [ ] 3.1 RED: `TestPollerReadsAwaitingApprovalRunsAndReleasesGate` — a fake channel
  + fake resolver: the poller enumerates awaiting-approval runs, `Read`s each thread,
  feeds the fresh `/semdev approve` Message to the core → the gate is released; a
  second poll with NO new comments keeps the cursor STABLE and does NOT re-read (M2); a
  restart (empty cursor) re-reads + re-applies as a NO-OP (idempotent —
  `alreadyApproved`). Also pins: an enumeration/`Read` TRANSPORT ERROR is retried next
  tick (loop survives), NOT conflated with empty (review H2).
- [ ] 3.2 RED: `TestListRunsAwaitingApproval` — the resolver enumerates runs at
  `agent.run.phase == awaiting_approval` and returns their `run.issue.ref` (read-only
  prefix query, G2 — never a lifecycle write) via `RequestClassified` (mirror
  `ResolveRunIDsByRef`); pin that a classified TRANSPORT ERROR PROPAGATES AS `err`, is
  NOT decoded as an empty slice (the ADR-060 silent-success shape; review H2), and that
  the enumeration is repo-scoped when `cfg.Repo` is bound (review L1).
- [ ] 3.3 RED: `TestPollConfigXORsTheWebhookConsumer` — with `poll.enabled`, Start
  wires the POLLER + the park consumer and SKIPS the `comment_events` webhook consumer;
  without it, the webhook consumer runs (today). The park-post consumer runs in BOTH.
  Also pins: `Validate`/`applyConfigDefaults` rejects a non-positive `poll.interval`
  and clamps to the ≥5s floor (review M4); `Stop` cancels the poll goroutine — it exits
  (review M5).
- [ ] 3.4 Implement the poller (interval loop, in-memory `map[ThreadRef]Cursor` pruned
  to the awaiting set each tick, awaiting-approval enumeration, `Read`→`handleMessage`;
  an enumerate/`Read` error is logged + retried next tick and never conflated with
  empty, never blocks; bound to a ctx `Stop` CANCELS) + the `poll` config block
  (`enabled`, `interval` default 15s, floor ≥5s) + `ListRunsAwaitingApproval`
  (`RequestClassified`, repo-scoped) on the resolver + the XOR wiring in Start + the
  LOUD active-inbound-mode startup log (review H1).
- [ ] 3.5 The boot coherence guard (review H1): `internal/boot` (it assembles the full
  bootstrap and sees both component blocks) fails closed / loud-warns when issue-intake
  `http_port == 0` AND conversation-channel `poll.enabled == false` — the silent
  dead-approval-lane combination. RED-first: a pin over the assembled bootstrap.

## 4. The by-poll journey (regression guard for the new transport)

- [ ] 4.1 RED: `TestBridgeProofApprovalByPollNoWebhook` — a bootstrap with
  issue-intake `http_port 0` (no receiver) + conversation-channel `poll.enabled true`;
  a run minted via the front-door publish, parked at approval; a `/semdev approve`
  comment posted to the thread's forge double is read by the POLLER (no webhook, no
  stand-in write) and releases the gate; the run resumes. Uses the protocol-faithful
  forge double's `ListComments`.
- [ ] 4.2 The existing webhook/park/approval journeys pass byte-for-byte (webhook mode
  unchanged) — no journey rewired; the poll journey is ADDED.

## 5. Spec + docs

- [ ] 5.1 The `conversation-channel` delta (MODIFY the seam requirement to add the
  `Read` verb + a read scenario; ADD "The approval gate is operable with no inbound
  webhook") matches the code — no new predicate, cursor in-memory (B-2), poll-vs-webhook
  XOR (B-1), poll-path auth = `Message.Author` (H-1).
- [ ] 5.2 Docs: the pull-first deployment shape (poll mode = `http_port 0` +
  `poll.enabled`; the webhook is the optional accelerator); update the real-llm /
  live-run runbook so a non-webhook-reachable target uses poll approval; note the
  paired admission-config invariant still holds (both front-door components).

## 6. Verification + review + evidence

- [ ] 6.1 Full offline ladder green (`task check`) + full `task e2e -race` uncached
  (all journeys incl. the new by-poll) + `openspec validate --strict`.
- [ ] 6.2 Adversarial review — BOTH reviewers (go + semstreams), zero blocking/high,
  all findings applied. Focus: the poll transport's fail-safety (idempotent re-read,
  bounded, read-only G2 enumeration, no new writer/fact), the XOR ownership (no
  double-processing), the shared approval core (webhook byte-identity), the in-memory
  cursor (B-2, not a fact).
- [ ] 6.3 sync-specs at archive folds the delta (conversation-channel modified;
  still 12 caps — no new capability).
