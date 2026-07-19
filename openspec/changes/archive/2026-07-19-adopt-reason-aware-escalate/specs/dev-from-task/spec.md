## MODIFIED Requirements

### Requirement: Bounded dev loop with escalation

Each attempt at a task SHALL be one bounded, multi-turn developer loop: the
developer reads the workspace, authors patches, and invokes measurement
inside the same loop, receiving the harness's real output as in-loop
feedback. The dispatching rule SHALL record the execution marker
(`task.attempt`) at spawn time — a crashed or wedged loop still consumed an
attempt. In-loop iteration is bounded by the loop engine's iteration cap; a
loop terminal without a passing harness measurement — including a terminal
with no measurement at all — is a failed attempt, EXCEPT a loop that failed
for a TRANSIENT reason (an infrastructure failure classified by the loop
engine as `model_error` or `handler_error`, distinct from genuine
non-convergence), which SHALL be re-dispatched under a separate bounded grace
budget without consuming the convergence budget (see "Transient developer-loop
failures do not consume the convergence budget"). On attempt-budget
exhaustion without a passing gate — where the failing attempts are genuine
non-convergence (the loop ran out of iterations, or ended with a red/absent
measurement) — the rail SHALL escalate toward the human and stop; it SHALL
NOT loop unbounded. At M0 the enforced attempt budget is the projected
`task.spec` budget read by the routing rules (clamped range); the escalation
toward the human SHALL carry the classified terminal reason (see "Escalation
toward the human carries the classified terminal reason").

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

#### Scenario: Genuine budget exhaustion escalates and halts
- **WHEN** a task reaches its convergence attempt budget through genuine non-convergence without passing its gates
- **THEN** the rail escalates toward the human and records no further convergence `task.attempt`

## ADDED Requirements

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
