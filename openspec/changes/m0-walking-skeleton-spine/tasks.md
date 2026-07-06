## 1. Foundation & scaffolding

- [x] 1.1 Init Go module (go 1.26) and repo layout (`cmd/semdev`, `cmd/e2e-semdev`, `internal/`, `test/`)
- [x] 1.2 Add semstreams `v1.0.0-beta.141`+ dependency; confirm build
- [x] 1.3 `docker compose` for NATS JetStream (never embedded) + Taskfile targets (build, lint, test, e2e)
- [x] 1.4 Mock-LLM harness scaffold (S6): deterministic fixtures, zero paid tokens
- [x] 1.5 `componentregistry.RegisterAll` wired into BOTH binaries (`cmd/semdev` and `cmd/e2e-semdev`)

## 2. Conformance registries + pins (land first, red-first)

- [x] 2.1 Tools registry (G1) + conformance test: an unregistered tool/processor fails the build; **binary-parity pin**: assert `cmd/semdev` and `cmd/e2e-semdev` register an identical factory set (guards the half-wired-binary class against a future direct registration bypassing `boot.RegisterAll`)
- [x] 2.2 Framework-alignment-note format (G1); each new Go addition links its note in the registry
- [x] 2.3 Single-writer table (G5) + writers-census test: every predicate maps to exactly one writer
- [x] 2.4 Predicate → introducing-change table (G9) + exhaustiveness test: an undeclared predicate fails
- [x] 2.5 G2 conformance test (red-first): zero lifecycle-transition callers in product Go (exception table, target 0)
- [x] 2.6 G3 schema conformance test (red-first): reject any tool schema accepting an outcome-shaped field
- [x] 2.7 Evidence-ledger schema (G7) + schema-validation test; regression-pin manifest (G6): deleting a named pin fails
- [x] 2.8 Docs inventory conformance test (G10): README/architecture tables compared against the registries

## 3. run-lifecycle (G2, T1)

- [ ] 3.1 Closed action taxonomy config (issue_intake, create_change, dev_from_task, verify, open_pr, ask_human, respond, archive_change) mirroring the OpenSpec lifecycle; no phase enum. **verify = clean-room outcome (G4) only; coherence is structural (`openspec.validated` + derived completion + `review.verdict`), no separate coherence action**
- [ ] 3.2 `agentic/agentrun` as the run entity; run created carrying `run.issue_ref`
- [ ] 3.3 Persona fragments — Sarah (coordinator), Amelia (dev), Quinn (reviewer); cosmetic, never fact-writers
- [ ] 3.4 Lifecycle-transition rules (phase-as-fact / `lifecycle_transition`): each station→next fires off a milestone fact
- [ ] 3.5 Change-approval human-gate rule: `dev_from_task` ineligible until `run.change_approved`
- [ ] 3.6 Park-toward-human rule: `run.awaiting_human` on unresolvable-by-rule; no Go reconciler advances a parked run
- [ ] 3.7 Validate-gate wiring: `openspec.validated` must be present before the change-approval gate is offered (create_change → validate → approval ordering)
- [ ] 3.8 Test: an out-of-taxonomy action is not routable — no rule routes it and the condition surfaces for attention
- [ ] 3.9 `archive_change` loop-closer (design-now, wire-M1): declare the action + `openspec.archived` fact and design the merge-triggered rule; assert the M0 mock journey terminates at `open_pr` (`pr.ref`) and the merge trigger + live archive land at M1

## 4. openspec-io (port format engine; hydrate / ingest / oracle)

- [ ] 4.1 Port semteams `cmd/semteams/openspec/` as a dep-free Go library (parse/render/`ReadChange`/`WriteChange`/`Facts`); re-vocabulary to semdev predicates (G9)
- [ ] 4.2 `create_change` author tool: emit `openspec.change.*` facts from an intaken issue (mock LLM)
- [ ] 4.3 Hydrate tool: render `proposal`/`specs`/`tasks` from `openspec.change.*` facts (G10 — projection of facts, not hand-authored)
- [ ] 4.4 CLI-validate step: shell `openspec validate --strict --json --no-interactive`; harness stamps `openspec.validated`; invalid → does not reach approval gate
- [ ] 4.5 `WriteChange`-to-workspace tool: write the change folder into the target repo for the PR
- [ ] 4.6 Brownfield projector: deterministic parse of an existing `openspec/` → facts under one owner (no LLM); retain raw bytes by reference
- [ ] 4.7 Round-trip fidelity test (red-first): ingest semdev's own `m0` change → facts → hydrate → semantically equivalent
- [ ] 4.8 Archive CLI-oracle (parallel to validate): design the `openspec archive` shell path; `openspec.archived` harness-stamped, single writer (G3/G5). M0 declares the shell path; M1 wires the merge trigger + live call

## 5. forge-io (T7, S5) + intake security

