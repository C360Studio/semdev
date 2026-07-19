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
- Go 1.26+ (go.mod declares 1.26.3); semstreams pinned at `v1.0.0-beta.153` (started at beta.134; beta.147
  is the canonical-predicate + entity-ID breaking wave; beta.149 landed the #551
  per-loop executor tool-enforcement fix; beta.150 enforces the canonical predicate/entity
  contract FAIL-CLOSED at the graph-write boundary — semdev's vocab already conforms; beta.153 landed
  all three filed asks — #568 `OpLengthGte`/`OpLengthLte`, #569 `LoopTerminalReason` stamped from
  `event.Reason`, #566 the `rule.Processor` health/flow-getter data-race fix — a clean compile+vet bump);
  NATS via docker compose (never embedded).
- Mock ladder green before any real-LLM token. Real-LLM runs get watch
  sidecars and evidence-ledger entries.

## Status

M0 walking skeleton COMPLETE end-to-end (mock-LLM, real containers), now on
**semstreams beta.153** (on the beta.147 breaking canonical-predicate + entity-ID wave;
beta.149 landed the #551 per-loop executor tool-enforcement fix; beta.150 hardens the
canonical contract fail-closed at graph-write — semdev's vocab already conforms, verified;
beta.153 landed all three filed asks: #568 `OpLengthGte`/`OpLengthLte` (routing-budgets unblocked),
#569 `LoopTerminalReason` from `event.Reason` (reason-aware escalate adoptable), and #566 the
health/flow-getter data-race fix — bumped as a clean compile+vet with the offline ladder, the flipped
#566 tripwire, and 3× green `-race` docker journeys as evidence).
OpenSpec changes on the `m0-walking-skeleton-spine` branch (draft PR):
`m0-walking-skeleton-spine` (the arc + evidence spine), `containerized-sandbox-dev-loop`
(the real sandbox + cold clean-room verify), `simplify-m0-execution-rail` (the
rule-native execution rail), and `adopt-per-task-routing-budgets` (designed; semstreams #568
landed in beta.153 → implementation UNBLOCKED, next in the arc); `migrate-semstreams-beta147`
(the beta.147 sweep) is archived.
`openspec/specs/` now holds the CANONICAL synced capability specs (the stacked
implemented deltas merged, oldest→newest — the unimplemented routing-budgets delta
deliberately NOT synced); the recorded evidence ledger is `docs/evidence-ledger.md`
(G7 — M0 claimed on named bridge proof, M1 not claimed).
The full arc runs against real docker: front door → issue_intake → create_change
→ validate → **human approval** → project task.spec → provision + prove-cold
sandbox → dispatch (Amelia) → apply_patch → measure IN-CONTAINER → structural
floors → route (advance/retry/escalate) → review (Quinn) → **cold clean-room
verify** of the committed artifact → coherence route → open_pr → `delivery.pr.ref`.
Proven by `test/e2e/journey_test.go` — all four bridge-proof journeys (happy +
retry + rejection + exhaustion) green on beta.153 (atop the beta.147 sweep) with zero paid tokens and zero
predicate/entity-contract rejections (beta.150's fail-closed graph-write gate stamps none), plus
docker-gated cold-proof pins. The journeys now run WITH `-race` (`task e2e`): the PRE-EXISTING framework
data race (`rule.Processor.Health()`/`DataFlow()` wrote under a read lock, filed as semstreams #566) is
FIXED in beta.153 — the getters derive into a local copy under RLock — and the `-race` journeys ran green
3× in a row on the bump; the `TestTripwireProcessorHealthRaceUnfixed` gap-open tripwire is now the
`TestTripwireProcessorHealthRaceFixed` regression guard.
beta.147 facts are CANONICAL (3-seg lower-kebab, declared via `internal/vocab.Register`);
every rule carries an `entity.pattern` (required to fire on the entity-state lane).
The beta.148 tripwires (#519 scalar `.value`, #528 per-spawn max_iterations, #529 typed
exhaustion sentinel) are now REGRESSION GUARDS — the fixes landed; the routing-behavior
UPGRADES they enable (per-task iteration/attempt budgets, a reason-aware escalate route)
are deferred M0.5 follow-ups, not required (the uniform-cap / literal-3 / outcome=failed
behavior stays e2e-proven). The pre-real-LLM carry-forwards are now RESOLVED: the
`target_files`-includes-test contract and the developer/tool-schema field descriptions were done,
and the `allowed_tools` scoping is now COMPLETE end-to-end. It was done at the MODEL boundary
(every spawn advertises a scoped `tools` list ⊆ populated `allowed_tools`, two conformance pins),
and its residual **MEDIUM-3** — the EXECUTOR-side enforcement backstop, a FRAMEWORK limitation not
closable in-tree — was filed as semstreams #551 and **LANDED in beta.149**: `agentic-loop` now
stamps `agent.tools.advertised` (the loop's cached `tools`) on every tool call and `agentic-tools`
`admitToolCall` rejects a call outside the advertised set (`ToolErrorPermission`), so semdev's
already-scoped lists became load-bearing at execution on the bump alone (no rule/config change).
The tripwire `TestTripwireExecutorHonorsPerLoopToolAllowlist` is now a REGRESSION GUARD; MEDIUM-3 no
longer blocks the first real-LLM token. Donor checkouts for reference:
`~/Code/c360/semteams` (shape), `~/Code/c360/semspec` (floors + audits).
