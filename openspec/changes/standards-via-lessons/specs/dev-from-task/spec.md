# Dev-from-task — standards-via-lessons delta

## MODIFIED Requirements

### Requirement: Deterministic floors gate the loop and cannot be skipped

Each attempt SHALL be evaluated by deterministic floor checks (source-build
integrity, authored-test integrity / vacuous tests, stub artifacts,
anti-mock, tests-must-exist, and post-measure working-tree cleanliness) that
emit findings as facts (`floor.finding`). Floors SHALL evaluate the attempt's
committed snapshot and SHALL run deterministically with no model turn. The
rail SHALL route on those facts via rules and SHALL NOT advance to semantic
review while a rejecting floor finding stands. When the provisioned repo's
standards file declares deterministic checks, each declared check command
SHALL additionally run in the run's sandbox container (never on the host —
repo-authored commands execute only in-container), with its result stamped by
the executing harness as a floor finding (G3 — the command's real exit status,
never a model claim): a failing check marked `required` SHALL reject the
attempt exactly like a built-in floor; a failing non-required check SHALL
surface as a non-rejecting finding. A repo declaring no checks leaves the
floors exactly as they are.

A repo-declared check is a gate semdev has not itself proven, so the parser
SHALL reject check commands that cannot fail by construction, naming the
defect, and each check's finding SHALL carry its gate status. A check MAY
declare a negative-control command that MUST exit non-zero; a control that
exits zero SHALL be stamped as an un-failing gate and SHALL reject the attempt
when the check is `required`. A check with no declared control SHALL still
gate as declared but SHALL be stamped `unproven` and SHALL NOT be rendered as
a clean pass in the run's evidence. A check the harness could not execute at
all SHALL be stamped `not-run` and SHALL NOT be read as a pass.

#### Scenario: A vacuous-test attempt is rejected by a floor
- **WHEN** an attempt submits a fabricated or vacuous test
- **THEN** a deterministic floor emits a rejecting `floor.finding`
- **AND** the rail does not advance that attempt to semantic review

#### Scenario: Test-time mutation of the artifact is rejected
- **WHEN** the working tree differs from the attempt's commit after measurement runs
- **THEN** a deterministic floor emits a rejecting `floor.finding`

#### Scenario: Floors run without a model turn
- **WHEN** floors evaluate an attempt
- **THEN** no model-publishing spawn occurs on the floors path

#### Scenario: A required repo-declared check gates the attempt
- **WHEN** the repo's standards file declares a required check and the check's
  command exits non-zero in the sandbox for an attempt
- **THEN** the harness stamps a rejecting `floor.finding` carrying the check's
  name and real exit status
- **AND** the rail does not advance that attempt to semantic review

#### Scenario: A non-required check failure does not block
- **WHEN** a declared non-required check fails for an attempt
- **THEN** its finding is stamped and visible but not rejecting
- **AND** the attempt advances if every other floor passes

#### Scenario: Repo checks run in the container, never the host
- **WHEN** a repo-declared check executes
- **THEN** it executes inside the run's per-run sandbox container
- **AND** no repo-authored command from the standards file runs on the host

#### Scenario: A check command that cannot fail is rejected at parse
- **WHEN** a standards file declares a check whose command suppresses its own
  status, is vacuous, or launders it through a top-level pipeline on a
  required check
- **THEN** the parse rejects the file naming the offending check and the
  construct
- **AND** no run provisions against that file

#### Scenario: A negative control that passes marks the gate un-failing
- **WHEN** a required check declares a negative-control command and that
  control exits zero in the sandbox
- **THEN** the harness stamps a rejecting `floor.finding` naming the check and
  its un-failing control
- **AND** the rail does not advance that attempt to semantic review

#### Scenario: A check with no negative control still gates but is unproven
- **WHEN** a declared check has no negative-control command
- **THEN** the check runs and gates exactly as declared
- **AND** its finding carries the `unproven` gate status
- **AND** the run's evidence does not render it as a clean pass

#### Scenario: A check that could not be executed is never a pass
- **WHEN** the harness cannot execute a declared check's command in the
  sandbox at all
- **THEN** the finding is stamped `not-run`, distinguished from a check that
  ran and exited non-zero
- **AND** the attempt does not advance on that check having passed
