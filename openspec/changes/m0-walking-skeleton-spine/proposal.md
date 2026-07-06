## Why

semdev has a brief, a constitution, and a port manifest — and zero product
code. Nothing yet proves the stations of the arc actually connect, or that the
load-bearing guardrail pins (G2, G3, G4, G7) are real rather than aspirational.
semspec's history is the warning: unproven wiring and floors that get severed
from the loop are exactly how a 10k-LOC wedge core grew. So we build the
thinnest end-to-end thread first — on a mock LLM, against a fixture repo, at
zero paid tokens (G6) — so every later change thickens a *proven* skeleton
instead of building on faith. This is M0's walking-skeleton spine, the first
rung of the milestone ladder.

## What Changes

- Introduce the **walking-skeleton spine**: one thin thread through the whole
  arc — issue intake → generated OpenSpec change (human-approval gate) → one
  bounded dev loop over immutable task facts → harness-owned measurement →
  deterministic floors + semantic review → clean-room verification in fresh
  isolation → PR delivery — driven by a mock LLM against a single fixture repo.
- The arc is expressed as **rules matching facts**, never product Go firing
  transitions (G2). Its action vocabulary is a closed taxonomy (T1): issue
  intake, create_change, dev_from_task, verify, open_pr, ask_human, respond —
  no phase enum, no Go terminal detector.
- Land the load-bearing pins, **red-first** where applicable:
  - **G2** — conformance test asserts zero lifecycle-transition callers in
    product Go (target 0).
  - **G3** — schema conformance rejects any tool whose schema accepts an
    outcome-shaped field (`pass`, `exit_code`, `success`, …) from the caller.
  - **G4** — the e2e journey asserts verify ran in fresh isolation and that a
    cache-masked fabrication fixture is rejected.
  - **G7** — an evidence ledger with honest statuses exists from this change;
    a run whose artifact fails verification is never `pass`.
- **Host neutrality**: the arc speaks *issue in → PR out* generically. GitHub
  is the v1 adapter behind a channel-agnostic seam (T7), owned by the `forge-io`
  capability and swappable for other code hosts without touching the arc.
- **Personas port as pattern, re-derived.** Donor persona names (semteams'
  Lisa / Ralph / CBG) are not carried; semteams' "Ralph loop" enters as the
  bounded dev loop (pattern, not name). Persona naming maps to **BMAD** where
  it supports semdev's flow — while adopting **none of BMAD's process**. The
  concrete roster is fixed in `design.md`.
- **Deterministic floors and semantic review enter thin** — as single
  requirements folded into `dev-from-task` (floors) and `harness-measurement`
  (review verdict gating on harness facts, T4), not as standalone capabilities
  at spine stage. They earn their own specs when a later change thickens them.

## Non-Goals

Explicit scope guard — these do **not** enter with the spine:

- No real-LLM run. Mock ladder only; the spine spends zero paid tokens (M1
  owns the first real token, with watch/liveness in place).
- No parallelism — one change, one loop, serial tasks (brief non-claim).
- No liveness / watchdog / `semdev trajectory` archive surface (grows from S4
  at M1+).
- No BMAD **process** adoption — we borrow BMAD persona naming/character where
  useful; we import none of its workflow, ceremonies, or templates.
- No arbitrary-repository generality — a single deliberately configured
  fixture repo.
- No new predicates beyond those named in this change's spec deltas (G9);
  semdev starts from zero vocabulary.
- No standalone floor / review / additional host-adapter capabilities beyond
  the single thin thread — deferred to thickening changes.

## Capabilities

### New Capabilities

- `run-lifecycle`: the fact- and rule-driven run arc through every station,
  including both human gates (change approval, PR review), expressed as a
  closed action taxonomy. Home for G2 and T1. Product Go fires no transitions;
  an engine gap becomes an upstream semstreams ask plus a documented interim,
  never a silent Go reconciler.
- `forge-io`: the code-host I/O seam — issue intake in, PR/comment delivery
  out — behind a channel-agnostic boundary (T7). GitHub is the v1 adapter
  (re-shaped from semspec's watcher/submitter as tools, not components — S5,
  G1). The arc never binds to a specific host.
- `dev-from-task`: an approved OpenSpec change projected into **immutable task
  facts**, run through one bounded dev loop (T2). Karpathy-shaped task schema
  enforced at stamp time — assumptions, non-goals, ≥1 target file, required
  test command, budget clamp `[1,5]` with escalation by iteration 6 (T3).
  Deterministic floors ride here as fact-emitting checks the loop cannot skip
  (T5, S1). Task status is derived from execution markers; the loop converges
  on tasks but can never redefine them.
- `harness-measurement`: the harness that executes a command **stamps** the
  pass/fail / exit-code / test-count fact; no schema accepts an LLM-supplied
  outcome (G3, S3). Semantic review reads these stamped facts — the reviewer's
  verdict gates on harness facts, not on the model's claim about the exit code
  (T4). Additive-constraints posture: findings never weaken the spec.
- `clean-room-verify`: the terminal gate — fresh isolated environment (distinct
  build-cache home per run), resolve and build from the artifact's own
  declarations, run the artifact's own tests, before any PR opens (G4, S2).
  Fail closed; transport errors retry, never terminally reject.
- `evidence-ledger`: the honest record of runs from day one — status
  vocabulary (pass / exploratory / blocked / …) with a run whose artifact
  fails independent verification never marked `pass`; mock-run and
  fixture-seeded journeys labeled as bridge proof (G7, T8, S10).

### Modified Capabilities

None — semdev starts from zero specs.

## Impact

- **First product code.** semdev moves from docs-only to a running spine. Every
  Go addition requires a G1 framework-alignment note and a registry entry;
  primitive-first is the default — prove a rule + persona + fact can't do it
  before proposing Go.
- **New engine dependency**: semstreams `v1.0.0-beta.134`+, NATS via docker
  compose (never embedded), plus a mock-LLM harness and the S6 e2e ladder
  discipline (mock ladder green before any real token).
- **Ports land per the manifest only**: T1, T2, T3, T4, T5, T7, T8 (semteams
  shape) and S1, S2, S3, S5, S6, S7, S10 (semspec floors). Nothing on the
  B1–B10 banned list crosses.
- **New checked-in registries** the pins compare against: the tools registry
  (G1), the single-writer table (G5), the predicate → introducing-change table
  (G9), and the evidence-ledger schema (G7).
- **Fixture repo** enters stripped of all orchestration-vocabulary coaching
  (G8); it documents only what a real upstream repo would.
