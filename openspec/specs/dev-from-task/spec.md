# Dev-From-Task Specification

## Purpose

Turn an approved change's tasks into immutable, Karpathy-shaped task facts and
execute them through a bounded, rules-routed developer loop: attempts are
dispatched under strict tool allowlists with full task context, measured
in-loop by the harness, gated by deterministic floors, and routed entirely by
rules over harness-stamped facts — escalating toward the human on budget
exhaustion rather than looping unbounded.
## Requirements
### Requirement: Projected task facts are immutable

Approval SHALL project the approved change's tasks onto the run as immutable task
facts (`task.spec`). The dev loop SHALL converge on these facts and SHALL NOT be
able to redefine them. Task status SHALL be derived from execution markers, not
written as a separate authoritative status field — no second planning state machine
is introduced.

The approval trigger SHALL be the single-valued gate decision
(`run.change.decision` == `approve`), not a standalone boolean approval fact.

#### Scenario: Approval projects immutable task facts
- **WHEN** `run.change.decision` == `approve` is present for a run
- **THEN** the change's tasks are projected as `task.spec` facts
- **AND** an attempt to mutate a projected `task.spec` is rejected

#### Scenario: Task status is derived, not authored
- **WHEN** a task's progress is queried
- **THEN** its status is derived from execution markers (`task.attempt`) and gate facts
- **AND** no authoritative `task.status` predicate is written

### Requirement: Karpathy-shaped task schema enforced at projection

Each `task.spec` MUST carry `assumptions`, `non_goals`, at least one
`target_file`, and a required `test_command`. The `target_files` set MUST
include the test files the `test_command` measures — an attempt whose
write-set is confined to `target_files` must be able to author both the fix
and its tests. A per-task attempt budget SHALL be clamped to the range [1,5]
at stamp time; the clamp ceiling is the structural bound. A task missing its
budget or `test_command` SHALL fail toward the human rather than be stamped
with a default that hides the gap.

#### Scenario: Missing test_command fails toward the human
- **WHEN** a task is projected without a `test_command`
- **THEN** projection does not stamp the task and the run parks toward the human

#### Scenario: Budget is clamped at stamp time
- **WHEN** a task declares an attempt budget greater than 5
- **THEN** the stamped `task.spec` budget is clamped to 5
- **AND** escalation fires within the structural bound regardless

#### Scenario: target_files without the measuring tests fails toward the human
- **WHEN** a task's `target_files` omits every test file its `test_command` measures
- **THEN** projection does not stamp the task and the run parks toward the human

### Requirement: Bounded dev loop with escalation

