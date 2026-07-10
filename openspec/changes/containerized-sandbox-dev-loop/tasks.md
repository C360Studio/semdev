## 1. Container Runner (the isolation substrate)

- [x] 1.1 Container `Runner` in `internal/cleanroom` (docker `run`/`exec`/`rm` via `os/exec`), implementing the existing `Runner` seam; per-run fresh container + fresh anonymous cache volume per cache-home; checkout `--mount`-bound at `/work` (SB1)
- [x] 1.2 Fail-closed when docker is absent/unhealthy (`DockerAvailable` probe; `ErrDockerUnavailable`/`ErrNoImage` sentinels the caller parks on, never a silent skip); red-first pins (SB5). Exec transport-vs-verdict classified by docker stderr signatures + a container-liveness check (NOT exit code — a dead container returns 1/137, indistinguishable from a real command exit)
- [x] 1.3 `MockRunner`/`LocalRunner` parity (ContainerRunner implements the `Runner` seam; unit tests need no docker via pure `buildRunArgs`/`execArgs`/sentinels; docker-gated integration tests runtime-skip when absent)
- [x] 1.4 G1: `cleanroom.Runner` is a seam under the `verify_artifact` tool (no separate registry entry, like Local/Mock); alignment note updated (container Runner is the M0 run path, revising D5)

## 2. Operator-declared image + cold proof (the readiness contract)

- [x] 2.1 Image resolution: locate the repo's committed `Dockerfile` / `.devcontainer` and build it (digest-pin the result); no harvest/inference; missing image → typed "declare an image" error (SB2)
- [x] 2.2 `customizations.semdev` / convention reader for the few run fields (test command, tier split, secret refs) — not an environment DSL (SB2)
- [x] 2.3 `internal/harness` reshaped: `Manifest` references the declared image + run fields (drop the toolchain-modeling fields it no longer owns); keep `AssessReadiness` tier logic
- [ ] 2.4 Cold-prove path: build image → fresh cache → resolve base deps + build cold → derive a readiness verdict (reuse `verify_artifact`'s cold-build core); red-first: a warm-only-resolvable dep fails cold (SB2/SB4)
- [ ] 2.5 Secrets: governed named-creds-ref store (git-ignored `.env` at M0) + injection via docker build-secret/env only; scrub from logs/results/facts; red-first: secret value in no stamped fact; missing required ref → park (SB2c/G7)

## 3. Go fixture (real code, real bug, real container)

- [ ] 3.1 `test/fixtures/go-health-class`: a minimal real Go module with a **real bug** (a failing `go test`) the dev loop fixes; stripped of orchestration-vocabulary coaching (G8)
- [ ] 3.2 Committed `Dockerfile` (`FROM golang:<pin>`) (+ optional `.devcontainer` referencing it) + `customizations.semdev` test command / single tier
- [ ] 3.3 Cache-masked-fabrication fixture variant for the G4 red-first pin (a dep that only resolves warm)
- [ ] 3.4 G8 lint over `test/fixtures/**` for the orchestration-vocabulary list

## 4. Wire the nil seams to the container

- [ ] 4.1 `Manifests` seam → resolve the declared image + run fields for a run
- [ ] 4.2 `Workspace` seam → resolve the run's container checkout root (for `measure_task`)
- [ ] 4.3 `Attempts` seam → resolve the developer's authored diff/files in the checkout (for `check_floors`)
- [ ] 4.4 `measure_task` / `check_floors` / `verify_artifact` execute against the container checkout via these seams (SB4/SB6)

## 5. Provision-and-prove-cold station (rule-owned, G2)

- [ ] 5.1 Vocab (G9): sandbox readiness / attestation / attempt predicates, named in the `sandbox` spec delta; docs row (G10)
- [ ] 5.2 Provisioning rule: on an approved run, stand up the sandbox + cold-prove → stamp readiness/attestation; self-extinguishing (fired-once marker + absence guard); red-first replay pin (SB7)
- [ ] 5.3 Readiness gate rule: the dev loop proceeds only on a proven sandbox-scope tier; an operator-CI/lab claim is deferred-and-noted, never gated in-sandbox, never a pass (SB5); red-first
- [ ] 5.4 Fail-closed park rules: absent docker / failed provision / missing secret → park toward human; red-first: no `verify.result` or "verified" fact over an absent sandbox (SB5)

## 6. apply_patch (the developer authors)

- [ ] 6.1 `apply_patch` tool: schema takes only patch/target (no outcome, G3); applies to the run's checkout path-guarded to `/work` (reject escape); registered (G1 entry + note + G5 writer if it stamps)
- [ ] 6.2 Red-first: apply_patch cannot write outside the checkout; the measured pass/fail is the command's real exit status, not model-supplied (G3)

## 7. Dev loop in the sandbox

- [ ] 7.1 dispatch-developer rule: on the `dev_from_task` decision, spawn Amelia for the task; self-extinguishing; record `task.attempt.*` (append)
- [ ] 7.2 The bounded loop runs in-sandbox: apply_patch → `measure_task` (real, in-container) → floors on the real diff → harness-stamped outcome (G3)
- [ ] 7.3 Floors-gate + budget/retry (reuse the existing floor tools + `task.attempt` counting); escalate/park on exhaustion

## 8. Clean-room verify (the cold gate) + close-out

- [ ] 8.1 Final verify: separate fresh cold container, `--recursive` clone of the fixed artifact, resolve+build+test cold, **no fixups** → `verify.result` (SB3); reuse `verify.Decide`
- [ ] 8.2 Red-first: a non-self-contained fix (builds only via a would-be harness fixup) fails the cold verify; the `forbidden_patterns` build-file tripwire (SB3)
- [ ] 8.3 open_pr coherence gate: `verify.result eq pass` ∧ every projected `review.verdict.* eq approved` ∧ `openspec.validated ne ""` (honors the m0 D16 roll-up)
- [ ] 8.4 g11 journey: grow the mock-LLM e2e — approval → provision+prove-cold → dispatch → apply_patch → measure (real, in-container) → floors → verify (cold) → PR; RequestCount pinned; WARN-free
- [ ] 8.5 semstreams-reviewer pre-commit on each increment; Codex on PR; docs (G10: architecture.md components/vocab rows, alignment notes, CLAUDE.md status)

## 9. Design reconciliation

- [ ] 9.1 Record the D5 reversal (container Runner at M0) in `m0-walking-skeleton-spine`'s design.md as superseded-by this change (cross-reference), so the two changes stay coherent
- [ ] 9.2 Confirm archive ordering (this change archives with or after m0; the `sandbox` capability + the m0 `dev-from-task`/`clean-room-verify` specs reconcile in main specs)
