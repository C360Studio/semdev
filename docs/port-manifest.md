# Port Manifest

What semdev takes from its two donors, in what form, and what is **banned**
from crossing. Nothing enters semdev except through this manifest; every port
lands with the constitution pin that makes it safe. "Port as pattern" means
re-derive in semdev's terms reading the donor for reference; "port as code"
means copy and adapt (small, pure, tested); "port as data" means the content
carries, not the mechanism.

Donor paths are relative to `~/Code/c360/semteams` (branch
`codex/archive-sdd-openspec` @ 089a5eb5) and `~/Code/c360/semspec` (branch
`hardfork/semstreams-lifecycle` @ 5a9496ee) at manifest time.

## From semteams — the shape

| # | Asset | Source | Port as | Notes / required pin |
|---|-------|--------|---------|----------------------|
| T1 | Coordinator as rules + persona, **closed action taxonomy** | `configs/rules/coordinator/`, `configs/personas/fragments/coordinator/` | pattern | Closed taxonomy documented as "the only values the rule layer consumes"; semdev's actions: issue intake, create_change, dev_from_task, verify, open_pr, ask_human, respond. No phase enum, no Go terminal detector. Pin: G2. |
| T2 | `dev-from-task`: approved OpenSpec change → immutable task facts → bounded loop | `configs/rules/dev-from-task/`, `cmd/semteams/tools/projectspecplan/` | pattern (+ code reference for the projector tool) | Plan facts immutable; task status derived from execution markers; "it deliberately does not create another planning state machine." Pins: G2, G5, G9. |
| T3 | Karpathy-shaped task schema enforcement | `emitdevviatestplan` executor (assumptions, non_goals, target_files ≥1, test_command required; budget clamp [1,5] at stamp time) | pattern | The clamp ceiling IS the structural bound; escalate fires by iteration 6 regardless. Missing budget fails toward the human. |
| T4 | Review contract, exit-code-primary | `configs/personas/fragments/reviewer-*/10-review-contract.md`, dev-via-test rules 04a/04b/07d | pattern | KEEP the additive-constraints posture (findings never weaken the spec). CHANGE: the verdict gates on harness facts (G3), not the reviewer's claim about the exit code. |
| T5 | Readiness as deterministic analysis | `cmd/semteams/tools/analyzeproof/` + `configs/rules/proof-readiness/` | pattern | "It does not ask an LLM to judge infrastructure; it projects graph facts into routeable findings." This is the slot ALL semdev floors use: deterministic tool → facts → rules route. |
| T6 | Per-run devcontainer isolation with attestation | `cmd/semteams/sandboxmanager/`, `sandboxruntime/`, `tools/requestsandbox/` | pattern + code reference | Capability contract → admission → `devcontainer up` → probes → attestation facts. Needs beta.134 rebase; donor CI exercises a mock runner, so treat as design-proven, not battle-proven. The **shared warm shell** (`cmd/semteams/sandbox/`) is NOT ported for anything that produces evidence — G4. |
| T7 | Channel-agnostic human comms seam | commit 089a5eb5 front-door bus (`user.response.<loop_id>`), coordinator rules 03/03b | pattern | v1 adapters: GitHub issue/PR comments. Slack/Jira later without touching the arc. |
| T8 | Evidence-honesty discipline | `docs/demo-mvp-claims.md` (Non-Claims + Evidence Rule) | data + practice | Adopted verbatim in spirit: journeys must not backdoor-write; fixture-seeded = bridge proof. Pin: G7. |
| T9 | Tool-accretion discipline | `cmd/semteams/tools/README.md` (framework-alignment review) | practice, hardened | semteams keeps it as review culture; semdev pins it (G1 registry test). |

