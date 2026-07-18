## 1. Upstream prerequisite (blocks all implementation)

- [ ] 1.1 semstreams **#568** (`length_gte`/`length_lte`) lands in a beta; bump the pin and `go mod tidy`
- [ ] 1.2 Re-verify on that beta: `length_gte` uses the missing-predicate→empty-array semantics of the sibling length operators (a rule condition `{operator: length_gte, value: 0}` matches an absent predicate); update the design Open Question if it diverges
- [ ] 1.3 Add a `length_gte`/`length_lte` regression guard to `test/conformance/upstream_asks_test.go` (the operator family is now complete)

## 2. Vocabulary (G5/G9)

- [ ] 2.1 Declare `route.task.budget` in `internal/vocab` (writer `route-mirror`, capability `dev-from-task`, introduced-by this change)
- [ ] 2.2 Confirm `route.task.budget` is canonical 3-seg so `.value` arity-disambiguates (`vocab.Register` panic-guard covers it)

## 3. Budget mirror (Go — the only product code, on the existing sanctioned mirror)

- [ ] 3.1 `internal/tools/checkfloors`: read the run's `task.spec.budget` (one `reader.ReadFacts(ctx, runEntityID, "task.spec.budget")`, same shape as the existing `measurement.result.*` / `task.attempt.instance` reads)
- [ ] 3.2 Stamp `route.task.budget` on L_n in `routeMirrorTriples` alongside `route.attempt.*`, under the `route-mirror` owner (raw copy of the clamped scalar; no re-clamp, no derived value — G3/G5)
- [ ] 3.3 Unit pin: given a run with `task.spec.budget=B`, the floors mirror stamps `route.task.budget=B` on the dispatch loop entity; absent budget → mirror behaves fail-closed (define: default to the min clamp or park — decide and pin)

## 4. Route rules (rule-native, no Go decision layer — G2)

- [ ] 4.1 `06c-route-retry` + `07b-review-retry`: swap `route.attempt.instance length_lt 3` → `length_lt $entity.triple.route.task.budget.value`
- [ ] 4.2 `06d-route-escalate` + `07c-review-park`: swap `route.attempt.instance length_gt 2` → `length_gte $entity.triple.route.task.budget.value`
- [ ] 4.3 Update each rule's `description`/`metadata` prose to describe the per-task boundary (G10 — no stale "constant 3" claims)

## 5. Conformance pins

- [ ] 5.1 Update the floors/review route-totality + fail-closed-partition pins (`TestFloorsRouteTotalityAndSelfExtinguish`, `TestReviewRouteTotalityAndSelfExtinguish`) to the variable `length_lt B` / `length_gte B` boundary (no gap, no overlap across `[1,5]`)
- [ ] 5.2 Add `route.task.budget` to the route-mirror allowlist in the G2 route-token census (a sanctioned mirror, NOT a Go-derived decision token — `TestNoGoDerivedRoutingTokens` must stay green)
- [ ] 5.3 G5: `route.task.budget` single-writer pin (writer == `route-mirror`, matching the stamping source)
- [ ] 5.4 Flip the #519 "MECHANICAL UPGRADE (not yet adopted)" note in `upstream_asks_test.go` to "adopted"; keep the #528/#529 notes as deferred with the D5/D4 rationale
- [ ] 5.5 Offline rule-load gate (`test/ruleload`) green with the substituted conditions

## 6. Evidence (e2e, without `-race` per #566)

- [ ] 6.1 `TestBridgeProofBudgetExhaustionParks`: author an explicit `task.spec.budget=2`; assert exactly 2 attempts run, then the run parks (no 3rd `task.attempt`, no `verify.cleanroom.result`, no `pr.ref`)
- [ ] 6.2 Add a second budget value (author `budget=1` → escalate on the first failed attempt) so the boundary is proven at more than one value
- [ ] 6.3 Full offline ladder (`go test ./...`) + all 4 docker journeys green on the #568 beta (without `-race`)

## 7. Deferred follow-ups (documented, NOT in this change)

- [x] 7.1 File the #529 loop-terminal-reason-as-fact upstream ask (companion to #568) so the reason-aware escalate gap is tracked — FILED as semstreams **#569**; keep `06d`/`07c` count-only until it lands
- [ ] 7.2 Record #528 (per-spawn iteration budget) as needing its own per-task iteration-budget field — a separate future change (D5)

## 8. Ship

- [ ] 8.1 Adversarial review (`semstreams-reviewer` + `go-reviewer` for the checkfloors change) — both pass
- [ ] 8.2 Docs (G10): `docs/architecture.md` vocab table gains `route.task.budget`; alignment note if the mirror changes shape
- [ ] 8.3 `openspec validate --strict` green; conventional commit; push
