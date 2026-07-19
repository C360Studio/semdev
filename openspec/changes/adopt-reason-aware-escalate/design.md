## Context

`adopt-per-task-routing-budgets` (D4) deferred reason-aware routing: the loop's
termination reason never reached the graph, so both max-iterations exhaustion and a
transient model error stamped `agent.loop.outcome="failed"`, indistinguishable at the fact
level. semstreams **#569** (landed beta.153) closes that — `buildLoopFailureTriples` now
stamps `agvocab.LoopTerminalReason` (`agent.loop.terminal-reason`) from
`LoopFailedEvent.Reason` on the loop entity, only on failure, single-valued. The classified
values (verified in `processor/agentic-loop/{component,handlers}.go`) are:

- `max_iterations` — the loop hit its iteration cap (the #529 typed sentinel
  `ErrMaxIterationsReached`, matched via `errors.Is`). GENUINE non-convergence.
- `model_error` — a transient model/transport failure. TRANSIENT infrastructure.
- `handler_error` — a generic handler failure. TRANSIENT infrastructure.
- (absent) — the loop COMPLETED (no failure event); a red/absent measurement is GENUINE
  non-convergence, routed by the existing floors mirror.

Today every failed developer terminal collapses to `route.attempt.unclean` and counts
identically against `route.task.budget`: a flaky endpoint burns a convergence slot exactly
like a genuinely-hard task, and the exhaustion park gives one generic message.

### Engine facts verified against the beta.153 module cache (load-bearing)

1. **Array operators on an absent predicate evaluate over an EMPTY array**
   (`processor/rule/expression/evaluator.go:244-255`): a predicate with zero matching
   triples yields `[]interface{}{}`, so `length_eq 0` is TRUE on an absent field and
   `length_lt N` is TRUE at count 0. This is the whole basis of the transient exclusion and
   the transient cap below.
2. **A non-array `eq`/`ne` on an absent field with `required:false` returns FALSE, no error**
   (`evaluator.go:277-289`); with `required:true` it ERRORS (swallowed at Debug → no match).
   So a reason condition on the sometimes-absent `agent.loop.terminal-reason` MUST be
   `eq … required:false` (the established `route.attempt.passed eq "true" required:false`
   pattern), and "transient not stamped" MUST be expressed as `length_eq 0`, never `ne`.
3. **`agent.loop.terminal-reason` is stamped on the LOOP entity the floors routes fire on**
   (graph_writer.go:897), so the developer-loop routes read it directly — no mirror needed
   (unlike the run-side budget). It is single-valued, so a later `.value` substitution
   arity-disambiguates.
4. Flat rule logic (one `logic` per rule; no nested `AND (C OR D)`) — so a reason set is
   OR-collapsed into an intermediate fact, the established `route.attempt.unclean` pattern
   (two guarded pure-AND rules).

## Goals / Non-Goals

**Goals:** a transient loop failure does not consume the convergence budget (bounded grace);
genuine non-convergence routes exactly as today; the park toward the human names the
terminal reason; stay rule-native (G2) with harness/engine-classified facts only (G3).

**Non-Goals:** review-loop (07-series) transient grace (07d's fail-closed no-verdict park
already covers a dead review loop; D6); a per-task *transient* cap field (a constant here,
D5); any change to the convergence `length_lt B` / `length_gte B` partition.

## Decisions

### D1 — Classify the reason via an OR-collapse intermediate `route.attempt.transient`

A transient terminal is `terminal-reason ∈ {model_error, handler_error}`. Flat logic (fact
4) can't inline the OR with the route conditions, so two guarded pure-AND rules
(`06f-reason-transient-model`, `06f-reason-transient-handler`) each stamp
`route.attempt.transient="true"` on the developer loop, gated on `agent.loop.role eq
"developer"` (scope) + `agent.loop.terminal-reason eq "<reason>" (required:false)` +
`route.attempt.transient length_eq 0` (self-extinguish). This mirrors the `06b` not-clean
OR-collapse exactly. GENUINE terminals (`max_iterations`, or absent) never match, so
`route.attempt.transient` stays absent for them. Writer `dev-route-rule` (the existing
rule-owned intermediate writer; no Source drift).

### D2 — Exclude transient from the convergence routes with `length_eq 0`, not `ne`

`06c-route-retry` and `06d-route-escalate` gain one condition:
`route.attempt.transient length_eq 0`. By fact 1 this is TRUE when transient is absent
(genuine failure → the convergence routes fire as today) and FALSE when transient is stamped
(the transient routes own it). `ne "true"` would be WRONG (fact 2: `ne` on the absent
genuine case returns false → the convergence routes would never fire). This gives clean
mutual exclusion between the convergence routes and the transient routes with no route ever
double-firing (preserving the one-developer-in-flight serialization invariant).

### D3 — Bounded transient grace via a mirrored counter + literal cap (reuses #568)

The transient-retry route `06g-route-transient-retry` fires on `route.attempt.transient eq
"true" (required:false)` AND `route.transient.instance length_lt <CAP>` AND
`route.attempt.routed length_eq 0`, and re-dispatches a fresh developer loop. At spawn it
appends `task.transient.instance` on the RUN (subject-override, R3-style) — NOT
`task.attempt.instance` — so the convergence budget is untouched. The transient-exhausted
park `06h-route-transient-park` fires on `route.attempt.transient eq "true"` AND
`route.transient.instance length_gte <CAP>` and parks. `length_lt`/`length_gte` against the
literal CAP partition the transient count with no gap (retry {0..CAP-1}, park {CAP,…}) —
the same shape as the budget partition, and by fact 1 an absent `route.transient.instance`
is 0 (`length_lt CAP` true) so the FIRST transient failure retries. Because CAP is a LITERAL
(not a substituted value), there is NO fail-open D7 wedge — an absent counter is a valid 0,
not an empty-substitution coerce error.

