# Dev-From-Task Specification

## Purpose

Turn an approved change's tasks into immutable, Karpathy-shaped task facts and
execute them through a bounded, rules-routed developer loop: attempts are
dispatched under strict tool allowlists with full task context, measured
in-loop by the harness, gated by deterministic floors, and routed entirely by
rules over harness-stamped facts — escalating toward the human on budget
exhaustion rather than looping unbounded.

## Requirements

### Requirement: Tasks are projected as immutable facts

WHEN a run's change is approved, the system SHALL project the change's tasks into
immutable task facts (`task.spec`). The dev loop SHALL converge on these facts
and SHALL NOT be able to redefine them. Task status SHALL be derived from
execution markers, not written as a separate authoritative status field — no
second planning state machine is introduced.

#### Scenario: Approval projects immutable task facts
- **WHEN** `run.change.approved` is present for a run
- **THEN** the change's tasks are projected as `task.spec` facts
- **AND** an attempt to mutate a projected `task.spec` is rejected

#### Scenario: Task status is derived, not authored
- **WHEN** a task's progress is queried
- **THEN** its status is derived from execution markers (`task.attempt`) and gate facts
- **AND** no authoritative `task.status` predicate is written

### Requirement: Karpathy-shaped task schema enforced at projection

Each `task.spec` MUST carry `assumptions`, `non_goals`, at least one
`target_file`, and a required `test_command`. The `target_files` set MUST
include the test files the `test_command` measures — an attempt whose
write-set is confined to `target_files` must be able to author both the fix
and its tests. A per-task attempt budget SHALL be clamped to the range [1,5]
at stamp time; the clamp ceiling is the structural bound. A task missing its
budget or `test_command` SHALL fail toward the human rather than be stamped
with a default that hides the gap.

#### Scenario: Missing test_command fails toward the human
- **WHEN** a task is projected without a `test_command`
- **THEN** projection does not stamp the task and the run parks toward the human

#### Scenario: Budget is clamped at stamp time
- **WHEN** a task declares an attempt budget greater than 5
- **THEN** the stamped `task.spec` budget is clamped to 5
- **AND** escalation fires within the structural bound regardless

#### Scenario: target_files without the measuring tests fails toward the human
- **WHEN** a task's `target_files` omits every test file its `test_command` measures
- **THEN** projection does not stamp the task and the run parks toward the human

### Requirement: Bounded dev loop with escalation

Each attempt at a task SHALL be one bounded, multi-turn developer loop: the
developer reads the workspace, authors patches, and invokes measurement
inside the same loop, receiving the harness's real output as in-loop
feedback. The dispatching rule SHALL record the execution marker
(`task.attempt`) at spawn time — a crashed or wedged loop still consumed an
attempt. In-loop iteration is bounded by the loop engine's iteration cap; a
loop terminal without a passing harness measurement — including a terminal
with no measurement at all — is a failed attempt. On attempt-budget
exhaustion without a passing gate, the rail SHALL escalate toward the human
and stop; it SHALL NOT loop unbounded. At M0 the enforced attempt budget is a
single declared constant within the clamp range; the projected `task.spec`
budget SHALL become the enforced value when the rule engine supports dynamic
scalar comparison (upstream ask filed), and a tripwire SHALL flag when that
capability lands.

#### Scenario: The developer iterates on harness feedback inside one loop
- **WHEN** the developer applies a patch and invokes measurement within its loop
- **THEN** the measurement's real output returns as in-loop feedback and the developer can author a follow-up patch in the same loop
- **AND** no additional model loop is spawned per iteration

#### Scenario: The attempt is recorded at dispatch
- **WHEN** a developer loop is dispatched for a task
- **THEN** `task.attempt` is appended before the loop's first model turn
- **AND** a loop that wedges or crashes has still consumed the attempt

#### Scenario: A terminal without a passing measurement is a failed attempt
- **WHEN** a developer loop ends without a harness-stamped passing measurement for the task
- **THEN** the attempt routes as failed (retry within budget, else escalate)
- **AND** the model's own stopping decision confers no success

