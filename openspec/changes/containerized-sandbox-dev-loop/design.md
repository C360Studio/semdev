## Context

semdev's dev loop and clean-room verify must run in a real, isolated,
reproducibility-proven sandbox. Today that sandbox does not exist: the M0 design
(D5) deferred containerization to M2 and left the `Workspace` / `Manifests` /
`Attempts` seams nil. That deferral silently recreates the exact failure that
killed both predecessors, so this change builds the substrate now.

**Two-donor forensics (the grounding for every decision below):**

- **semspec** (`~/Code/c360/semspec`): with `SANDBOX_URL` unset the worktree
  validator, commit guard, and verify gate all *skipped* and the run stamped
  `"execution verified"` over **zero executions**. Its warm shared Gradle cache
  masked fabricated dependency coordinates (`org.sensorhub:sensorhub-core:2.0.1`
  resolved from cache; a cold checkout 401s). Worse, its hard-scenario harness
  **injected a source-substitution `init.d` script that never propagated into the
  committed `build.gradle`** — so the delivered PR built in the harness but 401s
  on a clean `--recursive` checkout. The harness was masking that *the artifact
  itself was not reproducible*, and the e2e specs encoded "couldn't prove"
  (`operator_proof.placeholder === true`) as the expected pass.
- **semteams** (`~/Code/c360/semteams`): the *shape* is right — a
  single-component provisioner (`Manager.Request`: match profile → admit → `Up` →
  probe → attest), routed on attestation facts, **no second agentic team**. But
  it reuses `(profile, hash)` workspaces + containers across runs
  (`--remove-existing-container=false`) over **shared warm cache volumes**, and
  its attestation only proves the toolchain answers `--version` — never that the
  project builds cold. Real source-seeding (ADR-043 Phase E) was never built.

Neither donor ever proved a project builds cold in a fresh environment. The
OpenSensorHub ground truth shows *why* run-time guessing cannot work: the whole
hard-scenario difficulty is **infra, not code** — JDK 17 + Gradle 8.10.2 + protoc
4.28.2 pins, a GitHub-Packages-401-vs-git-submodule-composite resolution fork,
vendored native blobs, protobuf codegen from a submodule — and **every one of
those failures is invisible on a warm machine and only surfaces cold.**

## Goals / Non-Goals

**Goals:**
- A per-run **containerized** sandbox (the make-or-break infra), provisioned and
  **proven cold before** the dev loop, so a later red is a real regression.
- **Image identified by declaration, proven at init, never guessed at run.**
- The clean room proves the **committed artifact** from a `--recursive` clone,
  cold, with **zero harness fixups** — a non-self-contained build fails, loudly.
- **Fail closed**: absent docker / failed provision / unprovable claim → park
  toward the human. Never a silent skip or a placeholder-pass.
- One mechanism for Go (M0) and the hard tiers (OSH, M2): **same profile schema
  and Runner, different data** — the M0 Go fixture exercises the full spine.
- The developer can author a real fix (`apply_patch`) that the harness applies
  and measures in-sandbox (G3), so the loop produces honest evidence.

**Non-Goals:**
- Dynamic materialization / synthesis of arbitrary toolchains at run time
  (semteams' composer, semspec's `init.d` substitution) — explicitly rejected.
- Running agent-driven development on the developer's host (local-folder
  isolation is rejected — it isolates nothing and proves nothing).
- The hard OSH/MAVLink/Meshtastic tiers themselves (M2). M0 ships the Go profile
  and the substrate; the schema is shaped for the hard fields but they stay
  empty for Go.
- Real forge-io clone of a live target repo (M2 dogfood). M0's "checkout" is the
  in-repo Go fixture.
- Full `semdev init` harvest UX. M0 ships a **minimal init** that proves +
  commits a manifest (or a hand-committed fixture manifest init would produce).

## Decisions

### SB1 — Container `Runner` at M0 (revises D5)

**D5 is reversed:** the clean-room `Runner`'s container implementation moves from
M2 to M0. Every run executes in a per-run container — the **simplest dockerized
equal**: `docker run` / `exec` / `rm` via `os/exec` (no new deps; mirrors how
`LocalRunner` and the NATS compose already shell out), a **pinned official image**
(`golang:1.26`, digest-pinned) declared by the manifest, **one fresh container +
one fresh cache home per run**, the checkout bind-mounted at `/work`.
`LocalRunner` demotes to a unit-test / no-docker fallback shim, never the run
path. This is `internal/cleanroom.Runner`'s container backend behind the existing
seam.

*Why over D5's local-first:* a local folder on the host isolates nothing, runs
untrusted agent development on the host, and proves none of the cold-only
failures the hard scenarios are made of. The infra is the make-or-break; a
local stand-in is busywork that de-risks nothing.

*Alternative rejected:* devcontainer (`devcontainers/cli`) as the M0 isolator —
heavier (external Node CLI, version drift was a semteams risk), and it does not
solve cache-home freshness or the self-contained-artifact contract. Plain
`docker run` is the simplest thing that gives real isolation; A/B vs devcontainer
is an M2 question.

