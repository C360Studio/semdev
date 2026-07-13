## MODIFIED Requirements

### Requirement: Brownfield OpenSpec artifacts are ingested deterministically

The system SHALL read a target repository's existing **living specifications**
(`openspec/specs/*/spec.md`) and seed the graph with their `openspec.spec.*`
facts through a deterministic parser running as a **registered ingest
component** on the raw lane (raw-lane → projector → graph-ingest) — a
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
- **THEN** a deterministic parser reads each capability's `spec.md` and writes its `openspec.spec.*` facts to the graph under a single owner
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
