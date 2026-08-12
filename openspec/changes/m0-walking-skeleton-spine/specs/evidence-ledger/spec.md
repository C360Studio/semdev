## ADDED Requirements

### Requirement: Every run is recorded with an honest status

The evidence ledger SHALL record each run with a status drawn from a fixed
vocabulary (e.g. `pass`, `exploratory`, `blocked`). A run whose artifact fails
independent verification SHALL NOT be recorded as `pass`.

#### Scenario: A failed verification is never recorded as pass
- **WHEN** `verify.cleanroom.result` records a failure for a run
- **THEN** the run's ledger status is not `pass`

#### Scenario: Ledger schema rejects an out-of-vocabulary status
- **WHEN** a ledger entry is validated against the ledger schema
- **THEN** an entry whose status is outside the fixed vocabulary is rejected

### Requirement: Mock and fixture-seeded runs are labeled bridge proof

A run performed on a mock LLM, or a journey seeded from fixtures, SHALL be labeled
as bridge proof in the ledger and SHALL NOT be presented as real-LLM product
evidence.

#### Scenario: A mock-LLM run is labeled as bridge proof
- **WHEN** a run is executed against the mock LLM
- **THEN** its ledger entry is labeled mock / bridge proof and is not counted as real-LLM evidence

### Requirement: Journeys must not backdoor-write to make behavior pass

An end-to-end journey SHALL NOT write to NATS, the graph, or state stores to make
a claimed behavior pass. A journey that does so SHALL be treated as invalid
evidence.

#### Scenario: A journey that seeds a completion fact is invalid evidence
- **WHEN** a journey writes a completion or gate fact directly rather than letting the arc produce it
- **THEN** the journey-honesty rule flags the run as invalid evidence

### Requirement: Single writer for ledger entries

Evidence-ledger entries SHALL have exactly one writer, recorded in the writers
table.

#### Scenario: Writers census maps ledger entries to one writer
- **WHEN** the writers-census conformance test runs
- **THEN** the ledger-entry predicate maps to exactly one writer
