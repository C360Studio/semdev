## MODIFIED Requirements

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

## ADDED Requirements

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
