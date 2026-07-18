# CLAUDE.md

semdev: GitHub issue in → reviewed, clean-room-verified PR out. Built on
semstreams primitives — rules, personas, facts, and a small set of
deterministic tools. Successor to semspec (donor/forensics) taking semteams'
shape. **Read these three documents before changing anything:**

| Document | Purpose |
|----------|---------|
| [docs/brief.md](docs/brief.md) | What semdev is, v1 shape, milestone ladder, non-claims |
| [docs/constitution.md](docs/constitution.md) | The ten guardrails and their enforcement pins — law, not guidance |
| [docs/port-manifest.md](docs/port-manifest.md) | Exactly what enters from semteams/semspec, and the banned-pattern list (B1–B10) |

## Working rules (the constitution, operationalized)

- **Primitive-first (G1)**: before writing Go, prove a rule + persona + fact +
  existing component can't do it. New tools/components require a
  framework-alignment note and a registry entry.
- **Rules own the lifecycle (G2)**: product Go never fires a lifecycle
  transition. Engine gap? File the upstream semstreams ask and park toward
  the human — never a silent Go reconciler.
- **No LLM-supplied outcomes (G3)**: measurement facts are stamped by the
  harness that ran the command. Tool schemas must not accept outcome booleans.
- **Clean room before done (G4)**: nothing reaches PR without fresh-isolation
  verification of the artifact's own declarations and tests.
- **One writer per fact (G5)**, **pins before/with every fix (G6)**,
  **honest evidence (G7)**, **realistic fixtures (G8)**, **minimal vocabulary
  (G9)**, **docs match reality (G10)**.
- Fix commits include the red-first offline pin for their failure shape. A
  bug that reaches a paid run is a process failure — write the pin, then fix.
- Ports from semspec/semteams go through the manifest only. If you find
  yourself re-creating something on the B1–B10 banned list, stop and surface
  it.

## Development

- OpenSpec drives all non-trivial work: `/opsx:new` → artifacts → apply →
  verify → archive. semdev also *produces* OpenSpec changes as its product —
  keep the two roles distinct (our changes live in `openspec/`; product
  changes live in the target repo's workspace).
- Conventional commits: `<type>(scope): subject`.
- Go 1.25+; semstreams pinned at `v1.0.0-beta.148` (started at beta.134; beta.147
  is the canonical-predicate + entity-ID breaking wave); NATS via docker compose
  (never embedded).
- Mock ladder green before any real-LLM token. Real-LLM runs get watch
  sidecars and evidence-ledger entries.

## Status

M0 walking skeleton COMPLETE end-to-end (mock-LLM, real containers), now on
**semstreams beta.148** (on the beta.147 breaking canonical-predicate + entity-ID wave).
OpenSpec changes on the `m0-walking-skeleton-spine` branch (draft PR):
`m0-walking-skeleton-spine` (the arc + evidence spine), `containerized-sandbox-dev-loop`
(the real sandbox + cold clean-room verify), `simplify-m0-execution-rail` (the
rule-native execution rail), and `migrate-semstreams-beta147` (the beta.147 sweep).
The full arc runs against real docker: front door → issue_intake → create_change
→ validate → **human approval** → project task.spec → provision + prove-cold
sandbox → dispatch (Amelia) → apply_patch → measure IN-CONTAINER → structural
floors → route (advance/retry/escalate) → review (Quinn) → **cold clean-room
verify** of the committed artifact → coherence route → open_pr → `delivery.pr.ref`.
Proven by `test/e2e/journey_test.go` — all four bridge-proof journeys (happy +
retry + rejection + exhaustion) green on beta.148 (atop the beta.147 sweep) with zero paid tokens and zero
predicate/entity-contract rejections, plus docker-gated cold-proof pins.
beta.147 facts are CANONICAL (3-seg lower-kebab, declared via `internal/vocab.Register`);
every rule carries an `entity.pattern` (required to fire on the entity-state lane).
The beta.148 tripwires (#519 scalar `.value`, #528 per-spawn max_iterations, #529 typed
exhaustion sentinel) are now REGRESSION GUARDS — the fixes landed; the routing-behavior
UPGRADES they enable (per-task iteration/attempt budgets, a reason-aware escalate route)
are deferred M0.5 follow-ups, not required (the uniform-cap / literal-3 / outcome=failed
behavior stays e2e-proven). The pre-real-LLM carry-forwards are now resolved or correctly
tracked: the `target_files`-includes-test contract and the developer/tool-schema field
descriptions are DONE; the `allowed_tools` scoping is done at the MODEL boundary (every spawn
advertises a scoped `tools` list ⊆ populated `allowed_tools`, two conformance pins) — but its
residual **MEDIUM-3** is an EXECUTOR-side defense-in-depth gap that is a FRAMEWORK limitation,
not an in-tree fix: verified against beta.148, `agentic-tools` admits a call solely on its
GLOBAL `allowed_tools` (`component.go isToolAllowed`), never the loop's advertised set. It is an
UPSTREAM semstreams ask (per-loop/role executor enforcement) FILED as semstreams #551, tracked by
the gap-open tripwire `TestTripwireExecutorHonorsPerLoopToolAllowlist`, and it BLOCKS the first
real-LLM token (inert under the mock; mock-ladder-green is the backstop). Donor checkouts for reference:
`~/Code/c360/semteams` (shape), `~/Code/c360/semspec` (floors + audits).
