## ADDED Requirements

### Requirement: Tasks are projected as immutable facts

WHEN a run's change is approved, the system SHALL project the change's tasks into
immutable task facts (`task.spec`). The dev loop SHALL converge on these facts
and SHALL NOT be able to redefine them. Task status SHALL be derived from
execution markers, not written as a separate authoritative status field — no
second planning state machine is introduced.

#### Scenario: Approval projects immutable task facts
- **WHEN** `run.change_approved` is present for a run
- **THEN** the change's tasks are projected as `task.spec` facts
- **AND** an attempt to mutate a projected `task.spec` is rejected

#### Scenario: Task status is derived, not authored
- **WHEN** a task's progress is queried
- **THEN** its status is derived from execution markers (`task.attempt`) and gate facts
- **AND** no authoritative `task.status` predicate is written

### Requirement: Karpathy-shaped task schema enforced at projection

Each `task.spec` MUST carry `assumptions`, `non_goals`, at least one
`target_file`, and a required `test_command`. A per-task iteration budget SHALL
be clamped to the range [1,5] at stamp time; the clamp ceiling is the structural
bound. A task missing its budget or `test_command` SHALL fail toward the human
rather than be stamped with a default that hides the gap.

#### Scenario: Missing test_command fails toward the human
- **WHEN** a task is projected without a `test_command`
- **THEN** projection does not stamp the task and the run parks toward the human

#### Scenario: Budget is clamped at stamp time
- **WHEN** a task declares an iteration budget greater than 5
- **THEN** the stamped `task.spec` budget is clamped to 5
- **AND** escalation fires by iteration 6 regardless

### Requirement: Bounded dev loop with escalation

The dev loop SHALL attempt a task within its clamped iteration budget, recording
an execution marker (`task.attempt`) per iteration. On budget exhaustion without
a passing gate, the loop SHALL escalate toward the human and stop; it SHALL NOT
loop unbounded.

#### Scenario: Budget exhaustion escalates and halts
- **WHEN** a task reaches its clamped iteration budget without passing its gates
- **THEN** the loop escalates toward the human and records no further `task.attempt`

### Requirement: Deterministic floors gate the loop and cannot be skipped

Each attempt SHALL be evaluated by deterministic floor checks (source-build
integrity, authored-test integrity / vacuous tests, stub artifacts,
anti-mock, tests-must-exist) that emit findings as facts (`floor.finding`). The
loop SHALL route on those facts and SHALL NOT advance to semantic review while a
rejecting floor finding stands.

#### Scenario: A vacuous-test attempt is rejected by a floor
- **WHEN** an attempt submits a fabricated or vacuous test
- **THEN** a deterministic floor emits a rejecting `floor.finding`
- **AND** the loop does not advance that attempt to semantic review