- [ ] 5.1 Wire semstreams `github-webhook` input + `github_read`/`github_write` tools into both binaries
- [ ] 5.2 Normalized issue/PR/comment fact mapping; arc rules reference no host-specific field
- [ ] 5.3 Deterministic allowlist admission check (zero-token): `intake.actor` + `intake.admitted`; collaborator-or-allowlist + `semdev` label/command opt-in
- [ ] 5.4 G6 pin (red-first): an unauthorized intake creates no run and spends zero tokens
- [ ] 5.5 Inventory + add thin comment tools if missing (`list_comments`/`get_pr`); G1 note each
- [ ] 5.6 PR delivery carrying the evidence summary; record `pr.ref`
- [ ] 5.7 `ask_human` posts a comment; `respond` re-enters as `human.signal`; only the authorized requester steers the run's human gate
- [ ] 5.8 Host-neutrality conformance pin (red-first): a build-failing test that no arc rule references a host-specific predicate/field

## 6. dev-from-task (T2, T3, S1)

- [ ] 6.1 Task projector: approved change → immutable `task.spec` facts (reuse `project_spec_tasks` pattern); mutation rejected
- [ ] 6.2 Karpathy schema enforcement at stamp (assumptions, non_goals, ≥1 target_file, required test_command); missing → fail toward human
- [ ] 6.3 Budget clamp `[1,5]` at stamp; escalate rule fires by iteration 6
- [ ] 6.4 Bounded dev loop on `agentic-loop`; record `task.attempt` per iteration; escalate + halt on exhaustion
- [ ] 6.5 Deterministic floor checks as pure functions + red-first tables (S1): source-build, vacuous-test, stub, anti-mock, tests-must-exist → `floor.finding`
- [ ] 6.6 Floors gate the loop (cannot skip); a rejecting `floor.finding` blocks advance to review

## 7. harness-measurement (G3, T4, S3)

- [ ] 7.1 Measurement tool over the `bash` executor: capture OS exit code → `measurement.result`; schema has NO outcome field
- [ ] 7.2 `measurement.result` single writer (G5 table entry); red-first: a non-zero exit records failure regardless of model text
- [ ] 7.3 Reviewer (Quinn) reads `measurement.result` → `review.verdict`; additive-only, never weakens `task.spec`
- [ ] 7.4 Review gates `open_pr` on harness facts; red-first: a false success claim cannot be approved

## 8. clean-room-verify (G4, S2, T5, T6) — the make-or-break

- [ ] 8.1 Runner seam (`Up`/`Exec`) + `Mock` + local `ExecIsolated`; isolation product-supplied
- [ ] 8.2 Reproducibility-contract manifest schema (language-agnostic) + the **Go profile** (fresh `GOMODCACHE`/`GOCACHE` per proof)
- [ ] 8.3 `semdev init` (Go-profile slice): harvest → propose → prove-cold → commit `.semdev/harness.yaml`
- [ ] 8.4 Verification-capability readiness gate (T5): `sandbox` vs `operator-ci` tier check; park/defer, never fake
- [ ] 8.5 Port semspec `verify.Decide` (pure, offline-testable) + resolution-proof logic; **rule-wired, NOT the reconciler shell** (B3)
- [ ] 8.6 Fresh cache-home per proof; `verify.result` harness-stamped, single writer
- [ ] 8.7 G4 pins (red-first): cache-masked-fabrication fixture rejected; distinct build-cache home per run asserted; `open_pr` blocked until `verify.result` passes
- [ ] 8.8 Fail-closed with transport-error retry (never terminal-reject on an infra hiccup)

## 9. evidence-ledger (G7, S10)

- [ ] 9.1 Port semspec's small ledger + status vocabulary (pass/exploratory/blocked/…); `evidence.run` writer
- [ ] 9.2 Honest-status rules: a failed verification is never `pass`; ledger schema rejects an out-of-vocabulary status
- [ ] 9.3 Mock-LLM / fixture-seeded runs labeled bridge proof; not counted as real-LLM evidence
- [ ] 9.4 Journey-honesty check: a journey that backdoor-writes is flagged invalid evidence

## 10. Fixtures (S7, G8)

- [ ] 10.1 Light Go fixture repo (go-health-class), stripped of all orchestration-vocabulary coaching
- [ ] 10.2 Cache-masked-fabrication fixture for the G4 pin (8.7)
- [ ] 10.3 G8 lint over `test/fixtures/**` for the orchestration-vocabulary list

## 11. E2E journey — the spine (S6, mock ladder)

- [ ] 11.1 Mock-LLM e2e journey: issue → change → approval → dev loop → measurement → floors → review → clean-room verify → PR
- [ ] 11.2 Journey asserts the pins end-to-end: G2 (no Go transition), G3 (harness-stamped), G4 (fresh isolation + fabrication reject), G7 (honest ledger)
- [ ] 11.3 Mock ladder green; zero paid tokens; the journey does not write to NATS/graph/state to force a pass (G7)
- [ ] 11.4 Evidence-ledger entry for the M0 journey, labeled bridge proof

## 12. Docs (G10)

- [ ] 12.1 README/architecture topology tables generated or pinned against the registries; inventory test green
- [ ] 12.2 Update `CLAUDE.md` Status — no longer "no product code yet"
