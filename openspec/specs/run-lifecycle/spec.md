# Run-Lifecycle Specification

## Purpose

Defines how a run progresses from an intaken issue to a delivered change: rules
own every lifecycle transition over a closed action taxonomy mirroring the
OpenSpec workflow, human gates and parking govern anything a rule cannot
resolve, and deterministic stations execute with zero model turns.

## Requirements

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
write a lifecycle-transition fact, and SHALL NOT derive routing tokens
(advance/retry/escalate/coherent/blocked or equivalents) for rules to
dispatch — a Go-derived route is a lifecycle decision in disguise. Where a
transition genuinely cannot be expressed by a rule, the system SHALL record an
upstream semstreams ask and park toward the human (see "Park toward the
human"), never advance the run from Go.

#### Scenario: A station's completion fact drives the next action via a rule
- **WHEN** a station records its completion fact (e.g. a change is generated)
- **THEN** a rule matching that fact fires the next action in the taxonomy
- **AND** no product-Go path writes the transition

#### Scenario: Conformance census finds zero Go transition writers
- **WHEN** the lifecycle-caller conformance census runs over product Go
- **THEN** it finds zero lifecycle-transition writers outside an explicit,
  ADR-linked exception table (target size: 0)

#### Scenario: Conformance census finds zero Go route-token writers
- **WHEN** the widened conformance census runs over product Go
- **THEN** it finds zero functions whose output is a routing token consumed by a rule's dispatch condition

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
for "does the implementation match the change" — neither as an action nor as a
Go-computed roll-up fact. Coherence is enforced structurally instead — by
`openspec.change.validated` (artifacts well-formed), by task status derived from
execution markers, and by the semantic `review.verdict` — because `task.spec`
is the approved change projected immutably and cannot drift from it. The
delivery route SHALL be a rule whose conditions read `verify.cleanroom.result`, the
per-task review verdicts, and `openspec.change.validated` directly.

#### Scenario: Verify records an outcome, not a coherence judgment
- **WHEN** the `verify` action runs for a run
- **THEN** it records `verify.cleanroom.result` from a clean-room build-and-test outcome
- **AND** no separate coherence-verify action or authoritative coherence fact is written

#### Scenario: Delivery is routed by a rule reading the evidence directly
- **WHEN** a run carries a passing `verify.cleanroom.result`, approving review verdicts, and `openspec.change.validated`
- **THEN** the delivery rule fires from those facts directly
- **AND** no intermediate coherence decision fact exists

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

A deterministic station whose handler exhausts its bounded retries SHALL cause
the run to park: the station harness records `station.dispatch.failed` on the
dispatched entity (the harness that ran the retries is the fact's single
writer, `station-harness`; the object names the station and carries a bounded,
sanitized error; the stamp is upsert-idempotent so repeated failures never
append), and a park rule records `run.awaiting.human` from that fact, naming
the failed station. The station's SUCCESS path stamps nothing new — the
harness never writes a success fact, so a fault can never read as completion.
Every park SHALL fire at most once per failure via a one-shot marker. The
RUN-fired park rule (the fact landed on the run itself) SHALL NOT fire on an
already-parked or already-delivered run — its guards read the firing entity
directly. The LOOP-fired park rule cannot carry those guards (rule conditions
read the firing entity only, and a guard on a sibling entity's stamp is racy
by the engine's per-action revision semantics); it MAY therefore append a
benign duplicate `run.awaiting.human` triple to an already-parked run — every
consumer reads park state by presence, so a duplicate changes nothing.

#### Scenario: Unresolvable-by-rule condition parks the run
- **WHEN** a run reaches a condition no rule can resolve
- **THEN** `run.awaiting.human` is recorded and the run does not progress
- **AND** no Go reconciler advances it while parked

#### Scenario: A terminal station failure parks the run
- **WHEN** a station handler fails after its bounded retries
- **THEN** the station harness records `station.dispatch.failed` on the dispatched entity
- **AND** a rule records `run.awaiting.human` on the run naming the failed station
- **AND** no `verify.cleanroom.result` and no `delivery.pr.ref` ever appear on that run (no false green)

#### Scenario: A successful station stamps no harness outcome fact
- **WHEN** a station handler succeeds
- **THEN** the harness records no `station.dispatch.failed` and no success fact
- **AND** the station's own completion facts alone drive the arc forward

#### Scenario: The park fires once and respects terminal states
- **WHEN** `station.dispatch.failed` lands on a RUN that is already parked or already delivered
- **THEN** the run-fired park rule does not fire

#### Scenario: A loop-fired park on an already-parked run is benign
- **WHEN** `station.dispatch.failed` lands on a LOOP whose bound run is already parked
- **THEN** the loop-fired park rule fires at most once for that failure (its one-shot marker)
- **AND** any duplicate `run.awaiting.human` triple it appends is benign — park state is read by presence

### Requirement: Deterministic stations run as publish-triggered components with zero model turns

Deterministic stations SHALL execute with zero model turns: task projection,
change validation, sandbox provisioning, floors, clean-room verification, and
delivery are triggered by rules or component subscriptions over facts, with
no model-publishing spawn on their paths. Model loops SHALL exist only for
authoring/planning, task development, and adversarial review. A deterministic
station's failure SHALL park toward the human (fail-closed), never silently
pass.

#### Scenario: Only the three reasoning roles spawn model loops
- **WHEN** the rule pack's model-publishing spawns are counted
- **THEN** every spawn targets the author, developer, or reviewer role
- **AND** no spawn exists solely to relay a deterministic tool invocation

#### Scenario: A deterministic station failure parks
- **WHEN** a deterministic station (projection, validation, provisioning, floors, verify, delivery) fails
- **THEN** the run parks toward the human with the failure recorded as a fact
