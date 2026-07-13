## MODIFIED Requirements

### Requirement: Clean-room verification is the terminal gate

No run SHALL reach `open_pr` until clean-room verification has recorded a
passing result (`verify.result`). Verification SHALL consume an immutable
snapshot — a clone of the run's checkout at the attempt's recorded commit
(`attempt.commit`) — never a copy of the mutable warm working tree. Only work
carrying an approving review verdict SHALL be verified; rejected work
re-enters development instead. Verification SHALL run in a fresh isolated
environment with a distinct build-cache home per run, resolve and build
dependencies from the artifact's own declarations, and run the artifact's
own tests.

#### Scenario: PR blocked until verify passes
- **WHEN** `verify.result` is absent or failing for a run
- **THEN** the `open_pr` action does not fire

#### Scenario: Verification runs in fresh isolation
- **WHEN** clean-room verification runs for a run
- **THEN** it uses a distinct build-cache home isolated from any warm environment
- **AND** it resolves and builds from the artifact's own declarations

#### Scenario: Verification consumes the committed snapshot
- **WHEN** verification materializes its input
- **THEN** it clones the run's checkout at the recorded `attempt.commit`
- **AND** files present only in the mutable warm working tree do not enter the clean room

#### Scenario: Rejected work is not cold-verified
- **WHEN** a task's review verdict is `changes_requested`
- **THEN** clean-room verification does not run for that attempt
