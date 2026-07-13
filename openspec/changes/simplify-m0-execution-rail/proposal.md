## Why

The 2026-07-13 Codex review of PR #1 (verdict confirmed accurate against the
code) found that semspec-style complexity regrew in the M0 execution rail
around an otherwise solid kernel: product Go derives lifecycle routes
(`check_gate` / `check_coherence` route-tokens — the semspec disease in
miniature, G2), reviewer rejection never re-enters development (D16
violation), cold verify copies a **mutable** workspace instead of proving an
immutable artifact (re-opening semspec's grave, G4/G7), patches can escape the
approved task contract, a process restart permanently wedges admitted runs,
and the model turns are numerous but context-starved. The mock-LLM journey is
a **bridge proof** — it proves the rail connects, not that it is correct or
production-shaped — and the "M0 complete" claim was ahead of the
implementation.

The assumptions audit (verified against semstreams beta.146 source) shows the
fix is *simplification, not new machinery*: loops are genuinely multi-turn
(the single-turn starvation was self-inflicted via `tool_choice: function` +
`StopLoop` on every tool), route conditions are rule-expressible today
(`eq` on latest-wins facts, `length_*` counters over storage-deduped appends),
and deterministic stations can be publish-triggered components with **zero
model turns**. This change is a corrective **replacement/refinement** of the
execution rail — explicitly not an additive orchestration layer.

## What Changes

- **BREAKING — remove the Go route-token layer (G1/G2).** Delete `check_gate`
  and `check_coherence` (tools + packages), the `dev.gate_decision` /
  `dev.coherence_decided` vocabulary, and every relay marker that exists only
  to move between stations. Routing becomes rules: `eq` conditions on
  harness-stamped latest-wins facts plus `length_*` attempt counters
  (storage-layer dedup makes them distinct-counts). Where the engine falls
  short, the asks are FILED — semstreams #519 (scalar field-to-field `.value`,
  fix drafted upstream), #528 (per-spawn `max_iterations`), #529 (uniform
  exhaustion reason) — and the rail parks toward the human rather than growing
  Go reconcilers.