`check_floors` mirrors `route.transient.instance` onto the developer loop from the run's
`task.transient.instance` (the exact `route.attempt.instance` shape), so the transient routes
count it on the firing entity. `task.transient.instance` writer `dev-dispatch-rule` (appended
by 06g, the transient spawner); `route.transient.instance` writer `route-mirror`.

**Counting model:** `task.attempt.instance` counts the original dispatch (04) plus each
GENUINE convergence retry (06c/07b). A transient re-dispatch (06g) is free
(`task.transient.instance`), so a run of transient blips never advances the convergence
budget. The originally-dispatched attempt that then failed transiently was still a real
dispatch and stays counted — the budget bounds genuine convergence attempts, and transient
grace bounds the blips within them.

### D4 — The transient cap is a declared CONSTANT at M0.5 (a per-task field is deferred)

Like the pre-#519 attempt budget, `<CAP>` is a single declared literal in the two transient
rules (proposed: **2** — a small grace that absorbs an isolated blip without masking a dead
endpoint). A per-task *transient* budget field (author + project + clamp + mirror, the #519
treatment) is a separate future change; recorded here, not built.

### D5 — Reason-aware park message via `.value` substitution (one rule each)

`06d` (genuine escalate) and `06h` (transient park) substitute
`$entity.triple.agent.loop.terminal-reason.value` into the `run.awaiting.human` object, so
one rule carries the reason (empty for a completed-but-red attempt, which reads naturally as
"no engine failure reason — the last attempt measured red"). Substitution is firing-entity-
only (#519) and the reason is on the firing developer loop (fact 3), so it resolves with no
mirror. Two rules per park (one per reason) were rejected as duplicated action surface.

### D6 — Scope: the DEVELOPER loop only

Transient grace and the reason-aware message are added to the 06-series (developer) routes.
Quinn's review loop (07-series) failing transiently is already caught by `07d`'s fail-closed
no-verdict park (a review loop with no verdict parks) — safe, if not reason-aware. Extending
grace to the review loop is a noted follow-up, not this change.

### D7 — The transient route is a fourth sanctioned developer spawner

`06g` joins `04`/`06c`/`07b` as a sanctioned `role=developer` spawner; the serialization pin
(`TestOnlySanctionedDeveloperSpawners`) and the config-lint gain it. The one-in-flight
invariant holds: `06g` fires on a TERMINATED loop's terminal and is self-extinguishing
(`route.attempt.routed` before the publish, re-armed by the fresh loop), so exactly one
developer loop is ever in flight. Its prompt tells Amelia the prior attempt hit a transient
infrastructure error and to simply proceed with the task (nothing to "fix" from the blip).

## Risks / Trade-offs

- **[Interaction with the just-shipped budget partition]** → the transient exclusion
  (`route.attempt.transient length_eq 0`, D2) is additive to 06c/06d; the `length_lt B` /
  `length_gte B` convergence partition is unchanged and its pins stay green. The transient
  routes are a disjoint partition on `route.attempt.transient` (present vs absent).
- **[A persistently-dead endpoint]** → bounded by CAP (D3): after CAP transient retries the
  run parks (06h), never loops unbounded (the bounded-loop invariant, extended to the
  transient track).
- **[Reason misclassification upstream]** → the classification is the engine's
  (`failureReasonForHandlerError` / the fail paths), not semdev's; a wrong class at worst
  routes a genuine failure as transient (bounded grace, then park) or vice-versa (counts a
  blip as a convergence attempt) — both fail safe (still bounded, still park). G3 holds: no
  LLM-supplied outcome.
- **[Multi-pass latency]** → the reason-collapse adds one rule-evaluation pass
  (`floors mirror → 06b unclean / 06f transient-collapse → route`), the same chaining the
  rail already uses; functionally inert, mock-journey-proven.

## Migration Plan

1. `internal/vocab`: declare `task.transient.instance` (writer `dev-dispatch-rule`),
   `route.transient.instance` (writer `route-mirror`), `route.attempt.transient` (writer
   `dev-route-rule`).
2. `internal/tools/checkfloors`: read `task.transient.instance` off the run and mirror
   `route.transient.instance` onto L_n in the same `ReplaceTriples` pass as the other route
   mirrors (absent → empty set → count 0, no fail-open, so NO D7 loud-fault needed here).
3. Rules: add the two reason-collapse rules + `06g` transient-retry + `06h` transient-park;
   add `route.attempt.transient length_eq 0` to `06c`/`06d`; add the reason-aware message to
   `06d`/`06h`.
4. Conformance: transient-partition totality pin; `06g` added to the sanctioned-spawner +
   config-lint + G5 mirror-writer censuses; docs vocab table (G10).
5. e2e: a `model_error` grace journey (transient retry → then succeed, budget untouched) +
   a reason-aware park assertion; full ladder + docker journeys green `-race`.
6. Adversarial review (`semstreams-reviewer` + `go-reviewer`); commit; push.

Rollback: drop the transient rules + the exclusion guard + the mirror; the convergence
behavior is unchanged and independently proven.

## Open Questions

- **CAP value**: 2 proposed. Confirm before implementing (a per-task field is D4-deferred).
- **Transient-collapse gating on `route.attempt.unclean`**: a transient reason implies a
  failed loop (no green measure ⇒ unclean), so gating the collapse on the reason alone is
  sufficient; gating additionally on `role=developer` scopes it. Re-confirm no path stamps a
  transient reason on a loop that also measured green (would need the extra unclean gate).
