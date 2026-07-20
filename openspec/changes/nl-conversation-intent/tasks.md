# Tasks: nl-conversation-intent

Discipline (project law): red-first pin with every behavior (G6); the EXACT-COMMAND
approval stays BYTE-IDENTICAL (the webhook/poll approval journeys are the regression
guard); adversarial review (go + semstreams) before each group commit; no paid token on
the mock ladder. Scope: intent is APPROVE/REJECT/none ONLY; classification is bound to ONE
authorized author's HARNESS-bound message (never an LLM-supplied author, never aggregate
thread text); the LLM supplies a ROUTING judgment (G3), the authorization gate + the gate
writes stay deterministic (`approval-adapter`, G5). Pre-impl review (architect + semstreams)
folded: vocab-register-before-write (M8); intent facts on the RUN (H2); harness-bound
author/message-id (H3, HIGH-2); gate-still-open guard + conflict-terminal (H4); phase-guarded
reject→cancel (H1); NL-approve irreversible-once-landed + PR-merge backstop (BLOCKING-1);
append-set dedup ledger + self-extinguishing spawn marker (M5, MEDIUM-4); resolver phase
getter (M7); classifier-fault fallback note (HIGH-3); Post-then-stamp at-least-once (M6);
the G5 shared-writer census + the G1 why-not-decide note (MEDIUM-7).

## 1. Vocab + the closed taxonomy + the `conversation` persona (design D1, D10)

- [ ] 1.1 Register ALL new canonical predicates FIRST (beta.150 fails closed at graph-write
  on an unregistered predicate — a writer added before its registration cannot go green):
  `conversation.message.pending` (writer `conversation-adapter`), `conversation.intent` +
  `conversation.intent.classified` ledger (writer `conversation-classifier`),
  `run.change.rejected` (writer `approval-adapter`). `internal/vocab.Register` + the
  vocab census.
- [ ] 1.2 RED: `TestConversationIntentTaxonomyClosed` — `internal/conversationintent`
  declares exactly `{approve, reject, none}`; `Valid()`/`Names()` (mirrors `internal/taxonomy`).
- [ ] 1.3 RED: `TestConversationTaxonomyMatchesPersonaContract` — the Go taxonomy == the
  `conversation` persona decision-contract fragment's intents. (The routing-RULE arm of the
  drift census lands in group 4 with the rules — noted, not asserted red here.)
- [ ] 1.4 Implement `internal/conversationintent` + `configs/personas/fragments/conversation/`
  (00-identity + a decision contract: read the message, output exactly one intent + a
  reason, NEVER name an author, default `none` for anything short of an explicit directive —
  no approve from silence/reaction/ambiguity).

## 2. `classify_intent` — model supplies judgment, harness supplies identity (design D2)

- [ ] 2.1 RED: `TestClassifyIntentTakesNoAuthorOrMessageID` — the tool schema exposes ONLY
  `intent` + `reason`; it takes NO `author` and NO `message_id` (the identity is
  harness-bound, not model-supplied — HIGH-2/H3). The G3 outcome-field census passes (no
  outcome boolean, no measurement fact).
- [ ] 2.2 RED: `TestClassifyIntentStampsOnRunFromPending` — the tool subject-overrides to
  the RUN and stamps `conversation.intent`, `.message-id` + `.author` COPIED from the run's
  `conversation.message.pending.*` (matched by pending id), `.reason` (model echo), and
  appends the id to `conversation.intent.classified` — all on the run, none on the loop (H2).
- [ ] 2.3 RED: `TestClassifyIntentRejectsOffTaxonomy` — an `intent` outside the closed set
  is rejected (the decide-allowlist pattern), so a hallucinated value cannot route.
