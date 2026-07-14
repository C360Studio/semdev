## 1. Foundation: framework bump + honest baseline

- [x] 1.1 Bump semstreams beta.141 → beta.146; fix any API drift; full mock ladder green
- [x] 1.2 Add the three upstream-ask tripwire tests (#519 `.value` substitution, #528 per-spawn `max_iterations`, #529 uniform exhaustion reason) that FAIL when the capability lands, prompting the mechanical upgrade
- [x] 1.3 Rename the e2e journey a bridge proof (test names, comments, evidence-ledger labels) — no behavior change yet (G10)

## 2. Immutable snapshot: git-backed runspace

- [x] 2.1 Red-first pin: cold verify currently consumes bytes absent from any commit (reproduce the mutable-tree leak on the fixture)
- [x] 2.2 `Materialize` runs `git init` + pristine base commit with a fixed harness author identity
- [x] 2.3 `apply_patch` commits the checkout after each successful apply and stamps `attempt.commit` (latest-wins) on the run; add `attempt.commit` to the vocabulary (G9) with its single writer (G5)
- [x] 2.4 Replace `CloneForVerify`'s tree copy with clone-at-`attempt.commit`; delete the mutable-copy path
- [x] 2.5 Post-measure dirty-tree floor: `git status --porcelain` non-empty after measurement stamps a rejecting `floor.finding` (red-first pin with a test that mutates the tree during measure)

## 3. Patch scope: target_files enforcement

- [x] 3.1 Red-first pin: a diff touching a file outside `task.spec.target_files` currently applies (reproduce on the fixture)
- [x] 3.2 `Patcher.Apply` resolves the task's `target_files` and rejects any out-of-contract path atomically (no partial application)
- [x] 3.3 Projection enforces the `target_files`-includes-tests contract: a task whose `target_files` omits every test file its `test_command` measures parks toward the human

## 4. One bounded multi-turn developer loop

- [x] 4.1 Drop `StopLoop` from `apply_patch` and `measure_task` results; `measure_task` remains outcome-parameter-free and returns real output in-loop (G3)
- [x] 4.2 New `read_workspace` tool: path-guarded, checkout-rooted, read-only, paginated under the 32KB tool-result cap (framework-alignment note + registry entry, G1)
- [x] 4.3 New `read_diff` tool for the reviewer: returns `git diff <base>..HEAD` over the committed checkout (HEAD == `attempt.commit` under the one-in-flight invariant). NOTE: the explicit-SHA pinning of `CloneForVerify`/`read_diff` (the group-2 review carry-forward) is DEFERRED — nil at M0 (HEAD is provably the reviewed attempt under serialization), tracked as a carry-forward for multi-attempt concurrency
- [x] 4.4 Developer dispatch: explicit `tools` allowlist (`read_workspace`, `apply_patch`, `measure_task`, `ask_human`), `tool_choice: auto`, dev-loop component `max_iterations` set (8, in the bootstrap — per-spawn is #528)
- [x] 4.5 Context-complete prompts: instruct the developer/reviewer to read the full task contract (goal, `target_files`, `test_command`, assumptions, non-goals) via `query_entity` + `read_workspace` (a rule cannot template cross-entity task.spec fields, R7); re-entry prompts point at `review.findings.0`
- [x] 4.6 Config lint: every model-publishing spawn declares an explicit tool allowlist (`TestEveryModelSpawnDeclaresToolsAllowlist`); `allowed_tools` populated (closes MEDIUM-3)
- [x] 4.7 Reviewer loop: allowlist (`read_workspace`, `read_diff`, `submit_review`, `ask_human`), `tool_choice: auto`, `submit_review` terminal; `submit_review` stamps `review.verdict.0` + `review.findings.0` (added `review.findings.<i>` to vocabulary)

## 5. Rule-native routing: delete the route-token layer

- [x] 5.1 Red-first pins for the route family: advance/not_clean/retry/escalate totality + mutual exclusion, review approved/retry/park/no-verdict, delivery coherent/blocked — offline rule-evaluation tests (`TestFloorsRouteTotalityAndSelfExtinguish`, `TestReviewRouteTotalityAndSelfExtinguish`, `TestDeliveryRouteTotalityAndSelfExtinguish`)
- [x] 5.2 Delete `internal/tools/checkgate` + `internal/tools/checkcoherence`, their registrations, and the `dev.gate_decision` / `dev.coherence_decided` predicates
- [x] 5.3 Delete relay rules (old 05 measure, 06 floors, 07 gate, 08a/b/c, 09 review, 10 verify, 11 coherence, 12a/b) and the relay-marker predicates; author the replacing route rules (05 floors-trigger + 06a-d floors route + 07a-d review route + 08a/b delivery route, same commit)
- [x] 5.4 Attempt accounting: the dispatching rules (04/06c/07b) append `task.attempt.0 = $instance` at spawn time; constant budget 3 in route literals (via the `route.attempt` mirror + `length_lt 3`/`length_gt 2`). NOTE: an explicit redelivered-append regression pin is a carry-forward; robustness is by design (storage dedups exact triples, and `length_gt 2` catches an over-count fail-closed — pinned in `TestFloorsRouteTotalityAndSelfExtinguish`)
- [x] 5.5 Reviewer rejection routes: `changes_requested` + budget remaining → fresh developer attempt (D16 re-entry, 07b); `changes_requested` + exhausted → park (07c); no-verdict → park (07d)
- [x] 5.6 Widen the G2 conformance census: zero Go-derived routing tokens consumed by rule dispatch conditions (`TestNoGoDerivedRoutingTokens` — the pin that would have caught check_gate)
- [x] 5.7 Bootstrap config: removed deleted rules, added the new rules to the explicit list, bumped the config version (0.16.0), set `max_iterations` 8, populated `allowed_tools`; delivery route reads `verify.result` + `review.verdict.0` + `openspec.validated` directly

## 6. Deterministic stations as publish-triggered components

- [ ] 6.1 Floors component: subscribes to the developer-terminal publish, evaluates the committed snapshot, stamps `floor.finding` (framework-alignment note + registry entry; delete the forced-turn relay)
- [ ] 6.2 Verify component: fires on approved review verdict, runs the clean-room proof from clone-at-SHA, stamps `verify.result` (delete the forced-turn relay)
- [x] 6.3 Delivery component: fires on the delivery rule's publish, records `pr.ref` via the shared `openpr.Deliver` core (delete the forced-turn relay). This slice also establishes the reusable `internal/station` generic base (Discoverable + LifecycleComponent, core-NATS subscribe, dispatch decode, bounded idempotent retry, component-lifetime handler context, panic recovery) that 6.1/6.2/6.4 copy. `pr.ref` is M0-idempotent (latest-wins); the replay-safe existing-PR lookup is task 7.4 (R8), and the routed-without-result wedge is pinned as a known gap deferred to R8 (`TestDeliveryRoutedWithoutResultIsAKnownGap`)
- [~] 6.4 Projection + validation + provisioning conversions: same pattern, one commit each; confirm `issue_intake` stays a Sarah decide (design open question). PROJECTION + VALIDATION DONE (slice 2): both self-sufficient publish-triggered components calling the shared `projecttasks.Project` / `validatechange.Validate` cores (single writers preserved, G5). Validation fires on the authoring loop so it threads `run_entity_id`+`slug` as publish properties. PROVISIONING remains (6D — it shares boot's process-local `runspace.Checkouts`+`Sandboxes` with the measure_task tool, so it needs the DI seam introduced with floors, 6B)
- [ ] 6.5 Model-turn census pin: only author/developer/reviewer spawns remain in the rule packs; deterministic-station failure parks (fail-closed test per station)

## 7. Restart-safe provisioning and effects

- [ ] 7.1 Red-first pin: restart after `sandbox.ready` currently wedges the run (docker-gated reproduction of the provisionsandbox.go:182 no-op)
- [ ] 7.2 `sandbox.ready` carries reconstruction inputs (source ref, image digest, base commit) as facts
- [ ] 7.3 Resolvers reconstruct idempotently from durable facts on missing process-local state: re-clone at `attempt.commit` (preserving committed attempt state), re-`Up` from the pinned digest; `alreadyReady` verifies liveness before no-op'ing
- [ ] 7.4 Idempotent delivery: `open_pr` looks up the run's existing PR before creating (replay pin: no double-open)

## 8. Real forge delivery

- [ ] 8.1 `open_pr` speaks the real forge API through the forge-io adapter; delete the `local-delivery:<run>` stub path
- [ ] 8.2 Protocol-faithful local forge double for e2e (records the real request shape); evidence summary in the PR body
- [ ] 8.3 Decide the recorded real-forge run's target with the operator (disposable repo vs org sandbox) and record the evidence-ledger entry — M0-complete claims require it

## 9. Brownfield ingest runtime component

- [ ] 9.1 Registered ingest component wiring `internal/brownfield` onto the raw lane (raw-lane → projector → graph-ingest), stamping `openspec.spec.*` on spec entities under the single owner (G5); framework-alignment note + registry entry
- [ ] 9.2 Boot-path test: runtime against a target repo with living specs ingests without manual invocation; in-flight `openspec/changes/` stay un-ingested

## 10. Bridge-proof journey + docs honesty

- [ ] 10.1 Journey station: fail-then-pass (retry actually driven e2e — the standing MEDIUM carry-forward)
- [ ] 10.2 Journey station: reviewer rejection re-entry (Quinn rejects once, Amelia's fresh attempt carries the findings)
- [ ] 10.3 Journey station: budget exhaustion → park toward the human
- [ ] 10.4 Docker-gated restart-recovery station: kill the process mid-run, restart, run completes
- [ ] 10.5 Docs honesty pass (G10): brief/status restate M0 completion criteria (bridge proof ≠ complete; real-forge evidence required); document the gated-DAG M1 walker seam; update CLAUDE.md status
- [ ] 10.6 Full ladder: mock journey green, docker-gated suites green, `openspec validate --strict` green on all three changes; semstreams-reviewer pass over the reshaped rail
