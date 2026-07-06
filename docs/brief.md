# semdev — Product Brief

**Shape**: GitHub issue in → reviewed, clean-room-verified pull request out.

**One sentence**: semdev is an agentic development system built almost entirely
from semstreams primitives — rules, personas, and a small set of deterministic
tools — that takes a GitHub issue through an approved OpenSpec change, a
bounded dev loop, deterministic and semantic review, and clean-environment
verification, and delivers the result as a PR.

## Why this exists (and why now)

Two sibling systems taught us what works and what does not:

- **semspec** (donor, archived as forensics) proved the verification lessons
  the hard way: LLM self-report is worthless, green pipelines are not verified
  delivery, warm environments mask fabrication, and bespoke distributed state
  machines breed wedge classes faster than they can be pinned. Its 2026-07-05
  adversarial audit (`semspec/docs/audit-system-design-2026-07-05.md`) found a
  10k-LOC hybrid state core with 1/6 lifecycle edges rule-owned and its QA
  tiers severed — condemned, not fixable in place.
- **semteams** (upstream's reference product) proved the shape: coordination
  as rules + personas with a closed action taxonomy, dev as a bounded Ralph
  loop over immutable spec-projected tasks, deterministic readiness analysis,
  per-chain devcontainer isolation, and an evidence-honesty discipline — with
  **no bespoke lifecycle state machine anywhere**. Simple yet detailed, and it
  demonstrably works (real-LLM dev run with a correct reviewer rejection,
  ~$0.30).

semdev merges them: **semteams' shape, semspec's floors.** What each side
contributes — and what is banned from crossing — is specified precisely in
[docs/port-manifest.md](port-manifest.md). The rules that keep semdev from
re-growing semspec's complexity are law in
[docs/constitution.md](constitution.md).

## Product shape (v1)

- **Input**: a GitHub issue on a target repository.
- **Two human gates**:
  1. The generated OpenSpec change (proposal → specs → tasks) requires human
     approval before any dev loop runs.
  2. The PR itself — reviewed by humans like any PR.
  Between gates, semdev runs unattended within bounded budgets.
- **Execution**: one bounded dev loop per task (Ralph pattern) in an isolated
  per-run environment; tasks are projected from the approved change as
  immutable facts; the loop converges on them but can never redefine them.
- **Measurement is harness-owned**: pass/fail facts are stamped by the tool
  that executed the command. No schema in semdev accepts an LLM-supplied
  outcome boolean. (Constitution G3.)
- **Review**: deterministic floors first (fabrication, vacuous tests, scope
  drift — zero tokens), then semantic review against the spec.
- **Terminal gate**: clean-room verification — fresh isolated environment,
  build from the artifact's own declarations, run the artifact's own tests —
  before any PR is opened. (Constitution G4.)
- **Output**: a PR whose description carries the evidence: what was verified,
  how, and where the full trajectory lives.

## Communication and audit trail (v1)

- **No web UI.** GitHub is the product surface; questions to humans are posted
  as issue/PR comments. Comms ride a channel-agnostic seam (the semteams
  front-door bus pattern), so Slack/Jira adapters can be added without
  touching the arc.
- **Full trajectory per run, always captured**: prompts, tool calls, facts
  written, decisions, budgets spent — durable artifacts, not projections.
- **`semdev trajectory <run>`** renders a run's full audit trail as a
  self-contained static-site archive (zip) on demand.
- **semstreams-ui** remains available as an optional dashboard/search/admin
  surface reading the substrate directly — semdev ships no UI code.

## Milestone ladder

Each rung is gated by the evidence ledger (see G7); a rung is not claimed
until its evidence entry exists and survives the honesty rules.

- **M0 — walking skeleton (mock LLM)**: fixture repo, full arc: issue →
  change → approval → one-task dev loop → harness measurement → floors →
  semantic review → clean-room verify → PR. Every guardrail pin from the
  constitution lands here, red-first where applicable.
- **M1 — real-LLM easy tier**: same arc on a real model, fixture repo,
  bounded cost, watch/liveness in place.
- **M2 — dogfood**: semdev works its own GitHub issues. This is the first
  real target and the standing one: the product improves the product.
- **M3+ — harder tiers / sibling repos**: only after M2 evidence is boringly
  repeatable.

## Non-claims (v1)

- Not claiming arbitrary-repository generality; targets are repos we
  configure deliberately.
- Not claiming fully unattended operation until the liveness/watchdog rung
  has its own evidence (operator-authority park is the v1 posture).
- Not claiming parallel execution; one change, one loop, serial tasks. The
  wedge classes that parallel orchestration bred in semspec are documented;
  parallelism must earn its way in with a design, not drift in.
- Mock-run evidence is labeled as such and never presented as real-LLM
  evidence. Fixture-seeded journeys are bridge proof, not product proof.

## Stack

Go + semstreams (start at the current release; semspec drove the framework to
`v1.0.0-beta.134` — do not start on semteams' older pin), NATS JetStream,
OpenSpec for both semdev's own development and the product's plan artifacts.
Rule packs + persona fragments as the primary programming surface; Go only
where the constitution's G1 gate admits it.
