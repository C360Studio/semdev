# sandbox — delta for declare-cache-home-envs

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

### Requirement: A fresh cache home per proof, never shared across runs

Every cold proof (baseline and final verify) SHALL use a fresh (cold) cache home
so that a dependency masked by an accumulated cache cannot pass. Cache homes SHALL
NOT be shared across runs. Dev-loop iterations within a run MAY reuse the run's
warm cache for speed; the warm phase is never a verification gate.

The cache homes freshened per proof SHALL be named by the profile convention or
by the operator's declaration, with the declaration winning. A resolved manifest
that names no cache home SHALL fail closed at manifest resolution — the boundary
where the operator can act — because a proof with nothing to freshen cannot
establish the property this requirement exists for. An error reporting an
incomplete manifest SHALL name the specific missing fields.

#### Scenario: A fabricated dependency fails cold
- **WHEN** an artifact declares a dependency that only resolves from an
  accumulated warm cache
- **THEN** the cold proof (fresh cache) fails to resolve it and does not pass

#### Scenario: Runs do not share cache state
- **WHEN** two runs execute
- **THEN** each cold proof uses a distinct fresh cache home, so one run's
  resolution cannot mask another's

#### Scenario: A manifest naming no cache home fails closed at resolution
- **WHEN** neither the profile convention nor the operator's declaration names a
  cache home
- **THEN** manifest resolution fails closed toward the operator naming the
  missing field
- **AND** no cold proof is attempted on a manifest that could not be cold
