# sandbox — delta for sealed-run-contract

## MODIFIED Requirements

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
build context. The few semdev-specific run fields (resolve/build/test commands,
tier split, secret refs, and the cache-home env names) ride a
`customizations.semdev` block or convention, not an environment DSL. Every such
field SHALL be declarable by the operator, so that a profile shipping no
convention is still fully declarable; semdev SHALL NOT reject a manifest for a
field the operator has no surface to set.

The ecosystem profile SHALL be DECLARED by a human in a committed
`.semdev/profile.yaml`, not inferred. Marker-file detection SHALL be a CHECK
against that declaration rather than the source of truth: a repo that declares no
profile SHALL park toward the human, and a declaration that disagrees with what
the repo's markers show SHALL park in either direction — including detection
finding no markers at all. semdev SHALL NOT fall back to inferring a profile when
the declaration is absent; a silent guess in place of a human's answer is exactly
the failure this requirement exists to prevent.

The declaration surface — the profile declaration, the image declaration, and the
run fields — SHALL be SEALED for the duration of a run: it SHALL NOT change as a
side effect of the work under test. Sealing SHALL be enforced both where the work
writes and on the committed result, because a repo-declared command executed
during measurement can write files that no tool boundary observes. A run whose
sealed surface changed SHALL park toward the human and SHALL NOT be measured. A
change to that surface is a separate, human-reviewed change.

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

#### Scenario: A profile with no convention is declarable end to end
- **WHEN** a repo whose ecosystem ships no built-in convention declares its run
  commands and its cache-home env names in `customizations.semdev`
- **THEN** manifest resolution succeeds and the repo is cold-provable
- **AND** no field required by the cold proof is left without a declaration
  surface

#### Scenario: A repo that declares no profile parks
- **WHEN** a run targets a repo with no committed profile declaration
- **THEN** the run parks toward the human
- **AND** no profile is inferred from marker files

#### Scenario: A declaration disagreeing with the repo parks
- **WHEN** the declared profile and the repo's marker files indicate different
  ecosystems, or the repo carries no markers at all
- **THEN** the run parks toward the human naming the disagreement
- **AND** neither side silently wins

#### Scenario: The work under test cannot rewrite its own contract
- **WHEN** the work attempts to modify the profile declaration, the image
  declaration, or the run fields
- **THEN** the write is refused where it is attempted
- **AND** a change that reaches the committed result by any other path is caught
  before measurement and parks the run

### Requirement: The image is proven cold before the dev loop relies on it

Before the dev loop begins, the system SHALL build the declared image and prove
the repo resolves its base dependencies and builds **cold** (a fresh cache home),
stamping a readiness/attestation fact. If the cold proof fails, the run SHALL park
toward the operator; the dev loop SHALL NOT proceed on an unproven environment.
The baseline proof is resolve+build (not tests-pass), because the task's own
change and test may not exist yet.

The contract the run is measured against SHALL be resolved ONCE, before the dev
loop begins, and carried with the run. Every later gate SHALL use the carried
contract; no gate SHALL re-derive it from the artifact under test. A gate that
finds no carried contract SHALL fail closed and park — it SHALL NOT resolve one
for itself.

#### Scenario: A repo whose image builds it cold becomes ready
- **WHEN** provisioning builds the declared image and the repo resolves + builds
  cold
- **THEN** a readiness attestation is stamped and the dev loop may proceed

#### Scenario: An image that cannot build the repo cold parks
- **WHEN** the declared image cannot resolve the repo's base dependencies or build
  it in a fresh cache
- **THEN** the run parks toward the operator (fix the declared image)
- **AND** no readiness attestation is stamped

#### Scenario: The contract is resolved once and carried
- **WHEN** provisioning resolves the run's contract
- **THEN** the resolved contract is carried with the run
- **AND** later gates measure against it rather than re-deriving it

#### Scenario: A gate with no carried contract fails closed
- **WHEN** a gate finds no carried contract for the run
- **THEN** it parks and resolves nothing for itself
