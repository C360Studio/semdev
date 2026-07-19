## 0. Prerequisite (already satisfied)

- [x] 0.1 semstreams **#569** (`agent.loop.terminal-reason` stamped from `LoopFailedEvent.Reason`) landed in beta.153 — verified in the module cache (`graph_writer.go:897`); the reason values (`max_iterations`/`model_error`/`handler_error`) confirmed in `component.go`/`handlers.go`
- [x] 0.2 Engine facts verified against beta.153 (design "Engine facts"): array ops on an absent predicate see an empty array; `eq`/`ne required:false` on an absent field returns false; the reason is on the firing developer loop; **and (added after the live race) each rule action is its own KV revision (`triple_mutator.go`) with debounce-flush fetched-state evaluation (`entity_watcher.go`) — same-pass rule firings do NOT give same-revision stamps**

## 1. Vocabulary (G5/G9)

- [x] 1.1 Declare `route.attempt.transient` (writer `route-mirror` — the D1 ATOMIC mirror classification, "true"/"false" always stamped by check_floors; REVISED from the first cut's rule-stamped OR-collapse, which raced — see design facts 5+6)
- [x] 1.2 Declare `task.transient.instance` (writer `dev-dispatch-rule`, the run-side transient-retry counter appended by 06f at spawn — the `task.attempt.instance` precedent, multi-valued)
- [x] 1.3 Declare `route.transient.instance` (writer `route-mirror`, the developer-loop mirror of `task.transient.instance` counted via `length_*` — the `route.attempt.instance` precedent)
- [x] 1.4 Confirm all three are canonical 3-seg so `vocab.Register` accepts them

## 2. The mirror (Go — the only product code)

- [x] 2.1 `internal/tools/checkfloors`: read the run's `task.transient.instance` (the same shape as the existing `task.attempt.instance` read) and mirror `route.transient.instance` onto L_n in the SAME `ReplaceTriples` pass as `route.attempt.*` / `route.task.budget`
- [x] 2.2 NO D7 loud-fault for the counter: an absent counter is a valid 0 (fact 1 — array op over empty), and the cap is a LITERAL not a substitution, so there is no fail-open coerce-error wedge
- [x] 2.3 Unit pin (checkfloors): given `task.transient.instance` with N objects, the mirror stamps N `route.transient.instance` on L_n; given none, none is stamped (count 0)
- [x] 2.4 **THE RACE FIX (D1 revised)**: read L_n's `agent.loop.terminal-reason` (`readTerminalReason`, absent = completed loop = not transient), classify against the fixed transient set {model_error, handler_error}, and stamp `route.attempt.transient` "true"/"false" — ALWAYS — in the SAME `ReplaceTriples` as `route.attempt.passed`, with the flag in the replace-predicates set (single-valued upsert)
- [x] 2.5 Unit pins for 2.4: the classification table (absent/max_iterations/graph_state_reset_required → "false"; model_error/handler_error → "true"; `TestCheckFloorsMirrorTransientFlagClassification`) and the ATOMICITY pin (`TestCheckFloorsMirrorTransientFlagAtomicWithPassed` — the flag rides the same ReplaceTriples as passed; fails if it ever moves to its own write or back to a rule-stamped collapse)

## 3. Reason classification

- [x] 3.1 ~~Two rule-side OR-collapse rules~~ **REPLACED by the mirror classification (2.4)** after `TestBridgeProofTransientGraceRetries` caught the double-dispatch live (~50% under `-race`): a rule-stamped collapse lands in its own KV revision and the convergence routes' exclusion races it (design facts 5+6). NO rule stamps `route.attempt.transient` — pinned by the census in `TestTransientRouteTotalityAndSelfExtinguish`
- [x] 3.2 GENUINE terminals (`max_iterations`, absent) classify "false" (the classification-table pin)

## 4. Route rules (rule-native, G2)

- [x] 4.1 `06c-route-retry` + `06d-route-escalate`: exclude transient via `route.attempt.transient eq "false" (required:false)` — a condition PARTITION over the atomic mirror snapshot (REVISED from `length_eq 0`, the racy absence-guard shape)
- [x] 4.2 `06f-route-transient-retry`: `route.attempt.transient eq "true"` AND `route.attempt.unclean eq "true"` (REVISED from `passed eq "false"` — adversarial review caught the passed-gate stranding the passed∧rejected∧transient cell unrouted, a permanent silent stall; unclean restores totality, see D1) AND `route.transient.instance length_lt 2` AND `route.attempt.routed length_eq 0` → self-extinguish before publish, append `task.transient.instance` at spawn (NOT `task.attempt.instance`), re-dispatch a fresh developer loop
- [x] 4.3 `06g-route-transient-park`: `route.attempt.transient eq "true"` AND `route.attempt.unclean eq "true"` AND `route.transient.instance length_gte 2` AND self-extinguish → park (`run.awaiting.human`) + post to the user bus, NO lifecycle transition (G2)
- [x] 4.4 Reason-aware message (D5): `06d` and `06g` substitute `$entity.triple.agent.loop.terminal-reason.value` into the `run.awaiting.human` object
- [x] 4.5 Each new/edited rule's `description`/`metadata` describes the transient/genuine split, the CAP, and WHY the flag is a mirror fact (G10); every rule carries `entity.pattern`

## 5. Conformance pins

- [x] 5.1 Transient-partition totality pin (`TestTransientRouteTotalityAndSelfExtinguish`): retry `length_lt 2` / park `length_gte 2` partition the transient count (numeric-literal-typed, not string-coercible); both gate on the atomic flag + unclean; NO rule stamps the flag (the census)
- [x] 5.2 `06f` (`dev_from_task_route_transient_retry`) added to `TestOnlySanctionedDeveloperSpawners` (four sanctioned spawners: 04/06c/07b/06f) and covered by `TestEveryModelSpawnDeclaresToolsAllowlist` + `TestSpawnPromptToolsAreAdvertised`
- [x] 5.3 G5 mirror-writer census (`g5_writers_test.go`): `route.transient.instance` AND `route.attempt.transient` stamped by `check_floors` under `route-mirror`
- [x] 5.4 Convergence-route pins (`TestFloorsRouteTotalityAndSelfExtinguish`) assert the `eq "false"` exclusion on 06c/06d (and would fail on the racy absence-guard shape)
- [x] 5.5 `#529`/`#569` regression guards in `upstream_asks_test.go` (source-anchored `TestTripwire569TerminalReasonFact`); the `#529` note flipped to "adopted"; registered in the manifest
- [x] 5.6 Offline rule-load gate (`test/ruleload`) green; docs vocab table carries the three predicates with the mirror writer (G10)
- [x] 5.7 The 8-cell ROUTE-TOTALITY census (`TestFloorsRouteCellTotalityCensus`): every `passed × rejected × transient × count-regime` cell owned by EXACTLY ONE of 06a/06c/06d/06f/06g, with unclean derived exactly as 06b/06e stamp it — red-verified against the stranded-cell gate (the offline pin for the unrouted-cell class)
- [x] 5.8 The disk⊆bootstrap reverse census (`TestEveryRuleFileIsBootstrapped`): every rule file on disk is in the bootstrap rules_files — the offline pin for the silently-never-loaded-rule shape this change hit mid-WIP (06f/06h sat inert until the bootstrap gained their entries)

## 6. Evidence (e2e, `-race` per #566 fixed)

- [x] 6.1 `TestBridgeProofTransientGraceRetries`: the initial dispatch fails `model_error` (mockllm HTTP-500 error fixture), the transient route re-dispatches WITHOUT incrementing the convergence budget (`task.attempt.instance` stays 1, `task.transient.instance` stamped), the retry succeeds → advance → review → verify → deliver. This journey CAUGHT the first cut's double-dispatch live (the red-first docker pin for the race)
- [x] 6.2 Mock harness: `internal/mockllm` error fixture (HTTP-500 reverse proxy) drives a real `LoopFailedEvent Reason=model_error` terminal
- [x] 6.3 Reason-aware park assertion: the budget=1 escalate journey asserts 06d's park message carries the reason-aware template AND no unresolved substitution token
- [x] 6.4 `TestBridgeProofTransientCapParks`: three transient deaths (dispatch + both graced retries 500) → 2 graces consumed → 06g parks naming the SUBSTITUTED reason (model_error, non-empty — 06d's journey only proves the empty case); convergence budget still 1
- [x] 6.5 Full offline ladder green; transient journey repeated ≥6× green `-race` (the race reproduced ~50% pre-fix); all docker journeys green `-race` (re-run after the unclean-gate revision)

## 7. Deferred follow-ups (documented, NOT in this change)

- [ ] 7.1 A per-task transient-cap field (author + project + clamp + mirror, the #519 treatment) — a separate future change; the CAP stays a constant here (D4)
- [ ] 7.2 Review-loop (07-series) transient grace — 07d's fail-closed no-verdict park covers a dead review loop today (D6)
- [ ] 7.3 An upstream semstreams ask, if same-pass rule-action atomicity is ever wanted engine-side (batch a pass's add_triples into one revision) — not needed by this change (the mirror owns the atomic snapshot), recorded as an engine fact instead
- [ ] 7.4 e2e infra hardening (observed during this change's verification, NOT caused by it): the journey runtime inherits the framework's default service-manager HTTP port 8080, which collides with any co-resident stack publishing 8080 (an unrelated `semsource` container triggered one run where the bind failed and a JetStream `consumer not active` storm wedged the FRONT-of-arc — before any dev-loop surface). Configure an explicit ephemeral `http_port` for the e2e runtime; investigate whether the consumer storm is downstream of the failed bind

## 8. Ship

- [ ] 8.1 Adversarial review (`semstreams-reviewer` + `go-reviewer`) — the first cut passed structurally, then the docker journey falsified the pass-timing assumption; the revised (atomic-mirror) mechanism must be re-reviewed
- [ ] 8.2 `openspec validate --strict` green; conventional commit; push
