## 1. Container Runner (the isolation substrate)

- [x] 1.1 Container `Runner` in `internal/cleanroom` (docker `run`/`exec`/`rm` via `os/exec`), implementing the existing `Runner` seam; per-run fresh container + fresh anonymous cache volume per cache-home; checkout `--mount`-bound at `/work` (SB1)
- [x] 1.2 Fail-closed when docker is absent/unhealthy (`DockerAvailable` probe; `ErrDockerUnavailable`/`ErrNoImage` sentinels the caller parks on, never a silent skip); red-first pins (SB5). Exec transport-vs-verdict classified by docker stderr signatures + a container-liveness check (NOT exit code — a dead container returns 1/137, indistinguishable from a real command exit)
- [x] 1.3 `MockRunner`/`LocalRunner` parity (ContainerRunner implements the `Runner` seam; unit tests need no docker via pure `buildRunArgs`/`execArgs`/sentinels; docker-gated integration tests runtime-skip when absent)
- [x] 1.4 G1: `cleanroom.Runner` is a seam under the `verify_artifact` tool (no separate registry entry, like Local/Mock); alignment note updated (container Runner is the M0 run path, revising D5)

## 2. Operator-declared image + cold proof (the readiness contract)

- [x] 2.1 Image resolution: locate the repo's committed `Dockerfile` / `.devcontainer` and build it (digest-pin the result); no harvest/inference; missing image → typed "declare an image" error (SB2)
- [x] 2.2 `customizations.semdev` / convention reader for the few run fields (test command, tier split, secret refs) — not an environment DSL (SB2)
- [x] 2.3 `internal/harness` reshaped: `Manifest` references the declared image + run fields (drop the toolchain-modeling fields it no longer owns); keep `AssessReadiness` tier logic
- [x] 2.4 Cold-prove path: build image → fresh cache → resolve base deps + build cold → derive a readiness verdict (reuse `verify_artifact`'s cold-build core); red-first: a warm-only-resolvable dep fails cold (SB2/SB4)
- [x] 2.5 Secrets: governed named-creds-ref store (git-ignored `.env` at M0) + injection via docker build-secret/env only; scrub from logs/results/facts; red-first: secret value in no stamped fact; missing required ref → park (SB2c/G7)

## 3. Go fixture (real code, real bug, real container)

- [x] 3.1 `test/fixtures/go-health-class`: a minimal real Go module with a **real bug** (a failing `go test`) the dev loop fixes; stripped of orchestration-vocabulary coaching (G8)
- [x] 3.2 Committed `Dockerfile` (`FROM golang:<pin>`) (+ optional `.devcontainer` referencing it) + `customizations.semdev` test command / single tier
- [x] 3.3 Cache-masked-fabrication fixture variant for the G4 red-first pin (a dep that only resolves warm)
- [x] 3.4 G8 lint over `test/fixtures/**` for the orchestration-vocabulary list

## 4. Wire the nil seams to the container

- [x] 4.1 `Manifests` seam → resolve the declared image + run fields for a run
- [x] 4.2 `Workspace` seam → resolve the run's container checkout root (for `measure_task`)
- [x] 4.3 `Attempts` seam → resolve the developer's authored diff/files in the checkout (for `check_floors`)
- [x] 4.4 `measure_task` / `check_floors` / `verify_artifact` execute against the container checkout via these seams (SB4/SB6) — all three now resolve+run against the run's checkout via the wired Workspace/Manifests/Attempts seams. The container-RUNNER swap (measure/floors in-container, verify on the container runner) is the migration plan's steps 6/7 = groups 7/8, built with the live loop/verify.

## 5. Provision-and-prove-cold station (rule-owned, G2)

- [x] 5.1 Vocab (G9): sandbox readiness / attestation predicates (`sandbox.provisioned` rule-owned marker; `sandbox.ready`/`.blocked`/`.attestation.*` harness-owned — split so no predicate has two writers), capability `sandbox`, docs row (G10), G5 writer pin. The in-sandbox `task.attempt` is the existing dev-loop predicate (lands live in g7)
- [x] 5.2 Provisioning rule (`sandbox/01-provision`): on an approved, projected run, forces provision_sandbox via run_scope=inherit to stand up the sandbox + cold-prove → stamp readiness/attestation; SELF-EXTINGUISHING (fired-once `sandbox.provisioned` marker stamped before the publish + `length_eq 0` absence guard); pinned by `TestSandboxProvisionIsSelfExtinguishing` (SB7)
- [x] 5.3 Readiness gate: the dev-loop-proceed rule (`dev-from-task/02`) is gated on `sandbox.ready eq true`, so the dev loop proceeds only on a proven sandbox-scope tier; an unprovable sandbox stamps `sandbox.blocked` (never `sandbox.ready`) and parks; pinned by `TestDevRewakeGatedOnSandboxReadiness` (the dedicated dispatch-developer gate moves here at g7); an operator-ci/lab-only claim is deferred toward the operator, never gated in-sandbox (SB5)
- [x] 5.4 Fail-closed park rule (`sandbox/02-park-unprovable`): absent docker / failed provision / missing secret / deferred-only tier → `sandbox.blocked` → park toward human (`run.awaiting_human` + user-response bus), no lifecycle transition (G2); no `verify.result`/"verified" fact over an unproven sandbox (the readiness gate holds the loop); pinned by `TestSandboxParkOnUnprovable` (SB5)

## 6. apply_patch (the developer authors)

- [x] 6.1 `apply_patch` tool (`internal/tools/applypatch`): schema takes only the unified `diff` (no outcome, G3); the `runspace.Patcher` seam resolves the run's checkout, PATH-GUARDS every touched file to inside it (`safeJoin`, reject `..`/absolute — `git apply`'s own escape rejection is the backstop), then applies via `git apply -p1` on the host checkout root (= the container `/work` bind-mount). Registered (G1 entry + `apply-patch-tool` note); stamps NO fact so no G5 writer (G2 — the loop's measure/floors read the mutated checkout)
- [x] 6.2 Red-first: `TestPatcherRejectsPathEscape` (a `../` diff is rejected before git runs and nothing lands on the host) + `TestPatcherReportsApplyConflict`/`TestPatcherFailsClosedWithoutCheckout` + the tool's G3 schema pin (`TestApplySchemaTakesOnlyDiff` — only `diff`, no outcome field). Rename/copy diffs are rejected at M0 (paths the parser can't fully enumerate); the measured pass/fail stays a separate harness measurement (measure_task, g7)

