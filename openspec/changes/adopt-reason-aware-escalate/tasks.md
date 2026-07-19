## 0. Prerequisite (already satisfied)

- [x] 0.1 semstreams **#569** (`agent.loop.terminal-reason` stamped from `LoopFailedEvent.Reason`) landed in beta.153 — verified in the module cache (`graph_writer.go:897`); the reason values (`max_iterations`/`model_error`/`handler_error`) confirmed in `component.go`/`handlers.go`
- [x] 0.2 Engine facts verified against beta.153 (design "Engine facts"): array ops on an absent predicate see an empty array (`length_eq 0`/`length_lt N` semantics); `eq`/`ne required:false` on an absent field returns false; the reason is on the firing developer loop

## 1. Vocabulary (G5/G9)

- [ ] 1.1 Declare `route.attempt.transient` (writer `dev-route-rule`, the OR-collapse intermediate — model_error OR handler_error, stamped "true" on the developer loop; the `route.attempt.unclean` precedent)
- [ ] 1.2 Declare `task.transient.instance` (writer `dev-dispatch-rule`, the run-side transient-retry counter appended by 06g at spawn — the `task.attempt.instance` precedent, multi-valued)
- [ ] 1.3 Declare `route.transient.instance` (writer `route-mirror`, the developer-loop mirror of `task.transient.instance` counted via `length_*` — the `route.attempt.instance` precedent)
- [ ] 1.4 Confirm all three are canonical 3-seg (`route.attempt.transient` / `task.transient.instance` / `route.transient.instance`) so `vocab.Register` accepts them

## 2. Transient-counter mirror (Go — the only product code)

- [ ] 2.1 `internal/tools/checkfloors`: read the run's `task.transient.instance` (the same shape as the existing `task.attempt.instance` read) and mirror `route.transient.instance` onto L_n in the SAME `ReplaceTriples` pass as `route.attempt.*` / `route.task.budget`
- [ ] 2.2 NO D7 loud-fault here: an absent counter is a valid 0 (fact 1 — array op over empty), and the cap is a LITERAL not a substitution, so there is no fail-open coerce-error wedge — the mirror simply reflects the (possibly empty) transient set
- [ ] 2.3 Unit pin (checkfloors): given `task.transient.instance` with N objects, the mirror stamps N `route.transient.instance` on L_n; given none, none is stamped (count 0), and no route mirror predicate is dropped

## 3. Reason classification (rule-native OR-collapse, G2)

- [ ] 3.1 `06f-reason-transient-model` + `06f-reason-transient-handler`: `agent.loop.role eq "developer"` AND `agent.loop.terminal-reason eq "<model_error|handler_error>" (required:false)` AND `route.attempt.transient length_eq 0` (self-extinguish) → stamp `route.attempt.transient="true"` (two guarded pure-AND rules, the 06b pattern; no logic:or)
- [ ] 3.2 Confirm GENUINE terminals (`max_iterations`, or an absent reason for completed-but-red) never stamp `route.attempt.transient` (eq required:false → false on absent/other)

## 4. Route rules (rule-native, G2)

- [ ] 4.1 `06c-route-retry` + `06d-route-escalate`: add `route.attempt.transient length_eq 0` (exclude transient — TRUE when absent/genuine per fact 1, FALSE when transient stamped) so a transient terminal cannot fire the convergence routes (mutual exclusion, one-in-flight preserved)
- [ ] 4.2 `06g-route-transient-retry`: `route.attempt.transient eq "true" (required:false)` AND `route.transient.instance length_lt <CAP>` AND `route.attempt.routed length_eq 0` → self-extinguish (route.attempt.routed before publish), append `task.transient.instance` at spawn (NOT `task.attempt.instance`), re-dispatch a fresh developer loop (prompt: prior attempt hit a transient infra error, proceed with the task)
- [ ] 4.3 `06h-route-transient-park`: `route.attempt.transient eq "true" (required:false)` AND `route.transient.instance length_gte <CAP>` AND self-extinguish → park (`run.awaiting.human` on the run) + post to the user bus, NO lifecycle transition (G2)
- [ ] 4.4 Reason-aware message (D5): `06d` and `06h` substitute `$entity.triple.agent.loop.terminal-reason.value` into the `run.awaiting.human` object (empty reads as "no engine failure reason — the last attempt measured red")
- [ ] 4.5 Each new/edited rule's `description`/`metadata` describes the transient/genuine split + the CAP (G10); every rule carries `entity.pattern`

## 5. Conformance pins

- [ ] 5.1 Transient-partition totality pin: `route.attempt.transient` present → exactly one of {06g retry (`length_lt CAP`), 06h park (`length_gte CAP`)} fires; absent → neither (the convergence routes own it). No gap/overlap across the transient count `[0,CAP+1]`
- [ ] 5.2 `06g` added to `TestOnlySanctionedDeveloperSpawners` (the one-in-flight invariant — now four sanctioned spawners: 04/06c/07b/06g) and to `TestEveryModelSpawnDeclaresToolsAllowlist` + `TestSpawnPromptToolsAreAdvertised`
- [ ] 5.3 G5 mirror-writer census (`g5_writers_test.go`): `route.transient.instance` stamped by `check_floors` under `route-mirror`; single-writer pins for all three new predicates
- [ ] 5.4 Convergence-route pins (`TestFloorsRouteTotalityAndSelfExtinguish`) updated to assert the new `route.attempt.transient length_eq 0` exclusion guard on 06c/06d (transient cannot fire them)
- [ ] 5.5 A `#529`/`#569` regression guard in `upstream_asks_test.go` (source-anchored): `agent.loop.terminal-reason` stamped from `event.Reason` in `graph_writer.go` + the `ErrMaxIterationsReached` sentinel; flip the `#529` "MECHANICAL UPGRADE (not adopted)" note to "adopted"; register in the manifest
- [ ] 5.6 Offline rule-load gate (`test/ruleload`) green; docs vocab table gains the three predicates (G10)

## 6. Evidence (e2e, `-race` per #566 fixed)

- [ ] 6.1 `TestBridgeProofTransientGraceRetries`: a developer loop fails with `model_error`, the transient route re-dispatches WITHOUT incrementing the convergence budget (assert `task.attempt.instance` count unchanged, `task.transient.instance` +1), then the retry succeeds → advance → deliver. Requires the mock to script a loop-failure terminal with `terminal-reason=model_error` (see 6.2)
- [ ] 6.2 Mock harness: script a `model_error` loop terminal (a `LoopFailedEvent` with `Reason=model_error`) — verify `internal/mockllm` / the ssmock server can emit a failed-loop terminal; if not expressible, record the gap and prove the routing offline (ruleload + a rule-level pin) with the e2e labeled accordingly
- [ ] 6.3 Reason-aware park assertion: on genuine budget exhaustion the `run.awaiting.human` object carries the terminal reason (extend the existing exhaustion journey's park assertion)
- [ ] 6.4 Full offline ladder + all docker journeys green `-race`

## 7. Deferred follow-ups (documented, NOT in this change)

- [ ] 7.1 A per-task transient-cap field (author + project + clamp + mirror, the #519 treatment) — a separate future change; the CAP stays a constant here (D4)
- [ ] 7.2 Review-loop (07-series) transient grace — 07d's fail-closed no-verdict park covers a dead review loop today (D6)

## 8. Ship

- [ ] 8.1 Adversarial review (`semstreams-reviewer` + `go-reviewer`) — both pass
- [ ] 8.2 `openspec validate --strict` green; conventional commit; push