#### Scenario: Budget exhaustion escalates and halts
- **WHEN** a task reaches its attempt budget without passing its gates
- **THEN** the rail escalates toward the human and records no further `task.attempt`

### Requirement: Deterministic floors gate the loop and cannot be skipped

Each attempt SHALL be evaluated by deterministic floor checks (source-build
integrity, authored-test integrity / vacuous tests, stub artifacts,
anti-mock, tests-must-exist, and post-measure working-tree cleanliness) that
emit findings as facts (`floor.finding`). Floors SHALL evaluate the attempt's
committed snapshot and SHALL run deterministically with no model turn. The
rail SHALL route on those facts via rules and SHALL NOT advance to semantic
review while a rejecting floor finding stands.

#### Scenario: A vacuous-test attempt is rejected by a floor
- **WHEN** an attempt submits a fabricated or vacuous test
- **THEN** a deterministic floor emits a rejecting `floor.finding`
- **AND** the rail does not advance that attempt to semantic review

#### Scenario: Test-time mutation of the artifact is rejected
- **WHEN** the working tree differs from the attempt's commit after measurement runs
- **THEN** a deterministic floor emits a rejecting `floor.finding`

#### Scenario: Floors run without a model turn
- **WHEN** floors evaluate an attempt
- **THEN** no model-publishing spawn occurs on the floors path

### Requirement: Routing is rules over harness-stamped facts

Every route in the rail SHALL be decided by rule conditions reading
harness-stamped facts (measurement results, floor findings, review verdicts,
verify results, attempt counters) — advance to review, retry, escalate,
verify, deliver, park. Product Go SHALL NOT derive routing tokens for rules to
dispatch, and no fact SHALL exist whose only purpose is to relay control
between stations. Where the rule engine genuinely cannot express a route, the
system SHALL record the upstream semstreams ask and park toward the human —
never add a Go decision layer.

#### Scenario: An attempt's disposition is derivable from the rule pack alone
- **WHEN** a measured, floor-clean attempt's loop terminates
- **THEN** the advance-to-review rule fires from the harness-stamped facts directly
- **AND** no intermediate Go-derived decision fact exists between terminal and route

#### Scenario: The route-token census finds zero Go deciders
- **WHEN** the widened G2 conformance census runs over product Go
- **THEN** it finds zero functions deriving lifecycle or routing tokens consumed by rules

#### Scenario: An inexpressible route parks with a filed ask
- **WHEN** a needed route cannot be expressed as a rule condition
- **THEN** the run parks toward the human and the upstream engine ask is recorded

### Requirement: Model roles receive complete context under strict tool allowlists

Every model-publishing spawn SHALL declare an explicit tool allowlist scoped
to its role. Developer and reviewer prompts SHALL carry the full task
contract (goal, `target_files`, `test_command`, assumptions, non-goals) —
never entity IDs alone — and a re-entry attempt SHALL additionally carry the
prior review findings and measurement state. Roles SHALL hold read access to
the artifacts they judge: the developer reads the workspace; the reviewer
reads the cumulative diff and source.

#### Scenario: The developer starts with the contract in context
- **WHEN** a developer loop is dispatched
- **THEN** its prompt contains the task's goal, target files, and test command
- **AND** its allowlist permits reading workspace files before patching

#### Scenario: A re-entry attempt carries the rejection's findings
- **WHEN** a developer loop is dispatched after a reviewer rejection
- **THEN** its prompt contains the review findings that caused the rejection

#### Scenario: A spawn without an allowlist fails configuration lint
- **WHEN** a rule declares a model-publishing spawn without an explicit tool allowlist
- **THEN** configuration validation rejects the rule pack

### Requirement: The rail binds one task at M0 through a single declared seam

The M0 rail SHALL execute exactly one projected task, bound through a single
declared binding point marked as the multi-task seam. Copying rule chains per
task index is forbidden; the documented M1 path is readiness-based dispatch
over task dependency edges (the framework's gated-DAG component).

#### Scenario: No per-index rule duplication
- **WHEN** the rule pack is inspected for task-index references
- **THEN** exactly one declared binding point carries the task index
- **AND** no rule chain is duplicated per index
