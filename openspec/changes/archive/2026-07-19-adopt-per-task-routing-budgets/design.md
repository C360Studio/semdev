## Context

The dev-from-task rail hard-codes a constant attempt budget of `3` in the four
retry/escalate/park routes (`route.attempt.instance length_lt 3` on retry,
`length_gt 2` on escalate/park — `06c`, `06d`, `07b`, `07c`), and a uniform
in-loop iteration cap (`agentic-loop max_iterations: 8`). The projected per-task
`task.spec.budget` (clamped `[1,5]` by `devtask.Project`) is authored on every
task but read by nothing. The `dev-from-task` spec already promises the per-task
budget "SHALL become the enforced value when the rule engine supports dynamic
scalar comparison." The three enabling semstreams capabilities landed and are
regression-guarded on beta.150 (#519 scalar `.value`, #528 per-spawn
`max_iterations`, #529 typed exhaustion sentinel). This change adopts the one
that is cleanly expressible — the per-task **attempt** budget (#519) — and
records why the other two are deferred.

Engine verification (against the beta.150 module cache) produced three
load-bearing facts that shape every decision below:

1. **`$entity.triple.<pred>.value` resolves ONLY the firing entity's own
   triples** (`processor/rule/execution_context.go:658-673` iterates
   `ec.Entity.Triples`, returns `""` silently on absence; the `$related` lane is
   nil on the entity-state watcher). The routes fire on the developer loop L_n;
   `task.spec.budget` lives on the run. So the routes cannot read the budget
   directly.
2. **The floors station already mirrors run-side facts onto L_n.**
   `checkfloors.RunFloors` reads `measurement.result.*`, `task.attempt.instance`,
   and `attempt.commit.sha` off the run and stamps `route.attempt.*` on L_n via
   the `route-mirror` owner (`internal/tools/checkfloors/checkfloors.go`). A
   fourth run-side read + one more mirror triple rides the identical, sanctioned
   path.
3. **The engine has no `length_gte`/`length_lte`.** The numeric operator family
   is complete (`lt/lte/gt/gte`) but the array/length family is missing the
   `≥`/`≤` variants (`processor/rule/expression/types.go:121-143`) — filed as
   semstreams **#568**.

## Goals / Non-Goals

**Goals:**

- The enforced attempt budget is the per-task projected `task.spec.budget`,
  read by the routing rules with zero workaround artifacts.
- Preserve the current fail-closed, no-gap/no-overlap retry↔escalate partition
  across the full `[1,5]` clamp range.
- Stay rule-native (G2): no Go decision layer; the only Go is a copy on an
  existing sanctioned mirror.

**Non-Goals:**

- Per-spawn **iteration** budgets (#528) — a distinct knob needing its own
  per-task field (see D5).
- Reason-aware escalate (#529) — blocked on a framework fact (see D4).
- Any change to how attempts are counted, dispatched, or floored — only the
  budget *boundary value* changes from a literal to the mirrored contract.

## Decisions

### D1 — The routes read the per-task budget via a floors-station mirror (`route.task.budget`), not cross-entity substitution

The routes fire on L_n; the budget is on the run; `.value` is firing-entity-only
(fact 1). Options considered:

- **(a) Mirror the budget via the sanctioned route-mirror — CHOSEN.** The
  `route-mirror` owner is ONE logical writer with TWO code sites, and the four
  routes fire on two different loops — so the budget rides **both** sites:
  - `checkfloors.RunFloors` → the DEVELOPER loop L_n, where `06c`/`06d` fire:
    add `reader.ReadFacts(..., "task.spec.budget")` and one `route.task.budget`
    triple to `routeMirrorTriples`.
  - `submit_review` → **Quinn's REVIEW loop**, where `07b`/`07c` fire
    (`submitreview.go` already stamps `route.review.verdict` +
    `route.attempt.instance` on its own loop in one pass): the same run-side
    read, one more `route.task.budget` triple in that same pass.
  A budget stamped on only one of the two would leave the other lane's routes
  reading the silently-empty substitution on their ORDINARY path (e.g. every
  `changes_requested` verdict) — see D7. Minimal Go on the existing sanctioned
  mirror sites. G1 (the pure-rule path does not exist), G2 (no lifecycle
  transition — neither site fires one), G3 (a raw copy of the harness-projected
  clamped value, not a derived/model outcome), G5 (both sites stamp under the
  established one-logical-writer `route-mirror` precedent already documented for
  `route.attempt.instance`; new predicate declared with that writer), G9 (one
  new predicate, named in the spec delta).
- **(b) Carry the budget down via a rule at dispatch — REJECTED.** The dispatch
  rule (`04`) fires on the *coordinator* loop, which carries `agent.run.entity-id`
  (a pointer) but not `task.spec.budget`; `.value` there is `""` too. The
  projection rule (`03`) fires on the run but has no handle to the not-yet-born
  L_n. No rule-native carry-down exists.
- **(c) Upstream ask for cross-entity `.value` (follow `agent.run.entity-id`) —
  REJECTED as unnecessary.** Would make it fully rule-native, but is a larger
  framework change; the mirror already ships and is the established idiom. Revisit
  only if the mirror ever needs retiring.

### D2 — The `≥ B` boundary uses `length_gte` (semstreams #568), not a workaround

Retry is naturally `route.attempt.instance length_lt $…budget.value` (`< B`).
Escalate/park needs `≥ B`, which has no operator today. Options:

- **(a) Add `length_gte` upstream (#568) and use it — CHOSEN.** `length_gte` is a
  ~6-line, symmetric completion of the length-operator family (the numeric family
  already has `gte`/`lte`); it is a genuine engine gap, not a semdev workaround.
  The escalate/park routes read `route.attempt.instance length_gte
  $…budget.value`. One mirrored scalar, no rule split, no derived fact. This
  change is designed against `length_gte` and implemented once #568 lands.
- **(b) Precompute `B-1`, use `length_gt (B-1)` — REJECTED.** Needs a second
  mirrored scalar `route.task.budget-prev` (rules can't do arithmetic on a
  substituted value) — a derived fact that exists only to dodge the missing
  operator.
- **(c) Split escalate/park into two rules (`length_gt B` OR `length_eq B`) —
  REJECTED.** The engine's condition logic is flat (one `logic` per rule;
  `ConditionExpression` is a leaf with no nested group), so `unclean AND (>B OR
  =B) AND not-routed` cannot be inlined; escalate and review-park (both
  action-heavy: stamp `route.attempt.routed`, stamp `run.awaiting.human`, post to
  the user bus) would each duplicate into two rules. More surface, more pins, more
  drift risk than one clean operator.

Both rejected forms are cruft that exists solely because of the #568 gap; filing
the ask and designing against `length_gte` is the constitution-clean path (G2:
engine gap → upstream ask).

### D3 — One mirrored scalar (`route.task.budget` = B), not two

With `length_gte` (D2a), retry reads `length_lt B` and escalate reads
`length_gte B` — both against the single `route.task.budget`. No `B-1`. The
fail-closed partition holds for every B in `[1,5]`: retry `{0..B-1}`, escalate
`{B, B+1, …}` (an over-count still escalates, never retries).

### D4 — #529 reason-aware escalate is DEFERRED to a new upstream ask

The loop's termination reason never reaches the graph: `buildLoopFailureTriples`
(`processor/agentic-loop/graph_writer.go:864-898`) stamps
`agent.loop.outcome`/`iterations`/tokens but drops `event.Reason`; both
max-iterations exhaustion and a transient model error stamp
`agent.loop.outcome="failed"` (indistinguishable at the fact level;
`OutcomeTruncated` is context-window truncation, a different signal). A rule can
only read facts, not a Go `errors.Is` sentinel. Per G2 this is a framework ask
(have `buildLoopFailureTriples` stamp the terminal reason as a rule-readable
predicate, e.g. `agent.loop.terminal-reason`) — **parked, not forced in Go**
(synthesizing the reason product-side would breach the harness-stamps-the-terminal
boundary, G3). Until it lands, `06d`/`07c` stay count-only (the current
e2e-proven behavior). FILED as semstreams **#569**; the existing #529 regression
guard already tracks the sentinel.

### D5 — #528 per-spawn iteration budget is DEFERRED (needs its own field)

`task.spec.budget` is the *attempt* budget (# of dev loops), not the *iteration*
budget (turns per loop). Driving `loop_max_iterations` from it conflates two
semantically distinct knobs, and the first dispatch (`04`, on the coordinator
loop) hits the same cross-entity wall as D1(b). A clean #528 needs a new per-task
iteration-budget schema field (author + project + clamp) — its own change. The
uniform `agentic-loop max_iterations` stays (e2e-proven). The #528 regression
guard continues to track `TaskMessage.MaxIterations`.

### D6 — The exhaustion e2e journey authors an explicit budget

`TestBridgeProofBudgetExhaustionParks` currently relies on the implicit budget 3.
It SHALL author a task with an explicit budget (e.g. 2) and assert that exactly
that many attempts run before the park — proving the per-task budget is
load-bearing, not the old constant. A second journey/pin SHOULD cover a distinct
budget (e.g. author 1 → escalate on the first failed attempt) so the boundary is
exercised at more than one value.

### D7 — An absent budget is a loud station fault, never a silent default

`task.spec.budget` is authored and clamped `[1,5]` by the projection station on
every task, so its absence (or an unparseable value) on the run at mirror time is
a projection bug — an impossible state under the authored contract, not a normal
input. What the mirrors do then matters, because the substitution layer fails
*open*: `$entity.triple.route.task.budget.value` resolves silently to `""` on
absence (documented engine behavior, no log above Debug), and `length_lt ""` /
`length_gte ""` is a coerce error the evaluator swallows at Debug as no-match —
NEITHER retry nor escalate/park fires, and the run stalls with no park fact and
no operator signal (M0 has no liveness watchdog; the non-claims are explicit
about that). Both route-mirror sites (D1a) need a decided posture:

- **(a) Fail the mirror site's turn loudly — CHOSEN, per-site shape:**
  - **Floors station:** `RunFloors` returns an error when the run carries no
    parseable `task.spec.budget`; the station base retries, and a persistent
    fault stamps **nothing** on L_n. This inherits the documented R6 no-auto-park
    posture (the same failure surface as a resolve/read fault today — loud in
    logs, attempt does not advance, R8/group-8 owns stall-to-park). The budget
    fault surfaces in the MIRROR phase, after the run-side `floor.finding` stamp
    per the documented findings-first ordering: the current attempt's findings
    are genuine harness output, already durable, and MUST NOT be cleared (the
    resolve-fault clear exists to kill a stale PRIOR pass, not this) — only the
    chaining signal is withheld.
  - **submit_review executor:** the same absent-budget read returns an
    `errResult` back to the loop (the tool's documented loud-fail posture —
    "never a silent green"), stamping **nothing** on the review loop that pass.
- **(b) Default to the min clamp (1) — REJECTED.** Masks the projection bug and
  fabricates a contract value the projection never authored — a *derived* value
  on a mirror that must stay raw copies (G3-adjacent; the mirror's whole claim
  to G2-cleanliness is that it copies, never computes).
- **(c) A budget-missing park rule — REJECTED.** New rule surface for a state
  the atomicity invariant (below) makes structurally unreachable on both loops.
  Revisit only if R8 wants a general stall-to-park rule.

**ATOMICITY INVARIANT (pinned, owner-scoped):** NO route-mirror site stamps
`route.attempt.*` without `route.task.budget` in its one `ReplaceTriples` pass —
both sites: `checkfloors`→L_n and `submit_review`→review loop. "Attempt count
present, budget absent" therefore cannot exist on either firing entity, and
neither the 06c/06d nor the 07b/07c partition ever evaluates against a
silently-empty substitution.

## Risks / Trade-offs

- **[Depends on unreleased #568]** → This change is designed but not implemented
  until `length_gte` ships in a semstreams beta. No workaround is committed in the
  interim (deliberate — a workaround would be cruft to unwind). The semstreams
  team has indicated they will pick up #568 shortly.
- **[Absent budget fails open at substitution]** → `$…value` resolves `""`
  silently, and `length_lt ""` / `length_gte ""` is a swallowed coerce error →
  no-match — a stall with no park. Closed by D7: a loud per-site fault (station
  error / tool errResult) + the owner-scoped atomicity invariant (NO mirror site
  stamps `route.attempt.*` without `route.task.budget` — both `checkfloors`→L_n
  and `submit_review`→review loop, so neither route partition can see the
  half-mirror).
- **[The budget mirror could read stale]** → `route.task.budget` is stamped by the
  floors station on the same terminal that stamps `route.attempt.*`, so it is as
  fresh as the attempt count the route reads against it; the two are written in
  one `ReplaceTriples` pass (no cross-fact staleness window).
- **[G2 census mis-flags the mirror as a route-token]** → `route.task.budget` is a
  raw copy of an authored contract value (like `route.attempt.*`), not a derived
  decision token. The census pin allows the `route.*` mirrors and bans only
  single-fact *decision* tokens; add `route.task.budget` to the mirror allowlist,
  not the banned set.
- **[Clamp coupling]** → the budget the mirror copies is already clamped `[1,5]`
  by the projection station, so the routes inherit the structural bound; the
  mirror does not re-clamp (single responsibility, G5).

## Migration Plan

1. Land semstreams **#568** (`length_gte`/`length_lte`); bump the pin.
2. `internal/vocab`: declare `route.task.budget` (writer `route-mirror`).
3. `internal/tools/checkfloors`: read `task.spec.budget` off the run; stamp
   `route.task.budget` on L_n in `routeMirrorTriples` (same single pass; absent
   → error before any mirror write, findings NOT cleared — D7).
4. `internal/tools/submitreview`: the same run-side read; stamp
   `route.task.budget` on the REVIEW loop in the existing single
   `ReplaceTriples` pass alongside `route.review.verdict` /
   `route.attempt.instance` (absent → errResult, nothing stamped — D7).
5. Rules `06c`/`07b` (retry): `length_lt $entity.triple.route.task.budget.value`.
   Rules `06d`/`07c` (escalate/park): `length_gte
   $entity.triple.route.task.budget.value`.
6. Conformance: update the route-totality/partition pins to the variable boundary;
   add `route.task.budget` to the mirror allowlist (one logical writer, two
   sanctioned sites — the `route.attempt.instance` precedent); flip the #519
   `upstream_asks_test.go` guard note to "adopted."
7. e2e: D6 (explicit-budget exhaustion journey + a second budget value).
8. Full ladder + docker journeys (without `-race`, per #566); adversarial review;
   commit.

Rollback: revert the rule conditions to the literal `3` and drop the mirror — the
constant-budget behavior is unchanged and independently proven.

## Open Questions

- **#568 shape**: confirm the landed `length_gte` uses the same
  missing-predicate→empty-array semantics as the sibling length operators (so
  `length_gte 0` matches an absent predicate) — the acceptance criterion in the
  filed issue. Re-verify on the beta before implementing.
- ~~**#529 ask**: file now or later?~~ RESOLVED — filed as semstreams **#569**
  (loop-terminal-reason-as-fact), companion to #568; the change itself stays #519-only.
