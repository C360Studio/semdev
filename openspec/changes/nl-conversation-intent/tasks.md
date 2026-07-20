# Tasks: nl-conversation-intent

Discipline (project law): red-first pin with every behavior (G6); the EXACT-COMMAND
approval stays BYTE-IDENTICAL (the webhook/poll approval journeys are the regression
guard — this adds NL classification, it does not change the deterministic fast-path);
adversarial review (go + semstreams reviewers) before each group commit; no paid token
on the mock ladder. Scope guardrails: intent is APPROVE/REJECT/none ONLY (no ask_human
answer, no change-request — Phase 3); classification is bound to ONE authorized author's
message (never aggregate thread text); the LLM classifies a ROUTING signal (G3), the
authorization gate + the gate-fact write stay deterministic (`approval-adapter`, G5).

## 1. The closed conversation-intent taxonomy + the `conversation` persona (design D1, D3)

- [ ] 1.1 RED: `TestConversationIntentTaxonomyClosed` — `internal/conversationintent`
  declares exactly `{approve, reject, none}`; `Valid()` accepts only those, `Names()`
  is the allowlist; an off-taxonomy value is rejected (mirrors `internal/taxonomy`).
- [ ] 1.2 RED: `TestConversationTaxonomyMatchesPersonaContract` — a conformance census
  pins the Go taxonomy == the `conversation` persona decision-contract fragment's listed
  intents == the intents the routing rules match (no drift, mirroring the coordinator
  census `TestTaxonomyMatchesPersonaContract`).
- [ ] 1.3 Implement `internal/conversationintent` (the closed set + Valid/Names) and the
  `configs/personas/fragments/conversation/` fragment tree (00-identity + a decision
  contract: read the human's message, output exactly one intent, `none` for anything
  short of an explicit directive — NEVER approve from silence/reaction/ambiguity).

## 2. The `classify_intent` tool — the `decide` analog, G3-clean (design D2)

- [ ] 2.1 RED: `TestClassifyIntentStampsRoutingFactNotMeasurement` — the tool takes
  `intent` (from the taxonomy) + `message_id` + `author` + `reason`; it stamps
  `conversation.intent` / `.message-id` / `.reason` (routing facts) and NO measurement
  fact; its schema takes NO outcome boolean (the G3 census must pass on it).
- [ ] 2.2 RED: `TestClassifyIntentRejectsOffTaxonomy` — an `intent` outside the closed set
  is rejected (the decide-allowlist pattern), so a hallucinated value cannot route.
- [ ] 2.3 Implement `internal/tools/classifyintent` (`classify_intent`) + its schema +
  registration in `boot.RegisterTools` (schema-only without a writer; the G3 outcome-field
  census covers it). Its Source is `conversation-classifier` (G5, its own writer).

## 3. `handleMessage`: retain the fast-path, add reject, bridge NL (design D4, D5)

- [ ] 3.1 RED: `TestExactApproveCommandStillDeterministic` — the whole-token
  `/semdev approve` fast-path is BYTE-IDENTICAL (stamps `run.change.approved`, no pending
  fact, no classifier); the migrated approval pins stay green.
- [ ] 3.2 RED: `TestExactRejectCommandCancelsDeterministically` — a whole-token
  `/semdev reject` stamps `run.change.rejected` deterministically (no model turn).
- [ ] 3.3 RED: `TestNonCommandAuthorizedMessageStampsPending` — a non-command message from
  an AUTHORIZED author on an `awaiting_approval` run stamps
  `conversation.message.pending.{message-id,author,body}` and ACKs; an UNAUTHORIZED
  author's non-command message stamps NOTHING (no classification triggered); a message on
  a NON-gated run stamps nothing.
- [ ] 3.4 RED: `TestPendingDedupByMessageID` — a message whose id already appears in a
  recorded `conversation.intent.message-id` on the run re-stamps NO pending (the
  restart/redelivery re-read is inert); a NEWER message id overwrites the pending slot.
- [ ] 3.5 Implement the `handleMessage` changes: exact-command fast-path first (approve +
  new reject), else the authorized-non-command → `conversation.message.pending` bridge
  with message-id dedup. `conversation.message.pending` writer = `conversation-adapter`.

## 4. The spawn rule + the intent-routing rules (design D3, D6)

