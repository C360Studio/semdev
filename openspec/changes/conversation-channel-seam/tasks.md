# Tasks: conversation-channel-seam (the pure carve)

Discipline (project law): red-first pin with every behavior (G6); the arc's facts +
rules + journeys + the webhook transport stay BYTE-IDENTICAL (the regression guard —
this is a refactor behind stable facts, not a behavior change); adversarial review
(go + semstreams reviewers) before each group commit; no paid token. Scope guardrails:
NO poll transport (that is the follow-on `pull-first-transport`), NO NL intent
(Phase 2), NO non-GitHub impl, NO new arc behavior.

## 1. The ConversationChannel port + neutral types (design D1–D4)

- [x] 1.1 RED: pins that the neutral types carry no host shape — `ThreadRef` is an
  opaque string; `Message{ID,Author,Body,At}` has no owner/repo/number field. A
  conformance pin asserts the `conversation` package's EXPORTED surface imports no
  `githubwebhook` type (the CommentEvent→Message normalize is an unexported internal).
  [Done: `conversation_test.go` type-shape pins; `test/conformance/conversation_seam_test.go`
  + `exportedTypeLeaks` AST scanner (alias/dot-import-resolving, red-first self-test).]
- [x] 1.2 Add `internal/forge/conversation`: the `Channel` interface (named `Channel`,
  not `ConversationChannel` — avoids the revive stutter; reads as `conversation.Channel`)
  (`Post(ctx,thread,body)` + `ResolveThread(ctx,workRef)→ThreadRef` — NO `Read` verb;
  it would be latent code, added by `pull-first-transport`), the neutral
  `Message`/`ThreadRef` types, and the G1 framework-alignment package doc.

## 2. GitHub v1 implementation (design D1–D3)

- [x] 2.1 RED: `TestGitHubChannelPostResolvesThreadAndComments` — `Post` resolves the
  work coordinate via `SplitRef` and calls `github.Client.CreateComment(owner,repo,
  number,body)`; `ResolveThread(workRef)` is identity (`ThreadRef==workRef`). [Also pins
  fail-closed: nil-commenter (wiring) + malformed-thread + transport-error propagation.]
- [x] 2.2 RED: `TestGitHubChannelNormalizeContainsCommentEvent` — the impl's
  (unexported) normalize maps a `githubwebhook.CommentEvent` to a neutral `Message`,
  preserving today's `sender == author` attribution guard (EqualFold, both accept +
  reject sides pinned); the downstream consumer sees `Message`, never `CommentEvent`
  (coupling point 2 dissolved at the seam). [`ok` collapses old `Relevant && Attributable`
  — proven byte-identical for the sole consumer.]
- [x] 2.3 Implement the GitHub `Channel` over `github.Client` (Post→CreateComment; the
  CommentEvent→Message normalize contained/unexported; `NormalizeInboundComment([]byte)`
  the neutral boundary). Reuses the existing `github.Client` CreateComment path; `SplitRef`
  imported from `internal/intake` (grp4 repoints to `internal/intake/admission`).

## 3. Vocab: single writer + capability coherence (design D5; G5/G9/G10)

- [x] 3.1 RED: G5 writer census pin — `human.opt.signal`'s writer is
  `conversation-adapter` (NOT `comment-adapter`); the writer-census conformance test
  flips red until the reassignment lands. [`TestConversationChannelVocabReassignment`
  — verified red pre-change, green after; also pins run.change.approved's writer UNCHANGED.]
- [x] 3.2 Reassign in `internal/vocab`: `human.opt.signal` writer
  `comment-adapter`→`conversation-adapter`, capability `forge-io`→`conversation-channel`.
  Move `run.change.approved`'s capability tag `forge-io`→`conversation-channel` (writer
  `approval-adapter` UNCHANGED — it is read by `run-lifecycle/02`). NO new predicate.
  [Paper reassignment: no code stamps human.opt.signal; conversation-adapter needs no Source.]
