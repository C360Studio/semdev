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

- [x] 1.1 Register ALL new canonical predicates FIRST (beta.150 fails closed at graph-write
  on an unregistered predicate — a writer added before its registration cannot go green):
  `conversation.pending.{message-id,author,body}` (writer `conversation-adapter`),
  `conversation.intent.{value,message-id,author,reason}` + the
  `conversation.intent.classified` ledger (writer `conversation-classifier`),
  `conversation.classifier.dispatched` (the grp4 spawn rule's self-extinguishing marker —
  a rule `add_triple`, hence a graph write; writer `conversation-spawn-rule`), and
  `run.change.rejected` (writer `approval-adapter`). `internal/vocab.Register` + the vocab census.
- [x] 1.2 RED: `TestConversationIntentTaxonomyClosed` — `internal/conversationintent`
  declares exactly `{approve, reject, none}`; `Valid()`/`Names()` (mirrors `internal/taxonomy`).
- [x] 1.3 RED: `TestConversationTaxonomyMatchesPersonaContract` — the Go taxonomy == the
  `conversation` persona decision-contract fragment's intents. (The routing-RULE arm of the
  drift census lands in group 4 with the rules — noted, not asserted red here.)
- [x] 1.4 Implement `internal/conversationintent` + `configs/personas/fragments/conversation/`
  (00-identity + a decision contract: read the message, output exactly one intent + a
  reason, NEVER name an author, default `none` for anything short of an explicit directive —
  no approve from silence/reaction/ambiguity).

## 2. `classify_intent` — model supplies judgment, harness supplies identity (design D2)

- [x] 2.1 RED: `TestClassifyIntentTakesNoAuthorOrMessageID` — the tool schema exposes ONLY
  `intent` + `reason`; it takes NO `author` and NO `message_id` (the identity is
  harness-bound, not model-supplied — HIGH-2/H3). The G3 outcome-field census passes (no
  outcome boolean, no measurement fact).
- [x] 2.2 RED: `TestClassifyIntentStampsOnRunFromPending` — the tool subject-overrides to
  the RUN and stamps `conversation.intent`, `.message-id` + `.author` COPIED from the run's
  `conversation.pending.*` (matched by pending id), `.reason` (model echo), and
  appends the id to `conversation.intent.classified` — all on the run, none on the loop (H2).
- [x] 2.3 RED: `TestClassifyIntentRejectsOffTaxonomy` — an `intent` outside the closed set
  is rejected (the decide-allowlist pattern), so a hallucinated value cannot route.
