## Context

semsource (public beta, `~/Code/c360/semsource`) exposes a governed read
surface over a semantic knowledge graph of a repo's code/docs: MCP tools
`code_context` / `code_impact` / `code_search` / `doc_context`, the same verbs
over HTTP (`/code-context/<verb>`) and NATS (`code.v1.<verb>`), and a
`/source-manifest/health` readiness gate (consumers gate on `phase: "ready"`).
Its A/B validation design is a DRAFT — no measured evidence of token/tool-call
reduction exists. semdev's arc already harness-stamps everything an A/B needs:
per-loop tokens/iterations (framework `agent.loop.*` facts), `task.attempt`
counts, `floor.finding.*`, `measurement.result.*`, review verdicts, and the
recorded evidence ledger (`docs/evidence-ledger.md`).

Engine facts verified against the beta.150 module cache and the repo:

1. **semstreams has NO MCP client seam** — every "MCP" hit in the module is
   gateway-side (serving MCP), and the module carries no
   `modelcontextprotocol` dependency. Consuming semsource's MCP surface would
   add an SDK dependency semdev otherwise does not need.
2. **The tool seam is `RegisterExecutor`** (`internal/boot/boot.go`), with
   `github_list_comments` as the exact precedent — read it precisely: the
   tool registers in BOTH branches; the config (token) only selects a live
   client vs a schema-only nil client that fails loudly if executed.
   Registration is unconditional BY DESIGN so the G3 schema census (which
   boots the registry with fixed empty deps) sees every schema; a
   conditionally-registered tool would be invisible to the census.