Each attempt at a task SHALL be one bounded, multi-turn developer loop: the
developer reads the workspace, authors patches, and invokes measurement inside
the same loop, receiving the harness's real output as in-loop feedback. The
dispatching rule SHALL record the execution marker (`task.attempt`) at spawn
time — a crashed or wedged loop still consumed an attempt. In-loop iteration is
bounded by the loop engine's iteration cap; a loop terminal without a passing
harness measurement — including a terminal with no measurement at all — is a
failed attempt, EXCEPT a loop that failed for a TRANSIENT reason (an
infrastructure failure classified by the loop engine as `model_error` or
`handler_error`, distinct from genuine non-convergence), which SHALL be
re-dispatched under a separate bounded grace budget without consuming the
convergence budget (see "Transient developer-loop failures do not consume the
convergence budget"). On attempt-budget exhaustion without a passing gate —
where the failing attempts are genuine non-convergence (the loop ran out of
iterations, or ended with a red/absent measurement) — the rail SHALL escalate
toward the human and stop; it SHALL NOT loop unbounded. The escalation toward
the human SHALL carry the classified terminal reason (see "Escalation toward
the human carries the classified terminal reason").

The enforced attempt budget SHALL be the per-task projected `task.spec.budget`
(the authored budget clamped to `[1,5]` at projection), not a single global
constant. Because the routing rules fire on their own loops (retry/escalate on
the developer loop; review-retry/park on the review loop) while
`task.spec.budget` lives on the run entity, the harness SHALL make the projected
budget readable by those rules by mirroring it onto EVERY loop a budget-gated
route fires on, as `route.task.budget` — a raw copy of the already-clamped
scalar, written by the single logical `route-mirror` owner alongside that loop's
`route.attempt.*` mirror and in the same atomic pass (never attempts without the
budget), carrying no model-supplied or derived value (G3/G5). An absent or
unparseable budget at mirror time SHALL fail that mirror site's turn loudly and
stamp nothing — never a silent default. The retry boundary SHALL fire while the
attempt count is below the budget, and the escalate/park boundary SHALL fire when
the count reaches or exceeds it; the two boundaries SHALL partition the count with
no gap and no overlap (fail-closed — an over-count escalates, never retries past
budget).

#### Scenario: The developer iterates on harness feedback inside one loop
- **WHEN** the developer applies a patch and invokes measurement within its loop
- **THEN** the measurement's real output returns as in-loop feedback and the developer can author a follow-up patch in the same loop
- **AND** no additional model loop is spawned per iteration

#### Scenario: The attempt is recorded at dispatch
- **WHEN** a developer loop is dispatched for a task as a convergence attempt
- **THEN** `task.attempt` is appended before the loop's first model turn
- **AND** a loop that wedges or crashes has still consumed the attempt

#### Scenario: A genuine terminal without a passing measurement is a failed attempt
- **WHEN** a developer loop ends by genuine non-convergence (ran out of iterations, or completed with a red or absent measurement) without a harness-stamped passing measurement
- **THEN** the attempt routes as failed (retry within the convergence budget, else escalate)
- **AND** the model's own stopping decision confers no success

#### Scenario: The per-task budget bounds retries
- **WHEN** a task authored with an attempt budget of B fails B times without a passing gate
- **THEN** the rail escalates toward the human on the Bth failed attempt and records no (B+1)th `task.attempt`
- **AND** a task authored with a larger budget (within the clamp) is allowed correspondingly more attempts before escalation

#### Scenario: The enforced budget is the harness-mirrored contract, not a constant
- **WHEN** the retry and escalate/park routes evaluate the attempt count
- **THEN** they compare it against `route.task.budget` (the harness copy of the projected `task.spec.budget`), never a hard-coded literal
- **AND** `route.task.budget` is written only by the `route-mirror` owner and reflects the clamped authored value with no model input (a sanctioned mirror like `route.attempt.*`, not a Go-derived routing token)

#### Scenario: Genuine budget exhaustion escalates and halts
- **WHEN** a task reaches its convergence attempt budget through genuine non-convergence without passing its gates
- **THEN** the rail escalates toward the human and records no further convergence `task.attempt`

### Requirement: Transient developer-loop failures do not consume the convergence budget

A developer loop that fails for a TRANSIENT reason (`model_error` or `handler_error` — an infrastructure/transport failure, as opposed to genuine non-convergence) SHALL be re-dispatched as a fresh developer loop WITHOUT incrementing the convergence attempt budget, bounded by a separate transient-retry cap, so a flaky endpoint cannot exhaust a task's convergence budget.

When the transient-retry cap is reached the run SHALL park toward the human rather than retry
unbounded. The transient-retry count SHALL be a harness-stamped fact, never derived by
product Go, and the classification SHALL come from the loop engine's terminal reason, never
from an LLM-supplied outcome.

#### Scenario: A transient failure re-dispatches without consuming the budget
- **WHEN** a developer loop terminates with a transient reason (`model_error` or `handler_error`) and the transient-retry cap is not yet reached
- **THEN** a fresh developer loop is dispatched for the same task
- **AND** the convergence attempt budget count is unchanged (no convergence `task.attempt` is appended for the transient re-dispatch)

#### Scenario: A transient failure does not fire the convergence routes
- **WHEN** a developer loop terminates with a transient reason and transient grace remains
- **THEN** neither the convergence retry nor the convergence escalate route fires for that terminal
- **AND** exactly one fresh developer loop is dispatched (the one-in-flight serialization invariant holds)

#### Scenario: The transient cap parks toward the human
- **WHEN** developer loops keep failing transiently until the transient-retry cap is reached
- **THEN** the run parks toward the human with the transient reason, and no further transient re-dispatch occurs

### Requirement: Escalation toward the human carries the classified terminal reason

When a developer attempt escalates to a park (genuine convergence-budget exhaustion, or reaching the transient-retry cap) the `run.awaiting.human` record SHALL carry the loop's classified terminal reason so the human triaging the parked run can distinguish "needs more turns" from "flaky endpoint" from "genuine test failure".

The reason values are the loop engine's (`max_iterations`, `model_error`, `handler_error`, or
an absent reason for a completed-but-red attempt); the reason MUST be the harness/engine-
classified fact, carried by the rule, never synthesized by product Go.

#### Scenario: Genuine budget-exhaustion park names the reason
- **WHEN** the convergence budget exhausts and the rail parks toward the human
- **THEN** the `run.awaiting.human` record carries the last loop's terminal reason (or notes an absent reason for a completed-but-red attempt)

#### Scenario: Transient-cap park names the transient reason
- **WHEN** the transient-retry cap is reached and the rail parks toward the human
- **THEN** the `run.awaiting.human` record names the transient reason (`model_error`/`handler_error`) so the human knows a re-run may succeed

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

### Requirement: Routing is rules over harness-stamped facts

Every route in the rail SHALL be decided by rule conditions reading
harness-stamped facts (measurement results, floor findings, review verdicts,
verify results, attempt counters) — advance to review, retry, escalate,
verify, deliver, park. Product Go SHALL NOT derive routing tokens for rules to
dispatch, and no fact SHALL exist whose only purpose is to relay control
between stations. Where the rule engine genuinely cannot express a route, the
system SHALL record the upstream semstreams ask and park toward the human —
never add a Go decision layer.

#### Scenario: An attempt's disposition is derivable from the rule pack alone
- **WHEN** a measured, floor-clean attempt's loop terminates
- **THEN** the advance-to-review rule fires from the harness-stamped facts directly
- **AND** no intermediate Go-derived decision fact exists between terminal and route

#### Scenario: The route-token census finds zero Go deciders
- **WHEN** the widened G2 conformance census runs over product Go
- **THEN** it finds zero functions deriving lifecycle or routing tokens consumed by rules

#### Scenario: An inexpressible route parks with a filed ask
- **WHEN** a needed route cannot be expressed as a rule condition
- **THEN** the run parks toward the human and the upstream engine ask is recorded

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

### Requirement: The rail binds one task at M0 through a single declared seam

The M0 rail SHALL execute exactly one projected task, bound through a single
declared binding point marked as the multi-task seam. Copying rule chains per
task index is forbidden; the documented M1 path is readiness-based dispatch
over task dependency edges (the framework's gated-DAG component).

#### Scenario: No per-index rule duplication
- **WHEN** the rule pack is inspected for task-index references
- **THEN** exactly one declared binding point carries the task index
- **AND** no rule chain is duplicated per index

