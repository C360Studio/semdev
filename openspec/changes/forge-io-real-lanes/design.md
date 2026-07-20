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

CARRY-FORWARD from station-failure-parks (named by its review): if/when this
change (or a successor) adds a RESUME action for a parked run, the resume must
clear the park's WHOLE fact set, not just the marker the pre-parks resume knew:
`run.awaiting.human` AND `station.park.routed` AND `station.dispatch.failed`.
`station.park.routed` never self-clears and (run-fired half) lives on the RUN,
so a resumed run whose station fails terminally a SECOND time could never
re-park — re-opening the silent-stall class station-failure-parks closed. All
parks are terminal-until-resume today, so this is a resume-lane obligation,
not a current defect.

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

## As-built settlements (group 1 verification, 2026-07-19)

**1.1 — THE FRAMEWORK WEBHOOK INPUT NO LONGER EXISTS.** The design's Context
("the framework `github_webhook` input's `github.event.issue` subjects") was a
pre-beta.147 fossil: `input/github-webhook` — the FULL receiver (HTTP listener,
HMAC-SHA256 validation, event filter, flatten, publish `github.event.*`) — was
DELETED in the beta.147 boundary wave (semstreams `a533306e`, ADR-075), and the
sister-repo cutover checklist (semstreams docs/operations/31) explicitly
transfers ownership to semdev: "own the GitHub executors, webhook types, and
workflow/rule policy," with the two flattened-payload gaps transferred as
SEMDEV backlog (#2 comment parent number/id, #3 specific added/removed label)
— they are OURS to close, not upstream asks. Consequences:
- D1 grows the RECEIVER half: the intake component owns BOTH the HTTP receiver
  (HMAC, event filter, flatten → publish `github.event.*` onto the GITHUB
  stream, which semdev's bootstrap must now DECLARE — the framework no longer
  brings it) and the durable consumer (Normalize → Decide → wake). One
  component, two lanes; the removed framework component is the receiver's
  reference shape.
- Task 2.5's journey shape is exactly right: publish RAW `github.event.*`
  payloads onto the GITHUB stream (no HTTP) to drive the consumer half; the
  receiver half gets unit pins (HMAC accept/reject, flatten shape). Live HTTP
  stays env-gated/manual.
- Task 3.4's condition RESOLVES: the payload shape is semdev-defined now, so
  the comment-event flattening CAN carry the issue number — no upstream ask.

**1.2 — D2 SETTLED: the loop-entity rule route; the mint-rule add_triple route
is impossible.** Verified in the engine (`processor/rule/actions.go`
run_scope=new): the mint passes ONLY org/platform/firing-loop-ID to the
lifecycle manager — publish_agent `properties` never reach the run entity, and
the run's ID is not substitutable at mint-rule fire time. Settled route:
- `intake.CoordinatorTask` TaskID becomes the BARE ref (`owner/repo#number`,
  dropping the `intake:` prefix — verified: nothing binds on it; the journeys
  bind on the returned TaskID transparently). The framework stamps
  `agent.loop.task` = the ref on the front-door coordinator loop.
- A new coordinator-pack rule stamps `run.issue.ref` =
  `$entity.triple.agent.loop.task.value` onto
  `$entity.triple.agent.run.entity-id`, gated on
  `coordinator.decision.next-action eq issue_intake` — the ONLY coordinator
  loops with that decision are front-door intake loops, so the rewake/authoring
  coordinators (whose agent.loop.task is a rule-minted task id, NOT a ref) can
  never mis-stamp. Ref add FIRST, one-shot marker second (the
  station-failure-parks park-first lesson: a marker-set-ref-absent crash window
  would strand a ref-less run; the re-fire this allows appends a benign
  duplicate identical triple).
- G9 cost: one marker predicate (`run.issue.stamped`); `run.issue.ref`'s vocab
  writer moves to the rule-pack name exactly as the mint rule's
  `deferred_issue_ref` metadata planned.

**D3 SETTLED (per 1.1): v1 approval = the comment command `/semdev approve`.**
semdev's OWN flattener defines the comment payload: issue number + comment
author + body + sender (closing transferred backlog #2 for this flow).
Attribution: the sender==comment-author guard (the same Event-invariant shape
`opened` uses). Authorization reuses the admission core (`authorize`:
allowlist, else push-capable collaborator via the github client). Run
resolution: prefix-list `*.*.agent.chain.execution.*` via
`graph.ingest.query.prefix` (the framework's lesson-reader pattern), filter
`run.issue.ref` == the ref.

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

- **[Park→comment lane has no wire-level proof]** (review LOW, tracked not
  fixed): the user.response consumer + posting are unit-pinned only; every
  journey runs it commenter-less (no token), so a subject-filter regression
  would be invisible (posting is best-effort by design; the park fact stays
  durable + graph-visible). Named follow-up: a small NATS-backed integration —
  publish user.response.X onto USER, assert the double records create_comment.
- **[Token rides `git push` argv]** (review LOW, tracked not fixed): the
  credentialed https push URL is a process argument, visible in `ps` for the
  push's duration on the semdev host itself. Threat model is own-host at M2;
  the clean fix is env-injected git config (`GIT_CONFIG_COUNT` /
  `http.extraHeader`) which needs a small env extension on `cliexec.Runner` —
  named follow-up, not this change.

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
