# Sandbox — security-forge-containment delta

## MODIFIED Requirements

### Requirement: The developer authors fixes via apply_patch, measured in-sandbox

The developer SHALL author code changes by emitting a diff to an `apply_patch`
tool whose schema takes only the patch/target and no outcome field (G3). The
harness SHALL apply the diff to the run's isolated container checkout
(path-guarded to the checkout, never the host) and SHALL reject any diff
touching a file outside the approved `task.spec.target_files`. After a
successful apply, the harness SHALL commit the checkout and stamp the commit
as `attempt.commit.sha` (latest-wins), so measurement and later verification
consume exactly the committed tree. The attempt commit SHALL contain only
content introduced by contract-authorized diffs: working-tree content that did
not arrive through an applied diff (residue from tooling, prior attempts, or
measurement side effects) SHALL NOT be staged or committed, silently or
otherwise. The measurement harness SHALL stamp the pass/fail from actually
running the task's command in the sandbox. No schema in this path SHALL accept
an LLM-supplied outcome.

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

#### Scenario: Working-tree residue is not committed
- **WHEN** the checkout carries a file that no applied diff introduced (e.g. an
  artifact left by a prior attempt's test run) at commit time
- **THEN** the attempt commit does not contain that file
- **AND** the residue remains visible to the deterministic floors rather than
  being absorbed into the committed tree

### Requirement: The environment is an operator-declared image, never harvested or guessed

The container environment SHALL be declared by the operator as a standard
`Dockerfile` and/or `.devcontainer/devcontainer.json` committed to the target
repo. semdev SHALL build that declared image; it SHALL NOT harvest, infer, or
synthesize a toolchain, and it SHALL NOT define a bespoke environment DSL. A repo
that declares no usable image SHALL fail closed toward the operator, never a
guess. Repo-authored paths inside the declaration (Dockerfile location, build
context) SHALL resolve to locations inside the run's checkout; a declared path
that resolves outside the checkout SHALL fail closed exactly like a repo with no
buildable image, and no host path outside the checkout SHALL enter the image
build context. The few semdev-specific run fields (test command, tier split,
secret refs) ride a `customizations.semdev` block or convention, not an
environment DSL.

#### Scenario: Declared image is built and used
- **WHEN** a repo commits a `Dockerfile` / devcontainer
- **THEN** semdev builds that image (digest-pinned) and provisions the run's
  sandbox from it

#### Scenario: A repo with no declared image fails closed
- **WHEN** a run targets a repo that declares no `Dockerfile` / devcontainer
- **THEN** the run parks toward the operator to declare one
- **AND** no dev loop, measurement, or verification is attempted

#### Scenario: A traversal path in the devcontainer declaration fails closed
- **WHEN** a repo's devcontainer declaration points its Dockerfile or build
  context outside the checkout via path traversal
- **THEN** provisioning fails closed into the same park as a repo with no
  buildable image
- **AND** no file outside the checkout is read into the build context
