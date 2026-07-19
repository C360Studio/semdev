# Run Lifecycle — delta for station-failure-parks

## MODIFIED Requirements

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
The park rule SHALL NOT fire on an already-parked or already-delivered run,
and SHALL fire at most once per failure via a one-shot marker.

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
- **WHEN** `station.dispatch.failed` lands on an entity of a run that is already parked or already delivered
- **THEN** the park rule does not fire again