- [ ] 4.1 RED: `TestBootstrapWiresConversationClassifierRules` — the rule pack bootstraps
  a spawn rule (`conversation.message.pending` @ `agent.run.phase==awaiting_approval` →
  an `inherit`-scoped `role:conversation` loop, edge-triggered fire-once per pending
  value, the message body/author templated onto its prompt) and two routing rules
  (`conversation.intent==approve` and `==reject` → publish to the adapter apply lane).
  (Rule-load + `TestEveryRuleFileIsBootstrapped` shape.)
- [ ] 4.2 Implement the rules under `configs/rules/conversation/`: the classifier spawn
  (`tool_choice: required`, the decide-allowlist metadata scoped to the intent taxonomy)
  + the approve/reject routing publishes. No lifecycle transition in any of them (G2).

## 5. The deterministic apply consumer + the reject→cancel lane (design D6, D7)

- [ ] 5.1 RED: `TestApplyIntentReAuthorizesAndStamps` — the adapter apply consumer, given a
  routed approve intent + cited author, RE-RUNS `Authorize` (an unauthorized cited author
  yields ZERO writes), POSTS a transparency comment via `Channel.Post`, then stamps
  `run.change.approved` (Source `approval-adapter` — the SAME writer as the fast-path);
  the reject case stamps `run.change.rejected`.
- [ ] 5.2 RED: `TestRejectCancelsRun` — a run-lifecycle rule fires
  `awaiting_approval → cancelled` on `run.change.rejected` (rule-owned, G2); a
  bootstrap/rule-load pin covers it.
- [ ] 5.3 Implement the apply consumer (a rule-published lane on the conversation-channel
  component: re-Authorize → transparency Post → stamp approved/rejected) + the
  run-lifecycle `run.change.rejected → cancelled` rule. Both the exact-command path and
  the NL path converge on the one `approval-adapter` write method (G5). Register
  `run.change.rejected` in vocab (writer `approval-adapter`).

## 6. The NL bridge-proof journeys (regression guard for the new intent lane)

- [ ] 6.1 RED: `TestBridgeProofNLApprovalReleasesGate` — a run parked at
  `awaiting_approval`; an authorized author posts a NON-command NL approval ("looks good,
  ship it") to the forge double; the classifier (mock fixture) reads it, the transparency
  comment posts, `run.change.approved` lands, the run resumes — no exact command, no
  stand-in write.
- [ ] 6.2 RED: `TestBridgeProofNLRejectionCancelsRun` — an authorized author posts a NL
  rejection ("no, don't ship this"); the classifier reads reject, the transparency
  comment posts, `run.change.rejected` lands, the run reaches `cancelled`.
- [ ] 6.3 RED: `TestConservativeNoneDoesNotApprove` — an authorized author's ambiguous
  message ("thanks!", a 👍, "interesting") classifies `none`; the run stays gated, no
  gate fact.
- [ ] 6.4 The existing exact-command approval journeys (webhook + poll) pass byte-for-byte
  (the fast-path unchanged) — no journey rewired; the NL journeys are ADDED.
- [ ] 6.5 A real-LLM classification probe (env-gated, `SEMDEV_REAL_LLM=1`): the classifier
  persona classifies a real approval + a real rejection + an ambiguous message correctly
  against the live model, recorded per the runbook. Decide the classifier model tier here
  (design OQ4).

## 7. Spec + docs + verification + review + archive

- [ ] 7.1 The `conversation-channel` delta matches the code (NL approve/reject gate, the
  routing-not-measurement requirement, the fast-path retained). Docs: the NL-intent gate
  in the real-llm/live-run runbook (how a human approves in prose; the exact command
  still works; a rejection cancels).
- [ ] 7.2 Full offline ladder green (`task check`) + full `task e2e -race` uncached (incl.
  the NL journeys) + `openspec validate --strict`.
- [ ] 7.3 Adversarial review — BOTH reviewers (go + semstreams), zero blocking/high, all
  findings applied. Focus: the false-approval safety posture (deterministic double-Authorize,
  message-grounding, conservative persona, transparency), G3 (classification is a routing
  signal, no measurement/outcome field), G5 (one writer for `run.change.approved`/`rejected`),
  G2 (no Go lifecycle transition; the cancel is rule-owned), the dedup (no re-classification
  storm), and the exact-command byte-identity.
- [ ] 7.4 sync-specs at archive folds the delta (conversation-channel modified; still 12
  caps — no new capability).
