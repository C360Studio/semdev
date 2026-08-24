# Harness-measurement — standards-via-lessons delta

## MODIFIED Requirements

### Requirement: Adversarial semantic review runs per task, gated on harness facts

The reviewer SHALL review each unit of work — each projected task — on its own,
reading that task's `measurement.result` (not the model's assertion about the
command) and the attempt's cumulative committed diff via read tools, and
recording a per-task verdict (`review.verdict.<task_index>`) with its findings
(`review.findings.<task_index>`). The review SHALL be adversarial: the
reviewer attempts to refute the attempt, and its findings are additive
constraints only — they SHALL NOT weaken an immutable `task.spec`. When the
provisioned repo declares standards, the reviewer's brief SHALL carry the
active reviewer-scoped standards, its contract SHALL require reviewing the
attempt against them, and a finding grounded in a standard SHALL cite that
standard's id; a finding citing a violated `must` standard SHALL make the
task's verdict `changes_requested` (standards findings remain additive
constraints — a repo standard can tighten, never weaken, the spec or the
measurement floor). A task's verdict SHALL be approving ONLY when that task's
`measurement.result` records a pass AND the reviewer raised no finding against
it; the per-task measurement is the floor under the verdict. A
`changes_requested` verdict SHALL route the task back into a fresh bounded
developer attempt within the attempt budget — never onward to verification;
only approved work advances. Delivery (`open_pr`) SHALL require every
projected task to carry an approving `review.verdict.<i>`.

#### Scenario: A false success claim cannot be approved
- **WHEN** the model claims a task's tests pass but that task's `measurement.result` records failure
- **THEN** the reviewer cannot record an approving `review.verdict` for that task

#### Scenario: Each task is reviewed on its own evidence
- **WHEN** the reviewer reviews task N
- **THEN** `review.verdict.N` is derived from task N's measurement and the findings raised against task N
- **AND** a passing task's verdict is unaffected by a different task's failure

#### Scenario: Findings never relax the spec
- **WHEN** a reviewer raises a finding
- **THEN** the finding adds a constraint and does not remove or weaken any `task.spec` requirement

#### Scenario: Rejection re-enters development within budget
- **WHEN** the reviewer records `changes_requested` and the attempt budget is not exhausted
- **THEN** a fresh bounded developer attempt is dispatched carrying the findings
- **AND** clean-room verification does not run for the rejected attempt

#### Scenario: Rejection beyond budget parks toward the human
- **WHEN** the reviewer records `changes_requested` and the attempt budget is exhausted
- **THEN** the run parks toward the human with the findings

#### Scenario: The reviewer inspects the diff before the verdict
- **WHEN** a review loop runs
- **THEN** the reviewer can read the attempt's cumulative committed diff and source before `submit_review`

#### Scenario: A violated must-standard blocks approval
- **WHEN** the reviewer finds the attempt violates an active `must` standard of
  the provisioned repo
- **THEN** it records a finding citing that standard's id and the task's
  verdict is `changes_requested`
- **AND** the rejection re-enters development carrying the standard-citing
  finding

#### Scenario: A standards finding cites its standard
- **WHEN** a reviewer finding is grounded in a repo standard
- **THEN** the finding names the standard's id so the developer retry and the
  human can trace it to the declaration

#### Scenario: Standards tighten, never weaken
- **WHEN** a repo standard conflicts with the task.spec or would excuse a
  failing measurement
- **THEN** the spec and the measurement floor prevail — the standard cannot
  authorize approval of failing or out-of-spec work
