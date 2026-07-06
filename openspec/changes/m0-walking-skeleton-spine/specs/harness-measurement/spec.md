## ADDED Requirements

### Requirement: Measurement facts are stamped by the executing harness

The harness that executed a task's `test_command` SHALL stamp the measurement
fact (`measurement.result`: exit code, pass/fail, test counts). No measurement
fact SHALL originate from a model claim.

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

### Requirement: Semantic review gates on harness facts, not model claims

The reviewer SHALL read `measurement.result` — not the model's assertion about
the command — and produce a verdict (`review.verdict`). Review findings SHALL be
additive constraints only; they SHALL NOT weaken an immutable `task.spec`.

#### Scenario: A false success claim cannot be approved
- **WHEN** the model claims tests pass but `measurement.result` records failure
- **THEN** the reviewer cannot record an approving `review.verdict` on that claim

#### Scenario: Findings never relax the spec
- **WHEN** a reviewer raises a finding
- **THEN** the finding adds a constraint and does not remove or weaken any `task.spec` requirement

### Requirement: Single writer for measurement facts

`measurement.result` SHALL have exactly one writer — the harness that executed
the command — recorded in the checked-in writers table.

#### Scenario: Writers census maps measurement.result to one writer
- **WHEN** the writers-census conformance test runs
- **THEN** `measurement.result` maps to exactly one writing harness
