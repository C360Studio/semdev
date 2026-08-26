# clean-room-verify — delta for pin-declaration-drift

## MODIFIED Requirements

### Requirement: Clean-room verification is the terminal gate

No run SHALL reach `open_pr` until clean-room verification has recorded a
passing result (`verify.cleanroom.result`). Verification SHALL consume an immutable
snapshot — a clone of the run's checkout at the attempt's recorded commit
(`attempt.commit.sha`) — never a copy of the mutable warm working tree. Only work
carrying an approving review verdict SHALL be verified; rejected work
re-enters development instead. Verification SHALL run in a fresh isolated
environment with a distinct build-cache home per run, resolve and build
dependencies from the artifact's own declarations, and run the artifact's
own tests.

Verification SHALL run under the declaration the provisioning baseline proved.
The declaration is authored in the target repo and is therefore writable by the
work under test, so the system SHALL compare the declaration resolved from the
committed snapshot against the attested one. On any divergence it SHALL record
declaration drift and SHALL record NO verdict — neither passing nor failing —
because an artifact measured under a contract that moved has not been measured.
A run SHALL NOT be advanced, failed, or re-contracted on drift; it parks toward
the human. A verification that finds NO attestation to compare against SHALL
fail closed the same way, never proceed.

#### Scenario: PR blocked until verify passes
- **WHEN** `verify.cleanroom.result` is absent or failing for a run
- **THEN** the `open_pr` action does not fire

#### Scenario: Verification runs in fresh isolation
- **WHEN** clean-room verification runs for a run
- **THEN** it uses a distinct build-cache home isolated from any warm environment
- **AND** it resolves and builds from the artifact's own declarations

#### Scenario: Verification consumes the committed snapshot
- **WHEN** verification materializes its input
- **THEN** it clones the run's checkout at the recorded `attempt.commit.sha`
- **AND** files present only in the mutable warm working tree do not enter the clean room

#### Scenario: Rejected work is not cold-verified
- **WHEN** a task's review verdict is `changes_requested`
- **THEN** clean-room verification does not run for that attempt

#### Scenario: A weakened test command cannot render a passing verdict
- **WHEN** the committed declaration names a test command different from the one
  the baseline proved — for example one that runs nothing and exits zero
- **THEN** verification records declaration drift and records no verdict
- **AND** the run does not reach `open_pr`

#### Scenario: A rewritten image declaration is drift even when the run contract is identical
- **WHEN** the committed `Dockerfile` body changes but the resolved run commands,
  tiers, cache homes, and secret refs are unchanged
- **THEN** verification records declaration drift
- **AND** no verdict is recorded

#### Scenario: An unchanged declaration verifies exactly as before
- **WHEN** the declaration resolved from the committed snapshot matches the attested one
- **THEN** no drift is recorded and verification renders its verdict as usual

#### Scenario: A missing attestation fails closed
- **WHEN** verification finds no attested declaration to compare against
- **THEN** it records no verdict and does not proceed
