# OpenSpec IO Specification

## Purpose

Governs how semdev produces and consumes OpenSpec artifacts: generated changes
are projections of graph facts validated by the real OpenSpec CLI, a target
repository's existing living specs are ingested deterministically by a
registered runtime component, and merged changes fold back into living specs
via `openspec archive`.

## Requirements

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

A generated OpenSpec change SHALL pass the real OpenSpec CLI validator before it
reaches the human-approval gate. The harness SHALL invoke the validator
deterministically, targeting the change explicitly and running non-interactively
— `openspec validate <change> --strict --json --no-interactive` (or
`--changes` for the whole set). The bare `openspec validate --strict` form is
interactive-only and fails non-interactively ("Nothing to validate"), so it MUST
NOT be the harness invocation. The CLI is the compatibility oracle; semdev SHALL
NOT substitute a re-implementation of its rules, and the pass/fail SHALL be
stamped by the harness that ran the validator (`openspec.change.validated`), never by a
model. The `openspec.change.validated` marker SHALL bind to the exact change CONTENT the
validator blessed — its value is the change's content revision — so a later
re-author of the same change (which changes the content) is detectably stale until
the validator runs again against the new content. A consumer that acts
irreversibly on a validated change (freezing the immutable task specification) MUST
require the marker to match the change's current content revision, never mere
marker presence.

#### Scenario: An invalid change cannot reach approval
- **WHEN** a generated change fails `openspec validate <change> --strict --json --no-interactive`
- **THEN** it does not advance to the human-approval gate and the failure surfaces for correction

#### Scenario: A valid change is blessed by the sponsor's own tool
- **WHEN** a generated change passes `openspec validate <change> --strict --json --no-interactive`
- **THEN** the harness that ran the validator records `openspec.change.validated` for the change bound to the change's current content

#### Scenario: A re-authored change must be re-validated before its tasks are frozen
- **WHEN** a change that previously passed the validator is re-authored with changed content
- **THEN** the prior `openspec.change.validated` marker no longer matches the change's current content revision, so the change's tasks are not projected into the immutable task specification
- **AND** projection succeeds only after the validator runs again against the new content

### Requirement: Archiving folds the merged change back into the specs via the CLI

The "back to OpenSpec" step SHALL fold a merged change's spec deltas into the
target repository's living specs by shelling the real OpenSpec CLI
(`openspec archive`) — the second deterministic CLI oracle alongside `validate`.
The pass/fail SHALL be stamped by the harness that ran the archiver
(`openspec.change.archived`), never by a model, and semdev SHALL NOT re-implement the
CLI's archive rules. At M0 the fact and the shell path are declared; the
merge-event trigger and the live archive call are wired at M1.

#### Scenario: A merged change is archived by the sponsor's own tool
- **WHEN** a delivered change's PR is merged (M1 trigger)
- **THEN** `openspec archive` is shelled and the harness records `openspec.change.archived`
- **AND** semdev does not substitute a re-implementation of the CLI's archive rules

### Requirement: Brownfield OpenSpec artifacts are ingested deterministically

The system SHALL read a target repository's existing **living specifications**
(`openspec/specs/*/spec.md`) and seed the graph by projecting each capability's
spec to ONE canonical `openspec.spec.document` blob fact (the living-spec twin
of `openspec.change.document`) through a deterministic parser running as a
**registered ingest component** on the raw lane (raw-lane → projector →
graph-ingest) — a
library-only projector does not satisfy this requirement; no model SHALL
interpret the artifacts on the ingest path (G3). Parsing SHALL be lenient —
bullet, heading-case, and optional-file variance produce warnings, never a
hard failure — and the raw artifact bytes SHALL be retained by reference so
each ingested fact traces to its source file. A target repo's in-flight
`openspec/changes/` are the human's work-in-progress and SHALL NOT be
ingested: `openspec.change.*` has a single writer (the `create_change` author
tool, G5), so a second projector of that namespace is forbidden — semdev owns
only the changes it authors on a run.

#### Scenario: Existing living specs seed the graph
- **WHEN** init encounters a target repo containing `openspec/specs/`
- **THEN** a deterministic parser reads each capability's `spec.md` and writes its `openspec.spec.document` blob fact to the graph under a single owner
- **AND** the repo's in-flight `openspec/changes/` are left un-ingested
- **AND** no LLM is invoked on the parse path

#### Scenario: Lenient parse tolerates format variance and keeps provenance
- **WHEN** an ingested artifact uses `*`/`+` bullets or heading-case variance
- **THEN** the parser records warnings and still produces facts
- **AND** each fact retains a reference to the source artifact's stored bytes

#### Scenario: Ingestion runs from the registered runtime component
- **WHEN** the runtime boots against a target repo with living specs
- **THEN** the registered ingest component projects them without manual library invocation
- **AND** the facts land on their spec entities via the standard ingest lane

### Requirement: Round-trip fidelity is the compatibility test

Ingesting an OpenSpec change into graph facts and hydrating it back SHALL
reproduce the change's semantic content — its requirements, scenarios, and
tasks. semdev's own repository is the first round-trip fixture.

#### Scenario: semdev round-trips its own change
- **WHEN** semdev ingests its own `m0-walking-skeleton-spine` change and re-hydrates it from the resulting facts
- **THEN** the re-hydrated artifacts are semantically equivalent to the originals
