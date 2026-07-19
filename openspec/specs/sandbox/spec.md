# Sandbox Specification

## Purpose

Defines the per-run containerized sandbox in which semdev's dev loop,
measurement, and clean-room verification execute: an operator-declared image
proven cold before use, fresh cache homes per proof, governed secret injection,
and fail-closed parking whenever the sandbox is absent or unprovable. It also
governs how the developer's apply_patch changes are applied, committed, and
measured in-sandbox, and how provisioning and readiness are rule-owned,
self-extinguishing, and restart-safe.

## Requirements

### Requirement: Dev-loop and verify execute in a per-run container

The dev loop and the clean-room verify SHALL execute inside a per-run container
built from the operator-declared image, never on the host filesystem. A fresh
container SHALL be stood up per run; the host `LocalRunner` is a unit-test /
no-docker fallback only and SHALL NOT be the run path. Within a single run, dev
iterations MAY share the run's container (warm), but the container SHALL NOT be
shared across runs.

#### Scenario: The dev loop runs in a fresh per-run container
- **WHEN** a run reaches the dev loop
- **THEN** measurement and floor checks execute inside a container stood up for
  that run from its declared image
- **AND** no dev-loop command runs on the host filesystem

#### Scenario: Containers are not reused across runs
- **WHEN** two runs execute against the same repo
- **THEN** each run gets its own container instance (no `--remove-existing-container=false`
  reuse), so one run cannot inherit another's on-disk or process state

### Requirement: The environment is an operator-declared image, never harvested or guessed

The container environment SHALL be declared by the operator as a standard
`Dockerfile` and/or `.devcontainer/devcontainer.json` committed to the target
repo. semdev SHALL build that declared image; it SHALL NOT harvest, infer, or
synthesize a toolchain, and it SHALL NOT define a bespoke environment DSL. A repo
that declares no usable image SHALL fail closed toward the operator, never a
guess. The few semdev-specific run fields (test command, tier split, secret refs)
ride a `customizations.semdev` block or convention, not an environment DSL.

#### Scenario: Declared image is built and used
- **WHEN** a repo commits a `Dockerfile` / devcontainer
- **THEN** semdev builds that image (digest-pinned) and provisions the run's
  sandbox from it

#### Scenario: A repo with no declared image fails closed
- **WHEN** a run targets a repo that declares no `Dockerfile` / devcontainer
- **THEN** the run parks toward the operator to declare one
- **AND** no dev loop, measurement, or verification is attempted

### Requirement: The image is proven cold before the dev loop relies on it

Before the dev loop begins, the system SHALL build the declared image and prove
the repo resolves its base dependencies and builds **cold** (a fresh cache home),
stamping a readiness/attestation fact. If the cold proof fails, the run SHALL park
toward the operator; the dev loop SHALL NOT proceed on an unproven environment.
The baseline proof is resolve+build (not tests-pass), because the task's own
change and test may not exist yet.

#### Scenario: A repo whose image builds it cold becomes ready
- **WHEN** provisioning builds the declared image and the repo resolves + builds
  cold
- **THEN** a readiness attestation is stamped and the dev loop may proceed

#### Scenario: An image that cannot build the repo cold parks
- **WHEN** the declared image cannot resolve the repo's base dependencies or build
  it in a fresh cache
- **THEN** the run parks toward the operator (fix the declared image)
- **AND** no readiness attestation is stamped

### Requirement: A fresh cache home per proof, never shared across runs

Every cold proof (baseline and final verify) SHALL use a fresh (cold) cache home
so that a dependency masked by an accumulated cache cannot pass. Cache homes SHALL
NOT be shared across runs. Dev-loop iterations within a run MAY reuse the run's
warm cache for speed; the warm phase is never a verification gate.

#### Scenario: A fabricated dependency fails cold
- **WHEN** an artifact declares a dependency that only resolves from an
  accumulated warm cache
- **THEN** the cold proof (fresh cache) fails to resolve it and does not pass

#### Scenario: Runs do not share cache state
- **WHEN** two runs execute
- **THEN** each cold proof uses a distinct fresh cache home, so one run's
  resolution cannot mask another's

### Requirement: The clean room proves the committed artifact with no harness fixups

The final clean-room verify SHALL prove the **committed** artifact — a
`--recursive` clone of the run's checkout at its recorded `attempt.commit.sha` —
in a separate fresh cold environment, supplying only the declared ambient
environment (image + governed creds-refs) and applying **no out-of-band
fixups** to the artifact's resolution. An artifact that builds only because of
a harness-injected fixup SHALL fail. If the committed build does not resolve
cold, that is a dev-loop failure to fix (commit the proper dependency /
composite), not a thing the harness patches.

#### Scenario: A non-self-contained artifact fails the cold verify
- **WHEN** an artifact builds only because the harness injected a
  source-substitution or resolution override that is not committed to the artifact
- **THEN** the cold `--recursive` verify (no fixups) fails
- **AND** the run does not reach open_pr

#### Scenario: Build files may not smuggle runtime downloads
- **WHEN** a build file contains a hidden runtime fetch (e.g. `http_request` /
  `raw.githubusercontent.com`) that dodges cold resolution