### SB2 — The environment is an OPERATOR-DECLARED Dockerfile / devcontainer, not a semdev DSL

The container image a run needs is **declared by the operator, in the target
repo, as a standard `Dockerfile` and/or `.devcontainer/devcontainer.json`** — a
minimum-viable reproducible image. semdev does **not** invent a bespoke
`harness.yaml` environment DSL, and it does **not** harvest/infer/synthesize the
toolchain. semdev **builds** the operator's declared image (digest-pinned) and
**proves it cold** for the repo (`clone --recursive` → resolve base deps → build);
per-run provisioning stands up that proven image. If a repo declares no image, or
the declared image cannot build the repo cold, the run **fails closed toward the
operator** (fix/declare the image) — never a guess, never a silent skip.

*Why the operator declares it:* the OSH failures (401, missing submodule, missing
native blob, un-pinned protoc, wrong JDK) are cold-only, and the *operator knows
their repo's environment* — that knowledge belongs with them, expressed in the
industry-standard formats their repo may already carry (a devcontainer is
literally "a reproducible dev environment"). This deletes the flaky auto-harvest
/ recipe-catalog / inference engine an earlier draft proposed — the exact
"onboarding magic" that made both donors brittle (semteams' dynamic composer,
semspec's guessed substitution). Declaration + cold-proof, with the declaration
in a portable standard format the operator owns.

*The only semdev-specific residue* (not in a Dockerfile): the **test command**
and the **sandbox-vs-operator-CI tier split**. These are tiny and ride a
`customizations.semdev` block *inside the devcontainer* (a standard devcontainer
extension point) or convention (`go test` / `gradlew test`; the repo's own test
exclusions for the tier line) — not a bespoke environment DSL. `internal/harness`
shrinks accordingly: it references the declared image + these few run fields, it
does not model the toolchain.

*semdev owns the RUN, not the image lifecycle:* semdev uses the
Dockerfile/devcontainer only to **build the image**, then runs **fresh container +
fresh cache per run** itself (`docker run`), *not* `devcontainer up`'s
persistent/warm-reuse semantics — because devcontainer's reuse-across-runs *is*
semteams' cache-masking sin. Standard declaration (operator, portable),
semdev-owned freshness (the G4 control, SB4).

*M0 concrete:* the Go fixture ships a tiny `Dockerfile` (`FROM golang:1.26`) or a
devcontainer referencing it; the base cold-proof is `go mod download` +
`go build ./...`; test command `go test ./...`; single tier.

### SB2b — Onboarding a new repo: the operator declares its image; the cold proof gates it

A single semdev instance is realistically asked to work heterogeneous repos —
meshtastic, mavlink, Go, whatever arrives next. Onboarding is **not** semdev
harvesting/guessing an environment; it is **the operator ensuring the repo
declares a Dockerfile/devcontainer**, which semdev then builds and **proves cold**
before any dev run relies on it. Many repos already carry one; where a target
does not, the operator adds it once (it is committed and reused).

**One instance, heterogeneous repos** — each repo declares its own image; semdev
builds + cold-proves each; the proven image is cached per repo. No monolithic
all-toolchains image, no recipe catalog, no inference.

**The not-yet-present protocol still works** (the meshtastic/mavlink objection).
The declared image carries *tooling* (JDK, git, git-lfs, and whatever the operator
puts in the Dockerfile). The protocol-specific weight is **task-introduced**, per
D6/SB3: the agent commits `meshtastic/protobufs` as a submodule + the protobuf
Gradle plugin (protoc fetched at build), the mavsdk deps, the native blobs — into
the artifact — and the cold `--recursive` PR verify proves they are real and
self-contained (the thing semspec faked). So the image never needs to know the
protocol in advance; the *driver the agent writes* pulls it in, and the cold
verify is what proves that pull is honest.

**The only thing that triggers an image change** is a genuinely new *tool* the
declared image lacks (not a dep — deps resolve at build): the run fails cold and
parks toward the operator to extend the Dockerfile (D9: a harness-fix through
semdev's own arc), re-proven cold. Rare and controlled, not per-task.

**Still banned (D6 preserved):** semdev *synthesizing/inferring and running an
unproven toolchain*. What SB2 keeps is the operator declaring a portable standard
image and semdev proving it cold — the cold proof is what lets onboarding stay
honest even for a repo semdev has never seen.

*M0 scope:* the Go fixture's Dockerfile + the build-and-cold-prove path land at
M0. The auto-spawn/block/resume wiring for operator-declares-then-D9-extends on
the hard tiers is M2, but the operator-declared-image contract, the
ambient/task-introduced split, and the cold-proof gate are designed now so
meshtastic/mavlink are a committed devcontainer + task-introduced deps, not a
re-architecture.

### SB2c — Secrets: governed named creds-refs, injected at every proof, never in the artifact or evidence

Resolving real dependencies sometimes needs a secret (the OSH `osh-core` route is
GitHub Packages, which 401s without a PAT). The contract:

- A secret is a **named creds-ref** the operator registers in a **governed store**
  (a git-ignored `.env` with named entries at M0; a real secrets manager later).
  The Dockerfile/devcontainer and the run reference it **by name**, never by
  value.
- semdev injects it only through docker/devcontainer's real secret channels
  (`docker build --secret`, `--env-file` / `-e`, devcontainer `secrets`) at build
  and run — so it is present for cold resolution but **never baked into the image
  layer, never committed to the artifact, never written to logs, tool results, or
  attestation facts** (G7 — evidence stays honest; a secret value must not be
  derivable from any stamped fact).
- **The line (answers the earlier probe):** injecting a *declared* creds-ref to
  resolve a *real* dependency is legitimate and ambient (allowed at every proof —
  baseline, dev, cold verify). What is banned is the harness *rewriting the
  artifact's resolution* to make a dependency appear real (semspec's `init.d`
  substitution). Secrets unlock real resolution; they never mask fabrication.
- A missing required creds-ref **fails closed toward the operator** (register the
  secret), never a degraded/warm fallback that masks (semspec's credential
  fallback was a masking path).

### SB3 — Harness supplies the ENVIRONMENT; the artifact must be self-contained (no fixups)

The clean-room verify proves the **committed artifact** from a `--recursive`
clone of the PR, in a cold fresh environment, with the declared **ambient** infra
but **zero out-of-band harness fixups**. The manifest splits cleanly:

- **Ambient** (the operator-declared image + governed creds-ref, supplied cold to
  every proof, SB2/SB2c): the toolchain and how the *base* deps resolve.
- **Artifact-owned** (must be committed by the run, verified by the cold
  `--recursive` clone): the code, and any build config the resolution needs
  (e.g. a committed composite `settings.gradle` / `includeBuild`).

If the committed build does not resolve cold, that is a **real failure the dev
loop must fix** (commit the proper dependency / composite), not a thing the
harness patches. semspec's fatal bug was injecting the source-substitution as
*ambient* (a harness `init.d`) when a self-contained artifact needs it
*task-introduced* (committed) — so the PR built in the harness and 401'd on a
clean checkout.

*Also banned (manifest tripwire, ported from semspec's `forbidden_patterns`):* no
`web_search` / `http_request` / `raw.githubusercontent.com` in build files — no
hidden runtime downloads that dodge the cold-resolution proof (anti-B/G7).

### SB4 — One profile, three instances; cache-home freshness is the universal control

Sandbox and clean-room are **not two mechanisms** — they are one image (the
operator-declared Dockerfile/devcontainer, SB2) instantiated at three points,
differing only in cache-state:

1. **Cold baseline** (before dev): fresh container + fresh cache → the target at
   HEAD resolves + builds cold. Establishes a known-good green baseline and
   proves the environment is real. (The failing test is the bug; the baseline
   proves *build*, not all-tests-pass.)
2. **Warm dev iterations** (in the run): the provisioned sandbox's cache stays
   warm across `apply_patch` → `measure_task` → floors cycles — for speed
   (stakeholder decision). A newly *added* dependency still cold-misses and
   resolves for real.
3. **Cold final verify** (separate, fresh): a fresh container + fresh cache
   clones the PR `--recursive` and proves resolve + build + test cold. A
   fabrication or non-self-contained fix introduced during the warm phase cannot
   survive this.

**Cache homes are per-run and never shared across runs** — cross-run shared warm
caches are the precise semteams/semspec masking sin. Cache-home freshness
(`GOMODCACHE`/`GOCACHE`; `GRADLE_USER_HOME`+m2 for OSH) is *the* one universal G4
control, orthogonal to the container choice (D5 reaffirmed).

### SB5 — Fail closed; honest tier deferral; never placeholder-pass

- **Absent / failed sandbox → park toward the human** (G2 rule-owned). No
  `sandbox==nil` bypass, no "verified" over zero executions.
- **Readiness gate (D8):** the dev loop proceeds only once a sandbox-scope tier
  has *proven* the claim. A claim only an operator-CI / lab / SITL tier can prove
  is **deferred-and-noted** toward the operator — never gated in-sandbox, never
  faked. The tier line is **harvested from the repo's own test config** (OSH
  already `@Tag`-excludes its SITL tests), so the operator barely draws it.
- **Never encode "couldn't prove" as a pass** — semspec's `placeholder === true`
  anti-pattern is banned; an unprovable claim is a park, not a green.

### SB6 — `apply_patch`: the developer authors, the harness applies + measures (G3)

The developer (Amelia) authors a fix by emitting a unified diff to `apply_patch`;
the harness applies it inside the provisioned container checkout and
`measure_task` measures the **real** result. The model supplies the intelligence
(the diff); the harness supplies the outcome (G3 — no LLM-supplied pass/fail).
Under the mock, the fixture scripts the exact diff (mock the intelligence, not
the plumbing); the measurement is harness-real against the sandbox. This is the
code-authoring mechanism semdev entirely lacked.

*Schema (G3):* `apply_patch` takes only the diff/target — never an outcome. The
diff writes to the run's isolated checkout only (path-guarded to `/work`), never
the host.

### SB7 — Provisioning is rule-owned and self-extinguishing (G2, restart-safety)

The provision-and-prove-cold station and the readiness gate are **rules** (G2 —
no product-Go lifecycle transition; the harness stamps facts, a rule routes).
Every `publish_agent` spawn is **self-extinguishing** (fired-once marker +
absence guard) per the house restart-safety pattern (a graph replay with
`RULE_STATE` lost must not re-provision or double-spawn). Provisioning failures
that the engine cannot express park toward the human (G2) with the upstream
semstreams ask filed — never a silent Go reconciler (the B3 disease).

### SB8 — Ports: reuse the shape, ban the masking

- **Reuse (S-list):** the single-component provisioner shape + attestation-routing
  (semteams, minus the warm reuse); **the standard Dockerfile/devcontainer format
  as the operator's declaration** (semteams used devcontainers; we take the format
  but own the run for freshness); the pure `verify.Decide` + `internal/cleanroom`
  Runner seam + `LocalRunner` fresh-cache logic (already built); the
  `forbidden_patterns` tripwire (semspec fixtures).
