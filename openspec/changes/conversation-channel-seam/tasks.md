# Tasks: conversation-channel-seam (the pure carve)

Discipline (project law): red-first pin with every behavior (G6); the arc's facts +
rules + journeys + the webhook transport stay BYTE-IDENTICAL (the regression guard —
this is a refactor behind stable facts, not a behavior change); adversarial review
(go + semstreams reviewers) before each group commit; no paid token. Scope guardrails:
NO poll transport (that is the follow-on `pull-first-transport`), NO NL intent
(Phase 2), NO non-GitHub impl, NO new arc behavior.

## 1. The ConversationChannel port + neutral types (design D1–D4)

- [ ] 1.1 RED: pins that the neutral types carry no host shape — `ThreadRef` is an
  opaque string; `Message{ID,Author,Body,At}` has no owner/repo/number field. A
  conformance pin asserts the `conversation` package's EXPORTED surface imports no
  `githubwebhook` type (the CommentEvent→Message normalize is an unexported internal).
- [ ] 1.2 Add `internal/forge/conversation`: the `ConversationChannel` interface
  (`Post(ctx,thread,body)` + `ResolveThread(ctx,workRef)→ThreadRef` — NO `Read` verb;
  it would be latent code, added by `pull-first-transport`), the neutral
  `Message`/`ThreadRef` types, and the G1 framework-alignment package doc.

## 2. GitHub v1 implementation (design D1–D3)

- [ ] 2.1 RED: `TestGitHubChannelPostResolvesThreadAndComments` — `Post` resolves the
  work coordinate via `SplitRef` and calls `github.Client.CreateComment(owner,repo,
  number,body)`; `ResolveThread(workRef)` is identity (`ThreadRef==workRef`).
- [ ] 2.2 RED: `TestGitHubChannelNormalizeContainsCommentEvent` — the impl's
  (unexported) normalize maps a `githubwebhook.CommentEvent` to a neutral `Message`,
  preserving today's `sender == author` attribution guard; the downstream consumer
  sees `Message`, never `CommentEvent` (coupling point 2 dissolved at the seam).
- [ ] 2.3 Implement the GitHub `ConversationChannel` over `github.Client` (Post→
  CreateComment; the CommentEvent→Message normalize contained here). Reuse the existing
  `github.Client` token path (no-argv reuse; no new lane).

## 3. Vocab: single writer + capability coherence (design D5; G5/G9/G10)

- [ ] 3.1 RED: G5 writer census pin — `human.opt.signal`'s writer is
  `conversation-adapter` (NOT `comment-adapter`); the writer-census conformance test
  flips red until the reassignment lands.
- [ ] 3.2 Reassign in `internal/vocab`: `human.opt.signal` writer
  `comment-adapter`→`conversation-adapter`, capability `forge-io`→`conversation-channel`.
  Move `run.change.approved`'s capability tag `forge-io`→`conversation-channel` (writer
  `approval-adapter` UNCHANGED — it is read by `run-lifecycle/02`). NO new predicate.
- [ ] 3.3 RED: G10 mapping pin — the vocab/architecture census reflects both capability
  moves (the fact's spec'd home == its capability tag), so no G10 drift.

## 4. Extract the admission surface + the conversation-channel component (design D8; M-4)

- [ ] 4.1 Extract `internal/intake/admission` (the shared surface both components need):
  `Authorize`, `Config`, `PermissionChecker`, `SplitRef`, `RunResolver`/`ResolveRunByRef`,
  `natsEntityFetcher`, the `Event` type. Update `internal/launch` imports (`SplitRef`,
  `CoordinatorTask`, `NewRunResolver`, `FrontDoorSubject`, `Intake`) to their new homes.
  RED-first: a parity pin proves `internal/launch` still builds + the single `boot.Run`
  path is intact.
- [ ] 4.2 RED: `TestParkPostPostsViaPort` — `parkpost` posts the `run.awaiting.human`
  message through `ConversationChannel.Post` (not `Commenter.CreateComment`); the
  bounded-retry / never-block-the-park contract is preserved.
- [ ] 4.3 RED: `TestApprovalReadsNeutralMessage` — the approval adapter authorizes +
  releases the gate from a neutral `Message` (not a `CommentSignal`); the `/semdev
  approve` exact-command match is UNCHANGED; `run.change.approved` (writer
  `approval-adapter`) is untouched.
- [ ] 4.4 Extract the `conversation-channel` component: it owns comment `Post`,
  approval-from-`Message`, park-post, and the `user.response.>` USER-stream consumer;
  it consumes `github.event.comment` from the (unchanged) webhook-fed GITHUB stream and
  shares the `admission` core with `issue-intake`. `issue-intake` NARROWS to the
  code-host issue front door (`github.event.issue` → run mint) + the webhook receiver
  (which still flattens BOTH event types — B-1: comment events keep reaching the
  conversation component's consumer, unchanged).
- [ ] 4.5 Boot/DI wiring: BOTH binaries register the `conversation-channel` component
  (the half-wired-in-one-binary class); the G1 census updated; parity-scan-safe.
- [ ] 4.6 GUARD: the full `internal/...` unit layer + `test/conformance` green
  UNCHANGED — the arc reads the same facts, the transport is the same webhook path;
  only the code structure beneath moved.

## 5. Spec relocation coherence + docs (G10)

- [ ] 5.1 The `forge-io` delta (REMOVE "Human communication rides the seam"; MODIFY
  "Intake is gated" to drop the steering scenario) and the new `conversation-channel`
  spec match the code — no arc rule references a channel payload; the relocated steering
  requirement says "an authorized actor," not "the run's authorized requester" (M-1);
  the `human.opt.signal` reply lane is spec'd as a RESERVED forward contract (M-3).
- [ ] 5.2 Docs match reality: `docs/brief.md` (the conversation seam), CLAUDE.md status,
  `docs/port-manifest.md` (the T7 comms-seam row → the `conversation-channel` capability),
  the memory pointers. Note the `github_list_comments` LLM-grounding tool remains a
  legitimately host-specific code-host reader, NOT subsumed by the port (L-1).

## 6. Verification + review + evidence

- [ ] 6.1 The webhook/park/approval journeys pass **byte-for-byte** (the regression
  guard) on the re-homed adapter — same facts, same phases, same webhook transport. NO
  new journey is added (this change introduces no new behavior; the approval-by-poll
  journey belongs to `pull-first-transport`).
- [ ] 6.2 Full offline ladder green (`task check` — build + lint + unit `-race`,
  censuses) + full `task e2e -race` uncached + `openspec validate --strict`.
- [ ] 6.3 Adversarial review — BOTH reviewers (go + semstreams), zero blocking/high, all
  findings applied (standing directive). Focus: the G5 single-writer reassignment + the
  G10 capability moves, the neutral-`Message` carve (no `githubwebhook` on the arc path),
  the `admission` sub-package extraction blast radius, and both-binary registration.
- [ ] 6.4 sync-specs at archive folds the two deltas (forge-io narrows,
  conversation-channel added → 12 caps).

## Follow-on (NOT this change): `pull-first-transport`

Adds the port's `Read(thread, cursor) → []Message` verb + a poller that reads each
active run's thread on an interval, an IN-MEMORY cursor (rebuilt on restart by a
bounded idempotent re-read; NOT a domain-graph fact — review B-2), the deployment-level
poll-vs-webhook ownership model (review B-1 — the receiver serves issues AND comments),
poll-path authorization by `Message.Author` (review H-1), and the
`TestBridgeProofApprovalByPollNoWebhook` journey — all red-first, on top of this seam.
