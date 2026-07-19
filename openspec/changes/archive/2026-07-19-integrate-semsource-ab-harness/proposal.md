## Why

semsource stores the code under work as a live semantic knowledge graph and
claims agent-grokkable context cuts tool-calls and tokens — but its own A/B
validation doc is a draft: "the measurement result is not here." No measured
evidence exists, so semsource must stay OPTIONAL: a hard dependency would gate
semdev on an unproven claim, confound M1's first real-LLM evidence, and couple
our schedule to semsource's hardening (the repos also track different
semstreams pins). semdev already owns exactly the
instrument that proof needs: an arc with harness-stamped measurement, bounded
attempts, floors, and an evidence ledger. This change adds the missing lever —
an operator-declared per-run condition that swaps the semsource read tools into
the developer's scoped tool list — so identical issues run baseline vs
semsource and the ledger records the comparison. Burden of proof stays with
semsource; semdev provides the rig.

## What Changes

- **PREREQUISITE baseline fix (pre-existing, this change's blast radius)**:
  all four model-publishing spawn prompts instruct `query_entity`, but no
  spawn advertises it — post-#551 enforcement, every real-LLM attempt would
  open with a rejected tool call, contaminating both A/B arms. Fix in the
  BASELINE (add `query_entity` to the four spawn allowlists) with a red-first
  prompt⊆advertised conformance pin, BEFORE the parity pin freezes variants.
- **A/B condition seam**: an operator-declared experiment condition
  (`baseline` | `semsource`), selected at boot config, stamped on the run as
  ONE new fact `experiment.run.condition` (writer `experiment-intake`, G5) at
  run mint — evidence labeling, NOT a routing input (no rule document
  references it).
- **Semsource read tools enter the developer's ADVERTISED list** in the
  `semsource` condition only: `code_context`, `code_impact`, `code_search`,
  `doc_context` (semsource's MCP product surface), consumed READ-ONLY over
  HTTP via thin proxy executors. The proxies are ALWAYS registered
  (schema-only with no live client absent an endpoint — the G3 schema census
  must see every schema; execution without a live client fails loudly); the
  CONDITION controls advertisement via the variant dispatch pack (G1
  framework-alignment note + registry entries; schemas take query params
  only, stamp no facts — G3). Baseline condition is byte-identical to
  today's arc.
- **Fail-closed condition integrity**: a `semsource`-condition run PROVES
  semsource's documented per-signal readiness (structural index AND retrieval
  readiness, not the aggregate phase alone) at MINT, before the condition is
  stamped — a failed probe fails the launch loudly; a degraded run can never
  masquerade as condition evidence.
- **Evidence-ledger labeling**: run entries carry the condition, so A/B
  comparison is a ledger read (tokens, attempts, floors rejections, verdicts
  per condition). The verdict on "semsource helps" is a HUMAN reading of
  recorded evidence — semdev computes no winner.
- **NOT breaking**: with no condition declared, the arc is unchanged
  (baseline is the default; zero semsource code paths execute).

## Capabilities

### New Capabilities

- `semsource-ab`: the A/B instrument — condition declaration and stamping,
  condition-scoped tool admission, fail-closed reachability proof, and
  condition-labeled evidence.

### Modified Capabilities

- `dev-from-task`: the **Model roles receive complete context under strict
  tool allowlists** requirement gains the condition-scoped allowlist — the
  developer's advertised tools remain a strict, per-loop-enforced allowlist in
  BOTH conditions; the semsource condition extends (never replaces) the
  baseline read set.
- `evidence-ledger`: run entries record the experiment condition; a
  `semsource`-condition entry whose reachability proof failed is never
  recorded as condition evidence.

## Impact

- **Config**: boot config gains the condition + semsource endpoint; boot
  SELECTS the baseline or `_semsource` variant dispatch pack (rule JSON stays
  static — no load-time transformation, no per-run rule variants, per the
  serial-runs non-claim; a mutual-exclusion pin forbids loading both).
- **Go**: four thin read-only proxy executors + shared HTTP client (framework
  has no MCP client — verified) + the `experiment.run.condition` stamp at run
  mint + the mint reachability probe. No lifecycle transitions (G2); no
  outcome params (G3).
- **Rules**: `query_entity` added to the four baseline spawn allowlists
  (04/06c/07b/06a — the prerequisite fix) + the `_semsource` variant copies of
  the three developer spawns.
- **Vocabulary**: one new predicate `experiment.run.condition` (writer
  `experiment-intake`), declared in `internal/vocab` and this change's delta.
- **Upstream**: none required. semsource is consumed over its public HTTP
  surface; NO semsource facts enter semdev's graph (G5/G9 untouched); the
  semstreams pin skew between the repos (semsource tracks a NEWER beta than
  semdev at authoring time) is irrelevant to read-only HTTP consumption.
- **Evidence**: mock-ladder pins prove the plumbing (bridge proof only —
  retrieval value is unmeasurable against a mock LLM); the A/B itself runs at
  M1+ (real LLM, fixture repo) and M2 (dogfood), recorded per condition in
  `docs/evidence-ledger.md`.