- **Ban (B-list):** shared warm caches across runs; harness-injected resolution
  that doesn't propagate to the artifact; toolchain-presence-as-readiness;
  **semdev harvesting/inferring/synthesizing the toolchain** (the operator
  declares it, SB2); `devcontainer up` warm-reuse across runs; the execution-bridge
  Go reconciler (B3); the `sandbox==nil` skip; placeholder-pass; secrets in the
  image layer / artifact / evidence (SB2c).

## Risks / Trade-offs

- **[Warm dev cache can mask a fabrication *within* a run]** → the separate cold
  final verify (SB4.3) is the backstop; it clones the PR fresh and re-proves
  cold. The warm phase is never a gate.
- **[docker required at run time]** → fail closed when absent (SB5); unit tests
  use `LocalRunner`/mock shims and need no docker; the e2e journey already
  requires docker (NATS compose), so CI parity holds.
- **[Operator must declare an image per repo — a DevX cost]** → intentional, and
  cheaper than the alternative: it deletes the flaky harvest/inference engine, and
  the format is standard (many repos already ship a devcontainer/Dockerfile). The
  cost is bounded (once per repo, committed, reused) and it puts the environment
  knowledge where it lives — with the operator.
- **[Container provisioning latency per run]** → acceptable at M0 (one fixture);
  image is built once + digest-pinned; the warm dev phase amortizes within a run.
