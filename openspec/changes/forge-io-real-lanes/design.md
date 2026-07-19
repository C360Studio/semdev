# Design: forge-io-real-lanes

## Context

Verified against the tree (post-M1, `e15d4d4`):

- `internal/intake` holds the whole admission brain already: `Normalize`
  (framework `github_webhook` input's flattened `github.event.issue` payloads
  → host-neutral `Intake`; M0 trigger = issue `opened` with the
  sender==author attribution guard), the admission `Decide`/Event invariant
  (zero-token rejects), and `CoordinatorTask` (the wake with the M1-proven
  issue-content lane). NOTHING consumes the stream: the e2e journeys are the
  only publishers.
- `run.issue.ref` is vocab-declared (writer `issue-intake-adapter`) but
  deliberately unstamped — the mint rule's `deferred_issue_ref` metadata says
  stamping lands with the intake component, and the vocab writer moves to the
  spawn-rule name then.
- `internal/forge/github` is a token-authed client with permission checks and
  comments (the admission path's collaborator check). No PR-create, no branch
  push, no query-by-head-branch yet.
- `internal/tools/openpr` records `local-delivery:<run>` (the M0 stub) behind
  the graph-side read-before-create replay guard (reshape 7.4, shipped). The
  reshape's group 8 (real forge delivery: real API, local double, recorded
  real run) is deferred-open — THIS change discharges it; its group 7.1–7.3
  (restart reconstruction) stays deferred.
- The sandbox source at M0 is `StaticSource` (the fixture); the delivery
  lane's branch push needs a real remote — the SAME precondition the
  reshape's M2 notes name. This change takes the DELIVERY-side remote only
  (push the run's committed attempt branch to the configured repo); full
  per-run clone provisioning (self-target dogfood checkout) is the third M2
  change, not this one.

## Goals / Non-Goals

**Goals:**
- A live GitHub issue can drive the arc with no test code in the path, gated
  exactly as the admission spec demands.
- The human gates work from the issue (approve, see park messages), so
  dogfood runs are operable where the work lives.
- Delivery opens a real, evidence-bearing, doubly-idempotent PR; the stub
  dies.
- Everything e2e-provable stays zero-live-dependence (local forge double);
  live paths are env-gated like the real-LLM journey.

**Non-Goals:**
- Self-target checkout provisioning (the M2 change after this one) — the
  fixture remains the code-under-development for journeys.
- Restart reconstruction (reshape 7.1–7.3).
- Comment steering beyond approval + park-response (the upstream
  flattened-payload gaps for comment→run binding stand documented; the
  `respond` flow re-enters when the upstream fields land).
- The operator CLI driver (`experiment.Launch`) — separate change.

## Decisions

**D1 — The intake lane is a registered component (G1 note).** A rule cannot
consume a raw webhook stream, decode a host payload, run the admission
invariant, or build a prompt — this is exactly the component-shaped work the
primitives cannot express, mirroring the six deterministic stations'
justification. The component subscribes the GITHUB stream's issue subject
(durable consumer), runs `Normalize` → `Decide`, and on admission publishes
the wake via `intake.CoordinatorTask` + `PublishToStream` — the byte-shape
the journeys prove. Rejects: log + admission metric, zero publishes.

**D2 — `run.issue.ref` stamping stays rule-owned; the component carries the
ref in the wake's task metadata.** The run does not exist at wake time (the
mint rule creates it), so the component cannot stamp the run — and a Go
write racing the mint would be the semspec disease. Instead: the wake's
TaskMessage metadata carries the normalized ref (it already IS the TaskID
suffix, `intake:<ref>`), and the MINT RULE's `on_enter` gains an `add_triple`
stamping `run.issue.ref` on the minted run from `$entity` substitution —
settled at implementation against what the spawn context exposes; if
substitution cannot reach the ref, the fallback is the loop-entity route (the
framework stamps `agent.loop.task` = the wake's TaskID on the coordinator
loop; the rule threads the suffix). Either way the WRITER is the rule pack
(the vocab writer moves to the spawn-rule name exactly as the
`deferred_issue_ref` metadata planned) and G2 holds. The issue-CONTENT fact
does NOT move to the graph (the wake prompt is the lane; G9 — no new
predicate for content).

