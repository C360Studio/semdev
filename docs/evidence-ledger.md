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

**Status: NOT CLAIMED.** No real-LLM entry exists. The first `real-llm` entry
lands here with its watch-sidecar and cost record when the rung is attempted.