- **[Dependency-resolution token thrash]** (semspec burned ~3.5M tokens) → the
  dev loop works against a *known-good cold baseline* (SB4.1) in the operator's
  declared image, not a broken environment, and `apply_patch` can express
  artifact-owned build config, so the developer is not thrashing to
  reverse-engineer resolution.
- **[A repo declares an image that builds warm but not cold]** → the SB2 cold
  proof catches it before any dev run relies on it; parks toward the operator. The
  cold proof is the backstop that makes an operator-declared image trustworthy.

### Group-4 seam carry-forwards (runspace, from the 4A review — settle before the live loop)

- **[All-absent attempt reads green (SB5 theater)]** → the `Attempts` seam faithfully
  reports which declared target files a dev iteration authored, but the five floors do
  NOT reject an attempt that authored NONE of its targets (`tests-must-exist`/`anti-mock`
  "do not apply" on an empty set → all pass → `check_floors` rejected=false). Before
  `check_floors` runs against a live checkout (group 4B/7), add a PRESENCE gate — an
  attempt whose declared `TargetFiles` are absent from `Files` must reject or park — in
  `check_floors` / the floors (compare `len(Files)` vs `TargetFiles`), not the resolver.
  Not reachable at M0 (the `go-health-class` fixture ships both target files; the loop is
  not yet live).
