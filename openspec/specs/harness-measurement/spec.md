# Harness-Measurement Specification

## Purpose

Guarantees that measurement outcomes come only from the harness that executed
the command — never from a model claim: measurement facts have a single
harness writer, tool schemas accept no outcome parameters, and `measure_task`
is the developer's in-loop feedback channel. Per-task adversarial semantic
review is gated on these harness-stamped results, with rejection routing work
back into development within the attempt budget.

## Requirements

### Requirement: Measurement facts are stamped by the executing harness

The harness that executed a task's `test_command` SHALL stamp the measurement
fact (`measurement.result`: at minimum the exit code and derived pass/fail;
test-count evidence is a per-profile addition that arrives with the harness's
output parse, not an M0 guarantee). No measurement fact SHALL originate from a
model claim.

#### Scenario: A failing command records failure regardless of model text
- **WHEN** the executed `test_command` exits non-zero
- **THEN** `measurement.result` records a failing outcome
- **AND** any model text claiming success does not alter the recorded fact

### Requirement: Measurement tool schemas accept no outcome parameters

No registered measurement tool's schema SHALL accept an outcome-shaped field
(`pass`, `passed`, `exit_code`, `success`, `resolved`, `outcome`, …) from the
caller. The schema conformance census SHALL fail the build if one does.

#### Scenario: A schema with an outcome field fails the census
- **WHEN** the schema conformance census runs over all registered tools
- **THEN** any tool whose schema accepts a caller-supplied outcome field fails the build

### Requirement: Adversarial semantic review runs per task, gated on harness facts

The reviewer SHALL review each unit of work — each projected task — on its own,
reading that task's `measurement.result` (not the model's assertion about the
command) and the attempt's cumulative committed diff via read tools, and
recording a per-task verdict (`review.verdict.<task_index>`) with its findings
(`review.findings.<task_index>`). The review SHALL be adversarial: the
reviewer attempts to refute the attempt, and its findings are additive
constraints only — they SHALL NOT weaken an immutable `task.spec`. A task's
verdict SHALL be approving ONLY when that task's `measurement.result` records
a pass AND the reviewer raised no finding against it; the per-task measurement
is the floor under the verdict. A `changes_requested` verdict SHALL route the
task back into a fresh bounded developer attempt within the attempt budget —
never onward to verification; only approved work advances. Delivery
(`open_pr`) SHALL require every projected task to carry an approving
`review.verdict.<i>`.

#### Scenario: A false success claim cannot be approved
- **WHEN** the model claims a task's tests pass but that task's `measurement.result` records failure
- **THEN** the reviewer cannot record an approving `review.verdict` for that task

#### Scenario: Each task is reviewed on its own evidence
- **WHEN** the reviewer reviews task N
- **THEN** `review.verdict.N` is derived from task N's measurement and the findings raised against task N
- **AND** a passing task's verdict is unaffected by a different task's failure

#### Scenario: Findings never relax the spec
- **WHEN** a reviewer raises a finding
- **THEN** the finding adds a constraint and does not remove or weaken any `task.spec` requirement

#### Scenario: Rejection re-enters development within budget
- **WHEN** the reviewer records `changes_requested` and the attempt budget is not exhausted
- **THEN** a fresh bounded developer attempt is dispatched carrying the findings
- **AND** clean-room verification does not run for the rejected attempt

#### Scenario: Rejection beyond budget parks toward the human
- **WHEN** the reviewer records `changes_requested` and the attempt budget is exhausted
- **THEN** the run parks toward the human with the findings

#### Scenario: The reviewer inspects the diff before the verdict
- **WHEN** a review loop runs
- **THEN** the reviewer can read the attempt's cumulative committed diff and source before `submit_review`

### Requirement: Single writer for measurement facts

`measurement.result` SHALL have exactly one writer — the harness that executed
the command — recorded in the checked-in writers table.

#### Scenario: Writers census maps measurement.result to one writer
- **WHEN** the writers-census conformance test runs
- **THEN** `measurement.result` maps to exactly one writing harness

### Requirement: Measurement is the developer's in-loop feedback channel

`measure_task` SHALL be callable by the developer inside its own loop. The
harness executes the task's immutable `test_command` in the run's sandbox,
stamps `measurement.result` (latest-wins per task), and returns the command's
real output and harness-derived pass/fail as the tool result — the
developer's feedback for the next iteration. The call SHALL NOT terminate
the loop. The schema SHALL continue to accept no outcome or command
parameters (G3): the model chooses *when* to measure, never *what the
outcome is*.

#### Scenario: In-loop measurement feeds the next patch
- **WHEN** the developer invokes `measure_task` after applying a patch
- **THEN** the real command output and harness-derived pass/fail return into the same loop
- **AND** the loop continues for a follow-up patch

#### Scenario: Skipping measurement cannot advance the task
- **WHEN** a developer loop terminates without any harness-stamped measurement
- **THEN** the attempt routes as failed
- **AND** no route treats the model's own claim as a measurement
