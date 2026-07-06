# semdev Constitution

Ten guardrails. Each is a rule **plus the machine check that enforces it** —
a guardrail without a pin is a wish, and semspec's history shows wishes drift.
Pins land with the first code that makes them meaningful (M0), red-first when
they guard against a known failure shape. Amending a guardrail requires an ADR
that names what evidence changed.

The single meta-rule, learned from watching semteams succeed where semspec
drowned: **stay simple yet detailed — reach for a semstreams primitive, a
rule, a persona, or a fact before writing Go, every single time.**

## G1 — Primitive-first (the semteams prohibition, made enforceable)

No new Go component, tool, or subscriber may be added if semstreams
primitives (rules, personas, existing agentic/graph components, facts) can
express the behavior. Every Go addition requires a framework-alignment note
in its change: what primitive was considered and why it cannot do this.

- **Pin**: a conformance test inventories `cmd/semdev/tools/*` and any
  processor registrations against a checked-in registry; each registry entry
  links its alignment note. An unregistered addition fails the build.

## G2 — Rules own all lifecycle transitions, terminals included

The product's run lifecycle is decided by rules matching facts. Product Go
never fires a lifecycle transition. If a transition genuinely cannot be
expressed (e.g. a cross-entity aggregate), the answer is an upstream
semstreams ask plus a documented interim — never a silent Go reconciler.
semspec ended with 5/6 edges in Go; that is the disease.

- **Pin**: conformance test asserting zero lifecycle-transition callers in
  product Go outside an explicit, ADR-linked exception table (target size: 0).

## G3 — No LLM-supplied outcome facts

Any pass/fail, exit-code, resolved/unresolved, or test-count fact is stamped
by the harness that executed the command. Measurement tool schemas take **no
outcome parameters** from the model. The model may claim; only the harness
records. (semspec G3; semteams' one structural gap.)

- **Pin**: schema conformance test over all registered tools — any tool whose
  schema accepts an outcome-shaped field (`pass`, `passed`, `exit_code`,
  `success`, …) from the caller fails the build.

## G4 — Outcome-based terminal gate

Nothing reaches done/PR without clean-room verification: fresh isolated
environment, resolve and build from the artifact's own declarations, run the
artifact's own tests. Per-shape floors are defense-in-depth; the clean room
is the gate that catches the fabrication shapes nobody enumerated yet.

- **Pin**: the M0 e2e journey asserts the verify step ran in fresh isolation
  (distinct build-cache home per run) and that a cache-masked fabrication
  fixture is rejected.

## G5 — Single writer per fact

Every fact (predicate) has exactly one writer. The writers table is a
checked-in artifact, not tribal knowledge — semspec's "sole writer" comments
were false within weeks.

- **Pin**: a writers-census conformance test: every predicate in the
  vocabulary maps to exactly one writing tool/rule in the checked-in table;
  new predicates fail until declared.

## G6 — Zero-token bug discipline

A bug reaching a paid run is a process failure, not just a code failure.
Every fix lands with an offline pin that reproduces the failure shape
red-first. Plumbing is proven by `go test` and the mock ladder, never by
paid tokens.

- **Pin**: the regression-manifest pattern (a test that fails when a named
  pin is deleted), started at M0 with its first entries.

## G7 — Evidence honesty

An evidence ledger exists from day one. Runs are recorded with honest
statuses (pass / exploratory / blocked / …); a run whose artifact fails
independent verification is never `pass`. E2E journeys must not write to
NATS, the graph, or state stores to make a claimed behavior pass;
fixture-seeded journeys are labeled bridge proof. Non-claims are maintained
in the brief.

- **Pin**: ledger schema validation test + the journey-honesty rule enforced
  in e2e harness review (checklist, cited in every journey PR).

## G8 — Fixture realism

Fixtures are plausible upstream repositories. They may document what a real
repo would document (README, build comments); they must never contain
orchestration-vocabulary coaching ("declare readiness…", "emit a blocker…").
Teaching the platform's contracts belongs in personas and retry feedback,
not in fixtures posing as upstream content.

- **Pin**: a lint over `test/fixtures/**` for the orchestration vocabulary
  list; violations fail.

## G9 — Minimal vocabulary

Predicates are added only by the OpenSpec change that needs them, named in
its spec delta. No speculative facts. semspec accreted 224 predicates;
semdev starts from zero and every predicate must point at the change that
introduced it.

- **Pin**: vocabulary exhaustiveness test — every registered predicate maps
  to an introducing change slug in a checked-in table.

## G10 — Docs tell the truth

Any doc that describes topology (components, packs, tools, transitions) is
either generated or pinned. semspec's CLAUDE.md described ten components
that did not exist; nobody could reason about the system from its docs.

- **Pin**: inventory conformance test — the README/architecture tables are
  compared against the actual registries (G1's, G5's) and fail on drift.
