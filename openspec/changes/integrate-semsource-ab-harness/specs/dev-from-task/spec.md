## MODIFIED Requirements

### Requirement: Model roles receive complete context under strict tool allowlists

Every model-publishing spawn SHALL declare an explicit tool allowlist scoped
to its role. Developer and reviewer prompts SHALL carry the full task
contract (goal, `target_files`, `test_command`, assumptions, non-goals) —
never entity IDs alone — and a re-entry attempt SHALL additionally carry the
prior review findings and measurement state. Roles SHALL hold read access to
the artifacts they judge: the developer reads the workspace; the reviewer
reads the cumulative diff and source. Every tool a spawn's PROMPT instructs
the model to use SHALL be present in that spawn's tool allowlist — a prompt
that names an unadvertised tool directs the model into rejected calls and
fails configuration lint. When an experiment condition extends a
role's tool set (semsource-ab), the extension SHALL be additive to the
baseline allowlist for the developer role only, and the extended list SHALL
remain a strict, per-loop-enforced allowlist — the reviewer's allowlist SHALL
be identical across conditions.

#### Scenario: The developer starts with the contract in context
- **WHEN** a developer loop is dispatched
- **THEN** its prompt contains the task's goal, target files, and test command
- **AND** its allowlist permits reading workspace files before patching

#### Scenario: A re-entry attempt carries the rejection's findings
- **WHEN** a developer loop is dispatched after a reviewer rejection
- **THEN** its prompt contains the review findings that caused the rejection

#### Scenario: A spawn without an allowlist fails configuration lint
- **WHEN** a rule declares a model-publishing spawn without an explicit tool allowlist
- **THEN** configuration validation rejects the rule pack

#### Scenario: A prompt-named tool missing from the allowlist fails configuration lint
- **WHEN** a spawn's prompt instructs the model to use a named tool that is not in the spawn's tool allowlist
- **THEN** configuration validation rejects the rule pack

#### Scenario: A condition-extended allowlist is still strictly enforced
- **WHEN** a developer loop is dispatched under an experiment condition that extends its tool set
- **THEN** the advertised list is the baseline allowlist plus exactly the condition's declared tools
- **AND** a call outside the extended list is rejected at execution

#### Scenario: The reviewer's allowlist does not vary by condition
- **WHEN** a reviewer loop is dispatched under any experiment condition
- **THEN** its tool allowlist is identical to the baseline reviewer allowlist