## 7. Dev loop in the sandbox

<!-- Increment plan (architect-consulted): 7A dispatch-developer + apply_patch author
     station (journey st.10) · 7B measure in-container warm run-container (st.11) ·
     7C check_floors live + H1 presence gate (st.12) · 7D floors-gate + budget/retry/
     escalate (st.13). task.attempt is an APPENDED multi-valued predicate so the gate
     counts it with length_gt/length_lt vs task.spec.<i>.budget (ReplaceTriples upserts,
     so appending needs the add-triple lane). -->

- [x] 7.1 dispatch-developer rule (`dev-from-task/04`, 7A) + attempt counter (`dev-from-task/05`, 7B): 04 spawns Amelia (role=developer) forced to `apply_patch` on the coordinator's `dev_from_task` decision, SELF-EXTINGUISHING via a LOOP-scoped `dev.dispatched` marker (`TestDispatchDeveloperIsSelfExtinguishing`). 05 fires on the developer-loop terminal (`agent.loop.role=developer ∧ outcome=success`) and APPENDS `task.attempt.0` (distinct object = the developer loop instance, so retries do not collapse under set-dedup; `task.attempt` → `task.attempt.*` vocab, writer `dev-measure-rule`), pinned `TestMeasureTriggerIsSelfExtinguishing` (marker `dev.measured` + outcome=success + forces measure_task + appends the counter). Journey station 10 asserts a developer loop reached success (dispatch→Amelia→apply_patch applied cleanly).
- [~] 7.2 The bounded loop runs in-sandbox: apply_patch → `measure_task` (real, in-container) → floors on the real diff → harness-stamped outcome (G3). **Measure-in-container DONE (7B):** provision_sandbox leaves a WARM container Up (new `runspace.Sandboxes` seam, reaped by the runtime on Stop); `measure_task` swapped from the host `cliexec` runner to `cleanroom.Runner.Exec` over that warm sandbox (fails closed if none provisioned, SB5); `dev-from-task/05` triggers it on the developer terminal. Journey station 11 proves `measurement.result.0.passed=true` — the fix measured REAL, cold-built-then-warm, in-container (RequestCount 9). **[remaining 7.2: floors on the real diff (7C) — check_floors live + H1 presence gate; the per-attempt token handshake between measure/floors/gate lands there too.]**
- [ ] 7.3 Floors-gate + budget/retry (reuse the existing floor tools + `task.attempt.*` counting); escalate/park on exhaustion (7D)

## 8. Clean-room verify (the cold gate) + close-out

- [ ] 8.1 Final verify: separate fresh cold container, `--recursive` clone of the fixed artifact, resolve+build+test cold, **no fixups** → `verify.result` (SB3); reuse `verify.Decide`
- [ ] 8.2 Red-first: a non-self-contained fix (builds only via a would-be harness fixup) fails the cold verify; the `forbidden_patterns` build-file tripwire (SB3)
- [ ] 8.3 open_pr coherence gate: `verify.result eq pass` ∧ every projected `review.verdict.* eq approved` ∧ `openspec.validated ne ""` (honors the m0 D16 roll-up)
- [ ] 8.4 g11 journey: grow the mock-LLM e2e — approval → provision+prove-cold → dispatch → apply_patch → measure (real, in-container) → floors → verify (cold) → PR; RequestCount pinned; WARN-free
- [ ] 8.5 semstreams-reviewer pre-commit on each increment; Codex on PR; docs (G10: architecture.md components/vocab rows, alignment notes, CLAUDE.md status)

## 9. Design reconciliation

- [ ] 9.1 Record the D5 reversal (container Runner at M0) in `m0-walking-skeleton-spine`'s design.md as superseded-by this change (cross-reference), so the two changes stay coherent
- [ ] 9.2 Confirm archive ordering (this change archives with or after m0; the `sandbox` capability + the m0 `dev-from-task`/`clean-room-verify` specs reconcile in main specs)
