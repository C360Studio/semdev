# Evidence-Ledger Specification

## Purpose

Defines the honesty rules for recorded run evidence: every run carries a status
from a fixed vocabulary that cannot claim `pass` on failed verification,
mock/fixture runs are labeled bridge proof rather than real-LLM evidence,
backdoor-writing journeys are invalid evidence, and each ledger entry has
exactly one writer.

## Requirements

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

### Requirement: Run entries record the experiment condition

A run minted under a declared experiment condition SHALL have its ledger
entry labeled with that condition (`baseline` | `semsource`), read from the
run's `experiment.run.condition` fact. A `semsource`-condition run whose
trajectory records semsource proxy faults material to the attempt — or
(defense-in-depth: the mint gate makes this unmintable) one lacking a
successful mint-time readiness proof — SHALL NOT be presented as condition
evidence; the entry records the run with its degradation stated.
Cross-condition comparison is a human reading of condition-labeled entries;
the system SHALL NOT compute or record an aggregate verdict on the
conditions.

#### Scenario: A condition-labeled run lands in the ledger
- **WHEN** a run minted under a declared condition is recorded
- **THEN** its ledger entry carries the run's `experiment.condition` value

#### Scenario: A degraded semsource run is not condition evidence
- **WHEN** a `semsource`-condition run's trajectory records semsource proxy faults material to the attempt
- **THEN** its ledger entry states the degradation and is not presented as condition evidence

#### Scenario: No aggregate verdict is recorded
- **WHEN** ledger entries exist for both conditions
- **THEN** no system-written fact or ledger row declares a winning condition
