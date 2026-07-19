## ADDED Requirements

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
