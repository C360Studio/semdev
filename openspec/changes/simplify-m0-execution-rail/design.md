## Context

The as-built rail (head `2523fa2`) proved the mock-LLM bridge arc connects,
but the 2026-07-13 Codex review — every finding verified accurate — showed it
regrew semspec's shape: 15 model-publishing rules chain forced single-turn
loops through relay markers; `check_gate`/`check_coherence` derive routing
tokens in product Go; reviewer rejection routes to verify→park instead of
back into development; cold verify copies the mutable warm tree; the patcher
ignores `target_files`; a restart wedges runs; and the developer/reviewer
turns are context-starved.

The assumptions audit (2026-07-13, verified against semstreams beta.146
source) established the engine facts this design relies on:

- Loops are **genuinely multi-turn**: tool results append as tool-role
  messages and the model continues with full history. The single-turn
  starvation was self-inflicted (`tool_choice: function` applies to every
  turn; every semdev tool returns `StopLoop: true`).
- `publish_agent` supports per-spawn `tools` allowlists, `tool_choice`, and
  prompt templating of entity triples. It does NOT support per-spawn
  `max_iterations` (component-level only — semstreams #528 filed) or
  populating `TaskMessage.Context`.
- Conditions support `eq/ne/gt/lt/...` vs literals, `length_eq/gt/lt`
  counts over one predicate, and `length_eq 0` as an absence guard.
  Graph-ingest dedupes relationship appends on exact (subject, predicate,
  object), so `length_*` over a family whose objects are distinct loop
  instances IS a distinct count. Scalar field-to-field compare needs
  semstreams #519 (`.value` suffix — fix drafted upstream, unmerged).
- Loop terminals are rule-visible (`agent.loop.outcome` success/failed), but
  max-iterations exhaustion publishes a non-uniform failure reason
  (semstreams #529) — routes must key on outcome, not reason.
- Rules cannot invoke tools directly, but the `publish` action can trigger a
  registered **component** (the gated-DAG pattern) — deterministic work needs
  zero model turns.
- The checkout is a plain file copy — no git repo exists anywhere in the run
  path; an immutable snapshot must be added, not exposed.

Preserved kernel (not redesigned here): graph/OpenSpec foundation,
harness-owned evidence, `internal/floors`, `internal/coldproof` + `cleanroom`,
the brownfield library, secrets governance, the `go-health-class` fixture.

## Goals / Non-Goals

**Goals:**

- Replace the station-chain rail with: one bounded multi-turn developer loop
  per task, rule-native routing over harness-stamped facts, deterministic
  stations as publish-triggered components, and model reasoning confined to
  authoring (Sarah), development (Amelia), and adversarial review (Quinn).
- Close the four confirmed P1s: immutable-snapshot verify, `target_files`
  enforcement, restart-safe provisioning, rejection re-entry.
- Restate M0 honestly: single-task, journey = bridge proof, real forge
  evidence required for completion claims.

**Non-Goals:**

- No multi-task walker at M0 (the gated-DAG component is the documented M1
  path; requirement here is only that nothing hard-codes beyond one declared
  binding point).
- No re-litigation of the preserved kernel (floors logic, cold-proof
  mechanics, secrets governance, fixture realism).
- No waiting on upstream asks: #519/#528/#529 get interims (constant budget,
  uniform component cap, route-on-outcome) and tripwires, not parks — the
  rail is functional without them and upgrades mechanically when they land.
- No brownfield ingestion of in-flight `openspec/changes/` (living specs
  only, per the library's sole-writer invariant).

## Decisions

### R1. Routing is rules over harness-stamped facts — the route-token layer is deleted

`check_gate`, `check_coherence`, `dev.gate_decision`, `dev.coherence_decided`,
and every relay marker (`dev.measured`, `dev.measure_done`,
`dev.floors_dispatched`, `dev.floors_done`, `dev.gate_dispatched`,
`dev.routed`, `dev.review_dispatched`, `dev.verify_dispatched`,
`dev.coherence_dispatched`, `dev.pr_routed`) are removed. Routes become rule
conditions composed from three verified-expressible forms:

- `eq` on latest-wins facts: `measurement.result.0.passed`,
  `floor.finding.0.rejected`, `review.verdict`, `verify.result` — the strictly
  serial chain guarantees "latest = this attempt" (the prior architect ruling
  that dropped the token handshake still holds).
- `length_*` over `task.attempt.0` (objects = developer-loop instance IDs,
  distinct by construction, storage-deduped): `length_lt <budget>` → another
  attempt is allowed; `length_eq <budget>` → escalate. Counts rise exactly +1
  per dispatch, so the exact-match escalation cannot be skipped (pinned).
- `length_eq 0` absence guards, evaluated when a loop terminal fires — e.g. a
  developer terminal with `measurement.result.0.passed` absent is a failed
  attempt (the model never produced a measured state), routed like any red.

*Alternatives rejected:* keeping a "deliberately minimal" Go decider
(trajectory risk — this is exactly how semspec grew); the token handshake
(unbuildable pre-#519, unnecessary given serial chaining).

*Fail-closed posture (G2/G7):* every route family is total — for each trigger
either an advance rule, a retry rule, or an escalate/park rule fires; there is
no default-advance. The G2 pin is widened to catch Go-derived routing tokens,
not just direct lifecycle-manager calls.

### R2. One bounded multi-turn developer loop, feedback in-loop

Amelia is spawned once per attempt with:

- `tools` allowlist: `read_workspace` (new, R7), `apply_patch`,
  `measure_task`, `ask_human`. Nothing else.
- `tool_choice: auto` — she reads, patches, measures, reads the harness
  feedback (stdout/stderr + `passed` are already in `measure_task`'s result
  content), and iterates *inside the loop*.
- `measure_task` and `apply_patch` stop returning `StopLoop: true`. The loop
  ends when she stops calling tools (success) or hits the iteration cap
  (failed).

Routing never trusts her stopping decision: the terminal rule reads the
harness-stamped `measurement.result.0.passed` regardless of loop outcome
(G3). `outcome=success` with `passed` false or absent is a failed attempt.

*Budget:* the in-loop cap is the dev-loop component's `max_iterations`
(uniform at M0 — set to 8: headroom for read→patch→measure cycles). Per-task
in-loop budgets arrive with semstreams #528. Exhaustion routes on
`outcome=failed` only (never the reason string — #529).

### R3. Attempt accounting: counted at dispatch, constant budget at M0

The dispatching rule appends `task.attempt.0 = $instance` (subject-override
`add_triple` onto the run) *at spawn time*, not at terminal — so a crashed or
wedged loop still consumed an attempt (honest accounting, restart-safe).
Budget at M0 is the constant **3** in the route rules' literals.
`task.spec.0.budget` stays in the projected contract (clamped [1,5]) but is
advisory until #519's `.value` form lands, at which point the literals become
`$entity.triple.task.spec.0.budget.value` — a mechanical upgrade, tracked as a
tripwire test that flips when the semstreams version carries `.value`.

*Alternative rejected:* enforcing `task.spec.budget` via a Go counter — that
is `check_gate` again.

### R4. Reviewer rejection re-enters development (D16 honored)

`submit_review` stamps the per-task `review.verdict.<i> ∈ {approved,
changes_requested}` (the predicate harness-measurement already specifies) and
`review.findings.<i>` (prose triple) on the run — model-supplied *judgment*
(allowed; G3 restricts measurement outcomes, not review verdicts). Routes:

- `approved` → publish to the verify component (R6). Only approved work is
  cold-verified.
- `changes_requested` + `task.attempt.0 length_lt 3` → dispatch a fresh
  Amelia attempt whose prompt templates in `review.findings` and the prior
  measurement state.
- `changes_requested` + `length_eq 3` → park toward the human with the
  findings.

Review cycles and measurement retries share the one `task.attempt.0` budget —
an attempt is an attempt regardless of what sent it back.

### R5. Immutable snapshot: git-backed checkout, commit per attempt, verify-at-SHA

- `Materialize` runs `git init` + an initial commit of the pristine source
  (identity: a fixed semdev harness author, repo-local config).
- After each successful `apply_patch`, the harness commits the working tree
  (`git add -A`, message `attempt <loop-instance>`) and stamps
  `attempt.commit = <sha>` (latest-wins property) on the run. The commit
  happens at apply time, so what `measure_task` measures is exactly the
  committed tree.
- A post-measure dirty-tree check (`git status --porcelain` non-empty after
  the measured run) stamps a floor finding — test-time mutation of the
  artifact is evidence tampering, fail-closed (G7).
- Cold verify **clones at `attempt.commit`** into the fresh clean room —
  `CloneForVerify`'s mutable-tree copy is deleted. What Quinn reviews is the
  cumulative diff `git diff <base>..<attempt.commit>`, i.e. provably the same
  bytes verify proves.

*Alternative rejected:* applying the cumulative diff to pristine source —
weaker (diff application is itself a mutable step) and duplicates what git
already guarantees once the checkout is a repo.

### R6. Deterministic stations are publish-triggered components — zero model turns

Floors, cold verify, forge delivery, task projection, change validation, and
sandbox provisioning stop being forced coordinator turns. Each becomes a
registered component (framework-alignment note + registry entry per G1)
subscribing to a rule-`publish`ed subject, doing its deterministic work, and
stamping its facts as today's tool executors do (same single writers, G5).
`measure_task` alone remains a model-callable tool (it is Amelia's feedback
channel; schema still takes no outcome parameters, G3).

Under a real LLM every deleted relay turn is a paid model call saved; under
the mock it removes 6+ of the 15 model-publishing rules outright. Model tools
that remain: `create_change` (+ `issue_intake`/`dev_from_task` decides,
Sarah), `read_workspace`/`apply_patch`/`measure_task` (Amelia),
`read_workspace`/`read_diff`/`submit_review` (Quinn), `ask_human` (all).

*Alternative rejected:* keeping forced-turn tool relays with a mock-priced
model — dishonest cost accounting and context-starved by construction.

### R7. Complete context: templated triples in, read tools in-loop

- Prompts template the full task contract from triples: goal, `target_files`,
  `test_command`, assumptions, non-goals — plus, on re-entry, prior
  measurement state and `review.findings`. Entity-ID-only prompts are gone.
- File contents are not triples: Amelia and Quinn get `read_workspace`
  (path-guarded, checkout-rooted, read-only — the read sibling of the
  patcher's `safeJoin` containment) and Quinn additionally `read_diff`
  (returns `git diff <base>..<attempt.commit>`). Both are deterministic
  harness tools; their G1 justification is that no existing primitive can put
  checkout bytes into a loop.
- Rules cannot populate `TaskMessage.Context`, so prompt templating + read
  tools is the whole channel (audit-verified); tool results are capped at the
  component's 32KB `ToolResultMaxBytes` — `read_workspace` paginates.

### R8. Restart-safe, idempotent provisioning and effects

**As-built decision: M0 does NOT reconstruct. The correct posture is a rule-native
fail-closed park — but that park is BLOCKED on an upstream framework gap (below),
so at M0 a restarted in-flight run WEDGES: a known, documented gap, not a working
capability.** The original bullets below proposed reconstructing the
checkout/container from durable facts. Investigation against semstreams beta.146
showed reconstruction is both impossible-to-do-honestly and unnecessary at M0 — and
that the honest park replacement is itself not yet expressible in the engine:

- A run's live state is process-local in three places — the agentic loop's
  conversation state, the on-disk checkout (a temp dir holding the applied
  attempt's git objects), and the warm container. None survive a restart. The
  durable facts (`measurement.result`, `floor.finding`, `attempt.commit`,
  `verify.result`) are *pointers* into that lost state. The applied diff is not a
  durable fact, so M0 reconstruction could at most re-materialize the **pristine
  base** — leaving a run whose facts assert a green the tree no longer embodies:
  the exact semspec false-green the reshape exists to kill.
- On a normal restart the framework's rule engine is edge-triggered and does NOT
  re-fire a rule that was already matching (`prevState.IsMatching=true` →
  `TransitionNone`), so the resolvers are never even *called* — the run freezes.
  The only reliable post-restart hook is the framework's `on_recovery` bootstrap
  fork, which fires once for a rule that was matching before the crash.
- **The correct M0 posture is a rule-native fail-closed park** — a rule with an
  empty `on_enter` (so nothing fires during live operation, when a run is
  `executing` its whole working life) and the park in `on_recovery` (which the
  framework fires only on the post-restart bootstrap replay). A run that was
  `executing`, not delivered, not already parked, would park toward the human on
  the next boot: surfaced and honest, never silently wedged, never falsely
  resumed. This would be a third realization of the single `run.awaiting_human`
  park writer (G5); no Go reconciler/ticker (G2/B3/B7).
- **BLOCKED ON UPSTREAM (semstreams engine gap, G2 path — not shippable at M0):**
  that park is **inert** on semstreams beta.146. The wired rule Processor only
  routes a rule to the stateful evaluator when it has a non-empty
  `on_enter`/`on_exit`/`while_true` (`message_handler.go` `hasStatefulActions`
  gate — it excludes `on_recovery`), so an `on_recovery`-only rule is never
  evaluated: it persists no state during live operation and never fires
  `on_recovery` on restart. A second issue compounds it — the stale-revision
  guard (`stateful_evaluator.go`, `prevState.SourceRevision >= ev.Revision`)
  likely suppresses the bootstrap recovery fork for an entity whose last write
  triggered its last eval. There is **no** end-to-end test proving `on_recovery`
  fires through the wired path. Per G2 (engine gap → file the upstream ask + park
  toward the human, never a silent Go reconciler), this is filed upstream and
  **deferred**, tripwired at `test/conformance/upstream_asks_test.go`
  (`TestTripwireOnRecoveryRoutingGate`). **Until the fix lands, a restarted
  in-flight run WEDGES — a known, documented M0 gap, not a claimed capability.**
  A Go boot-time reconciler that swept durable state to recover runs would be
  exactly the B3/B7 the constitution bars, so it is deliberately NOT built.
- `open_pr` becomes idempotent: it looks up an existing delivery for the run
  before creating, so a replay never double-opens (real payoff at M2 real-forge;
  at M0 the local-delivery stub is already upsert-idempotent). This half of R8 is
  NOT blocked and ships.

**Deferred to M2 (stated non-claim, not faked):** genuine attempt-level recovery
— `Checkouts.Root` / `Sandboxes.Resolve` reconstructing a checkout at
`attempt.commit` and re-`Up`ping the container — requires a **persistent git
remote that survives process death** (the same M2 work that replaces
`StaticSource` with a per-run `--recursive` PR clone). Until that precondition
holds, reconstruction cannot restore the applied attempt, so M0 parks instead.
The original reconstruction bullets (checkout/sandbox reconstruction, `alreadyReady`
liveness) belong to that M2 seam.

### R9. Honestly single-task at M0

Exactly one rule binds the rail to `task.spec.0`, marked as the M1 seam. The
M1 walker is the shipped semstreams gated-DAG component (readiness dispatch
over `depends_on` edges) — documented in this design as the successor so
nobody copies rule chains per task index.

### R10. Bridge proof + genuine evidence before completion claims

`journey_test.go` is renamed a **bridge proof** and extended with the
previously-missing stations: fail-then-pass (retry actually driven e2e),
rejection re-entry (Quinn rejects once), and budget exhaustion → park. **The
docker-gated restart-recovery station is DEFERRED with the R8 recovery park**
(both blocked on the same upstream `on_recovery` routing gap): once the framework
gate fix lands (the `TestTripwireOnRecoveryRoutingGate` tripwire fires), the
station kills the process mid-run, restarts over the SAME durable NATS/JetStream
(the buckets must survive — the inverse of the usual `nats:reset`), and asserts
the restart-recovery park fired (`run.awaiting_human` set with the restart reason)
with NO false green (no `verify.result`, no `pr.ref`). Until then a restarted
in-flight run wedges — honestly documented, never claimed as "completes." Forge
delivery in e2e speaks the real forge API against a protocol-faithful local
double; **M0-complete additionally requires one recorded real-forge run in the
evidence ledger** — the stub
`local-delivery:<run>` satisfies nothing.

### R11. semstreams beta.141 → beta.146; upstream asks tracked, non-blocking

The bump rides this change (despawn primitive, lifecycle idempotency,
`$entity.lifecycle.*`). Asks: #519 (scalar `.value`; fix drafted upstream) —
interim constant budget; #528 (per-spawn `max_iterations`) — interim uniform
component cap; #529 (uniform exhaustion reason) — interim route-on-outcome; and
the **`on_recovery` routing gap** (R8/group 7) — the rule Processor's
`hasStatefulActions` gate (`message_handler.go`) excludes `on_recovery`, so an
`on_recovery`-only park is never evaluated; compounded by the stale-revision guard
suppressing the bootstrap recovery fork. Interim: the restart-recovery park is
DEFERRED and a restarted in-flight run wedges (a documented M0 gap, NOT a Go
reconciler — G2). Each interim carries a tripwire test that fails when the
capability lands, so the upgrade is prompted, not forgotten
(`TestTripwireOnRecoveryRoutingGate` for this one).

### R12. Vocabulary diet (G9)

**Removed predicates:** `dev.gate_decision`, `dev.coherence_decided`, and the
ten relay markers listed in R1. **Added predicates:** `attempt.commit`,
`review.findings.<i>` (exact names declared in the spec deltas;
`review.verdict.<i>` already exists in harness-measurement). Spawn rules that
remain keep the house self-extinguishing fired-once markers — those are
restart-safety, not relays, and stay.

## Risks / Trade-offs

- **[Exact-match escalation]** `length_eq 3` escalation assumes the counter
  rises exactly +1 per dispatch. Mitigated: objects are distinct loop
  instances, storage dedupes exact triples, appends happen in one rule —
  pinned by a regression test that simulates a redelivered append.
- **[Model never measures]** Amelia can stop without calling `measure_task`.
  Mitigated: terminal route treats absent measurement as a failed attempt
  (`length_eq 0` guard); budget still bounds total work.
- **[Exhaustion vs model-error conflation]** Until #529, `outcome=failed`
  covers both max-iterations and model errors; both route to
  retry-within-budget, which is acceptable (a model error deserves a fresh
  attempt no less than a red measure). Tripwire flips when #529 lands.
- **[git inside the bind mount]** The container sees `.git`; measure runs as
  the container user and could theoretically touch it. Mitigated: the
  post-measure dirty-tree floor catches working-tree tampering, and verify
  clones from the host-side repo at a SHA — a container-side `.git` mutation
  cannot alter an already-stamped commit hash's content without detection.
- **[Component conversions widen the boot surface]** Six new component
  registrations. Mitigated: each wraps an existing, tested internal package;
  conversions are staged in the migration plan with the mock ladder green at
  every step.
- **[Uniform in-loop cap]** Until #528, a trivial task gets the same
  8-iteration ceiling as a hard one. Accepted at M0: the cross-loop attempt
  budget (3) is the real bound; the in-loop cap is a cost guard, not the
  contract.
- **[Sibling-change coherence]** This change deltas capabilities owned by two
  unarchived siblings on the same branch. Mitigated: reshape lands in place
  on the draft PR; archive order is declared (spine → sandbox → this) and
  `openspec validate --strict` gates each.

## Migration Plan

Reshape in place on the `m0-walking-skeleton-spine` draft PR, mock ladder
green after every group (real-container tests stay docker-gated):

1. semstreams beta.146 bump + green baseline.
2. Git-backed runspace: init-at-materialize, commit-per-apply,
   `attempt.commit` fact, dirty-tree floor, `CloneForVerify` → clone-at-SHA.
3. Patcher `target_files` enforcement (+ the `target_files`-includes-tests
   contract check at projection).
4. Multi-turn developer loop: allowlists, `tool_choice: auto`, drop
   `StopLoop` from `apply_patch`/`measure_task`, `read_workspace` tool,
   context-complete prompts.
5. Rules rework: delete gate/coherence/relay rules + vocabulary; add the R1
   route family; widen the G2 pin.
6. Component conversions (floors, verify, delivery, projection, validation,
   provisioning) — one commit each, with framework-alignment notes.
7. Review re-entry (R4) + budget routes (R3).
8. Restart-safe reconstruction (R8) + idempotent `open_pr`.
9. Brownfield ingest component (raw-lane wiring).
10. Bridge-proof journey extensions (fail-then-pass, rejection, exhaustion,
    restart) + docs honesty pass (brief/status/G10).

Rollback: each group is an independent conventional commit on the draft
branch; the arc stays runnable between groups because deletions (group 5)
land in the same commit as their replacing rules.

## Open Questions

- Forge evidence shape for the recorded real run: disposable GitHub repo vs
  org sandbox — decide with the operator when group 10 starts (does not block
  groups 1–9).
- Whether `issue_intake` stays a Sarah decide or becomes deterministic
  admission at M0 scope — current lean: keep the decide (it is judgment), but
  confirm during group 6.
- Dev-loop component `max_iterations` value (8 proposed) — tune against the
  fixture once group 4 is measurable.