- **[Checkout restart-safety + destructive re-materialize]** → a run's checkout is
  in-memory infra (lost on restart) and `Materialize` is idempotent-destructive (a new
  copy replaces the prior). Two contracts the provision rule (SB7) must honor: (a) its
  self-extinguishing absence guard must key on the physical checkout STILL EXISTING (a
  lost in-memory checkout after a restart must re-provision, not stay parked on the
  fired-once marker); (b) it must NOT re-materialize mid-loop (that would discard
  apply_patch's in-progress work, SB6). Fail-closed-on-missing is the correct posture; the
  open question is the recovery path (re-materialize-on-restart vs park-and-human-retrigger).
- **[Symlink containment is lexical at M0]** → `copyTree` skips symlinks (closing the
  escape surface for the M0 local copy) and `safeJoin` is a lexical `filepath.Rel` check.
  When the M2 real `--recursive` clone preserves symlinks, add `EvalSymlinks`-based
  containment (or `O_NOFOLLOW`) so a symlink-based path escape cannot slip past.
- **[Checkout temp-dir leak]** → `Checkouts` has no teardown wired; materialized `run-*`
  dirs and the per-process base leak for the process lifetime (M0-acceptable, journey-
  driven/short-lived). Wire a `Close`/reap when the runtime lifecycle owns the checkouts
  (group 5).

### Group-5 provision-station decisions (as built; architect-validated)

- **Readiness gate = a condition, not a standalone rule.** SB5's "the dev loop
  proceeds only on a proven sandbox-scope tier" is realized as a `sandbox.ready eq true`
  condition on the dev-loop-proceed rule (`dev-from-task/02`, the current head of the dev
  rail), pinned by `TestDevRewakeGatedOnSandboxReadiness`. A separate no-op "gate rule"
  would be redundant — a gate is a precondition, and the rule that advances is the one
  that must carry it. When the dedicated developer dispatch lands (g7 dispatch-developer),
  the same `sandbox.ready` gate moves onto it.
- **Ordering is data-enforced, not timing-lucky.** approval → `project_tasks`
  (dev-from-task/03) → `provision_sandbox` (sandbox/01, gated on `task.spec.0.test_command
  ne ""`) → `decide(dev_from_task)` (dev-from-task/02, gated on task.spec AND
  `sandbox.ready`). Each forced-tool turn is gated on the prior's output fact, so the
  mock's positional cursor cannot be raced and the sequence holds under any scheduling.
- **Source resolution = an injected `Sources` seam** (`runspace.StaticSource`), NOT a
  graph fact. The run's portable coordinate already lives in the graph as `run.issue_ref`;
  the seam resolves that (M2 forge-io) or a boot-configured constant (M0 fixture) into a
  HOST PATH that stays out of the graph (same class as the checkout dir). M2 carry-forward:
  the seam's return contract may flip from a host dir to a clone coordinate when the real
  `--recursive` clone lands (the clone likely moves INTO `Materialize` to avoid a
  clone-then-`copyTree` that mangles submodules/`.git`/symlinks — colliding with the
  symlink-containment carry-forward above).
- **Restart-safety: fired-once marker at M0; recoverable checkout at M2.** `sandbox/01`
  uses the house self-extinguishing pattern (`sandbox.provisioned` marker stamped before
  the publish + `length_eq 0` guard), correct for the single-process e2e. The split
  durability (marker + `sandbox.ready` survive a restart; the in-memory `Checkouts` map
  does not) is UNREACHABLE at M0 and, if it occurred, is a fail-closed park (the dev
  tools' `Workspace.Root` errors → no false verify), never a silent proceed. The M2 fix
  (architect-preferred over a durable checkout-provenance fact or a B3 reconciler): make
  `Checkouts` recoverable — a deterministically run-keyed dir under a STABLE configured
  base, so `Root` re-adopts the surviving on-disk checkout after a restart (stat-and-adopt,
  never re-`Materialize` — which is destructive and would wipe apply_patch work). That one
  change dissolves the split-durability wedge AND retires the temp-dir-leak carry-forward
  (`Remove` becomes `rm base/run-<id>`, a reaper can GC terminal runs). The rule guard
  stays the fired-once marker — NOT a `sandbox.ready`-absence re-fire, which would spawn
  duplicate provision loops during the 30–60s cold-proof window.
- **Fail-closed park writes `run.awaiting_human`** (`sandbox/02-park-unprovable`), the one
  park marker the D15 park-exclusion contract reads — the park-rule subsystem is one
  logical G5 writer realized by two rule files (this + run-lifecycle/03), differing only in
  trigger (an unprovable sandbox vs a coordinator `ask_human`). Confirmed acceptable by the
  reviewer against the framework: rule `add_triple` stamps a constant `Source="rule_engine"`
  (`processor/rule/actions.go`), so the two writes are byte-indistinguishable at the write
  layer — the exact "sole-writer-was-a-false-comment" disease G5 targets cannot occur, and
  the vocab's single `run.awaiting_human` entry is not drifted.

### Group-5 carry-forwards (from the increment reviews — settle before the forge-io respond handler)

- **[Resume of a sandbox-blocked run must un-provision, not just un-park] (MEDIUM)** → the
  fired-once `sandbox.provisioned` marker that correctly prevents duplicate provisioning
  ALSO prevents *re*-provisioning after an operator fixes the declared image. So a resume
  path that clears ONLY `run.awaiting_human` leaves the run un-parked but wedged: `sandbox/01`
  won't re-fire (marker present), `sandbox.ready` never appears, `dev-from-task/02` never
  fires. The group-5 forge-io respond handler MUST, on resume of a sandbox-blocked run,
  clear `sandbox.blocked` + `sandbox.provisioned` + the readiness package so a re-provision
  can fire — capture this as an explicit resume-path contract alongside the D15 "respond
  removes `run.awaiting_human`" contract. Unreachable now (resume path unbuilt).
- **[Park PATH is offline-pinned only, not e2e-exercised]** → the green journey takes the
  READY path; `sandbox/02`'s `run.awaiting_human`=`$entity.triple.sandbox.blocked` on the run
  entity and the `user.response.$entity.instance` bus post are structurally identical to the
  proven `run-lifecycle/03` shapes (so they resolve), and each half is unit/structurally
  pinned (the tool's six `block()` tests + `TestSandboxParkOnUnprovable`), but the
  tool-blocks→rule-parks INTEGRATION has no end-to-end proof. Add a blocked-fixture park
  journey (docker-absent or a non-buildable declared image) when the resume handler lands.
- **[The "one logical park writer, N realizations" invariant lives only in prose]** → nothing
  offline stops a future THIRD writer of `run.awaiting_human` that omits the `length_eq 0`
  fire-once guard (which would re-post to the user bus every re-scan). A naive "every writer
  must guard" pin false-positives on the intentionally-unguarded decision-driven
  `run-lifecycle/03`, so this needs a targeted pin (guard required unless the trigger is a
  coordinator decision), not a blanket one. Known soft spot.

### Group-7 dev-loop decisions + carry-forwards (as built; architect-validated, from the increment reviews)

- **[Token handshake DROPPED → pure loop-terminal chaining] (architect ruling + #519 research)** →
  the original blueprint's per-attempt "token handshake" (measure stamps `measurement.result.<i>.attempt`,
  floors echoes it, the gate compares `floor.finding.<i>.attempt eq measurement.result.<i>.attempt`) is
  UNNECESSARY and UNBUILDABLE. Unbuildable: those gate conditions are field-to-field, which floods the
  semstreams #519 WARN (the `$entity.triple.` value form, guarded by `TestChangeApprovalGateFreshnessForwardContract`).
  Unnecessary: the chain is strictly serial (dispatch → developer → measure → floors → gate, one developer
  loop in flight per task, retries gate-dispatched), so "measure and floors evaluated the same attempt" is
  guaranteed by construction — the only checkout writer is `apply_patch` inside a developer loop, and none
  runs between measure's Exec and floors' Resolve, so the bytes are frozen. Each stage instead fires on the
  PRIOR loop's terminal via a tool-stamped loop marker (`dev.measure_done` on the measure loop → floors;
  `dev.floors_done` on the floors loop → gate, 7D), all warn-free literal conditions. Per-attempt re-arm comes
  from fresh loops per attempt (the 7B property), not a token.
- **[The serialization invariant is now LOAD-BEARING for correctness] (7D pin)** → dropping the token means
  "one developer loop in flight per task; only dispatch-developer and the gate's retry branch may spawn a
  `role=developer` loop into a run" is no longer just tidiness — it replaces the token's matched-attempt guard.
  Add a conformance pin asserting exactly that WITH group 7D (G6: pin the change that creates the failure mode —
  7C's arc is strictly linear with no retry driver, so no second developer loop can exist yet).
- **[Floor fidelity is coupled to model-authored `target_files` including the test file] (MEDIUM)** → the
  Attempts resolver evaluates the CURRENT contents of the task's DECLARED `target_files` (it includes any
  target that exists on disk, touched or not). So `TestsMustExist` only sees a test when a `_test.go` is a
  declared target. `target_files` originates from the change proposal `create_change` authors (effectively
  model-authored). If a real task's `target_files` omits its test, `TestsMustExist` FALSE-PARKS a correct fix —
  which is exactly why the journey's task declares BOTH `health.go` and `health_test.go`. Before the first
  real-LLM token: either a `project_tasks`/`create_change` contract that always scopes the test file alongside
  production targets, OR a resolver that derives sibling `_test.go` targets — plus a pin. Not 7C-introduced
  (the group-3/4 resolver contract), but 7C makes it LIVE.
- **[Floors reject-teeth are offline-only; station 12 is the happy-path bridge]** → the live journey proves
  the floors RUN and PASS over a clean fix (`floor.finding.0.rejected=false`); a floor CATCHING fabrication
  end-to-end is covered offline (`TestPresenceFloor`, `TestCheckFloorsVacuousTestRejected`), not in the arc.
  Correctly-labeled bridge proof — add a rejecting-fixture station when a fabrication fixture variant drives
  the loop (g10/g11).
- **[measure_task's marker-write-after-measurement stall] (LOW, folds into the dev-loop-rail cap-exhaust gap)** →
  measurement (run) then `dev.measure_done` (loop) are two non-atomic writes; a persistent marker-write failure
  trips MaxIterations with no `run.awaiting_human` (marker-less measure loop chains nothing). Same posture as the
  reviewed `create_change` two-write; the escalate/park lands in 7D. Ordering (substance-first) is correct.
- **[The gate is a TOOL, not rule conditions] (7D, as built — the architect's ruling made executable)** →
  `check_gate` (`internal/tools/checkgate`) reads `measurement.result.<i>.passed` + `floor.finding.<i>.rejected` +
  the DISTINCT-object count of `task.attempt.<i>` vs `task.spec.<i>.budget` and derives advance/retry/escalate in
  Go. Three blockers force this out of rule conditions: a loop-fired rule cannot read run facts; distinct-object
  counting is not a `length_*`; a scalar field-to-field compare floods #519. The route is still G3-derived (schema
  is `task_index` only) and it fires no transition (G2) — it stamps `dev.gate.<i>.{decision,reason}` evidence + the
  `dev.gate_decision` loop marker; three router rules (`08a/b/c`) act on the marker. It FAILS CLOSED: a missing
  judgment fact escalates (never a false retry); a read fault is a retryable tool error, not a stamped decision.
  The policy is a pure `Decide` pinned exhaustively offline (the live loop only drives the advance path). The
  chain extends the loop-marker pattern one more link (`dev.floors_done` on the floors loop → gate).
- **[The serialization invariant is now pinned] (7D, as built)** → `TestOnlySanctionedDeveloperSpawners` asserts
  ONLY dispatch-developer (04) and the gate's retry router (08b) spawn a `role=developer` loop — the load-bearing
  guarantee (replacing the dropped token handshake) that measure and floors evaluated the same frozen checkout,
  because exactly one developer loop is ever in flight per task. A third developer-spawner would race the shared
  checkout and mis-count the budget; the pin fails the build if one is added.
- **[Escalate is the THIRD park realization] (7D, as built)** → `dev-from-task/08c` stamps `run.awaiting_human`
  on budget exhaustion / fail-closed, joining run-lifecycle/03 (ask_human) and sandbox/02 (unprovable sandbox) as
  the single logical park writer realized by three rule files. It fires on the gate LOOP (not the run), so its
  self-extinguish guard MUST be loop-scoped (`dev.routed`), not `run.awaiting_human length_eq 0` — a run-scoped
  guard would read absent on the loop every rescan and re-post to the user bus. (sandbox/02 fires on the run, so
  it correctly uses the run-scoped guard; the two are not interchangeable.)
- **[The retry path is offline-only; station 13 is the advance bridge] (7D carry-forward, semstreams-reviewer MEDIUM)** →
  the live journey proves the gate ADVANCES a clean attempt; retry (re-dispatch → re-measure → re-gate) and escalate
  (park) are pinned offline (`checkgate` decision table + the router structure pins), not driven end-to-end. A
  retry/escalate journey needs a fixture whose first attempt fails then a second passes (retry), or never passes
  (escalate) — add it alongside the rejecting-fixture floors station when a fabrication variant drives the loop
  (g10/g11). **Load-bearing mechanism the e2e should protect:** the retry chain is a series of `run_scope=inherit`
  CROSS-loop spawns, so a per-loop `MaxIterations` cap does NOT bound it — the ONLY runtime bound is `check_gate`'s
  distinct-count-vs-`budget`, which itself depends on `dev-from-task/05` appending a distinct `task.attempt.<i>` per
  attempt. If that append silently regressed, the loop would exceed budget with nothing to catch it. The gate counts
  correctly today (pinned), but an integration test that forces ≥1 retry and ≥1 budget-exhaust escalate is the
  missing guard on the gate's core purpose. Deferred like the sandbox blocked-park e2e.

### Group-8 clean-room cold-verify decisions + carry-forwards (as built; architect-ruled, semstreams-reviewer 8A)

- **[The cold verify is the THIRD sandbox instance — a fresh CLONE + fresh cache, not the warm checkout] (8A, as built)** →
  `verify_artifact` was rewired off `LocalRunner`+warm-checkout (a masking hole) onto `coldproof.ProveArtifact` (the
  BuildImage→fresh throwaway container→Gather core, sharing the anti-masking prologue `proveCold` with the baseline so a
  fabrication reads IDENTICALLY in both) over `runspace.Checkouts.CloneForVerify` — a fresh copy of the warm checkout's
  bytes (the committed artifact at M0; apply_patch stores no durable diff, so the tree IS the commit) tracked in a
  SEPARATE `verifyRoots` map. The masking defense is fresh cache + zero fixups, NOT the clone provenance: the cold
  container mints fresh anonymous cache volumes, so a cache-masked fabrication or a harness-only fixup that never hit the
  committed bytes FAILS here (docker-gated `TestProveArtifactReal{Pass,TestsFailIsFail,FabricationIsFail}`). verify uses
  `TestCmd` (proves the artifact's own tests, which compile it), the baseline uses `BuildCmd`.
- **[CloneForVerify must NEVER re-materialize] (8A trap, pinned)** → calling `Materialize` for verify would wipe the
  applied diff (the group-4 destructive-re-materialize trap); `CloneForVerify` reads `roots[run]` and writes only
  `verifyRoots[run]`, non-destructive of the warm checkout (`TestCloneForVerifyIsFreshAndNonDestructive`).
- **[8D carry-forward: single-verify-per-run serialization] (semstreams-reviewer 8A L2)** → `CloneForVerify` reaps the
  prior clone on re-clone; two concurrent verifies for one run would reap an in-flight clone out from under a
  `ProveArtifact` bind-mount → the exec faults → Retry (fail-closed, never a false green). Not triggered in 8A (no rule
  wires verify). The 8D verify-trigger MUST preserve one-verify-in-flight (the same serialization posture as the
  one-developer-in-flight invariant) — the loop-marker chain (`dev.reviewed` → verify → `dev.verified`) gives this for
  free as long as no second verify-spawner is added.
- **[8A L3, unreachable]** → `ProveArtifact`'s incomplete-manifest branch is classed retryable (verify_artifact maps it
  to a network-kind tool error) rather than an operator park like `ProveBaseline` — unreachable in the live flow
  (`ResolveManifest` guarantees `TestCmd`, and the baseline already proved `ResolveCmd`+`CacheHomeEnvs`). Defense-in-depth
  only; if verify ever parks the operator on a declaration fault, split this branch.
- **[8B script-indirection close — the tripwire is the SOLE static control] (semstreams-reviewer 8B HIGH, fixed)** → the
  cold-verify container runs WITH network (its job is to resolve declared deps), so a smuggled build-time fetch (`RUN
  ./setup.sh` where the script curls a raw URL) would ALSO succeed cold — the cold proof is blind to it by design. The
  `forbidden` scan is therefore the only control for the class, so it scans the build files AND the scripts they invoke
  (`*.sh`/`*.bash`/`gradlew`), skips vendor/testdata, and is framed as an HONEST denylist (it catches the known vectors,
  does not claim to prove self-containment). A build ecosystem not yet in the scan set is a fail-open residual to close
  as new profiles land (the JVM wrapper `distributionUrl` files are stubbed for the OSH M2 target).
- **[8C review station — the first role=reviewer loop; role choice confirmed] (8C, semstreams-reviewer APPROVE)** → D16's
  per-task review is realized by `dev-from-task/09`, co-firing on the gate loop's `dev.gate_decision eq advance` (distinct
  `dev.review_dispatched` marker from 08a's `dev.routed`) and spawning Quinn (`role=reviewer` — the seeded reviewer
  persona/model; the ONE non-coordinator forced-tool loop, since submit_review is Quinn's D16 gate). The verdict stays
  harness-DERIVED (floored by the measurement, G3), so role=coordinator would be functionally identical — role=reviewer is
  the roster-honest choice AND provably re-triggers no rule (no rule keys on role=reviewer; the verify station keys on the
  `dev.reviewed` loop marker). submit_review stamps `dev.reviewed` on its own loop (value = task index) after the verdict,
  even for `changes_requested` (a blocking review must still reach the coherence gate). **⚠ 8D FORWARD CHECK (reviewer
  note):** the verify-trigger will fire on `dev.reviewed` on the REVIEW loop and use `run_scope=inherit` to reach the run —
  verify explicitly that a `role=reviewer`, `run_scope=inherit` loop carries the `agent.run` anchor (it is inert in 8C, so
  8C did not exercise the run-binding of a reviewer loop). The gate/floors/measure loops (role=coordinator) all bind
  correctly; a reviewer loop is the first of its role to be depended on for chaining.

## Migration Plan

Infra-first sequence (each rung proven before the next):
1. Container `Runner` (docker build/up/exec/down) + fail-closed + `MockRunner`/
   `LocalRunner` parity; unit + integration pins.
2. Go fixture (`test/fixtures/go-health-class`, real bug) + its committed
   `Dockerfile`/devcontainer (`FROM golang:1.26`) + the `customizations.semdev`
   test-command/tier bits; the build-image-and-prove-base-cold path.
3. Wire the `Manifests` (declared-image ref) / `Workspace` / `Attempts` seams to
   the container.
4. Provision-and-prove-cold station + attestation facts + readiness gate
   (rule-owned, self-extinguishing); fail-closed pins; secrets governance (SB2c).
5. `apply_patch` tool (path-guarded, G3).
6. dispatch-developer + the bounded loop running **in** the sandbox
   (`measure_task` / floors in-container).
7. Clean-room verify wired to the container runner (cold, `--recursive`,
   no-fixups); the open_pr coherence gate.

Rollback: the change is additive behind the `Runner` seam; reverting to
`LocalRunner` restores the prior (rejected) local path without touching callers.

## Open Questions

- **Where the semdev-specific run bits live** — a `customizations.semdev` block
  inside the devcontainer vs. a tiny sidecar vs. pure convention (`go test` /
  `gradlew test`; repo test exclusions → tier line). Lean: devcontainer
  customizations when a devcontainer is present, convention otherwise. Pin in the
  spec.
- **devcontainer realization** — build the image via `devcontainer build` and then
  semdev-owned `docker run`, vs. parse the devcontainer's referenced Dockerfile
  directly. Either keeps semdev-owned freshness; M0 Go uses a bare Dockerfile so
  this defers.
- **Where the cold-verify `--recursive` PR clone comes from at M0** — the fixture
  is in-repo (no real PR yet); the M0 verify clones the fixture-with-the-applied-
  diff into a fresh dir. The real PR-clone lands with forge-io (M2).
- **OpenSpec reconciliation** — this change owns the net-new `sandbox` capability
  (all `ADDED`); `dev-from-task` / `clean-room-verify` (delta specs in the
  unarchived `m0-walking-skeleton-spine` change) *consume* it by reference, so no
  `MODIFIED` delta stacks on that change. Confirm both archive together, or this
  archives after m0.
