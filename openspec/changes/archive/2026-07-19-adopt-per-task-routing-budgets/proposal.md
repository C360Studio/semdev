## Why

The dev-from-task rail hard-codes a constant attempt budget of `3`
(`route.attempt.instance length_lt 3` / `length_gt 2` in the retry/escalate
routes) and a uniform in-loop iteration cap (`agentic-loop max_iterations: 8`),
and its escalate route parks on outcome (`route.attempt.unclean` + count) blind
to *why* the loop terminated. Meanwhile the projected per-task
`task.spec.budget` (clamped `[1,5]` by `devtask.Project`) is authored on every
task but **inert** — nothing reads it. The three semstreams capabilities the
rail was built to route *around* have all landed and are regression-guarded on
beta.150: **#519** (scalar `.value` field-to-field substitution), **#528**
(per-spawn `max_iterations`), **#529** (typed exhaustion sentinel
`agentic.ErrMaxIterationsReached`). The `dev-from-task` spec already promises the
per-task budget "SHALL become the enforced value when the rule engine supports
dynamic budgets" — the engine now does. This change redeems that promise.

## What Changes

**In scope — #519, per-task attempt budget becomes load-bearing.** Engine
verification (design.md) established that `$entity.triple.…value` reads only the
firing entity, so the routes cannot read `task.spec.budget` (which lives on the
run) directly — and the four routes fire on TWO loops: `06c`/`06d` on the
developer loop L_n, `07b`/`07c` on Quinn's review loop. The budget therefore
rides **both sanctioned route-mirror sites** (the one logical `route-mirror`
writer that already stamps `route.attempt.*` on each): `checkfloors` reads
`task.spec.budget` off the run and stamps `route.task.budget` on L_n, and
`submit_review` does the same onto the review loop in its existing single pass,
and the routes gate on it:

- `06c-route-retry` / `07b-review-retry`: `route.attempt.instance length_lt
  $entity.triple.route.task.budget.value` (retry while count < B).
- `06d-route-escalate` / `07c-review-park`: `route.attempt.instance **length_gte**
  $entity.triple.route.task.budget.value` (escalate/park when count ≥ B).

The `length_gte` operator does not exist in the engine today — filed as
**semstreams #568** (the length-operator family is missing the `≥`/`≤` variants
the numeric family has). This change is **designed against `length_gte`** and
implemented once #568 lands; it uses no `B−1`/rule-split workaround.

**Deferred (documented, not in this change):**

- **#528 — per-spawn iteration budget.** `task.spec.budget` is the *attempt*
  budget (# of dev loops), not the *iteration* budget (turns per loop); driving
  `loop_max_iterations` from it conflates two knobs, and the first dispatch (04,
  on the coordinator loop) hits the same cross-entity wall. A clean #528 needs
  its own per-task iteration schema field — separate scope. The uniform
  `agentic-loop max_iterations` stays (e2e-proven).
- **#529 — reason-aware escalate.** The loop's termination reason (exhaustion vs
  transient model error) never reaches the graph — `buildLoopFailureTriples`
  stamps `agent.loop.outcome="failed"` for both, dropping `event.Reason`. A rule
  can't branch on a Go-only sentinel. Its own upstream ask (have the framework
  stamp the terminal reason as a fact) is FILED as semstreams **#569**; until it
  lands `06d`/`07c` stay count-only. Parked (G2 — do not synthesize product-side).

**NOT breaking**: the current literal-3 / uniform-cap / outcome-based behavior
stays correct; #519 replaces the literal `3` with the authored contract on the
same rails (G2 rule-native routing preserved). The
`test/conformance/upstream_asks_test.go` #519 guard note flips to "adopted."

## Capabilities

### New Capabilities

<!-- none — this adopts already-landed engine capabilities into existing rules. -->

### Modified Capabilities

- `dev-from-task`: the **Bounded dev loop with escalation** and **Routing is
  rules over harness-stamped facts** requirements change — the enforced attempt
  budget becomes the per-task projected `task.spec.budget`, in-loop iterations
  are per-spawn budgeted, and escalation distinguishes exhaustion from a
  transient model error. (The capability's base spec lives in the sibling
  unarchived changes on this branch — `simplify-m0-execution-rail` /
  `m0-walking-skeleton-spine` — not yet in `openspec/specs/`; the delta here
  re-projects those requirements.)

## Impact

- **Go** (two small extensions to the existing sanctioned route-mirror sites —
  G1: the pure-rule path does not exist, verified): `internal/tools/checkfloors`
  reads `task.spec.budget` off the run and stamps `route.task.budget` on L_n,
  and `internal/tools/submitreview` stamps the same onto the review loop in its
  existing single `ReplaceTriples` pass — both under the one logical
  `route-mirror` writer (G5, the `route.attempt.instance` precedent). No new
  component/tool; no lifecycle transition (G2); a raw copy of the
  harness-projected clamped budget, not a derived outcome (G3). Absent budget →
  loud per-site fault, nothing stamped (design D7 — never a silent default).
- **Vocabulary**: one new predicate `route.task.budget` (writer `route-mirror`,
  G5/G9), declared in `internal/vocab` and the change's spec delta.
- **Rules**: `configs/rules/dev-from-task/{06c,06d,07b,07c}.json` — the four
  retry/escalate/park routes swap the literal `3` for
  `$entity.triple.route.task.budget.value` (`length_lt` on retry, `length_gte` on
  escalate/park). No `add_triple`/spawn changes.
- **Config**: `agentic-loop max_iterations` retained as the in-loop ceiling.
- **Upstream dependency**: **semstreams #568** (`length_gte`/`length_lte`) must
  land first — this change is designed against it and does not ship a workaround.
- **Tests**: the `upstream_asks_test.go` #519 guard note flips to "adopted"; new
  route pins for the per-task budget boundary; the
  `TestBridgeProofBudgetExhaustionParks` e2e journey (currently relies on the
  implicit budget 3) is updated to author a budget explicitly and assert it
  bounds the retries.
