# sandbox — delta for pin-declaration-drift

## MODIFIED Requirements

### Requirement: The image is proven cold before the dev loop relies on it

Before the dev loop begins, the system SHALL build the declared image and prove
the repo resolves its base dependencies and builds **cold** (a fresh cache home),
stamping a readiness/attestation fact. If the cold proof fails, the run SHALL park
toward the operator; the dev loop SHALL NOT proceed on an unproven environment.
The baseline proof is resolve+build (not tests-pass), because the task's own
change and test may not exist yet.

The attestation SHALL identify the DECLARATION that was proven, not only its
outcome, so a later gate can tell whether it is still proving the same contract.
That identity SHALL cover both the resolved run fields and the bytes of the
committed declaration sources, because an image declaration names a path rather
than its content — a rewritten `Dockerfile` leaves the resolved run fields
identical. The identity SHALL be derived by one shared computation, so the
component that attests and the component that later compares cannot drift apart.

#### Scenario: A repo whose image builds it cold becomes ready
- **WHEN** provisioning builds the declared image and the repo resolves + builds
  cold
- **THEN** a readiness attestation is stamped and the dev loop may proceed
- **AND** the attestation identifies the declaration that was proven

#### Scenario: An image that cannot build the repo cold parks
- **WHEN** the declared image cannot resolve the repo's base dependencies or build
  it in a fresh cache
- **THEN** the run parks toward the operator (fix the declared image)
- **AND** no readiness attestation is stamped

#### Scenario: The attested identity distinguishes an edited declaration
- **WHEN** two checkouts differ only in a committed declaration — a run field, or
  the body of the declared `Dockerfile`
- **THEN** their attested declaration identities differ
