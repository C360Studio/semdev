## MODIFIED Requirements

### Requirement: Change-approval human gate

The dev loop SHALL NOT start for a run until a human-approval decision
(`run.change.decision` == `approve`) is present for that run's generated change.
This is the first of the two human gates.

The gate decision SHALL be recorded as ONE single-valued fact, never as a pair of
independent booleans. A run SHALL NOT be able to carry both an approval and a
rejection: with two booleans the mutually-exclusive lifecycle rules fired NEITHER,
leaving the run wedged at the gate permanently with no park, no post, and no
operator surface. A single-valued fact makes that state unrepresentable under
replace-by-predicate.

The decision SHALL be first-writer-wins: once a run is decided, the opposite
decision is REFUSED. An approval is irreversible once landed, and a rejected run is
not resurrected.

An exact approval or rejection command SHALL only decide a run that is AT the gate.
A decision recorded before the gate opens would make the gate unreachable — the
rule that opens it requires the gate undecided — and therefore unrecoverable.

#### Scenario: Dev loop blocked without approval
- **WHEN** a change has been generated but `run.change.decision` is absent
- **THEN** the `dev_from_task` action is not eligible to fire

#### Scenario: Dev loop eligible after approval
- **WHEN** a human approves the generated change and `run.change.decision` == `approve` is written
- **THEN** the `dev_from_task` action becomes eligible for that run's tasks

#### Scenario: A rejected run is cancelled, not resumed
- **WHEN** `run.change.decision` == `reject` is present for a run at `awaiting_approval`
- **THEN** a rule fires the run's `awaiting_approval → cancelled` transition
- **AND** the resume rule does not fire, because it matches the opposite value of the same fact

#### Scenario: The opposite decision on a decided run is refused
- **WHEN** an authorized actor issues the opposite decision for an already-decided run
- **THEN** the recorded decision is unchanged and the run carries exactly one decision
- **AND** the refusal is surfaced, never counted or logged as a decision that landed

#### Scenario: A command before the gate opens does not decide it
- **WHEN** an authorized actor issues an exact approve or reject command for a run that is not at `awaiting_approval`
- **THEN** no decision is recorded and the command is definitively discarded