- [x] 2.4 Implement `internal/tools/classifyintent` + schema + `boot.RegisterTools`
  registration (Source `conversation-classifier`, G5). Doc the G1 why-not-`decide` note
  (no message-id grounding; `decide`'s Source would collide with the coordinator lane).
  (Review folds: added `TestClassifyIntentIgnoresModelSuppliedIdentity` [H3 security
  regression pin — smuggled author/message_id in args is ignored] +
  `TestClassifyIntentDedupIsIdempotentOnRedelivery` [L2]; documented the load-bearing
  single-writer-per-run ledger invariant serialized by the grp4 spawn marker [M1].)

## 3. `handleMessage`: resolver phase getter, fast-path retained, NL bridge (design D4, D5)

- [x] 3.1 RED: `TestResolverSurfacesPhase` — the run resolver surfaces `agent.run.phase`
  (today it returns only `(runID, approved, err)`) so `handleMessage` can gate on
  `awaiting_approval` (M7). DONE: `ResolveRunByRef` → `(runID, approved, phase, err)`;
  callers/fakes (`intake/component.go`, both fakes) updated.
- [x] 3.2 RED: `TestExactApproveCommandStillDeterministic` — the whole-token `/semdev approve`
  fast-path is BYTE-IDENTICAL (stamps `run.change.approved`, no pending, no classifier); the
  migrated approval pins stay green. DONE: refactored into `releaseGate`/`stampGateFact`;
  the test also pins zero classifier-ledger reads on the fast-path.
- [x] 3.3 RED: `TestExactRejectCommandStampsRejected` — a whole-token `/semdev reject` stamps
  `run.change.rejected` deterministically (no model turn). (Cancellation is asserted in 5.x.)
  DONE (+ `TestExactRejectRefusedOnApprovedRun`: H1 — reject-on-approved is a no-op).
- [x] 3.4 RED: `TestNonCommandAuthorizedGatedMessageStampsPending` — a non-command message
  from an AUTHORIZED author on an `awaiting_approval` run stamps
  `conversation.pending.{message-id,author,body}` and ACKs; an UNAUTHORIZED author, a
  NON-gated run (phase ≠ awaiting_approval), and a message whose id is already in
  `conversation.intent.classified` each stamp NOTHING (no model turn). (The spawn marker
  `conversation.classifier.dispatched` is the grp4 spawn RULE's, not handleMessage's — the
  `dev.developer.dispatched` precedent: the rule stamps its own fire-once marker.) DONE
  (grp3-review M1 fold: the internal phase-gate precedes the external Authorize call, so
  non-gated chatter never spends a code-host permission call).
- [x] 3.5 RED: `TestPendingDedupByAppendSetLedger` — dedup is against the MULTI-VALUED
  `conversation.intent.classified` ledger (not single-valued latest-wins): a redelivered
  NON-latest classified id is not re-stamped (MEDIUM-4); a genuinely new id is. DONE
  (+ `TestNonCommandLedgerReadFaultRedelivers`: a ledger read fault is transient).
- [x] 3.6 Implement: the resolver phase getter; the exact-command fast-path (approve +
  reject); the authorized-non-command-gated → `conversation.pending.*` bridge (writer
  `conversation-adapter`) with append-set-ledger dedup. The spawn marker is grp4's. DONE:
  added `conversationintent.AdapterSource` + `PendingBodyPredicate`; G5 ties in
  `g5_writers_test.go` for all three pending predicates. Both reviewers APPROVE (zero
  blocking/high); M1 + nits folded, M2/L4 carried into grp5 (5.3, 5.5).

## 4. The spawn rule + the intent-routing rules (design D3, D6)

- [x] 4.1 RED: `TestBootstrapWiresConversationClassifierRules` — the pack bootstraps a spawn
  rule (`conversation.pending` @ `awaiting_approval`, guarded on the
  `conversation.classifier.dispatched` marker fire-once, `inherit` `role:conversation`,
  `tool_choice:required`, the intent-allowlist metadata, the pending body/author templated
  onto the prompt) and two routing rules (`conversation.intent==approve` / `==reject`, EACH
  gated on `agent.run.phase==awaiting_approval` AND both gate facts absent → publish to
  `component.conversation-apply.dispatch`). Also completes the taxonomy census routing-rule
  arm (1.3). DONE — delivered as four conformance tests (bootstrap wiring; spawn contract;
  route contract; release contract) + `TestConversationRoutingRulesMatchTaxonomy` swept over
  ALL packs. The "intent-allowlist metadata" phrasing was delivered as the in-tool
  closed-taxonomy validation (grp2) + the exactly-`[classify_intent]` advertised-tools pin
  (`action_allowlist` is a decide-only knob — grp4-review L4, task text synced not code).
- [x] 4.2 Implement the rules under `configs/rules/conversation/`. No lifecycle transition in
  any (G2). DONE — FIVE rules, not three (two structural discoveries): `01-anchor-gated-run`
  (a run entity never carries the bare `agent.loop.run` anchor before dev-from-task/01 fires
  at executing+approved, so the gate-time inherit-spawn needs its own anchor — the two-rule
  snapshot split; every other anchor reader also requires `run.change.approved`, so the early
  stamp is inert) and `04-classifier-terminal-release` (the fire-once marker must RESET for
  the run's next message — journey 6.4 needs two classifications; the release clears pending
  author→body→message-id then the marker, order pinned, ANY terminal incl. fault). Review
  folds (both reviewers, all blocking/high closed): explicit `max_iterations: 0` on the
  spawn + route actions (go-H1 — the engine's DEFAULT per-action firing cap of 3 per
  rule+entity would silently kill the NL lane on the run's 4th message; first rule in the
  repo designed to re-fire indefinitely on one entity); gate-facts-absent guards on the
  spawn (go-M1 — no dead paid turn on a gate-fact-bearing run); classify_intent READ-ONCE
  BINDING (semstreams HIGH-1 — the tool faults, stamping and deduping nothing, when the
  latest-wins pending slot no longer matches the dispatched marker id; red-first
  `TestClassifyIntentFaultsWhenPendingSlotMoved`); honest best-effort-action + marker-leak
  descriptions (go-M3/M4, semstreams MEDIUM-1 — `actionFailuresTotal` is the operator
  tripwire). Carried forward: 5.1/5.6 (go-M6 consumer serialization), 6.6/7.1 (live-config
  wiring, docs sync, the upstream atomic-multi-remove ask).

## 5. The apply consumer + the reject→cancel lane + the fault note (design D6, D7, D9, D11)

- [x] 5.1 RED: `TestApplyConsumerGateStillOpenReAuthorizeStamps` — the apply consumer: (a)
  re-checks the gate is OPEN (neither `run.change.approved` nor `run.change.rejected`
  present — a second racing intent is a NO-OP, H4a); (b) reads the cited author from
  `conversation.intent.author` (HARNESS-bound, NOT pending — H4b) and RE-RUNS `Authorize`
  (an unauthorized cited author → ZERO writes); (c) POSTS the transparency comment; (d)
  stamps `run.change.approved`/`rejected` via the ONE shared `approval-adapter` writer.
  **grp4-review M6 carry-forward:** the consumer acts on the run's CURRENT intent facts at
  consume time (the 03a/03b routes deliberately thread no snapshot) — pin that behavior, and
  either process dispatches serially per run (e.g. max-ack-pending 1 on the input port) or
  explicitly pin the both-facts safe-park outcome when two concurrent opposite dispatches
  interleave their gate-open reads (survivable by the D7 partition, but a serialized
  consumer never produces it).
- [x] 5.2 RED: `TestApplyPostFailureBlocksStamp` — a `Channel.Post` failure returns TRANSIENT
  and the gate fact is NOT stamped (transparency-before-effect; redelivery re-Posts, M6).
- [x] 5.3 RED: `TestRejectCancelsGatedRunOnly` — a run-lifecycle rule fires
  `awaiting_approval → cancelled` on `run.change.rejected`, PHASE-GUARDED to
  `awaiting_approval` (H1: it must NOT fire the legal `executing→cancelled` edge on an
  already-approved run); a bootstrap/rule-load pin covers the guard. **Cell-space
  partition (grp3-review M2):** the exact-command fast-paths can leave a still-gated run
  carrying BOTH gate facts, so the cancel rule MUST also carry `run.change.approved
  length_eq 0` AND the existing RESUME rule (`run-lifecycle/02`) MUST gain
  `run.change.rejected length_eq 0` — a run holding both facts transitions to NEITHER (a
  safe park), never both. Add a rule-load pin asserting both mutual-exclusion guards.
- [x] 5.4 RED: `TestClassifierFaultPostsFallbackNote` — a faulted classifier terminal (no
  `conversation.intent`, `agent.loop.outcome` faulted) triggers a fallback-to-command note
  post; a confident `none` posts nothing (HIGH-3).
- [x] 5.5 RED: `TestOnlySanctionedGateWriters` — the G5 census: the ONLY Source of
  `run.change.approved`/`rejected` is `approval-adapter`, and both the fast-path and the
  apply consumer route through one shared writer method (D11). Include the
  `TestToolSourceMatchesVocabWriter` ties for BOTH gate predicates
  (`approval-adapter`→`run.change.approved` and →`run.change.rejected`) — grp3-review L4
  (the gate facts are adapter-stamped, so their Source↔vocab tie belongs with this D11
  census, alongside the grp3 `conversation-adapter`→pending ties already added).
- [x] 5.6 Implement: the apply consumer (a declared jetstream input port on the
  conversation-channel component: gate-still-open → harness-bound Authorize → Post → stamp,
  transient-on-Post-failure) + the phase-guarded `run.change.rejected → cancelled`
  run-lifecycle rule + the faulted-classifier fallback-note rule + the shared writer method.

## 6. The NL bridge-proof journeys (regression guard for the intent lane)

- [x] 6.1 RED: `TestBridgeProofNLApprovalReleasesGate` — a run parked at `awaiting_approval`;
  an authorized author posts a NON-command NL approval to the forge double; the mock
  classifier reads approve; the transparency comment posts; `run.change.approved` lands; the
  run resumes — no exact command, no stand-in.
- [x] 6.2 RED: `TestBridgeProofNLRejectionCancelsRun` — a NL rejection → the transparency
  comment posts, `run.change.rejected` lands, the run reaches `cancelled`.
- [x] 6.3 RED: `TestConservativeNoneDoesNotApprove` — an ambiguous authorized message
  ("thanks!", 👍) classifies `none`; the run stays gated, no gate fact.
- [x] 6.4 RED: `TestConflictingIntentsResolveToOneTerminal` — two authorized NL messages, one
  approve + one reject, both classified; the run reaches EXACTLY ONE of resumed-or-cancelled
  and carries EXACTLY ONE gate fact, never both (the H4 gate-still-open guard end-to-end).
- [x] 6.5 The existing exact-command approval journeys (webhook + poll) pass byte-for-byte —
  no journey rewired; the NL journeys are ADDED.
- [x] 6.6a WIRE the NL lane into `configs/semdev-live-gemini.json` — DONE: the five (six
  files) `rules/conversation/*` entries, `classify_intent` in `allowed_tools`, and a
  `conversation` model_registry capability at the gemini tier (OQ4 decided: same tier as the
  other roles — classification is a short, cheap, high-stakes read, so it does not get a
  weaker model than the work it gates). Pinned by `TestLiveConfigCarriesTheNLLane`, because
  each missing piece fails differently silent (dead rules / "tool not allowed" per message /
  a capability fallback that misroutes rather than errors).
- [ ] 6.6b A real-LLM classification probe (env-gated `SEMDEV_REAL_LLM=1`): the persona
  classifies a real approval + rejection + ambiguous message correctly against the live
  model; recorded per the runbook. Decide the classifier model tier (OQ4) — and WIRE the
  NL lane into `configs/semdev-live-gemini.json` with it (grp4-review MEDIUM-2/L5: the
  five `rules/conversation/*` rules_files entries, `classify_intent` in `allowed_tools`,
  and a `conversation` model_registry capability at the chosen tier — the bootstrap census
  pins the mock config only, and an unknown capability silently falls back to
  `defaults.model`, so the live lane stays dead-or-misrouted until this lands).

## 7. Spec + docs + verification + review + archive

- [ ] 7.1a RECORD in the change docs: `conversation/05` publishes a HUMAN-VISIBLE comment
  with no self-extinguish marker, so on RULE_STATE loss every still-matching historical
  conversation loop re-posts the fallback note to its thread (rule 04 shares the unguarded
  shape but its actions are idempotent removes). Decide deliberately between a marker and
  accepting the replay exposure alongside the 04 replay note.
- [ ] 7.1 The `conversation-channel` delta matches the code. Docs: the NL-intent gate in the
  runbook (approve in prose; the exact command still works; a rejection cancels a GATED run;
  NL-approve is not reversible via NL — the PR merge is the downstream stop). grp4-review
  doc items: note dev-from-task/01's dormancy on live paths (superseded by conversation/01's
  gate-time anchor — go-L3); name the marker-leak posture in the runbook (a wedged/never-
  terminal classifier closes the NL lane silently for that run; exact command recovers;
  `actionFailuresTotal` is the tripwire — go-M4) with the marker-leak reconciliation as a
  named pre-production follow-up; FILE the upstream semstreams ask for an atomic
  multi-remove (or abort-on-first-failure `on_enter`) — best-effort action continuation is
  what leaves the stale-marker brick reachable (semstreams MEDIUM-1; house-wide value: every
  marker-before-publish rule shares the inversion). Two named LOWs from the grp4 re-review:
  (a) classify_intent's deterministic contract faults (slot-moved / marker-absent) retry to
  the loop's iteration cap before faulting terminal — a framework faulted-stop
  (StopLoop-on-contract-violation) would short-circuit the dead paid turns (grp5-able or an
  upstream note); (b) under compound RULE_STATE loss a replayed release + fresh spawn can
  re-arm the marker to a NEW id while an old classifier is in flight — strictly narrower
  than the closed HIGH-1 window, bounded by the deterministic consumer + transparency +
  PR backstop; name it in 04's replay notes.
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
