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
  **RE-TICKED 2026-08-11 with the contract genuinely met (8.4 / design D15).** The earlier
  tick was against a test whose second message the phase gate discarded (one classification,
  a dead second fixture, no `requireModelTurns` — external review #10, confirmed ours). The
  rework: the forge double's one-shot `HoldNextCreateComment` barrier blocks the apply
  consumer's transparency Post (post-before-stamp, D6/M6 — the deterministic hold point), so
  BOTH opposite intents classify against a still-open gate; `requireModelTurns(5)` proves
  both fixtures consumed; zero decisions while held (post-before-stamp end-to-end); the
  winner is asserted by SHAPE not value (one decision, its matching terminal, still exactly
  one after settle — every interleaving of first-writer-wins is legal). The old test is
  KEPT, renamed `TestLateIntentAfterGateClosesIsIgnored`, its dead fixture REMOVED, and its
  real contract pinned by ledger counts (classified==1, attempted==1 — a model-turn total
  there would race the dev-rewake decide). GREEN on real docker with -race.
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

## 8. Post-review corrections — RUNS BEFORE GROUP 7 (design D12–D15)

A 2026-07-21 external review filed 16 findings. Each was verified against the code before any
was acted on: 3 are REFUTED (G5 "one logical writer" is the documented pattern; the station's
`context.Background` rooting is deliberate), 3 are pre-existing openly-tracked deferrals (R8,
task 10.4) that must NOT be reopened here, and the SECURITY (token on `git push` argv,
`git add -A` escaping the task contract, devcontainer `..` traversal), PAID-RUN readiness,
station-panic, and evidence-ledger findings belong to their OWN changes — they are NOT in
scope for this one. The four below are defects in what THIS change built. Archiving without
them would sync a spec asserting behavior the code lacks (G10).

- [x] 8.1 RED-first: the **gate-open watermark** (D12). **(b) LANDED** with 8.2 (they share
  the resolver): deterministic active-run resolution + `RunState`, pinned by
  `TestResolverPrefersTheActiveGatedRun` and mutation-verified. **(a) DONE** — the sequencing hazard the
  review named is closed. The watermark reads the FRAMEWORK's declared
  `agent.run.last-transition-at` audit fact, NOT the phase triple's `Timestamp` metadata: a
  security guard must read something a contract promises is populated, or it degrades
  silently and in the fail-OPEN direction. A 30s `gateWatermarkSkew` covers the poll path's
  cross-clock comparison (code-host `created_at` vs a semdev-stamped gate-open). Fails CLOSED
  on an unestablishable watermark or an untimestamped message. All pins mutation-verified. `ResolveRunByRef` additionally
  returns `gateOpenedAt` (the `agent.run.phase` triple's `Timestamp` while the phase is
  `awaiting_approval`) and resolves DETERMINISTICALLY to the active run (awaiting-approval
  preferred, newest gate-open next, entity ID as a stable tiebreak) instead of first-match-in-
  page-order. BOTH inbound paths — the NL bridge AND the exact-command fast-path — require
  `msg.At > gateOpenedAt`; an at-or-before message is definitively dropped (acked, counted,
  logged LOUD), never redelivered. A run at the gate with NO usable phase timestamp FAILS
  CLOSED (refuse + loud log), never honored. Pins: historical NL approval on a second run over
  a reused issue; historical exact command likewise; a poller restart re-reading a thread from
  cursor zero; the missing-watermark fail-closed path; and a fresh message still approving
  normally. NOTE the accepted consequence: PRE-APPROVAL (an approve typed before the proposal
  exists) no longer releases the gate — assert that explicitly rather than letting it regress
  silently.

- [x] 8.2 RED-first: **ONE single-valued decision fact** (D13). Retire
  `run.change.approved` + `run.change.rejected`; introduce canonical `run.change.decision` ∈
  {`approve`,`reject`} (writer `approval-adapter`), registered in `internal/vocab` BEFORE any
  writer (beta.150 fails closed). `stampDecision` is the ONE shared writer: it reads the
  current decision and REFUSES to change a decided run (approve-on-rejected and
  reject-on-approved are both no-ops — H1 generalized symmetrically), same-value replay stays
  idempotent. Migrate in ONE commit: `run-lifecycle/01,02,07`, `conversation/01,02,03a,03b`,
  `dev-from-task/01,02,03`, `sandbox/01`, the resolver's `approved bool` getter, `approval.go`,
  `apply.go`, the D11 census, the journeys' stand-in writes. Pins: the previously-wedging
  sequence (`/semdev reject` then `/semdev approve` on a gated run) now yields ONE decision and
  ONE terminal; a decision arriving after the run left `awaiting_approval` is inert (phase
  guard); `TestEveryRuleFileIsBootstrapped` + the rule-condition censuses catch a
  half-migrated predicate.

- [x] 8.3 RED-first: the **classifier spend bound** (D14) — DONE 2026-08-11. As designed:
  the spawn rule appends the dispatched id onto `conversation.classifier.attempted`
  (writer `conversation-spawn-rule`, vocab-registered) in the same action set as the
  marker — append BEFORE publish, so a best-effort action failure errs spend-safe — and
  gains `length_lte 2` (N=3; the guard carries N-1 because length_lte evaluates before the
  appending fire). `06-classifier-budget-exhausted` announces exhaustion once per run on
  `user.note.>` behind its own `conversation.budget.noted` marker (stamped before the
  publish), with the LOAD-BEARING `conversation.classifier.dispatched length_eq 0` guard:
  the ledger already reads full while attempt N is mid-flight, so without it the note fires
  against a message being classified, not refused. The publish carries
  `properties.note=budget-exhausted`; the note consumer selects the escape-hatch body by it
  (the fault note publishes bare; the envelope decodes properties as map[string]any so a
  non-string property degrades instead of killing every note — unit-pinned). Conformance
  pins RED-verified first (spawn budget guard + append order; release never clears the
  ledger; the full budget-rule contract). Journey `TestClassifierBudgetCapsSpend` GREEN on
  docker -race: N+2 messages → EXACTLY N=3 dispatches (`requireModelTurns(6)` at the
  deterministic pre-refusal point; end-state ledger counts after the release, which cannot
  move once executing), the note posts exactly once, and `/semdev approve` still releases
  the gate after exhaustion. Both shipped configs wired (+ version bumps 0.31.0/0.29.2,
  mock still greater).

- [x] 8.4 RED-first: the **reworked conflict journey** (D15) — DONE 2026-08-11, re-ticks 6.4
  (see 6.4's tick for the delivered mechanism: the `HoldNextCreateComment` one-shot barrier,
  both-classified against the open gate, one decision by shape, the renamed late-intent pin
  with its dead fixture removed). GREEN on docker -race. ALSO LANDED HERE — **the six red
  journeys' ONE root cause**: the forge double served a HARDCODED `created_at`
  ("2026-07-20T12:00:00Z", protocol-faithful when the field was decorative), so the 8.1a
  watermark — working exactly as designed — dropped every journey comment as historical and
  the whole approval lane died in every journey that drives it. The double now stamps
  `CreatedAt` at append time (both AddComment and the bot's create_comment) and serves it
  RFC3339. All six previously-red journeys verified GREEN with -race, including
  `TestBridgeProofApprovalByPollNoWebhook` — the ARCHIVED pull-first-transport capability is
  no longer red. New helper `requireTripleClears` for marker-release waits
  (`requireRunTripleCount` fails fast on over-count — right for monotonic ledgers, wrong for
  a 1→0 marker).

- [x] 8.5a The `run.change.decision` migration touches THREE canonical capability specs,
  not one. Deltas for `run-lifecycle` (the gate requirement: single-valued, first-writer-
  wins, phase-guarded commands) and `dev-from-task` (the projection trigger) ship WITH this
  change — archiving with only the `conversation-channel` delta would leave two canonical
  specs asserting a predicate the code no longer has, which is the exact failure group 8
  exists to prevent, aimed at a different capability. 7.4's "still 12 caps" holds: three
  capabilities MODIFIED, none added.

- [x] 8.5 Cheap truth fixes folded in — DONE 2026-08-11: the live config's fault-note port
  description now says the conversation pack IS wired (6.6a); `apply.go` says nine park
  rules (verified: nine rule files stamp `run.awaiting.human`); rule 05's description now
  cites `TestClassifierBindingFaultTellsTheHuman` for the one-tick observation (the old
  citation pointed at the journey 8.4 renamed).

- [x] 8.6 Adversarial review — BOTH reviewers, zero blocking/high, all findings applied.
  **ROUND 2 DONE 2026-08-11** (on the full 8.3/8.4/8.5 + journey-fix working tree):
  go-reviewer 0B/1H/3L/3N, semstreams-reviewer 1B/1H/1M/2L/2N — every finding folded, then
  BOTH finding authors re-verified their own fixes and returned **APPROVE (0 blocking/high)**.
  Round 2's catches, so they are not re-derived: **BLOCKING (semstreams)** — the 8.3 spawn
  order (ledger-append before marker) exposed an intermediate revision on the run's FINAL
  allowed spawn (ledger full, marker absent — each action is its own KV revision, every
  revision evaluated per-rule) that satisfied rule 06's entire condition set: the exhaustion
  note fired DURING the last classification, once-marker spent, and the journey was
  structurally blind to it. Fixed marker→ledger→publish, red-first pin
  (marker-before-ledger with the intermediate-revision rationale), both rule descriptions
  truth-swept (the "errs spend-safe" ordering claim was FALSE — publish-last is the only
  spend protection; a failed append with a surviving publish is an uncounted turn in ANY add
  order). **HIGH (both reviewers independently)** — the D15 one-shot hold, consumed at
  arrival, did not survive the posting client's fixed 10s HTTP timeout: the apply consumer's
  retry sailed through and stamped while the journey believed the gate held (a coin-flip
  flake indicting a product violation that did not happen). Fixed: an aborted held request
  RE-ARMS the hold and applies nothing, so retries block until release; verified persistent
  through retry exhaustion into NATS redelivery. **MEDIUM (semstreams)** — the fault note's
  "try again" invitation was budget-blind; noteBody now selects the escape hatch whenever
  the run's attempted ledger is full, any kind, unit-pinned. Two documented-benign
  residuals: a release/abort coincidence can duplicate the transparency COMMENT
  (at-least-once, nothing counts comments, the decision stamps once), and the
  retry-before-FIN window degrades to a visible flake, never a silent green. Evidence:
  offline suites + conformance green under -race; the five affected journeys green on
  docker -race (174s); the FULL suite green pre-fold (521s, zero skips).
  ROUND 1 (on the 8.2 diff) was folded earlier. What round 1 found, so it is not re-derived: **BLOCKING** — migrating
  `run-lifecycle/01`'s guard to `decision length_eq 0` was NOT meaning-preserving (the old
  guard was blind to rejection), so a pre-gate `/semdev reject` through the UNGUARDED
  `releaseGate` made the gate unreachable and the run unrecoverable — one wedge closed, a
  worse one opened one rule over. Fixed by phase-guarding `releaseGate` (which is also D12's
  uniform rule) + `TestExactCommandOnUngatedRunIsIgnored`. **HIGH** — a silent refusal let the
  apply consumer announce a decision it did not take (its guard-1 read and its write are
  separated by THREE external round-trips, not the "sub-millisecond" the design claimed);
  `stampDecision` now returns what stands and the consumer posts a correction. **HIGH** — the
  full-page scan discarded a match found before page exhaustion, which would have silently
  killed `/semdev approve` on every run past ~16k entities. **MEDIUM** — the retired-NAME
  census could not see a wrong VALUE; a planted `"approved"` typo passed the whole suite
  green, so `TestDecisionConditionsUseTheLegalEnum` was added and verified against it.
  Focus: the watermark's fail-closed path and clock-skew posture, the decision-fact migration
  completeness (no rule left reading a retired predicate), the spend bound's honesty (does it
  count what it claims to count), and whether the reworked conflict journey can go vacuous-green.