- **BREAKING — one bounded multi-turn developer loop per task.** The developer
  (Amelia) gets a strict tool allowlist (read/inspect + `apply_patch` +
  `measure_task`), `tool_choice: auto`, and iterates
  read → patch → measure → read harness feedback **inside one loop**. The
  in-loop budget is the uniform component-level `max_iterations` cap at M0
  (per-task budgets arrive with semstreams #528). The 15-rule
  forced-single-turn station chain collapses.
- **Reviewer rejection re-enters development (D16).** `changes_requested`
  routes back into a fresh bounded developer attempt carrying the review
  findings (templated into the prompt — they are triples); only `approved`
  work reaches cold verification. Review-cycle budget is a constant at M0,
  counted rule-natively (`length_*` over the attempt family); it becomes
  dynamic (`task.spec` budget) when #519's `.value` form lands.
- **Cold verify consumes an immutable snapshot (G4/G7).** The checkout becomes
  a real git repository (`git init` at materialize; the harness commits after
  each applied attempt and stamps the commit SHA as a fact). Verification
  clones at that commit — never a copy of the mutable warm working tree.
- **Patch scope is enforced.** `apply_patch` rejects any diff touching files
  outside the approved `task.spec.target_files` (which must include the test
  files) — closing the unreviewed workflow/build/config escape.
- **Restart-safe, idempotent provisioning.** Durable readiness facts carry
  enough to reconstruct; `provision_sandbox` re-materializes the checkout and
  re-establishes the container instead of no-op'ing on a stale
  `sandbox.ready`; external effects (PR opening) are idempotent.
- **Deterministic stations become publish-triggered components — zero model
  turns.** Floors, cold verify, and delivery run as registered components
  reacting to facts (the gated-DAG pattern); measurement stays a
  harness-executed tool callable in-loop (G3: schema takes no outcome).
  Only three activities get model reasoning: authoring/planning (Sarah),
  task development (Amelia), adversarial review (Quinn) — each with complete
  context (task.spec triples templated into prompts, read tools in-loop) and
  strict allowlists.
- **M0 is honestly single-task.** The rail runs exactly `task.spec.0`; the
  generic walker is documented as the M1 path over the shipped gated-DAG
  component (`depends_on` dispatch) — never copied rule chains.
- **Brownfield ingestion is wired into the runtime.** The deterministic
  library (`internal/brownfield`) gets its registered ingest component
  (raw-lane → projector → graph-ingest, design D1).
- **Honest completion claims (G7/G10).** The e2e journey is renamed a bridge
  proof; M0 completion additionally requires genuine issue/admission/approval
  evidence and a real, idempotent forge delivery (`open_pr` stops writing
  `local-delivery:<run>` stubs).
- **Ride the semstreams beta.141 → beta.146 upgrade** (despawn primitive,
  lifecycle idempotency, `$entity.lifecycle.*` lookups).

**Preserved unchanged (the kernel Codex praised):** the graph/OpenSpec
foundation, harness-owned evidence, `internal/floors`, `internal/coldproof` +
`cleanroom` cold-proof machinery, the brownfield library, secrets governance,
and the realistic Go fixture.

## Capabilities

> Baseline note: no main specs are synced yet (`openspec/specs/` is empty);
> the capabilities below are owned by the two unarchived sibling changes on
> this branch (`m0-walking-skeleton-spine`, `containerized-sandbox-dev-loop`).
> This change deltas those capabilities as a corrective refinement; at archive
> time the changes sync in branch order.

### New Capabilities

None — this is a corrective refinement; every touched capability already
exists in the sibling changes (brownfield ingestion already lives under
`openspec-io`, and per-task review verdicts under `harness-measurement`).

### Modified Capabilities
- `dev-from-task`: the execution rail is replaced — one bounded multi-turn
  developer loop with in-loop harness feedback; rule-native routing (no Go
  route-tokens, no relay markers); reviewer rejection re-enters development
  within budget; single-task at M0 with the walker documented for M1.
- `clean-room-verify`: verification input becomes an immutable git commit
  snapshot (clone-at-SHA), never the mutable warm workspace; only approved
  work is verified.
- `harness-measurement`: `measure_task` becomes callable in-loop by the
  developer (feedback channel) while remaining harness-executed and
  outcome-parameter-free (G3); floors run as a deterministic component, not a
  forced model turn.
- `sandbox`: provisioning becomes restart-safe and idempotent (reconstruct
  from durable facts, never no-op over missing process-local state);
  `apply_patch` enforces the `target_files` contract.
- `run-lifecycle`: park/escalation routes are expressed as rules over
  harness-stamped facts (budget exhaustion, review rejection beyond budget,
  verify failure); no Go-derived routing tokens.
- `forge-io`: delivery is a real, idempotent forge PR with recorded evidence;
  the local stub no longer satisfies the requirement.
- `openspec-io`: deterministic brownfield ingestion gains its registered
  runtime component (raw-lane → projector → graph-ingest) at M0 — the
  library-only state no longer satisfies the requirement.

## Impact

- **Removed:** `internal/tools/checkgate`, `internal/tools/checkcoherence`,
  rules `dev-from-task/06,07,08a/b/c,11,12a/b` (and relay-marker vocabulary),
  the forced-single-turn dispatch pattern.
- **Reworked:** `configs/rules/dev-from-task/*` (fewer, route-bearing rules),
  `internal/runspace` (git-backed checkouts, snapshot commits, restart
  reconstruction), `internal/runspace/patcher.go` (target_files enforcement),
  `internal/tools/provisionsandbox` (idempotent re-provision),
  `internal/tools/measuretask` (in-loop, no `StopLoop`),
  `internal/tools/openpr` (real forge delivery), personas/prompts (complete
  context), `configs/semdev-bootstrap.json` (rule list, tool allowlists,
  model registry), `test/e2e/journey_test.go` (renamed bridge proof; new
  fail-then-pass, rejection-re-entry, and restart stations).
- **New:** floors/verify/delivery component registrations (framework-alignment
  notes + registry entries per G1), the brownfield ingest runtime component
  (under `openspec-io`), git snapshot seam in `runspace`.
- **Dependencies:** semstreams beta.141 → beta.146; upstream asks #519
  (drafted), #528, #529 tracked as non-blocking (interim: constant budgets,
  uniform component cap, route-on-outcome).
- **Docs:** brief/status corrections — the journey is a bridge proof; M0
  completion criteria restated honestly (G10).
