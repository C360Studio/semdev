# Repo-Standards — standards-via-lessons delta

## Purpose

Lets a target repository declare its own standards — the "in this repo, X is a
must" layer — in one small human-authored committed file, and delivers them to
the right agent roles' briefs through the semstreams lesson substrate with
honest provenance, so runs develop and review against the repo's rules, not
only the change spec and the fixed floors.

## ADDED Requirements

### Requirement: A target repo declares standards in one committed file

A target repository MAY commit a single structured standards file at a fixed
conventional path. Each standard SHALL carry a stable id, a terse normative
text, and a severity from exactly `must|should|may`; a standard MAY scope
itself to specific agent roles. The file MAY also carry a `checks` section of
deterministic commands (consumed by the floors lane — see the dev-from-task
delta). The file SHALL be human-authorable without graph or vocabulary
knowledge. A repo with NO standards file runs exactly as today — standards are
opt-in, and their absence SHALL NOT park, warn, or alter the arc. A standards
file that is present but malformed SHALL fail closed toward the operator
(park) — never a silent partial parse or a guessed interpretation.

#### Scenario: A valid standards file is consumed
- **WHEN** a run provisions a target repo carrying a well-formed standards file
- **THEN** every declared standard is available to the run's lifecycle as a
  standard record
- **AND** the run proceeds with no operator intervention

#### Scenario: No standards file, no change in behavior
- **WHEN** a run provisions a target repo with no standards file
- **THEN** the run proceeds exactly as before this capability existed
- **AND** no standard records are born and nothing parks or warns

#### Scenario: A malformed standards file parks toward the operator
- **WHEN** the standards file is present but unparseable or declares an invalid
  severity, duplicate id, or missing required field
- **THEN** the run fails closed toward the operator naming the defect
- **AND** no partial set of standards is born

### Requirement: Standards are born as lesson records with honest provenance

Each declared standard SHALL be born as a lesson record on the substrate's
gated lifecycle, with a content-derived deterministic identity so re-birth is
idempotent (re-provisioning an unchanged file creates nothing new and errors
nothing). Each record's evidence SHALL cite the standards-file source it was
derived from — an entity that exists in the graph before promotion resolves
evidence — and its provenance SHALL be distinguishable from agent-emitted
lessons. A standard whose rendered injection form exceeds the substrate's byte
bound SHALL be rejected loudly at birth, naming the standard id — never
truncated and never silently dropped. A standard removed from the file SHALL
be retired through the substrate's lifecycle by the same sync step on the next
provision; retirement SHALL NOT delete the record or its history.

#### Scenario: Re-provisioning an unchanged file is idempotent
- **WHEN** a second run provisions the same repo revision's standards file
- **THEN** no duplicate records are created and the sync step succeeds
- **AND** the existing records' lifecycle states are unchanged

#### Scenario: An oversized standard is rejected loudly
- **WHEN** a declared standard's injection text exceeds the substrate's byte
  bound
- **THEN** the sync fails closed toward the operator naming that standard's id
- **AND** no truncated form of it is ever born

#### Scenario: A removed standard is retired
- **WHEN** a standard present in an earlier revision is absent from the
  currently provisioned file
- **THEN** the sync step retires that record through the gated lifecycle
- **AND** retired standards no longer reach any brief

### Requirement: Repo-file standards auto-promote under an explicit named policy

Standards born from a repo's committed standards file SHALL be promoted
proposed→active by an explicit auto-promotion policy whose recorded rationale
is that the git commit / pull-request review of the standards file IS the
human gate. The promotion SHALL ride the substrate's validated curation path
(evidence-existence resolved before activation). NO other lesson record SHALL
be auto-promoted by this policy: an agent-emitted or otherwise-proposed lesson
stays proposed until an operator promotes it, and `emit_lesson` remains
unadvertised to every semdev loop.

#### Scenario: A file-derived standard activates without an operator step
- **WHEN** a standard is born from the provisioned repo's standards file
- **THEN** it reaches `active` without additional human interaction
- **AND** the activation resolved its cited evidence first

#### Scenario: A non-file lesson is not auto-promoted
- **WHEN** a lesson record exists that was not born from the standards file
- **THEN** the auto-promotion policy does not touch it
- **AND** it activates only through the operator-gated path

### Requirement: Active standards reach the right roles' briefs within bounds

Active standards SHALL be delivered into agent briefs through the substrate's
existing deterministic injection: scoped by role (a standard scoped to
`reviewer` SHALL NOT enter a developer brief, and vice versa; an unscoped
standard reaches both), ordered so that `must` standards outrank `should`,
and `should` outrank `may`, whenever the injection count or byte bounds bite.
The mapping SHALL be `must`→highest, `should`→middle, `may`→lowest of the
substrate's severity levels. Injection failure SHALL degrade silently per the
substrate's availability-over-consistency contract — a dispatch without its
standards block is a degraded brief, never a blocked loop.

#### Scenario: A developer-scoped standard reaches the developer
- **WHEN** a developer-role loop dispatches for a repo with an active
  developer-scoped standard
- **THEN** that standard's text appears in the loop's brief with its id

#### Scenario: Role scoping excludes the other role
- **WHEN** a standard is scoped to the reviewer role only
- **THEN** it appears in reviewer briefs and never in developer briefs

#### Scenario: Musts survive the bound
- **WHEN** a repo declares more standards than the injection count bound admits
- **THEN** every `must` standard is injected before any `should` or `may`
  standard consumes a slot

### Requirement: Fixture standards carry no coaching

Standards text in test fixtures SHALL follow the realistic-fixture discipline:
no orchestration vocabulary, tool names, expected outcomes, or step-by-step
instructions that would coach a model through the arc (G8/B10). A conformance
pin SHALL enforce the banned-vocabulary check over every fixture standards
file.

#### Scenario: A coaching fixture fails conformance
- **WHEN** a fixture standards file contains orchestration-vocabulary coaching
- **THEN** the conformance pin fails naming the file and the banned term
