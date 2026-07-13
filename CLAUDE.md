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
- Go 1.25+; semstreams current release (started at `v1.0.0-beta.134`); NATS
  via docker compose (never embedded).
- Mock ladder green before any real-LLM token. Real-LLM runs get watch
  sidecars and evidence-ledger entries.

## Status

M0 walking skeleton COMPLETE end-to-end (mock-LLM, real containers). Two
sibling OpenSpec changes on the `m0-walking-skeleton-spine` branch (draft PR):
`m0-walking-skeleton-spine` (the arc + evidence spine) and
`containerized-sandbox-dev-loop` (the real sandbox + cold clean-room verify).
The full arc runs against real docker: front door → issue_intake → create_change
→ validate → **human approval** → project task.spec → provision + prove-cold
sandbox → dispatch (Amelia) → apply_patch → measure IN-CONTAINER → structural
floors → gate (advance/retry/escalate) → review (Quinn) → **cold clean-room
verify** of the committed artifact → coherence gate → open_pr → `pr.ref`. Proven
by `test/e2e/journey_test.go` (16 stations, zero paid tokens) plus docker-gated
cold-proof pins. Remaining before a first real-LLM token: the pre-real-LLM
carry-forwards (bootstrap `allowed_tools` scoping; the retry/escalate + blocked-
park e2e stations; the `target_files`-includes-test contract) tracked in
`containerized-sandbox-dev-loop`'s design.md. Donor checkouts for reference:
`~/Code/c360/semteams` (shape), `~/Code/c360/semspec` (floors + audits).
