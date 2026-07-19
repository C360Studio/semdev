# Evidence ledger

The recorded evidence ledger (constitution G7): every milestone-ladder rung is
claimed here, on named evidence, with honest labels — or it is not claimed. The
entry schema and its fail-closed validation pins live in `internal/ledger`
(status vocabulary `pass / exploratory / diagnostic / blocked / skipped`, kind
`real-llm / mock / fixture-seeded`); the graph writer for `evidence.ledger.run`
(single writer `evidence-ledger`, reserved in the vocab) lands with the
evidence-ledger capability at M1. Until then this file IS the ledger.

Bridge-proof discipline (G7): a `mock` or `fixture-seeded` entry never counts as
real-LLM product evidence, whatever its status.

## Rung: M0 — walking skeleton (mock LLM)

**Status: CLAIMED — on bridge proof only.** The full arc (issue → change →
human approval → provisioned+cold-proven sandbox → bounded dev loop → in-container
measurement → structural floors → rule-native route → semantic review → cold
clean-room verify → PR ref) runs end-to-end against real containers with zero
paid tokens. semstreams pin `v1.0.0-beta.150` at claim time.

| Journey (evidence ref: `test/e2e/journey_test.go`) | Kind | Status | Artifact verified | What it proves |
|---|---|---|---|---|
| `TestBridgeProofIssueToPRAgainstMock` | mock | pass | yes — in-arc cold clean-room verify of the committed artifact | the happy-path arc, front door → `delivery.pr.ref` |
| `TestBridgeProofRetryFailThenPass` | mock | pass | yes — verify passes on the corrected attempt | measured-red → retry → measured-green re-entry; attempt count is the proof |
| `TestBridgeProofReviewRejectionReentry` | mock | pass | yes — verify passes after the re-entered attempt | reviewer rejection re-enters the dev loop (no dead-end on changes_requested) |
| `TestBridgeProofBudgetExhaustionParks` | mock | pass | no — BY DESIGN: the expected terminal is the park (`run.awaiting.human`), no `verify.cleanroom.result`, no `pr.ref` | budget exhaustion fails closed to the human, never a false delivery |

All four journeys drive the fixture-seeded target repo (`test/fixtures/go-health-class`),
so each is doubly bridge proof under the kind vocabulary (`mock` and
`fixture-seeded`). The Status column records the JOURNEY-TEST outcome; the
schema's `Entry` mapping (`pass` ⇒ `Verified=true` + `EvidenceRef`) applies to
artifact-producing rows only — the exhaustion journey's asserted terminal is the
ABSENCE of an artifact, so its M1 graph entry maps to a verified assertion of
the park facts, never a `pass` with `Verified=false` (which `Entry.Validate`
rejects, correctly).

Claim provenance: journeys re-proven green on beta.150 at commit `c67e88b`
(lineage: first green on the reshape at `7461cad`/`dee35dc`). Run **without
`-race`** — a pre-existing framework data race
(`rule.Processor.Health()/DataFlow()` write-under-RLock, semstreams #566, gap-open
tripwire `TestTripwireProcessorHealthRaceUnfixed`) makes the `-race` suite flaky;
behavior-benign, tracked upstream. Zero paid tokens; zero
predicate/entity-contract rejections under beta.150's fail-closed graph-write
gate.

Post-claim addendum (beta.153): #566 landed, the caveat above is historical —
the whole suite runs **with `-race`** green. The journey roster has since grown
(same kind vocabulary, all mock + fixture-seeded, zero paid tokens): the
per-task-budget journeys (`TestBridgeProofBudgetOneEscalatesOnFirstRed`,
budget-boundary escalate; the exhaustion journey re-keyed to the authored
budget) and the reason-aware transient pair
(`TestBridgeProofTransientGraceRetries` — a real `model_error` terminal gets
bounded grace outside the convergence budget then delivers;
`TestBridgeProofTransientCapParks` — three transient deaths exhaust the grace
cap and park naming the substituted reason). The grace journey is additionally
the RED-FIRST docker pin for the rule-engine double-dispatch race
(adopt-reason-aware-escalate): it failed ~50% of `-race` runs against the
first-cut rule-stamped classification and is the live falsifier the shipped
atomic-mirror mechanism was built against.

Non-claims carried with this rung (brief §Non-claims): no real-LLM evidence, no
arbitrary-repo generality, no unattended-operation claim (operator-authority
park is the posture), no parallel execution.

## Rung: M1 — real-LLM easy tier

**Status: CLAIMED — on run 2 below (2026-07-19).** The brief's M1 bar — same
arc on a real model, fixture repo, bounded cost, watch/liveness in place —
is met on every clause by a converging, fully-recorded run. Delivery is the
M0 local-delivery stub (`delivery.pr.ref` recorded; the real forge-io PR is
a later group), exactly as the arc defines it today — no claim beyond that. The attempt procedure and the entry template are
`docs/real-llm-runbook.md` (first-real-llm-journey): mock ladder green first,
the smoke probe, the armed sidecar with pre-written abort criteria, then the
entry — converged, parked, and aborted runs all get one (honest failures are
evidence).

### 2026-07-19 — first real-LLM journey, run 2 — THE M1 RUN   [kind: real-llm]

- Status: **converged** — `--- PASS: TestRealLLMJourneyIssueToPR (84.24s)`,
  `delivery.pr.ref` recorded, slug `fix-classify-warning-boundary`,
  **attempts=1** of the authored budget (clamp [1,5] load-bearing), zero
  retries, zero transient grace consumed
