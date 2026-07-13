## MODIFIED Requirements

### Requirement: The developer authors fixes via apply_patch, measured in-sandbox

The developer SHALL author code changes by emitting a diff to an `apply_patch`
tool whose schema takes only the patch/target and no outcome field (G3). The
harness SHALL apply the diff to the run's isolated container checkout
(path-guarded to the checkout, never the host) and SHALL reject any diff
touching a file outside the approved `task.spec.target_files`. After a
successful apply, the harness SHALL commit the checkout and stamp the commit
as `attempt.commit` (latest-wins), so measurement and later verification
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
- **THEN** the checkout is committed and `attempt.commit` records the commit
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
- **WHEN** the process restarts after `sandbox.ready` was stamped and later work needs the checkout or container
- **THEN** the resolver reconstructs the checkout at its recorded commit and re-establishes the container from the pinned digest
- **AND** the run proceeds to completion without human intervention

#### Scenario: Reconstruction preserves committed attempt state
- **WHEN** reconstruction runs for a checkout carrying a recorded `attempt.commit`
- **THEN** the reconstructed checkout is at that commit, not the pristine base

### Requirement: The clean room proves the committed artifact with no harness fixups

The final clean-room verify SHALL prove the **committed** artifact — a
`--recursive` clone of the run's checkout at its recorded `attempt.commit` —
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
- **THEN** it clones at the recorded `attempt.commit`
- **AND** uncommitted working-tree bytes cannot enter the proof
