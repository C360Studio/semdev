## ADDED Requirements

### Requirement: OpenSpec artifacts are hydrated from graph facts

The system SHALL render every OpenSpec artifact it produces (proposal, spec
deltas, tasks) from graph facts, not from hand-authored prose. The graph is
authoritative and the artifact is a projection of it; the artifact SHALL NOT be
a separate source of truth.

#### Scenario: The generated change is a projection of facts
- **WHEN** semdev produces an OpenSpec change for a run
- **THEN** its `proposal.md`, `specs/*/spec.md`, and `tasks.md` are rendered from the run's `openspec.change.*` facts
- **AND** no product code hand-authors the artifact content independently of those facts

### Requirement: Produced artifacts pass the OpenSpec CLI validator

A generated OpenSpec change SHALL pass the real OpenSpec CLI validator
(`openspec validate --strict`) before it reaches the human-approval gate. The
CLI is the compatibility oracle; semdev SHALL NOT substitute a re-implementation
of the CLI's rules for this check, and the pass/fail SHALL be stamped by the
harness that ran the validator (`openspec.validated`), never by a model.

#### Scenario: An invalid change cannot reach approval
- **WHEN** a generated change fails `openspec validate --strict`
- **THEN** it does not advance to the human-approval gate and the failure surfaces for correction

#### Scenario: A valid change is blessed by the sponsor's own tool
- **WHEN** a generated change passes `openspec validate --strict`
- **THEN** the harness that ran the validator records `openspec.validated` for the change

### Requirement: Brownfield OpenSpec artifacts are ingested deterministically

The system SHALL read existing OpenSpec artifacts in a target repository's
`openspec/` workspace and seed the graph with their facts through a
deterministic parser; no model SHALL interpret the artifacts on the ingest path
(G3). Parsing SHALL be lenient — bullet, heading-case, and optional-file
variance produce warnings, never a hard failure — and the raw artifact bytes
SHALL be retained by reference so each ingested fact traces to its source file.

#### Scenario: Existing artifacts seed the graph
- **WHEN** init encounters a target repo containing `openspec/changes/` and `openspec/specs/`
- **THEN** a deterministic parser reads them and writes their facts to the graph under a single owner
- **AND** no LLM is invoked on the parse path

#### Scenario: Lenient parse tolerates format variance and keeps provenance
- **WHEN** an ingested artifact uses `*`/`+` bullets or heading-case variance
- **THEN** the parser records warnings and still produces facts
- **AND** each fact retains a reference to the source artifact's stored bytes

### Requirement: Round-trip fidelity is the compatibility test

Ingesting an OpenSpec change into graph facts and hydrating it back SHALL
reproduce the change's semantic content — its requirements, scenarios, and
tasks. semdev's own repository is the first round-trip fixture.

#### Scenario: semdev round-trips its own change
- **WHEN** semdev ingests its own `m0-walking-skeleton-spine` change and re-hydrates it from the resulting facts
- **THEN** the re-hydrated artifacts are semantically equivalent to the originals
