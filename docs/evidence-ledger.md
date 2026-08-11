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
- **ADDENDUM (2026-07-19, station-failure-parks): this failure class now
  PARKS.** The exact shape above — a station refusal exhausting the harness's
  bounded retries — now stamps `station.dispatch.failed` on the dispatched
  entity (writer `station-harness`) and the run-lifecycle park rules record
  `run.awaiting.human` naming the failed station and its refusal. Reproduced
  as the zero-token mock journey `TestBridgeProofStationFailureParks`
  (`test/e2e/parks_journey_test.go`): RED-verified without the park rules
  (the run stalled exactly as this entry records), GREEN with them (parked in
  14.7s, no task.spec / no cold verify / no delivery — no false green,
  exactly 3 model turns). An unattended M2 run hitting this class no longer
  stalls silently.
- M1 remains NOT CLAIMED as of this entry. (Claimed the same day by the
  run-2 entry above.)

## Rung: M0-COMPLETION — one recorded live-forge delivery (self-target)

**Status: CLAIMED (2026-07-20).** The G7 M0-completion requirement — at least one
recorded REAL-forge delivery (forge-io task 5.4 = self-target task 7.4) — is met by
the live run below: a real GitHub issue, cloned from its own coordinate, developed
by real Gemini turns, cold-room verified, and delivered as a real evidence-bearing
pull request. The M2 self-target mechanism is now proven LIVE, not only offline.
Full M2 *dogfood* (semdev working its OWN issues) remains ahead; this run used a
disposable target (`C360Studio/semdev-test`).

### 2026-07-20 — first live-forge delivery (M0-completion)   [kind: live-forge]

- Status: **converged** (attempts=1)
- Target: `C360Studio/semdev-test#1` (a real GitHub repo, seeded with the
  go-health-class project + the warning-boundary bug), CLONED from its coordinate
  via the forge-clone source (self-target provisioning)
- Trigger: `semdev launch C360Studio/semdev-test#1 --model gemini` (operator front
  door, OUTBOUND / pull-first — no webhook)
- Approval: pull-first STAND-IN — a `/semdev approve` comment event published to the
  GITHUB stream, released by the REAL approval adapter (`actor=cglusky`), authorized
  and attributed; the `semdev approve` CLI is the queued `pull-first-forge` change
- Run: `c360.semdev-001.agent.chain.execution.990ad0d7-ab81-474a-99c4-8f45dae04273`
- Delivered PR: **https://github.com/C360Studio/semdev-test/pull/2**
  (`head=semdev/990ad0d7… base=main`), body carries the evidence summary
- Change: `fix-health-classify-warning-boundary`; OpenSpec-validated
  (`sha256:00dfff6f…`)
- Measured (harness-stamped, in-container): `go test -run TestClassify ./...`
  exit_code=0 passed=true; commit `ad53f0777df2a82660c1fd4964b9f48779ef8ad8`
- Floors: rejected=false · Review (Quinn): approved, findings=0 · Clean-room verify:
  pass (profile go)
- Delivered diff = the FIX ALONE against main (`case pressure > warningThreshold` →
  `>=` in health.go); the delivered tip is the verified `attempt.commit.sha` — the
  bytes measured + cold-verified are the bytes delivered (G4/G7)
- Model: gemini-3.1-pro-preview via generativelanguage.googleapis.com (provider
  gemini + wire backend), all roles
- Cost record (harness token facts; NO `cost-usd` stamped — upstream #584 —
  reconciled from tokens at the config prices in $2.00/1M, out $12.00/1M):
  tokens-in 81,924, tokens-out 1,201 → **≈ $0.178**; per-loop: coordinator
  21,321/747, developer 51,167/367, reviewer 9,436/87
- Wall: launch 08:56:53 → delivery 08:59:01 (~2m08s incl. the approval wait)
- Hazards observed: benign `model not in registry, using default context limit`
  WARNs (the wake names the `coordinator`/`developer`/`reviewer` capability, not an
  endpoint — context limit defaults to 128000, ample for this task); the probe's
  hardcoded `max_tokens:64` truncates gemini-3.1-pro's reasoning before the tool
  call (re-probed at 512 tokens → clean `ping` — a probe artifact, not a fault)

## Framework migrations

### 2026-07-31 — semstreams beta.154 → beta.159 (ADR-056 projection mutation client)   [kind: migration]

**Status: OFFLINE + DOCKER GREEN. No paid tokens.** The Go fact-write path moved
off the DELETED `agentictools.OwnedFactWriter` onto `pkg/projection`'s
contract-bound mutation client.

| Evidence | Result |
|---|---|
| `go build ./...`, `go vet ./...`, `go vet -tags e2e ./...`, gofmt | clean |
| `go test ./...` | 57 packages, 0 failures |
| `openspec validate --strict` | valid |
| `go test -race -tags=e2e ./test/e2e/...` (real docker) | **0 failures, 320s, no timeout** — minus six PRE-EXISTING red journeys (below) |

