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
   So a condition on a sometimes-absent field MUST be `eq … required:false` (the
   established `route.attempt.passed eq "true" required:false` pattern) — and `ne` on a
   sometimes-absent field is always wrong (false on absent). This is why D2's exclusion is
   only sound because the mirror makes the flag ALWAYS present: `eq "false"` over a
   guaranteed-present fact, not an absence test.
3. **`agent.loop.terminal-reason` is stamped on the LOOP entity the floors routes fire on**
   (graph_writer.go:897), so the developer-loop routes read it directly — no mirror needed
   (unlike the run-side budget). It is single-valued, so a later `.value` substitution
   arity-disambiguates.
4. Flat rule logic (one `logic` per rule; no nested `AND (C OR D)`) — so a reason set
   cannot be inlined as `(eq model_error OR eq handler_error) AND <guards>`; it must be
   collapsed into a single readable fact first.
5. **Each rule action is its OWN graph mutation — one KV revision per `add_triple`**
   (`processor/rule/triple_mutator.go` `AddTriple`: one NATS request per triple; no batching
   across actions or across same-pass rules). Two rules firing in the SAME evaluation pass
   stamp their triples as SEPARATE entity revisions in unspecified order.
6. **Rules evaluate per debounce-flush against freshly-FETCHED entity state**
   (`entity_watcher.go`: live revisions enter a coalescing set; after the debounce window
   the batch path fetches CURRENT state and evaluates). Whether two adjacent revisions are
   seen in one pass or two is a TIMING ACCIDENT of the debounce window vs the write gap.
   **Corollary (the falsifier this change's first cut hit live): same-pass rule FIRING does
   not give same-revision STAMPS, so a rule excluding on a SIBLING RULE's stamped triple is
   a race, not an invariant.** The double-dispatch it produces was caught by
   `TestBridgeProofTransientGraceRetries` on docker (~50% reproduction under `-race`):
   06b's `unclean` revision flushed and evaluated before the sibling collapse's `transient`
   revision landed → 06c's absence-exclusion read absent → 06c fired alongside the
   transient retry. Route mutual exclusion is sound ONLY as a condition PARTITION over one
   atomic snapshot — and the one atomic-snapshot primitive the rail owns is the
   `check_floors` mirror's single `ReplaceTriples` (the D7-of-#568 atomicity invariant).

## Goals / Non-Goals

**Goals:** a transient loop failure does not consume the convergence budget (bounded grace);
genuine non-convergence routes exactly as today; the park toward the human names the
terminal reason; stay rule-native (G2) with harness/engine-classified facts only (G3).

**Non-Goals:** review-loop (07-series) transient grace (07d's fail-closed no-verdict park
already covers a dead review loop; D6); a per-task *transient* cap field (a constant here,
D5); any change to the convergence `length_lt B` / `length_gte B` partition.

## Decisions

### D1 — Classify the reason IN the atomic floors mirror: `route.attempt.transient` = "true"/"false", always stamped

**(REVISED after the live race — the first cut's rule-side OR-collapse was implemented,
caught double-dispatching on docker, and replaced by this.)** A transient terminal is
`terminal-reason ∈ {model_error, handler_error}`. `check_floors` reads the LOOP's
harness-stamped `agent.loop.terminal-reason` (fact 3 — on L_n, atomic with the outcome that
triggered the floors dispatch, so always readable by mirror time) and stamps
`route.attempt.transient` **"true"/"false" — ALWAYS present — in the SAME `ReplaceTriples`
as `route.attempt.passed`** (writer `route-mirror`, G5).

Why not a rule (G1's escape clause, invoked with proof): facts 5+6. A rule-stamped collapse
lands in its OWN KV revision; any convergence-route exclusion reading it races that
revision against 06b's `unclean` revision and the debounce flush — the double-dispatch
`TestBridgeProofTransientGraceRetries` caught live. Flat logic (fact 4) rules out inlining
the reason-OR into the routes. The one place the rail can make "unclean's inputs" and "the
transient classification" a single atomic snapshot is the mirror's single `ReplaceTriples`
(fact 6's corollary). The classification is a fixed normalization of an engine-classified
fact — no LLM input (G3), no lifecycle transition, the routes still decide (G2) — the exact
posture of the existing fail-closed `route.attempt.passed="false"`-when-unmeasured copy.

The transient ROUTES (not the flag) gate on `route.attempt.unclean eq "true"` — the shared
not-clean trigger, NOT `passed eq "false"`. Adversarial review caught the passed-gate
stranding a cell: the mirror stamps passed/rejected/transient INDEPENDENTLY, so
`passed=true ∧ rejected=true ∧ transient=true` is reachable (measured green, a structural
floor rejected the artifact, the loop's final model call then died transiently) — with a
passed gate NO route fires there (06a needs rejected=false; 06c/06d are transient-excluded)
and the run wedges silently, forever (no backstop by design, B7). `unclean` covers exactly
the not-advance region, restoring `06c ∪ 06d ∪ 06f ∪ 06g ⊇ unclean`; the 8-cell
`TestFloorsRouteCellTotalityCensus` pins exactly-one-route ownership of every
`passed × rejected × transient × count-regime` cell (red-verified against the passed-gate).
Race-safe: unclean is only the shared TRIGGER — landing late it delays the transient route
a pass; it cannot split the partition, which lives on the atomic mirror flag. A
measured-green rejected=false loop still advances via 06a (unclean never stamped), and the
flag stays a pure reason classification; genuine terminals (`max_iterations`, absent)
classify "false".

### D2 — Convergence routes exclude via `eq "false"` — a condition PARTITION, not an absence guard

`06c-route-retry` and `06d-route-escalate` gain `route.attempt.transient eq "false"
(required:false)`; the transient routes fire on `eq "true"`. Because the flag is ALWAYS
stamped atomically with `route.attempt.passed` (D1), every evaluation pass in which any
floors-route condition is satisfiable sees exactly one of "true"/"false" — the convergence
and transient routes are mutually exclusive by CONDITION on one snapshot (the same
soundness shape as the `length_lt B`/`length_gte B` budget partition), never by racing a
sibling rule's stamp. An absence guard (`length_eq 0`, the first cut) is exactly the racy
shape: TRUE on the pass where the sibling's stamp hasn't landed yet → double-dispatch.
(`ne "true"` on a sometimes-absent field would also be wrong by fact 2 — but the flag is
never absent where it matters, which is what makes `eq` sound.)

### D3 — Bounded transient grace via a mirrored counter + literal cap (reuses #568)

The transient-retry route `06f-route-transient-retry` fires on `route.attempt.transient eq
"true" (required:false)` AND `route.attempt.unclean eq "true"` (D1 totality) AND
`route.transient.instance length_lt <CAP>` AND
`route.attempt.routed length_eq 0`, and re-dispatches a fresh developer loop. At spawn it
appends `task.transient.instance` on the RUN (subject-override, R3-style) — NOT
`task.attempt.instance` — so the convergence budget is untouched. The transient-exhausted
park `06g-route-transient-park` fires on `route.attempt.transient eq "true"` AND
`route.attempt.unclean eq "true"` AND `route.transient.instance length_gte <CAP>` and parks. `length_lt`/`length_gte` against the
literal CAP partition the transient count with no gap (retry {0..CAP-1}, park {CAP,…}) —
the same shape as the budget partition, and by fact 1 an absent `route.transient.instance`
is 0 (`length_lt CAP` true) so the FIRST transient failure retries. Because CAP is a LITERAL
(not a substituted value), there is NO fail-open D7 wedge — an absent counter is a valid 0,
not an empty-substitution coerce error.

`check_floors` mirrors `route.transient.instance` onto the developer loop from the run's
`task.transient.instance` (the exact `route.attempt.instance` shape), so the transient routes
count it on the firing entity. `task.transient.instance` writer `dev-dispatch-rule` (appended
by 06f, the transient spawner); `route.transient.instance` writer `route-mirror`.

**Counting model:** `task.attempt.instance` counts the original dispatch (04) plus each
GENUINE convergence retry (06c/07b). A transient re-dispatch (06f) is free
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

`06d` (genuine escalate) and `06g` (transient park) substitute
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

`06f` joins `04`/`06c`/`07b` as a sanctioned `role=developer` spawner; the serialization pin
(`TestOnlySanctionedDeveloperSpawners`) and the config-lint gain it. The one-in-flight
invariant holds: `06f` fires on a TERMINATED loop's terminal and is self-extinguishing
(`route.attempt.routed` before the publish, re-armed by the fresh loop), so exactly one
developer loop is ever in flight. Its prompt tells Amelia the prior attempt hit a transient
infrastructure error and to simply proceed with the task (nothing to "fix" from the blip).

## Risks / Trade-offs

- **[Interaction with the just-shipped budget partition]** → the transient exclusion
  (`route.attempt.transient length_eq 0`, D2) is additive to 06c/06d; the `length_lt B` /
  `length_gte B` convergence partition is unchanged and its pins stay green. The transient
  routes are a disjoint partition on `route.attempt.transient` (present vs absent).
- **[A persistently-dead endpoint]** → bounded by CAP (D3): after CAP transient retries the
  run parks (06g), never loops unbounded (the bounded-loop invariant, extended to the
  transient track).
- **[`handler_error` is the framework's GENERIC fallback]** → any non-max_iterations
  handler fault classifies `handler_error` (`component.go:1156`), so a deterministic
  in-tree tool bug reads as "transient": it gets CAP free re-dispatches, then parks with
  prose suggesting a re-run may help. Bounded and human-terminated, so the posture is safe;
  the park message text hedges accordingly. Also deliberate: `task.transient.instance` is
  RUN-scoped and never reset — the grace is a per-run TOTAL (two blips anywhere in a long
  multi-attempt run consume it), not per-attempt; a persistent flake parks rather than
  slowly bleeding budget.
- **[Reason misclassification upstream]** → the classification is the engine's
  (`failureReasonForHandlerError` / the fail paths), not semdev's; a wrong class at worst
  routes a genuine failure as transient (bounded grace, then park) or vice-versa (counts a
  blip as a convergence attempt) — both fail safe (still bounded, still park). G3 holds: no
  LLM-supplied outcome.
- **[Multi-pass latency]** → NONE added: the mirror-side classification (D1) stamps the flag
  in the mirror pass itself — the transient routes fire directly on the mirror revision,
  one pass EARLIER than the first cut's rule-collapse chain.

## Migration Plan

1. `internal/vocab`: declare `task.transient.instance` (writer `dev-dispatch-rule`),
   `route.transient.instance` (writer `route-mirror`), `route.attempt.transient` (writer
   `route-mirror` — the D1 mirror classification).
2. `internal/tools/checkfloors`: read `task.transient.instance` off the run and mirror
   `route.transient.instance` onto L_n (absent → empty set → count 0, no fail-open, so NO
   D7 loud-fault needed here); read L_n's `agent.loop.terminal-reason`, classify against
   the fixed transient set, and stamp `route.attempt.transient` "true"/"false" in the SAME
   `ReplaceTriples` as `route.attempt.passed` (the atomicity pin
   `TestCheckFloorsMirrorTransientFlagAtomicWithPassed` — THE race fix).
3. Rules: add `06f` transient-retry + `06g` transient-park (`eq "true"` + `passed eq
   "false"` + the count partition); add `route.attempt.transient eq "false"` to
   `06c`/`06d`; add the reason-aware message to `06d`/`06g`. NO classify rules — a
   rule-stamped collapse is the racy shape (facts 5+6), pinned against by the
   no-rule-stamps-the-flag census in `TestTransientRouteTotalityAndSelfExtinguish`.
4. Conformance: transient-partition totality pin; `06f` added to the sanctioned-spawner +
   config-lint + G5 mirror-writer censuses; docs vocab table (G10).
5. e2e: a `model_error` grace journey (transient retry → then succeed, budget untouched) +
   a reason-aware park assertion; full ladder + docker journeys green `-race` — the grace
   journey repeated (the race reproduced ~50% pre-fix, so one green is not evidence).
6. Adversarial review (`semstreams-reviewer` + `go-reviewer`); commit; push.

Rollback: drop the transient rules + the exclusion guard + the mirror; the convergence
behavior is unchanged and independently proven.

## Open Questions

- **CAP value**: 2 proposed. Confirm before implementing (a per-task field is D4-deferred).
- ~~**Transient-collapse gating**~~ RESOLVED twice, each by live falsification: (1) the
  first cut's rule-side collapse + `length_eq 0` exclusion double-dispatched on docker (the
  pass-timing argument was wrong — facts 5+6) → classification moved into the atomic mirror
  (D1 revised) with `eq "false"`/`eq "true"` partition (D2 revised); (2) the routes' initial
  `passed eq "false"` gate stranded the `passed∧rejected∧transient` cell (adversarial
  review) → gates moved to `unclean eq "true"`, pinned by the 8-cell census. CAP resolved
  to **2** (`length_lt 2` retry {0,1} / `length_gte 2` park). e2e evidence: `mockllm`
  HTTP-500 error fixtures drive REAL `model_error` terminals — the grace journey (recovers,
  budget untouched) and the cap-park journey (three deaths → 06g parks naming model_error).
