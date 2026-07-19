## Why

The dev-loop routes treat every failed developer attempt identically: a transient
model/API failure (`agent.loop.terminal-reason=model_error`) burns a convergence-budget
slot exactly like a genuinely-red measurement, and when the budget exhausts the run parks
with one generic message regardless of *why* it failed. semstreams **#569** (landed
beta.153) now stamps the classified terminal reason as a rule-readable fact on the loop, so
the rail can finally distinguish transient infrastructure failures from genuine
non-convergence — the routing upgrade `adopt-per-task-routing-budgets` deferred as D4.

## What Changes

- **Transient-failure grace (behavioral):** a developer loop that fails with a TRANSIENT
  reason (`model_error`/`handler_error`) — infrastructure, not the model's inability to
  converge — is re-dispatched via a NEW transient-retry route WITHOUT consuming
  `route.task.budget`, bounded by a separate small transient cap so a persistently-dead
  endpoint still parks. GENUINE non-convergence (`max_iterations`, or a completed-but-red
  attempt) counts against the convergence budget exactly as today.
- **Reason-aware park message (honest evidence, G7):** when a developer attempt escalates
  to a park (budget exhausted, or the transient cap reached), the `run.awaiting.human`
  message carries the classified terminal reason so the human triaging the parked run gets
  an actionable diagnosis (needs more turns / flaky endpoint / genuine test failure).
- New fact `task.transient.instance` (a run-side transient-retry counter, writer
  `dev-dispatch-rule`, appended once per transient re-dispatch) mirrored onto the developer
  loop as `route.transient.instance` (writer `route-mirror`, like `route.attempt.instance`);
  and a new intermediate `route.attempt.transient` (the reason OR-collapse — model_error OR
  handler_error, writer `dev-route-rule`). All named in the spec delta.
- New developer-loop route rules: a transient-retry (`route.attempt.transient` true AND
  `route.transient.instance length_lt CAP`) and a transient-exhausted park (`length_gte
  CAP`, reusing #568). The convergence retry/escalate routes gain a
  `route.attempt.transient length_eq 0` exclusion guard (so a transient terminal cannot fire
  them — clean mutual exclusion) and the escalate/park routes gain the reason-aware message.
  The convergence partition (`length_lt B` / `length_gte B`) is otherwise unchanged.

Scope: the DEVELOPER loop (06-series). Quinn's review loop transient failures stay covered
by the existing fail-closed no-verdict park (07d); review-loop grace is a noted follow-up.

## Impact

- Affected specs: `dev-from-task` (the routing capability).
- Affected code: `internal/tools/checkfloors` (mirror the new transient counter), the
  `dev-from-task` route rule pack (new/edited rules), `internal/vocab` (two new predicates),
  conformance pins, and the e2e journeys (a `model_error` grace journey + a reason-aware
  park assertion).
- No lifecycle transition, no LLM-supplied outcome (G2/G3): the reason is a
  harness/framework-classified fact; routing stays rule-native.
- Deferred (documented, not in this change): a per-task transient cap field (kept a
  constant here, like the pre-#519 budget), and review-loop (07-series) transient grace.