All eight core bridge proofs pass through the new write path, including the full
arc (issue→change→validate→approval→project→provision→dispatch→apply→measure→
floors→route→review→cold-verify→deliver), the forge-clone journey, the webhook
journey, and the station-failure park.

**Six journeys are RED and this migration did not cause them.** Attributed by A/B
against a detached worktree at `490a784` — pre-migration, still beta.154 with the
deleted writer — where they fail IDENTICALLY (same assertions, same timings):
`TestBridgeProofNLApprovalReleasesGate`, `TestBridgeProofNLRejectionCancelsRun`,
`TestConservativeNoneDoesNotApprove`, `TestConflictingIntentsResolveToOneTerminal`,
`TestClassifierBindingFaultTellsTheHuman`, `TestBridgeProofApprovalByPollNoWebhook`.
All depend on the change-approval gate lane, which is mid-surgery in the parked
`nl-conversation-intent` group 8. ⚠ The last of those belongs to the ARCHIVED
`pull-first-transport` change and was recorded green there — group 8 has left an
archived capability red. Recorded here so it is not lost; it is a group-8 finding.

**What the journeys caught that the offline ladder could not.** Two runs were red
first, on real beta.159 requirements the compile and unit suites were blind to:
ordinary streams must declare `max_bytes`+`discard` (rejected only when a changed
config is re-validated — so most journeys booted fine while two could not), and
`RegisterBuiltins` hard-fails on `write_todos` without a projection mutation client
a product shell cannot supply. Both closed. The stream-bounds half is pinned
offline (`TestEveryStreamDeclaresItsBounds`); the write_todos half is pinned by
`TestWriteTodosStaysSkipped` (the skip-list entry plus its referenced-nowhere
precondition) — the boot gate itself sits in `RegisterBuiltins`' live-NATS branch
and is exercised only in the docker lanes.

**Not claimed:** enforcement is NOT flipped (`enforce_owner_lease` stays false,
pinned) — at entry time that was the gated group-6 step. *As-built addendum
(2026-08-11):* the flip is now VOID, permanently for this change — semstreams'
final refactor phase (the next tag) removes the ownership/lease mechanism (per
operator report; design D5 as-built), so beta.159 lands and stays observe-only
and the posture question transfers to the next-tag migration change, re-asked
against ownership's replacement. The review fold (`723b4e7` — both reviewers
APPROVE, zero blocking/high, all findings applied) re-ran the full ladder:
offline green (57 packages), docker `go test -race -tags=e2e` green in 268s
with the six pre-existing red journeys skipped by name. The admission create is
NOT migrated
(design D3c: the projection client swallows the `EntityExists` signal the intake
lane needs to skip a duplicate wake). No real-LLM run was made for this migration.

## Rung: M2 — dogfood (self-target foundation)

**Status: FOUNDATION PROVEN (offline + one live delivery above).** The M2 mechanism
— a run provisions its sandbox by CLONING the real target from its own coordinate
(`run.issue.ref`), develops in the clone, and delivers a PR back to it — is
bridge-proven against a local bare remote with zero paid tokens AND proven live
(the M0-completion entry above). Full dogfood (semdev on its OWN issues) is next.

| Journey (evidence ref: `test/e2e/selftarget_journey_test.go`) | Kind | Status | Artifact verified | What it proves |
|---|---|---|---|---|
| `TestBridgeProofSelfTargetForgeCloneToPR` | mock + forge-clone | pass | yes — in-arc cold clean-room verify of the committed artifact | the run clones a real target (history preserved via `refs/semdev/base`), develops, and delivers a PR back to the SAME repo whose diff (`main..semdev/<suffix>`) is the FIX ALONE, history excluded |

The front of the arc drives from a flattened webhook event so `coordinator/04`
stamps `run.issue.ref` (the coordinate the forge-clone source reads); the run
provisions with `RunOptions.ForgeSource` set and `SandboxSourceDir` unset, so a
fixture fallback is structurally unreachable (verified in review). Proven green
**with `-race`** (~29s), zero paid tokens.

HONESTY (task 6.1 review M3): the clone runs over file:// transport, which never
prompts for credentials, so the D3 no-argv-leak token path is NOT exercised here —
only the `clone.TestResolveTokenRidesEnvNotArgv` unit pin and the operator-gated
live run do. The local bare remote (clone source == delivery target) cannot
reproduce a moved server-side merge-base. This journey proves the
clone→develop→diff→deliver MECHANICS for the full-clone case; token-auth + a moved
base remain covered by the unit pin + the live-forge run.

The operator launch driver (`semdev launch <owner/repo#n>`) mints one run against a
live issue OUTBOUND through `experiment.Launch` (pull-first; no webhook secret).
Runnable via `task serve` + `task launch` (runbook §8); the live-forge delivery
that would claim this rung is deliberately unrun (it spends real forge state, and
the target must be SEEDED first — enforced in code).

Reviewers: go-reviewer + semstreams-reviewer both APPROVE (zero blocking/high; the
false-green impossibility verified against real git — fixture-fallback is
structurally unreachable when `ForgeSource` is set, and the merge-base + name-only
assertions are independently load-bearing).

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
