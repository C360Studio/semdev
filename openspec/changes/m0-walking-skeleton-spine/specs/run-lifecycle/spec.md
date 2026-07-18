## ADDED Requirements

### Requirement: Run created from an intaken issue

The system SHALL create a run entity when an issue is intaken, carrying a
reference to the source issue (`run.issue.ref`). Once created, the run's
progression through the arc SHALL be decided by rules matching facts — there is
no authoritative phase field that any component sets.

#### Scenario: Issue intake creates a run
- **WHEN** an issue is intaken through a configured code-host adapter
- **THEN** a run entity is created carrying `run.issue.ref` to the source issue
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
`ask_human`, `respond`, `archive_change`. No other action value is routable, and
no phase enum is introduced.

The taxonomy mirrors the proven OpenSpec lifecycle wrapped with semdev's human
gates and delivery: `create_change` is `openspec new`, `dev_from_task` is
`openspec apply`, `verify` is the clean-room outcome gate, and `archive_change`
is `openspec archive` — with `issue_intake`, `open_pr`, `ask_human`, and
`respond` as semdev's front-door, delivery, and HITL additions. Every OpenSpec
checkpoint maps to an action or a fact; semdev does not re-implement the workflow.

#### Scenario: An out-of-taxonomy action is not routable
- **WHEN** a fact requests an action outside the closed taxonomy
- **THEN** no rule routes it and the condition surfaces for human attention

### Requirement: Verify is outcome verification; coherence is structural

The `verify` action SHALL mean clean-room outcome verification (G4): the
artifact builds and passes its own tests in fresh isolation, recorded as
`verify.cleanroom.result`. semdev SHALL NOT introduce a separate coherence-verify action
for "does the implementation match the change." Coherence is enforced
structurally instead — by `openspec.change.validated` (artifacts well-formed), by task
status derived from execution markers, and by the semantic `review.verdict` —
because `task.spec` is the approved change projected immutably and cannot drift
from it.

#### Scenario: Verify records an outcome, not a coherence judgment
- **WHEN** the `verify` action runs for a run
- **THEN** it records `verify.cleanroom.result` from a clean-room build-and-test outcome
- **AND** no separate coherence-verify action or authoritative coherence fact is written

### Requirement: archive_change closes the loop back to OpenSpec

The `archive_change` action SHALL fold a merged change's spec deltas into the
target repository's living specs by shelling the real OpenSpec CLI
(`openspec archive`), stamping `openspec.change.archived` from the harness that ran it —
the "back to OpenSpec" step that keeps the specs truthful (G10). At M0 the action
and its fact are declared and the loop-closer rule is designed; the merge-event
trigger and the live archive call are wired at M1. The M0 mock journey terminates
at `open_pr` (`delivery.pr.ref`).

#### Scenario: A merged change is archived back into the specs
- **WHEN** a delivered change's PR is merged (M1 trigger)
- **THEN** the `archive_change` action shells `openspec archive` and the harness records `openspec.change.archived`
- **AND** no model stamps the archive outcome (G3)

### Requirement: Change-approval human gate

The dev loop SHALL NOT start for a run until a human-approval fact
(`run.change.approved`) is present for that run's generated change. This is the
first of the two human gates.

#### Scenario: Dev loop blocked without approval
- **WHEN** a change has been generated but `run.change.approved` is absent
- **THEN** the `dev_from_task` action is not eligible to fire

#### Scenario: Dev loop eligible after approval
- **WHEN** a human approves the generated change and `run.change.approved` is written
- **THEN** the `dev_from_task` action becomes eligible for that run's tasks

### Requirement: Park toward the human rather than reconcile in Go

A run SHALL park by recording `run.awaiting.human` whenever it cannot progress by
rule — a genuine engine gap, or a decision that requires a human — and SHALL
resume only on a human signal. No Go reconciler, backstop ticker, or app-side
state machine SHALL advance a parked run.

#### Scenario: Unresolvable-by-rule condition parks the run
- **WHEN** a run reaches a condition no rule can resolve
- **THEN** `run.awaiting.human` is recorded and the run does not progress
- **AND** no Go reconciler advances it while parked
