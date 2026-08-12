## MODIFIED Requirements

### Requirement: Bounded dev loop with escalation

Each attempt at a task SHALL be one bounded, multi-turn developer loop: the
developer reads the workspace, authors patches, and invokes measurement inside
the same loop, receiving the harness's real output as in-loop feedback. The
dispatching rule SHALL record the execution marker (`task.attempt`) at spawn
time — a crashed or wedged loop still consumed an attempt. In-loop iteration is
bounded by the loop engine's iteration cap; a loop terminal without a passing
harness measurement — including a terminal with no measurement at all — is a
failed attempt. On attempt-budget exhaustion without a passing gate, the rail
SHALL escalate toward the human and stop; it SHALL NOT loop unbounded.

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
- **WHEN** a developer loop is dispatched for a task
- **THEN** `task.attempt` is appended before the loop's first model turn
- **AND** a loop that wedges or crashes has still consumed the attempt

#### Scenario: A terminal without a passing measurement is a failed attempt
- **WHEN** a developer loop ends without a harness-stamped passing measurement for the task
- **THEN** the attempt routes as failed (retry within budget, else escalate)
- **AND** the model's own stopping decision confers no success

#### Scenario: The per-task budget bounds retries
- **WHEN** a task authored with an attempt budget of B fails B times without a passing gate
- **THEN** the rail escalates toward the human on the Bth failed attempt and records no (B+1)th `task.attempt`
- **AND** a task authored with a larger budget (within the clamp) is allowed correspondingly more attempts before escalation

#### Scenario: The enforced budget is the harness-mirrored contract, not a constant
- **WHEN** the retry and escalate/park routes evaluate the attempt count
- **THEN** they compare it against `route.task.budget` (the harness copy of the projected `task.spec.budget`), never a hard-coded literal
- **AND** `route.task.budget` is written only by the `route-mirror` owner and reflects the clamped authored value with no model input (a sanctioned mirror like `route.attempt.*`, not a Go-derived routing token)
