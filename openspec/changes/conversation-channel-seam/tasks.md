# Tasks: conversation-channel-seam

Discipline (project law): red-first pin with every behavior (G6); the arc's facts
+ rules + journeys stay byte-identical (the regression guard — this is a refactor
behind stable facts, not a behavior change); adversarial review (go + semstreams
reviewers) before each group commit; no paid token — every task is provable
OFFLINE or against a LOCAL forge double. Scope guardrails: NO NL intent (Phase 2),
NO non-GitHub impl, NO new arc behavior.

## 1. The ConversationChannel port + neutral types (design D1–D4)

- [ ] 1.1 RED: pins that the port's neutral types carry no host shape — `ThreadRef`
  is an opaque string; `Message{ID,Author,Body,At}` has no owner/repo/number field.
  A conformance pin asserts the `conversation` package imports no `githubwebhook`
  type on its exported surface.
- [ ] 1.2 Add `internal/forge/conversation`: the `ConversationChannel` interface
  (`Post(ctx,thread,body)` / `Read(ctx,thread,cursor)→([]Message,next,err)` /
  `ResolveThread(ctx,workRef)→ThreadRef`), the neutral `Message`/`ThreadRef` types,
  and the G1 framework-alignment package doc (no new primitive — one component owns
  the fact, adapters plug in behind it).

## 2. GitHub v1 implementation (design D1/D3)

- [ ] 2.1 RED: `TestGitHubChannelPostResolvesThreadAndComments` — `Post` resolves the
  work coordinate via `SplitRef` and calls `github.Client.CreateComment(owner,repo,
  number,body)`; `ResolveThread(workRef)` is identity (`ThreadRef==workRef`) for GitHub.
- [ ] 2.2 RED: `TestGitHubChannelReadNormalizesAfterCursor` — `Read(thread,cursor)`
  calls `ListComments`, returns only comments after the cursor as neutral `Message`s
  (ordered by id/time), and returns the last id as the next cursor; attribution
  (sender==author) is computed and carried, mirroring today's `NormalizeComment` guard.
- [ ] 2.3 Implement the GitHub `ConversationChannel` over `github.Client` (Post→
  CreateComment, Read→ListComments, ResolveThread→identity). The `githubwebhook`
  flatten stays consumed INSIDE the impl / optional receiver, never on the neutral path.
- [ ] 2.4 NO-ARGV token reuse pin: the impl reuses the existing `github.Client` token
  path (the token never rides argv — reuse the forge-io guarantee, no new lane).

## 3. Vocab: single writer + the poll cursor (design D5; G5/G9)

- [ ] 3.1 RED: G5 writer census pin — `human.opt.signal`'s writer is
  `conversation-channel` (NOT `comment-adapter`); the writer-census conformance test
  (`TestEveryFactHasOneWriter`-class) flips red until the reassignment lands.
- [ ] 3.2 Reassign `human.opt.signal` in `internal/vocab`: writer
  `comment-adapter`→`conversation-channel`, capability `forge-io`→`conversation-channel`.
- [ ] 3.3 Register the NEW predicate `conversation.thread.cursor` (writer
  `conversation-channel`, cap `conversation-channel`) — the per-thread poll cursor
  fact (design OQ2: a fact on the run entity, no new store). RED-first `Register`
  + census pin.

## 4. Pull-first poll transport (design D6; the split-tripwire lives here)

- [ ] 4.1 RED: `TestPollReadsOnlyAfterCursorAndDedups` — the poller reads a thread,
  advances `conversation.thread.cursor` past processed messages, dedups by
  `Message.ID` (a re-poll re-writes no fact), and only surfaces new messages.
- [ ] 4.2 Implement the poller: poll each ACTIVE run's thread on a config interval
  via `Read(thread,cursor)`, persist the cursor as `conversation.thread.cursor`,
  feed new messages to the shared processing (approval command match + `human.opt.signal`).
- [ ] 4.3 RED: `TestPollAndWebhookMutuallyExclusiveFailsClosed` — boot with BOTH a
  poll transport and a webhook receiver for the channel is a LOUD boot error (D6 —
  no cross-transport double-delivery). Poll is the default; webhook is the optional XOR.
- [ ] 4.4 TRIPWIRE CHECK (design/ledger note): if cursor durability needs a stateful
  KV subsystem, cost-gating needs adaptive backoff, or fan-out over many threads needs
  a scheduler, STOP and split `pull-first-transport` into its own change ahead of the
  seam (the roadmap tripwire). Otherwise it stays in this change.

## 5. Extract the conversation-channel component; rewire behind the port (design D7/D8)

- [ ] 5.1 RED: `TestParkPostPostsViaPort` — `parkpost` posts the `run.awaiting.human`
  message through `ConversationChannel.Post` (not `Commenter.CreateComment`); the
  bounded-retry / never-block-the-park contract is preserved.
- [ ] 5.2 RED: `TestApprovalReadsNeutralMessage` — the approval adapter authorizes +
  releases the gate from a neutral `Message` (not a `CommentSignal`); the `/semdev
  approve` exact-command match is UNCHANGED (Phase 1 carries it over verbatim);
  `run.change.approved` (writer `approval-adapter`) is untouched.
- [ ] 5.3 Extract a `conversation-channel` component: it owns comment read (poll)/
  post, approval-from-message, and park-post; it shares the `Authorize` core with
  intake. `issue-intake` NARROWS to the code-host issue front door (issue events →
  run mint) — its comment-half moves out.
- [ ] 5.4 Boot/DI wiring: register the `conversation-channel` component + select the
  poll transport (webhook XOR per D6). G1 census updated; the single `boot.Run` path
  untouched; parity-scan-safe.
- [ ] 5.5 GUARD: the full `internal/...` unit layer + `test/conformance` green
  UNCHANGED — the arc reads the same facts; only the adapter beneath changed.

## 6. Spec relocation coherence + docs (G10)

- [ ] 6.1 The `forge-io` spec delta (REMOVE "Human communication rides the seam";
  MODIFY "Intake is gated" to drop the steering scenario) and the new
  `conversation-channel` spec match the code — no arc rule references a channel payload.
- [ ] 6.2 Docs match reality: `docs/brief.md` (the conversation seam), CLAUDE.md
  status, `docs/port-manifest.md` (the T7 comms-seam row now points at the
  `conversation-channel` capability), the memory pointers.

## 7. Verification + review + evidence

- [ ] 7.1 Existing webhook/park/approval journeys pass **byte-for-byte** (the
  regression guard) on the rewired adapter — same facts, same phases.
- [ ] 7.2 NEW `-race` docker journey `TestBridgeProofApprovalByPollNoWebhook`: a
  `/semdev approve` message delivered by POLL (no webhook receiver configured) drives
  the change gate end-to-end; a re-poll re-acts on nothing; the config-XOR fail-closed
  path is pinned. Zero paid tokens (mock LLM + local forge double).
- [ ] 7.3 Full offline ladder green (`task check`) + full `task e2e -race` uncached +
  `openspec validate --strict`.
- [ ] 7.4 Adversarial review — BOTH reviewers (go + semstreams), zero blocking/high,
  all findings applied (standing directive). Focus: the G5 single-writer reassignment,
  the neutral-Message carve (no `githubwebhook` on the arc path), the poll cursor/dedup
  idempotency, the config-XOR fail-closed, and the component extraction's blast radius.
- [ ] 7.5 sync-specs at archive folds the two deltas (forge-io narrows,
  conversation-channel added → 12 caps).