**D3 — Approval signal v1 = a comment command (`/semdev approve`) by an
authorized actor, consumed from the comment subject.** CAVEAT the
implementation must respect: the framework's flattened comment payload drops
the issue number (the documented upstream gap that defers the general
`respond` flow) — IF that gap still holds at implementation time, v1 approval
falls back to the LABEL path (`semdev-approved` applied by an authorized
actor on a `labeled` event carries the issue number) — the design mandates
whichever signal the CURRENT payload can bind to both the issue AND the
actor; the spec scenario is signal-agnostic on purpose. The adapter writes
`run.change.approved` with Source `approval-adapter` (the exact stand-in
shape the journeys prove; the resume rule is untouched). Run resolution:
ref → run via `run.issue.ref` (D2 makes it queryable).

**D4 — Park messages post through the existing park lane's publish.** The
park rules already `publish user.response.<instance>`; a small comms
component (or the intake component's second consumer — settled at
implementation by G1-minimality) consumes that subject and posts the parked
message as an issue comment via the GitHub client, resolving the issue from
`run.issue.ref`. No new predicate; posting failures log + retry bounded and
NEVER block the park itself (the park fact is already durable).

**D5 — Delivery: real API through the adapter seam, doubly idempotent.**
`openpr` gains the adapter call path: push the run's committed attempt branch
(`semdev/<run-suffix>`; the checkout's git objects exist at delivery time in
the SAME process — restart-orphaned deliveries stay in the reshape's
deferred half) to the configured remote with the token from the dotenv lane,
then create-or-adopt the PR: query by head branch FIRST (the forge-level
guard the reshape's M2 caveat demanded), create only on absence, body =
the evidence summary (verify/measure/review facts + trajectory pointer).
`delivery.pr.ref` = the real PR URL. The `local-delivery:` path is DELETED —
config with no forge = the tool errors loud (fail closed, no silent stub).
e2e speaks to a protocol-faithful local double (httptest server recording
request shapes, asserting branch-push + query + create ordering); the
recorded REAL-forge run (disposable repo, operator picks the target) is the
ledger requirement the M0-completion claim was already waiting on.

**D6 — Config surface.** Intake + delivery config live in the bootstrap
(target repo, allowlist knobs the admission spec already names, token env
names — `GITHUB_TOKEN` via the dotenv lane; never a token in config). The
env-gated live journey pattern (SEMDEV_REAL_LLM's gate discipline) applies to
any test that would touch the live forge.

## Risks / Trade-offs

- **[Upstream flattened-payload gaps constrain the approval signal]** →
  D3's signal-agnostic mandate: bind to whatever the current payload can
  attribute; the spec tests the OUTCOME (authorized approval releases the
  gate), not the signal shape. If neither comment nor label binds, the
  upstream ask gets filed and the gate keeps the journey stand-in — the
  change ships the other lanes rather than faking attribution (Event
  invariant is non-negotiable).
- **[Branch push needs the run's checkout alive]** → same-process delivery
  only (true today); restart-orphaned delivery stays parked by the
  station-failure-parks lane rather than half-delivering.
- **[A live webhook lane invites unsolicited events]** → the admission gate
  is the security door (already spec'd + pinned: authorized AND opted-in,
  actor-bound signals, zero-token rejects); the component adds NO admission
  logic of its own.
- **[Two changes touch the mint rule]** (this one's D2 `add_triple` and
  station-failure-parks' rules pack) → both are additive `on_enter` items in
  different rule files; implementation order free, noted for the reviewers.

## Migration Plan

Additive component + adapter paths; the one DELETION is the local-delivery
stub (its journeys move to the local double in the same group — the arc
keeps a green delivery station throughout). Rollback = revert; no persisted
state.

## Open Questions

- D3's live payload check (comment vs label attribution) — first
  implementation task, against the CURRENT semstreams webhook input.
- The local forge double's home (`internal/forge/forgetest` vs `test/`) —
  implementer's call with the reviewers.
