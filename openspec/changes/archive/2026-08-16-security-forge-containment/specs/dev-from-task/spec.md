# Dev-from-task — security-forge-containment delta

## MODIFIED Requirements

### Requirement: Deterministic floors gate the loop and cannot be skipped

Each attempt SHALL be evaluated by deterministic floor checks (source-build
integrity, authored-test integrity / vacuous tests, stub artifacts,
anti-mock, tests-must-exist, and post-measure working-tree cleanliness) that
emit findings as facts (`floor.finding`). Floors SHALL evaluate the attempt's
committed snapshot and SHALL run deterministically with no model turn. The
rail SHALL route on those facts via rules and SHALL NOT advance to semantic
review while a rejecting floor finding stands. The cleanliness floor SHALL
fail closed in both directions: a tree that differs from the commit rejects,
and committing unauthorized content SHALL never be the mechanism by which a
later attempt's tree becomes clean — residue that caused one attempt's
rejection SHALL NOT appear inside a subsequent attempt's committed snapshot.

#### Scenario: A vacuous-test attempt is rejected by a floor
- **WHEN** an attempt submits a fabricated or vacuous test
- **THEN** a deterministic floor emits a rejecting `floor.finding`
- **AND** the rail does not advance that attempt to semantic review

#### Scenario: Test-time mutation of the artifact is rejected
- **WHEN** the working tree differs from the attempt's commit after measurement runs
- **THEN** a deterministic floor emits a rejecting `floor.finding`

#### Scenario: Prior-attempt residue cannot launder into a later commit
- **WHEN** an attempt is rejected for working-tree residue and the loop retries
- **THEN** the retry attempt's committed snapshot does not contain the prior
  attempt's residue
- **AND** the delivered `attempt.commit.sha` can never carry content that a
  cleanliness rejection previously flagged

#### Scenario: Floors run without a model turn
- **WHEN** floors evaluate an attempt
- **THEN** no model-publishing spawn occurs on the floors path