3. **Per-loop tool scoping is enforced at execution** (#551, beta.149+): the
   spawn's `tools` list is advertised and `admitToolCall` rejects calls
   outside it — so condition-scoped tool sets are load-bearing, not advisory,
   and ADVERTISEMENT (not registration) is the correct condition lever.
4. **Rules are static JSON**: the `tools` array lives in the dispatch rules'
   `publish_agent` actions (`04`, `06c`, `07b`); there is no per-run
   substitution into that list.
5. **PRE-EXISTING DEFECT in this change's blast radius (prerequisite):** all
   four model-publishing spawn prompts (`04`, `06c`, `07b` — Amelia; `06a` —
   Quinn) instruct the model to read the task contract with `query_entity`,
   but NO spawn advertises it. Under #551 enforcement every real-LLM attempt
   opens with a rejected tool call (`not_advertised`). Invisible-green today
   only because the mock cursor never calls it. It detonates at exactly this
   change's target milestone (M1 real-LLM A/B) and contaminates BOTH arms
   with rejection noise — asymmetrically (the semsource arm can route around
   a blocked contract read via `code_context`; baseline cannot). It MUST be
   fixed in the baseline BEFORE the parity pin freezes variants.

## Goals / Non-Goals

**Goals:**

- A per-run, operator-declared condition (`baseline` | `semsource`) where the
  ONLY variable is the developer's available read tools; everything else —
  rules, prompts, budgets, floors, review, verify — is byte-identical.
- Harness-measured, condition-labeled evidence a human can compare in the
  ledger; semdev computes no winner.
- Fail-closed condition integrity: a run cannot silently masquerade as a
  `semsource`-condition run when semsource was unavailable.
- Baseline unchanged: with no condition configured, zero new code paths run.

**Non-Goals:**

- Making semsource a requirement, or writing ANY semsource fact into semdev's
  graph (G5/G9 stay untouched; the semstreams pin skew between the repos —
  semsource tracks a NEWER beta than semdev at authoring time — never
  matters because nothing is shared but HTTP).
- Reviewer-side semsource tools (Quinn stays identical across conditions to
  isolate the variable; a reviewer condition is a future change).
- Semsource-coached personas (a coached variant is a different experiment; v1
  measures tool AVAILABILITY, so prompts stay identical).
- An automated A/B verdict, statistics, or aggregation code.
- Parallel condition runs (serial runs per the v1 non-claims).

## Decisions

### D1 — Transport is HTTP via thin read-only proxy executors, not MCP, not NATS

- **(a) Four thin executors over semsource's HTTP surface — CHOSEN.** One
  shared HTTP client (endpoint from boot config), four tools named exactly as
  semsource's product surface names them (`code_context`, `code_impact`,
  `code_search`, `doc_context`) so semsource's own docs/prompts transfer.
  **ALWAYS registered** — schema-only with a nil client when no endpoint is
  configured, failing loudly if executed (the true `github_list_comments`
  precedent, fact 2): unconditional registration keeps the G3 schema census
  sighted on every schema. **The CONDITION controls advertisement**, via the
  variant pack (fact 3: a baseline loop that never advertises the tools
  cannot call them — #551 makes that load-bearing). Read-only: no facts
  stamped (no G5 writer), schemas take query parameters only (G3), results
  return to the loop as tool content. G1: the framework has no MCP client and
  no generic HTTP-proxy tool; the executor seam is the sanctioned extension
  point, with an alignment note + registry entries.
- **(b) MCP SDK client — REJECTED.** Adds a dependency to consume a surface
  the same gateway already serves over plain HTTP; no framework seam wants it.
- **(c) NATS `code.v1.*` request/reply — REJECTED.** semdev's NATS and
  semsource's NATS are separate clusters; bridging them couples two
  substrates (B1-adjacent) for zero benefit over HTTP.

### D2 — The condition selects a dispatch-rule VARIANT PACK at boot, pinned identical-but-for-tools

Rules are static (fact 4), so the semsource condition ships variant copies of
exactly the three developer-spawning rules (`04`, `06c`, `07b`) whose `tools`
arrays add the four semsource tools (ids suffixed `_semsource`, matching the
pack's underscore id style); boot config selects baseline or variant pack.
PREREQUISITE (fact 5): the baseline `query_entity` allowlist fix lands FIRST,
so the parity pin freezes correct rules, not the defect. The drift risk of
copied rules is killed mechanically:

- **PARITY PIN (conformance):** each variant rule MUST be byte-identical to
  its baseline sibling except `id`/`name` suffix and the `tools` array, and
  the tools delta MUST be exactly the four semsource tools appended. A
  variant that drifts in prompt, conditions, budget, or actions fails the
  pin — this is what makes "the only variable is the tool set" a checked
  property, not a hope.
- **(b) Load-time rule transformation — REJECTED.** Rules on disk would no
  longer match rules loaded (G10 violation; blinds the offline
  `test/ruleload/` gate).
- **(c) Advertise semsource tools unconditionally, gate by registration —
  REJECTED.** A baseline loop would advertise tools that error on call,
  contaminating the baseline condition with failure noise (and #551 makes
  advertised lists load-bearing — advertising fiction is the exact dishonesty
  the enforcement exists to kill). NOTE the asymmetry with D1: registration
  is unconditional (census integrity), advertisement is conditional (arm
  purity) — they are different levers and only the second may vary.
- **MUTUAL-EXCLUSION PIN:** the loaded rule set never contains BOTH a rule
  and its `_semsource` sibling. Without it a double-load double-fires the
  developer spawn on the same decide event — the shared
  `dev.developer.dispatched length_eq 0` guard is a race across two distinct
  rules, not a guard, and `publish_agent` is not idempotent.

### D3 — `experiment.run.condition` is an evidence label, never a routing input

Stamped once on the run at mint by the front-door driver (writer
`experiment-intake`, G5), value from boot config. The ledger reads it; NO rule
does. **Pin (whole-document):** no rule DOCUMENT in any pack — conditions,
actions, prompts, substitution tokens — references an `experiment.*` field
(a conditions-only lint would miss a `$entity.triple.experiment…` prompt
substitution that resolves differently per condition while passing the parity
pin). ACCEPTED CONFOUND, recorded here and in Risks: the fact lives on the
run entity, and any role holding `query_entity` (the coordinator today; the
developer/reviewer once the fact-5 prerequisite lands) CAN observe the label
by querying the run. The deterministic arc cannot act on it (no rule reads
it); a model could. We accept this: hiding the label would cost more
machinery than the residual bias is worth at M1, and the trajectory records
any query that read it.

### D4 — Condition integrity is proven at mint, fail-closed, on PER-SIGNAL readiness

In the `semsource` condition the front door probes semsource's status surface
BEFORE stamping `experiment.run.condition`, and gates on the PER-SIGNAL
readiness semsource itself documents for the tools we advertise — structural
index readiness (`index.ready`, gating `code_context`/`code_impact`) AND
retrieval/embedding readiness (`embedding.ready`, gating `code_search`) —
NOT the aggregate `phase: "ready"` alone: the aggregate means "all sources
reported," and a cold-embeddings gateway returns 200-OK weak/empty
`code_search` results — silent degradation with no errResult, the exact hole
a phase-only probe leaves open. A failed probe fails the launch loudly (no
run, no half-labeled evidence) — the sandbox-provision "prove it or park"
posture applied at the experiment boundary. A mid-run semsource outage
surfaces as loud tool errResults in the trajectory (never silent fallback:
the tool set never mutates mid-run); the evidence-ledger requirement makes
such a run ineligible as condition evidence (G7 — the human labels it, the
trajectory proves it).

### D5 — The A/B reads existing harness facts; this change adds ZERO measurement code

Tokens/iterations (`agent.loop.*`, framework-stamped), attempts
(`task.attempt.instance`), floors (`floor.finding.*`), measurement
(`measurement.result.*`), review verdicts/findings, and wall time (fact
timestamps) already exist with single writers. The A/B comparison is a human
reading condition-labeled ledger entries + trajectories. Any aggregation
script is a future nicety, not this change.

## Risks / Trade-offs

- **[Variant rule drift]** → the D2 parity pin fails the build on any
  difference beyond the tools array; the mutual-exclusion pin forbids loading
  both packs.
- **[The prerequisite `query_entity` fix changes baseline behavior at M1]** →
  it fixes a defect that would have hit BOTH arms (and asymmetrically); the
  fix lands in the baseline first with its own red-first prompt⊆advertised
  pin, so the A/B measures the intended variable, not the defect.
- **[Model-visible condition label]** → accepted confound (D3): no rule reads
  it, but a `query_entity`-holding role can; the trajectory records any such
  read, and the whole-document lint kills every deterministic channel.
- **[Prompt/coaching bias between conditions]** → prompts are inside the
  parity pin's byte-identical surface; no persona change ships in this change.
- **[Mid-run semsource outage contaminates the condition]** → D4: loud
  errResults + G7 ledger ineligibility; no silent fallback to baseline tools
  (the tool set never mutates mid-run).
- **[A/B doubles paid-run cost at M1]** → bounded by the existing per-task
  budgets; the operator chooses the issue set and run count; mock ladder
  proves plumbing for free first.
- **[semsource beta churn]** → consumption is its documented public surface
  (MCP-named verbs over HTTP + the per-signal readiness gate); endpoint/config
  isolated in boot config; baseline path has zero exposure. The repos' pin
  skew (semsource tracks a newer semstreams beta) is irrelevant over HTTP.

## Migration Plan

0. PREREQUISITE (baseline, fact 5): red-first prompt⊆advertised conformance
   pin (fails on today's pack), then add `query_entity` to the four spawn
   allowlists (`04`/`06c`/`07b`/`06a`); journeys stay green.
1. Vocab: declare `experiment.run.condition` (writer `experiment-intake`).
2. Executors: shared semsource HTTP client + four read-only proxy tools,
   ALWAYS registered (schema-only nil client without an endpoint, loud-fail
   on execute); live client only when boot declares the `semsource` condition
   (alignment note + registry entries).
3. Variant pack (`_semsource`) for `04`/`06c`/`07b` + boot-config pack
   selection; parity pin + mutual-exclusion pin.
4. Front-door mint: per-signal readiness probe (semsource condition only) +
   condition stamp; D3 whole-document lint; D4 fail-at-mint pin.
5. Ledger: condition column in `docs/evidence-ledger.md` schema prose; the
   ineligibility rule in the evidence-ledger spec delta.
6. Mock-ladder pins green (plumbing bridge proof; labeled as such); docker
   journeys green in baseline condition (byte-identical arc, now with
   `query_entity` advertised) and semsource condition against a local
   semsource compose (plumbing only).
7. Adversarial review; commit. Rollback: delete the variant pack + config key;
   baseline keeps the prerequisite fix (a defect repair, wanted regardless).

## Open Questions

- The boot-config shape for selecting the variant pack (how
  `configs/semdev-bootstrap.json` names rule packs today) — resolve at
  implementation, not a design blocker.
- Whether `code_changes` (semsource's fifth read tool) joins the set — start
  with four; add only with a reason recorded in the ledger design.
- Local semsource deployment recipe for the M1 A/B (its docker compose +
  `add_source` of the fixture repo) — an operator runbook item, documented
  with the first real run's evidence entry.
