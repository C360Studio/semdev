## ADDED Requirements

### Requirement: The run develops the real target named by its coordinate

The run's source SHALL be resolved PER RUN from the run's own `run.issue.ref`
coordinate — the real repository its issue names — not from one operator-configured
directory shared by every run. The provisioning pipeline SHALL obtain that source
(clone at the repository's default branch) and materialize the run's checkout as a
working branch off the target's base, so the run develops the actual target.

Source resolution SHALL fail closed exactly as the static-fixture path does: a run
with no resolvable coordinate, an unparseable ref, or a source that cannot be
obtained SHALL park toward the operator, never a guessed or defaulted target (the
sandbox==nil / SB5 discipline).

An operator-configured fixture DIRECTORY SHALL remain available as an explicit
development/test source, selected by configuration; when selected, every downstream
stage (materialize, cold-prove, warm sandbox, apply_patch, measure, verify, deliver)
SHALL behave byte-for-byte as before this change.

#### Scenario: A run resolves and develops its real target
- **WHEN** a run whose `run.issue.ref` names a real repository reaches provisioning
- **THEN** the pipeline obtains that repository's source at its default branch and
  materializes the run's checkout as a working branch off that base
- **AND** the dev loop develops the real target, not a fixture

#### Scenario: An unresolvable target parks toward the operator
- **WHEN** a run has no resolvable coordinate, an unparseable ref, or its source
  cannot be obtained
- **THEN** the run parks toward the operator
- **AND** no checkout is materialized from a guessed or defaulted target

#### Scenario: The fixture source path is preserved for development
- **WHEN** configuration selects the fixture-directory source
- **THEN** the run develops that directory exactly as before this change
- **AND** no forge coordinate is read

### Requirement: The authored change is measured, reviewed, and delivered against the target's base

The run's checkout SHALL preserve the obtained history so that the authored change is
exactly the difference between the target's base and the run's committed attempt —
never the target's entire history. The cumulative authored diff the measurement,
review, and delivery stages consume SHALL be computed against the RECORDED base the
run started from (the materialize-time tip), not against the repository's root commit.
A pull request opened from a real target's run SHALL therefore present the fix alone.

For the fixture-directory source (which carries no history), the recorded base SHALL
be the pristine baseline commit, so the authored diff is byte-for-byte identical to
today.

#### Scenario: A real target's diff is the fix, not the whole repository
- **WHEN** a run develops a real target with existing history and commits an attempt
- **THEN** the cumulative authored diff is the change against the recorded base branch
- **AND** it does not include the target's pre-existing history as authored content

#### Scenario: The fixture diff is unchanged
- **WHEN** a run develops the fixture-directory source and commits an attempt
- **THEN** the cumulative authored diff against the recorded (pristine baseline) base
  is identical to the pre-change behavior