- **THEN** a deterministic tripwire rejects it

#### Scenario: The clean room never sees the mutable working tree
- **WHEN** the clean-room verify materializes its input
- **THEN** it clones at the recorded `attempt.commit.sha`
- **AND** uncommitted working-tree bytes cannot enter the proof

### Requirement: Secrets are governed named refs, never in the image, artifact, or evidence

A secret required for real dependency resolution SHALL be a named creds-ref from a
governed store, injected only through the container's real secret channels
(build secret / env) at each proof. A secret value SHALL NOT be baked into an
image layer, committed to the artifact, or written to any log, tool result, or
attestation fact. A missing required creds-ref SHALL fail closed toward the
operator, never a degraded fallback that masks resolution.

#### Scenario: A creds-ref resolves a real dependency without leaking
- **WHEN** a run uses a declared creds-ref to resolve an authenticated dependency
- **THEN** resolution succeeds cold
- **AND** the secret value appears in no log, tool result, or stamped fact

#### Scenario: A missing required secret parks
- **WHEN** a required creds-ref is not registered in the governed store
- **THEN** the run parks toward the operator to register it
- **AND** no warm/anonymous fallback is used to mask the missing secret

### Requirement: Absent or unprovable sandbox fails closed

The run SHALL park toward the human when docker is unavailable, when provisioning
fails, or when no sandbox-scope tier can prove a claim. The system SHALL NOT
report measurement, review, or verification over an absent or unproven sandbox,
and SHALL NOT record an unprovable claim as a passing outcome.

#### Scenario: Docker absent parks, never false-verifies
- **WHEN** docker is unavailable at run time
- **THEN** the run parks toward the human
- **AND** no `verify.cleanroom.result`, measurement, or "verified" fact is stamped

#### Scenario: An unprovable claim is deferred, not passed
- **WHEN** a claim can only be proven by an operator-CI / lab / SITL tier the
  sandbox cannot run
- **THEN** it is deferred-and-noted toward the operator
- **AND** it is never gated in-sandbox and never recorded as a pass

### Requirement: The developer authors fixes via apply_patch, measured in-sandbox

The developer SHALL author code changes by emitting a diff to an `apply_patch`
tool whose schema takes only the patch/target and no outcome field (G3). The
harness SHALL apply the diff to the run's isolated container checkout
(path-guarded to the checkout, never the host) and SHALL reject any diff
touching a file outside the approved `task.spec.target_files`. After a
successful apply, the harness SHALL commit the checkout and stamp the commit
as `attempt.commit.sha` (latest-wins), so measurement and later verification
consume exactly the committed tree. The measurement harness SHALL stamp the
pass/fail from actually running the task's command in the sandbox. No schema
in this path SHALL accept an LLM-supplied outcome.

#### Scenario: A scripted diff is applied and measured for real
- **WHEN** the developer emits a diff via apply_patch
- **THEN** the harness applies it inside the sandbox checkout and measures the
  real result of the task's test command
- **AND** the recorded pass/fail is derived from the command's real exit status,
  not supplied by the model

#### Scenario: apply_patch cannot escape the checkout
- **WHEN** a diff targets a path outside the run's checkout
- **THEN** apply_patch rejects it

#### Scenario: apply_patch cannot escape the task contract
- **WHEN** a diff touches a file not listed in the task's `target_files`
- **THEN** apply_patch rejects it and no partial application occurs

#### Scenario: Each applied attempt is committed and stamped
- **WHEN** apply_patch succeeds
- **THEN** the checkout is committed and `attempt.commit.sha` records the commit
- **AND** the measured tree is identical to the committed tree

### Requirement: Provisioning and readiness are rule-owned and self-extinguishing

Sandbox provisioning, attestation, and the readiness gate SHALL be rule-owned —
no product Go SHALL fire a lifecycle transition for them (G2); the harness stamps
facts and rules route. Every provisioning spawn SHALL be self-extinguishing (a
fired-once marker guarded by its own absence) so a graph replay with lost
rule-state cannot re-provision or double-spawn. Provisioning SHALL be
restart-safe: durable readiness facts SHALL carry the inputs needed to
reconstruct the workspace and sandbox (source reference, pinned image digest,
checkout base commit), and any resolver that finds durable readiness without
its process-local resource SHALL reconstruct idempotently from those facts —
never no-op over the gap and never destroy an in-progress attempt's committed
state.

#### Scenario: A replay does not re-provision
- **WHEN** the rule engine re-evaluates a run whose sandbox was already provisioned
- **THEN** the provisioning spawn does not fire again (its fired-once marker guards
  it), so no duplicate sandbox or loop is spawned

#### Scenario: A process restart does not wedge an admitted run
- **WHEN** the process restarts after `sandbox.provision.ready` was stamped and later work needs the checkout or container
- **THEN** the resolver reconstructs the checkout at its recorded commit and re-establishes the container from the pinned digest
- **AND** the run proceeds to completion without human intervention

#### Scenario: Reconstruction preserves committed attempt state
- **WHEN** reconstruction runs for a checkout carrying a recorded `attempt.commit.sha`
- **THEN** the reconstructed checkout is at that commit, not the pristine base