**NOT taken from semteams**: the shared warm sandbox shell as an evidence
surface (cache-masking class); LLM-supplied `pass=` measurement
(`emitdevviatestmeasurement`) — replaced under G3; the read-only ops
observers (documented over-fire bug) — superseded by the semspec watch port
(S4); its beta.115 framework pin; the conversational front-door product
direction (semdev's front door is a GitHub issue).

## From semspec — the floors (requirements and pins more than code)

| # | Asset | Source | Port as | Notes / required pin |
|---|-------|--------|---------|----------------------|
| S1 | Deterministic floor library | `processor/structural-validator/*_check.go`: source-build coordinates, authored-test integrity (vacuous tests), stub artifacts, implementation-completeness markers, anti-mock, tests-must-exist, gradle wrapper | code (pure functions + their red-first table tests) | Re-homed into the T5 slot (fact-emitting tools) and wired into the loop harness where they **cannot be skipped** — in semspec they were discovered severed from production. Verbatim hard#2 reject fixtures travel with them. |
| S2 | Clean-room verify | `workflow/verify/verify.go` (pure `Decide` shape), isolated resolution proof (`runDependencyResolutionProof`, sandbox `isolated` exec mode) | code (pure core) + pattern (wiring) | Becomes the G4 terminal gate running in T6 isolation: fresh cache home, `--refresh-dependencies`-class resolution proof, build + run the artifact's own tests. Fail closed; transport errors retry, never terminally reject (go-reviewer H1 lesson). |
| S3 | Trusted measurement | requirement distilled from semspec G3 (audit `docs/audit-system-design-2026-07-05.md`) | requirement, implemented fresh | Harness executes, harness stamps. Schema-level pin (G3). This is deliberately NOT a code port — semspec never had it wired correctly either. |
| S4 | Watch CLI / liveness | `cmd/semspec` watch (`--live`, ALERT dedupe, snapshot bundles, bail-on) | code | Operator surface for M1+; replaces semteams' broken observers. Trajectory-archive command (`semdev trajectory <run>`) grows from this codebase. |
| S5 | GitHub I/O donors | `processor/github-watcher/`, `processor/github-submitter/` | code reference only | Re-shaped as tools/adapters behind the T7 comms seam (issue intake, PR creation, comment posting) — NOT as components (G1). |
| S6 | E2E ladder discipline | `test/e2e/` tiering (T0/T1/T2), mock-LLM fixture harness, offline conformance gates, `docker/compose` patterns | pattern + selective code | Mock ladder green before any real-LLM token, always. Port the discipline and the mock harness shape; write semdev-native journeys. |
| S7 | Fixtures | `test/e2e/fixtures/` (realistic baselines) | data, **stripped** | Strip every orchestration-vocabulary coaching comment before entry (G8). The meshtastic/OSH source-build shape is the reference hard fixture. |
| S8 | Lessons corpus | `docs/model-testing-findings.md`, failure-mode taxonomy, retry-feedback rules (bounded, delimited, reasons-not-rules) | data | Feeds personas and review prompts. History, not mechanism. |
| S9 | Conformance-pin practice | `test/plumbing/` manifest pattern, lockstep config pins, reviewvocab-style exhaustiveness tests | pattern | The G6/G5/G9/G10 pin mechanics. The practice is the port; the specific pins are semdev-native. |
| S10 | Evidence ledger | `workflow/validation/e2e_evidence.go` (status vocabulary: pass/exploratory/blocked/…, CountsAsPass discipline) | code (small) | G7's substrate, present from M0. |

**BANNED from semspec — the leak list.** These patterns are why semdev
exists; none of them crosses, and reviews cite this table by number:

| # | Banned pattern | Why (audit evidence) |
|---|----------------|----------------------|
| B1 | Manager pattern (cache + KV bucket + tripleWriter write-through), KV twofer, domain state buckets (`PLAN_STATES`-class) | Bespoke distributed state; bred the wedge families |
| B2 | The 224-predicate vocabulary, or any bulk vocabulary import | G9; facts arrive one change at a time |
| B3 | execution-bridge / recovery-consumer / plan-api state code; marker choreography; run-phase reconcilers | The condemned 10k-LOC hybrid core; 5/6 edges in Go |
| B4 | Multi-writer facts; "sole writer" as a comment instead of a pin | Audit found 4 multi-writer facts, one documented falsely |
| B5 | Parallel presentation-layer status derivations | Four divergent derivations, two with contradictory precedence |
| B6 | Component-per-concern topology (16-component pattern) | G1; new processors need the registry + alignment note |
| B7 | Bespoke reconciler/backstop tickers as liveness | Compensations for engine gaps; file upstream asks instead, park toward the human meanwhile |
| B8 | Retry-fidelity vocabulary matching (reviewvocab-class machinery) | Unnecessary once specs are immutable by construction (T2); the whole #299 family dissolves |
| B9 | Streak/state JSON blobs in triples | Violated semspec's own standards within weeks |
| B10 | Fixture coaching in orchestration vocabulary | G8; measured "can it follow planted instructions," not capability |

## Framework

Start on current semstreams (`v1.0.0-beta.134`+). Where semdev hits an engine
gap (cross-entity aggregates, guaranteed re-evaluation, re-entrant iteration
budgets, foreign-bucket watch), the move is: file the upstream ask, park
toward the human in the interim, record the ask in the change. Never a silent
Go workaround (G2).
