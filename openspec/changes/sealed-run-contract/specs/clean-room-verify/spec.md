# clean-room-verify — delta for sealed-run-contract

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

Verification SHALL prove the committed artifact under the contract CARRIED with
the run, never under a contract re-derived from that artifact. The declaration is
authored in the target repo and is therefore writable by the work under test, so
re-deriving it at this gate would let the work choose the terms of its own
measurement. A verification that finds no carried contract SHALL record no verdict
and park.

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

#### Scenario: A weakened committed declaration does not take effect
- **WHEN** the committed artifact declares a test command different from the one
  carried with the run — for example one that runs nothing and exits zero
- **THEN** verification measures under the CARRIED command, not the committed one
- **AND** an artifact that fails its own tests does not reach a passing verdict

#### Scenario: Verification with no carried contract parks
- **WHEN** verification finds no carried contract for the run
- **THEN** it records no verdict and parks