## 7. Spec + docs + verification + review + archive

**RUNS LAST — after group 8.** Archiving before the group-8 corrections would sync a spec
asserting behavior the code does not have (G10). 7.1's spec delta must describe
`run.change.decision` (D13), the watermark (D12), and the spend bound (D14) — not the retired
two-fact partition.

- [ ] 7.1 The `conversation-channel` delta matches the code. Docs: the NL-intent gate in the
  runbook (approve in prose; the exact command still works; a rejection cancels a GATED run;
  NL-approve is not reversible via NL — the PR merge is the downstream stop). grp4-review
  doc items: note dev-from-task/01's dormancy on live paths (superseded by conversation/01's
  gate-time anchor — go-L3); name the marker-leak posture in the runbook (a wedged/never-
  terminal classifier closes the NL lane silently for that run; exact command recovers;
  `actionFailuresTotal` is the tripwire — go-M4) with the marker-leak reconciliation as a
  named pre-production follow-up — the reconciliation should ALSO sweep the exhaustion
  residue (8.6 round-2 NOTE: after a spent budget + an exact-command decision, the last
  refused message's `conversation.pending.*` trio stays on the run forever — no classifier
  terminal will fire the release again; harmless under the phase gates, but it reads as
  "in flight" forensically); FILE the upstream semstreams ask for an atomic
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