- [ ] 2.4 Implement `internal/tools/classifyintent` + schema + `boot.RegisterTools`
  registration (Source `conversation-classifier`, G5). Doc the G1 why-not-`decide` note
  (no message-id grounding; `decide`'s Source would collide with the coordinator lane).

## 3. `handleMessage`: resolver phase getter, fast-path retained, NL bridge (design D4, D5)

- [ ] 3.1 RED: `TestResolverSurfacesPhase` — the run resolver surfaces `agent.run.phase`
  (today it returns only `(runID, approved, err)`) so `handleMessage` can gate on
  `awaiting_approval` (M7).
- [ ] 3.2 RED: `TestExactApproveCommandStillDeterministic` — the whole-token `/semdev approve`
  fast-path is BYTE-IDENTICAL (stamps `run.change.approved`, no pending, no classifier); the
  migrated approval pins stay green.
- [ ] 3.3 RED: `TestExactRejectCommandStampsRejected` — a whole-token `/semdev reject` stamps
  `run.change.rejected` deterministically (no model turn). (Cancellation is asserted in 5.x.)
- [ ] 3.4 RED: `TestNonCommandAuthorizedGatedMessageStampsPending` — a non-command message
  from an AUTHORIZED author on an `awaiting_approval` run stamps
  `conversation.message.pending.{message-id,author,body}` + the spawn marker
  `conversation.classifier.dispatched` and ACKs; an UNAUTHORIZED author, a NON-gated run
  (phase ≠ awaiting_approval), and a message whose id is already in
  `conversation.intent.classified` each stamp NOTHING (no model turn).
- [ ] 3.5 RED: `TestPendingDedupByAppendSetLedger` — dedup is against the MULTI-VALUED
  `conversation.intent.classified` ledger (not single-valued latest-wins): a redelivered
  NON-latest classified id is not re-stamped (MEDIUM-4); a genuinely new id is.
- [ ] 3.6 Implement: the resolver phase getter; the exact-command fast-path (approve +
  reject); the authorized-non-command-gated → pending bridge with append-set dedup + the
  self-extinguishing spawn marker. `conversation.message.pending` writer = `conversation-adapter`.

## 4. The spawn rule + the intent-routing rules (design D3, D6)

- [ ] 4.1 RED: `TestBootstrapWiresConversationClassifierRules` — the pack bootstraps a spawn
  rule (`conversation.message.pending` @ `awaiting_approval`, guarded on the
  `conversation.classifier.dispatched` marker fire-once, `inherit` `role:conversation`,
  `tool_choice:required`, the intent-allowlist metadata, the pending body/author templated
  onto the prompt) and two routing rules (`conversation.intent==approve` / `==reject`, EACH
  gated on `agent.run.phase==awaiting_approval` AND both gate facts absent → publish to
  `component.conversation-apply.dispatch`). Also completes the taxonomy census routing-rule
  arm (1.3).
- [ ] 4.2 Implement the rules under `configs/rules/conversation/`. No lifecycle transition in
  any (G2).

## 5. The apply consumer + the reject→cancel lane + the fault note (design D6, D7, D9, D11)

- [ ] 5.1 RED: `TestApplyConsumerGateStillOpenReAuthorizeStamps` — the apply consumer: (a)
  re-checks the gate is OPEN (neither `run.change.approved` nor `run.change.rejected`
  present — a second racing intent is a NO-OP, H4a); (b) reads the cited author from
  `conversation.intent.author` (HARNESS-bound, NOT pending — H4b) and RE-RUNS `Authorize`
  (an unauthorized cited author → ZERO writes); (c) POSTS the transparency comment; (d)
  stamps `run.change.approved`/`rejected` via the ONE shared `approval-adapter` writer.
- [ ] 5.2 RED: `TestApplyPostFailureBlocksStamp` — a `Channel.Post` failure returns TRANSIENT
  and the gate fact is NOT stamped (transparency-before-effect; redelivery re-Posts, M6).
- [ ] 5.3 RED: `TestRejectCancelsGatedRunOnly` — a run-lifecycle rule fires
  `awaiting_approval → cancelled` on `run.change.rejected`, PHASE-GUARDED to
  `awaiting_approval` (H1: it must NOT fire the legal `executing→cancelled` edge on an
  already-approved run); a bootstrap/rule-load pin covers the guard.
- [ ] 5.4 RED: `TestClassifierFaultPostsFallbackNote` — a faulted classifier terminal (no
  `conversation.intent`, `agent.loop.outcome` faulted) triggers a fallback-to-command note
  post; a confident `none` posts nothing (HIGH-3).
- [ ] 5.5 RED: `TestOnlySanctionedGateWriters` — the G5 census: the ONLY Source of
  `run.change.approved`/`rejected` is `approval-adapter`, and both the fast-path and the
  apply consumer route through one shared writer method (D11).
- [ ] 5.6 Implement: the apply consumer (a declared jetstream input port on the
  conversation-channel component: gate-still-open → harness-bound Authorize → Post → stamp,
  transient-on-Post-failure) + the phase-guarded `run.change.rejected → cancelled`
  run-lifecycle rule + the faulted-classifier fallback-note rule + the shared writer method.

## 6. The NL bridge-proof journeys (regression guard for the intent lane)

- [ ] 6.1 RED: `TestBridgeProofNLApprovalReleasesGate` — a run parked at `awaiting_approval`;
  an authorized author posts a NON-command NL approval to the forge double; the mock
  classifier reads approve; the transparency comment posts; `run.change.approved` lands; the
  run resumes — no exact command, no stand-in.
- [ ] 6.2 RED: `TestBridgeProofNLRejectionCancelsRun` — a NL rejection → the transparency
  comment posts, `run.change.rejected` lands, the run reaches `cancelled`.
- [ ] 6.3 RED: `TestConservativeNoneDoesNotApprove` — an ambiguous authorized message
  ("thanks!", 👍) classifies `none`; the run stays gated, no gate fact.
- [ ] 6.4 RED: `TestConflictingIntentsResolveToOneTerminal` — two authorized NL messages, one
  approve + one reject, both classified; the run reaches EXACTLY ONE of resumed-or-cancelled
  and carries EXACTLY ONE gate fact, never both (the H4 gate-still-open guard end-to-end).
- [ ] 6.5 The existing exact-command approval journeys (webhook + poll) pass byte-for-byte —
  no journey rewired; the NL journeys are ADDED.
- [ ] 6.6 A real-LLM classification probe (env-gated `SEMDEV_REAL_LLM=1`): the persona
  classifies a real approval + rejection + ambiguous message correctly against the live
  model; recorded per the runbook. Decide the classifier model tier (OQ4).

## 7. Spec + docs + verification + review + archive

- [ ] 7.1 The `conversation-channel` delta matches the code. Docs: the NL-intent gate in the
  runbook (approve in prose; the exact command still works; a rejection cancels a GATED run;
  NL-approve is not reversible via NL — the PR merge is the downstream stop).
- [ ] 7.2 Full offline ladder (`task check`) + full `task e2e -race` uncached (incl. the NL
  journeys) + `openspec validate --strict`.
- [ ] 7.3 Adversarial review — BOTH reviewers, zero blocking/high, all findings applied.
  Focus: the false-approval posture (deterministic double-Authorize on the HARNESS-bound
  author, grounding, conservative persona, transparency-before-effect, the PR-merge
  backstop), G3 (routing not measurement, no outcome field), G5 (one Source for the gate
  facts, the shared-writer census), G2 (phase-guarded rule-owned cancel; no Go transition),
  the dedup (append-set + spawn marker, no storm), the conflict-terminal guard, and the
  exact-command byte-identity.
- [ ] 7.4 sync-specs at archive folds the delta (conversation-channel modified; still 12
  caps — no new capability).