- Command: `task realllm:launch` (immediately after run 1's includes-test
  fix `78426e7`; mock ladder green same session; probe green)
- Model: `gemini-3.1-pro-preview` via generativelanguage.googleapis.com
  (provider `gemini` + `wire_backend: wire`), all three roles
- Stations, ALL with real model turns where the arc demands one:
  decide→issue_intake → run minted → executing → change authored →
  CLI-oracle validated (first try, again) → awaiting_approval → stood-in
  approval → task.spec projected (the run-1 fix PROVED: target_files carried
  the measuring test) → sandbox provisioned + cold-proven → dev_from_task →
  Amelia authored a REAL diff → **in-container `go test` GREEN** → floors
  passed → Quinn approved → **clean-room cold verify PASSED** →
  `delivery.pr.ref`
- Cost record (harness-stamped + token-reconciled): 6 run-bound loops —
  reviewer 10,107/87, developer 53,347/409, coordinators 2,496/433 +
  9,855/142 + 1,940/93 + 6,619/95 (tokens-in/out); Σ in 84,364, Σ out 1,259;
  stamped `agent.loop.cost-usd` only on the front-door loop (0.014378 — the
  spawned-loop cost-stamping gap recurs — filed as semstreams #584);
  token-reconciled total ≈ **$0.184** at 2.00/12.00 per 1M
- Sidecar record: narration monitor + 60s wallclock stall sidecar armed for
  the whole run; no wedge, no anomaly; run wall-clock 84s
- Evidence ref: `test/e2e/realllm_journey_test.go` (`TestRealLLMJourneyIssueToPR`),
  run log narration + LEDGER lines 2026-07-19

### 2026-07-19 — first real-LLM journey, run 1   [kind: real-llm]

- Status: **failed at projection** (station refusal, 77s — no park: M0 has no
  auto-park on station failure; the journey's own window failed loud with the
  station log as evidence)
- Command: `task realllm:launch` (`TestRealLLMJourneyIssueToPR`, gate + key
  via the dotenv lane; `task realllm:probe` green first — auth, model, forced
  tool call, thought_signature all confirmed on the wire)
- Model: `gemini-3.1-pro-preview` via generativelanguage.googleapis.com
  (provider `gemini` + `wire_backend: wire`), all roles
- Stations reached with REAL model turns: decide→issue_intake → run minted →
  executing → change authored (slug `inclusive-warning-threshold` — the
  content lane WORKED: wake body → decision reason → author) → **CLI-oracle
  VALIDATED first try** → awaiting_approval → stood-in approval → **REFUSED
  at projection**: task 0's `target_files` carried no `*_test.go`
  (`project_tasks` includes-test contract — enforced but never communicated
  to the model; the schema description said only "Files this task will create
  or change")
- Cost record (harness-stamped + token-reconciled): 3 coordinator loops;
  tokens-in 6554/1940/2424 (Σ 10,918), tokens-out 94/83/385 (Σ 562); stamped
  `agent.loop.cost-usd` 0.014236 on the front-door loop, the two spawned
  loops carried tokens but NO cost fact (framework stamping gap — noted for
  an upstream ask); token-reconciled total ≈ **$0.029** at 2.00/12.00 per 1M
- Sidecar record: narration monitor + 60s wallclock stall sidecar armed; no
  wedge (failure at 77s on the journey's own window); probe cost ~130 tokens
- Fix landed WITH this entry (G6): red-first pin
  `TestSchemaTargetFilesNamesTheIncludesTestContract` + the `create_change`
  schema's `target_files` description now names the includes-test contract
  and its consequence; the journey's projection station poll now dumps
  evidence and names the refusal class
- M1 remains NOT CLAIMED.

## Experiment conditions (semsource A/B — integrate-semsource-ab-harness)

Runs minted under a declared experiment condition carry
`experiment.run.condition` (`baseline` | `semsource`), stamped once at mint by
the launch path (writer `experiment-intake`) after — for the `semsource`
condition — a PER-SIGNAL readiness proof (`index.ready` AND `embedding.ready`,
never the aggregate phase alone). Ledger rules for condition-labeled entries:

- **Every entry for a condition-minted run records the condition** — the label
  comes from the run's fact, never re-derived.
- **A degraded `semsource` run is NOT condition evidence.** If the trajectory
  records semsource proxy faults material to the attempt (a mid-run outage is
  LOUD by design — errResults in the trajectory, never a silent fallback), the
  entry states the degradation and the run is ineligible as condition
  evidence. Defense-in-depth: the SANCTIONED launch path
  (`experiment.Launch`) fails closed — a failed probe publishes nothing, so no
  run and no half-labeled evidence exist from it; the M1 real driver MUST mint
  condition runs through it (a run minted any other way is unlabeled or
  unproven and ineligible as condition evidence).
- **No aggregate verdict, ever.** Cross-condition comparison is a HUMAN
  reading condition-labeled entries and trajectories (tokens/iterations from
  the framework's `agent.loop.*` facts, attempts, floors, measurements, review
  verdicts — all pre-existing single-writer facts; the A/B added zero
  measurement code). semdev computes and records no winner (G3/G7).

Unconditioned runs (every entry above this section) predate the instrument and
carry no condition label — they are baseline-shaped but NOT retroactively
labeled.
