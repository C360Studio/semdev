## MODIFIED Requirements

### Requirement: Projected task facts are immutable

Approval SHALL project the approved change's tasks onto the run as immutable task
facts (`task.spec`). The dev loop SHALL converge on these facts and SHALL NOT be
able to redefine them. Task status SHALL be derived from execution markers, not
written as a separate authoritative status field — no second planning state machine
is introduced.

The approval trigger SHALL be the single-valued gate decision
(`run.change.decision` == `approve`), not a standalone boolean approval fact.

#### Scenario: Approval projects immutable task facts
- **WHEN** `run.change.decision` == `approve` is present for a run
- **THEN** the change's tasks are projected as `task.spec` facts
- **AND** an attempt to mutate a projected `task.spec` is rejected

#### Scenario: Task status is derived, not authored
- **WHEN** a task's progress is queried
- **THEN** its status is derived from execution markers (`task.attempt`) and gate facts
- **AND** no authoritative `task.status` predicate is written