- [x] 3.3 RED: G10 mapping pin — the vocab/architecture census reflects both capability
  moves (the fact's spec'd home == its capability tag), so no G10 drift. [architecture.md
  Fact-vocabulary rows updated; TestDocsVocabularyMatchesRegistry + G9 provenance green
  (conversation-channel declared by the change's specs/conversation-channel/ dir).]

## 4. Extract the admission surface + the conversation-channel component (design D8; M-4)

- [x] 4.1 Extract `internal/intake/admission` (the shared surface both components need):
  `Authorize`, `Config`, `PermissionChecker`, `SplitRef`, `RunResolver`/`ResolveRunByRef`,
  `natsEntityFetcher`, the `Event` type. Update `internal/launch` imports (`SplitRef`,
  `CoordinatorTask`, `NewRunResolver`, `FrontDoorSubject`, `Intake`) to their new homes.
  RED-first: a parity pin proves `internal/launch` still builds + the single `boot.Run`
  path is intact.
- [x] 4.2 RED: `TestParkPostPostsViaPort` — `parkpost` posts the `run.awaiting.human`
  message through `Channel.Post` (not `Commenter.CreateComment`); the bounded-retry /
  never-block-the-park contract is preserved. GRP2-REVIEW CARRY-FORWARDS (the consumer
  owns the definitive-skip — `Channel.Post` fails closed on ALL error classes, so the
  park consumer must reproduce today's `parkpost` degrades, NOT redeliver them to
  exhaustion): (a) NO forge client → graph-only skip + definitive ack (the channel is
  built with a nil commenter in allowlist-only/journey boots; the consumer skips `Post`
  or maps its wiring error to a definitive ack); (b) unparseable `run.issue.ref` →
  graph-only skip + ack (today's `parkpost.go:97-102`), not a `Post`-error redeliver.
- [x] 4.3 RED: `TestApprovalReadsNeutralMessage` — the approval adapter authorizes +
  releases the gate from a neutral `Message` (not a `CommentSignal`); the `/semdev
  approve` exact-command match is UNCHANGED; `run.change.approved` (writer
  `approval-adapter`) is untouched. GRP2-REVIEW CARRY-FORWARDS: (a) the admission
  `Event{Actor,Owner,Repo,AuthoredText}` is rebuilt from `Message.Author` + `SplitRef(thread)`
  (byte-identical to the old `Event.Actor=Sender` / `Repository.Owner/Name` — proven
  equivalent because `ok` requires `sender==author` and `FullName==owner/name`); (b) a
  DECODE failure from `NormalizeInboundComment` (err != nil) must be logged + ACKED, not
  redelivered (today's `approval.go:85-88` definitive skip — the receiver only publishes
  shapes it flattened itself).
- [x] 4.4 Extract the `conversation-channel` component: it owns comment `Post`,
  approval-from-`Message`, park-post, and the `user.response.>` USER-stream consumer;
  it consumes `github.event.comment` from the (unchanged) webhook-fed GITHUB stream and
  shares the `admission` core with `issue-intake`. `issue-intake` NARROWS to the
  code-host issue front door (`github.event.issue` → run mint) + the webhook receiver
  (which still flattens BOTH event types — B-1: comment events keep reaching the
  conversation component's consumer, unchanged).
- [x] 4.5 Boot/DI wiring: BOTH binaries register the `conversation-channel` component
  (the half-wired-in-one-binary class); the G1 census updated; parity-scan-safe.
- [x] 4.6 GUARD: the full `internal/...` unit layer + `test/conformance` green
  UNCHANGED — the arc reads the same facts, the transport is the same webhook path;
  only the code structure beneath moved.

## 5. Spec relocation coherence + docs (G10)

- [x] 5.1 The `forge-io` delta (REMOVE "Human communication rides the seam"; MODIFY
  "Intake is gated" to drop the steering scenario) and the new `conversation-channel`
  spec match the code — no arc rule references a channel payload; the relocated steering
  requirement says "an authorized actor," not "the run's authorized requester" (M-1);
  the `human.opt.signal` reply lane is spec'd as a RESERVED forward contract (M-3).
- [x] 5.2 Docs match reality: `docs/brief.md` (the conversation seam), CLAUDE.md status,
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
