## 1. Upstream prerequisite (blocks all implementation)

- [x] 1.1 semstreams **#568** (`length_gte`/`length_lte`) lands in a beta; bump the pin and `go mod tidy`
- [x] 1.2 Re-verify on that beta: `length_gte` uses the missing-predicate→empty-array semantics of the sibling length operators (a rule condition `{operator: length_gte, value: 0}` matches an absent predicate); update the design Open Question if it diverges
- [x] 1.3 Add a `length_gte`/`length_lte` regression guard to `test/conformance/upstream_asks_test.go` (the operator family is now complete)

## 2. Vocabulary (G5/G9)

- [x] 2.1 Declare `route.task.budget` in `internal/vocab` (writer `route-mirror`, capability `dev-from-task`, introduced-by this change). One LOGICAL writer, two sanctioned code sites (`checkfloors` + `submit_review`) — the established `route.attempt.instance` precedent, noted in both stamping sources
- [x] 2.2 Confirm `route.task.budget` is canonical 3-seg so `.value` arity-disambiguates (`vocab.Register` panic-guard covers it)

## 3. Budget mirror (Go — the only product code, on BOTH sanctioned route-mirror sites, D1a/D7)

- [x] 3.1 `internal/tools/checkfloors`: read the run's `task.spec.budget` (one `reader.ReadFacts(ctx, runEntityID, "task.spec.budget")`, same shape as the existing `measurement.result.*` / `task.attempt.instance` reads)
- [x] 3.2 Stamp `route.task.budget` on L_n in `routeMirrorTriples` alongside `route.attempt.*`, under the `route-mirror` owner; add it to the replaced-predicates list next to `RoutePassedPredicate`/`RouteRejectedPredicate` (single-valued). Raw copy of the AUTHORED string — parse-validate, never re-render; no re-clamp, no derived value (G3/G5)
- [x] 3.3 Unit pin (floors site): given a run with `task.spec.budget=B`, the mirror stamps `route.task.budget=B` on the dispatch loop entity; absent/unparseable budget → `RunFloors` returns an ERROR before any mirror write (D7a — loud R6 station fault, never a silent default). The fault is in the MIRROR phase: the current attempt's already-durable `floor.finding.*` are genuine harness output and MUST NOT be cleared (assert both: no L_n stamp AND findings intact — the resolve-fault clear is for stale PRIOR passes only)
- [x] 3.4 `internal/tools/submitreview`: the same run-side budget read; stamp `route.task.budget` on the REVIEW loop in the tool's existing single `ReplaceTriples` pass alongside `route.review.verdict` / `route.attempt.instance` (rules 07b/07c fire on the review loop — a budget stamped only by the floors site would stall EVERY changes_requested verdict on the empty substitution)
- [x] 3.5 Unit pin (review site): verdict pass stamps `route.task.budget=B` with the verdict; absent/unparseable budget → `errResult` back to the loop (the tool's documented loud-fail posture), NOTHING stamped on the review loop that pass
- [x] 3.6 Atomicity pin (D7, owner-scoped): NO route-mirror site stamps `route.attempt.*` without `route.task.budget` in its one `ReplaceTriples` pass — both `checkfloors`→L_n and `submit_review`→review loop — so neither the 06c/06d nor the 07b/07c partition can evaluate the silently-empty `$…value` substitution (the fail-open wedge)

## 4. Route rules (rule-native, no Go decision layer — G2)

- [x] 4.1 `06c-route-retry` + `07b-review-retry`: swap `route.attempt.instance length_lt 3` → `length_lt $entity.triple.route.task.budget.value`
- [x] 4.2 `06d-route-escalate` + `07c-review-park`: swap `route.attempt.instance length_gt 2` → `length_gte $entity.triple.route.task.budget.value`
- [x] 4.3 Update each rule's `description`/`metadata` prose to describe the per-task boundary (G10 — no stale "constant 3" claims)

## 5. Conformance pins

- [x] 5.1 Update the floors/review route-totality + fail-closed-partition pins (`TestFloorsRouteTotalityAndSelfExtinguish`, `TestReviewRouteTotalityAndSelfExtinguish`) to the variable `length_lt B` / `length_gte B` boundary (no gap, no overlap across `[1,5]`)
- [x] 5.2 Add `route.task.budget` to the route-mirror allowlist in the G2 route-token census (a sanctioned mirror, NOT a Go-derived decision token — `TestNoGoDerivedRoutingTokens` must stay green)
- [x] 5.3 G5: `route.task.budget` single-writer pin (writer == `route-mirror`, matching BOTH stamping sources — the one-logical-writer/two-sites precedent of `route.attempt.instance`)
- [x] 5.4 Flip the #519 "MECHANICAL UPGRADE (not yet adopted)" note in `upstream_asks_test.go` to "adopted"; keep the #528/#529 notes as deferred with the D5/D4 rationale
- [x] 5.5 Offline rule-load gate (`test/ruleload`) green with the substituted conditions

## 6. Evidence (e2e, WITH `-race` — #566 fixed in beta.153)

- [x] 6.1 `TestBridgeProofBudgetExhaustionParks`: author an explicit `task.spec.budget=2`; assert exactly 2 attempts run, then the run parks via 07c (no 3rd `task.attempt`, no `verify.cleanroom.result`, no `pr.ref`)
- [x] 6.2 Second budget value: `TestBridgeProofBudgetOneEscalatesOnFirstRed` authors `budget=1` → the first RED attempt escalates via 06d (floors), so the boundary is proven at two values
- [x] 6.3 Full offline ladder (`go test ./...`) + all 5 docker journeys green WITH `-race` on beta.153 (#566 landed, so `task e2e` runs `-race` again — 3× green on the bump, green again on this change)

## 7. Deferred follow-ups (documented, NOT in this change)

- [x] 7.1 File the #529 loop-terminal-reason-as-fact upstream ask (companion to #568) so the reason-aware escalate gap is tracked — FILED as semstreams **#569**; keep `06d`/`07c` count-only until it lands
- [x] 7.2 Record #528 (per-spawn iteration budget) as needing its own per-task iteration-budget field — a separate future change (recorded in design.md D5; the #528 regression guard continues to track `TaskMessage.MaxIterations`)

## 8. Ship

- [x] 8.1 Adversarial review (`semstreams-reviewer` + `go-reviewer` for the checkfloors change) — both pass
- [x] 8.2 Docs (G10): `docs/architecture.md` vocab table gains `route.task.budget`; alignment note if the mirror changes shape
- [x] 8.3 `openspec validate --strict` green; conventional commit; push
