## ADDED Requirements

### Requirement: Run created from an intaken issue

The system SHALL create a run entity when an issue is intaken, carrying a
reference to the source issue (`run.issue_ref`). Once created, the run's
progression through the arc SHALL be decided by rules matching facts — there is
no authoritative phase field that any component sets.

#### Scenario: Issue intake creates a run
- **WHEN** an issue is intaken through a configured code-host adapter
- **THEN** a run entity is created carrying `run.issue_ref` to the source issue
- **AND** no `run.phase` (or equivalent phase-enum) fact is written

### Requirement: Rules own every lifecycle transition

Every transition of a run from one station of the arc to the next SHALL be
decided by a rule matching the facts for that transition. Product Go SHALL NOT
write a lifecycle-transition fact. Where a transition genuinely cannot be
expressed by a rule, the system SHALL record an upstream semstreams ask and park
toward the human (see "Park toward the human"), never advance the run from Go.

#### Scenario: A station's completion fact drives the next action via a rule
- **WHEN** a station records its completion fact (e.g. a change is generated)
- **THEN** a rule matching that fact fires the next action in the taxonomy
- **AND** no product-Go path writes the transition

#### Scenario: Conformance census finds zero Go transition writers
- **WHEN** the lifecycle-caller conformance census runs over product Go
- **THEN** it finds zero lifecycle-transition writers outside an explicit,
  ADR-linked exception table (target size: 0)

### Requirement: Closed action taxonomy

The rule layer SHALL consume only a closed set of actions:
`issue_intake`, `create_change`, `dev_from_task`, `verify`, `open_pr`,
`ask_human`, `respond`. No other action value is routable, and no phase enum is
introduced.

#### Scenario: An out-of-taxonomy action is not routable
- **WHEN** a fact requests an action outside the closed taxonomy
- **THEN** no rule routes it and the condition surfaces for human attention

### Requirement: Change-approval human gate

The dev loop SHALL NOT start for a run until a human-approval fact
(`run.change_approved`) is present for that run's generated change. This is the
first of the two human gates.

#### Scenario: Dev loop blocked without approval
- **WHEN** a change has been generated but `run.change_approved` is absent
- **THEN** the `dev_from_task` action is not eligible to fire

#### Scenario: Dev loop eligible after approval
- **WHEN** a human approves the generated change and `run.change_approved` is written
- **THEN** the `dev_from_task` action becomes eligible for that run's tasks

### Requirement: Park toward the human rather than reconcile in Go

A run SHALL park by recording `run.awaiting_human` whenever it cannot progress by
rule — a genuine engine gap, or a decision that requires a human — and SHALL
resume only on a human signal. No Go reconciler, backstop ticker, or app-side
state machine SHALL advance a parked run.

#### Scenario: Unresolvable-by-rule condition parks the run
- **WHEN** a run reaches a condition no rule can resolve
- **THEN** `run.awaiting_human` is recorded and the run does not progress
- **AND** no Go reconciler advances it while parked
