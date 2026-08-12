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
- Go 1.26+ (go.mod declares 1.26.3); semstreams pinned at `v1.0.0-beta.160` (started at beta.134; beta.147
  is the canonical-predicate + entity-ID breaking wave; beta.149 landed the #551
  per-loop executor tool-enforcement fix; beta.150 enforces the canonical predicate/entity
  contract FAIL-CLOSED at the graph-write boundary; beta.153 landed the three filed asks
  (#568/#569/#566); beta.154 is additive — the ADR-080 lesson substrate (semdev mints no
  lessons; adoption deferred by user decision 2026-08-11) + the #583 cache-race fix;
  beta.159 was the ADR-056 wave (contract-bound mutation client, `internal/graphown` derives
  the contracts from the vocab table); **beta.160 is the FINAL pre-v1 BREAKING wave
  (ADR-091 + Foundation B + the graph-query closure), migrated in
  `migrate-semstreams-beta160`** — tag = its own candidate-proof release (`8403a221`).
  OWNERSHIP IS DELETED: contracts validate local intent only (nothing registers, leases,
  or fences — the bind-only-what-you-write discipline is retired); one shared
  `projection.NewMutationClient` carries all 19 contracts; `graphown.Writer.Replace` rides
  `Reconcile` (SAME group-blast-radius semantics — the group is still each write's blast
  radius) with seam-owned bounded retry (revision-conflict ×3, transport ×4), and the
  admission birth is a strict Create whose CONFLICT is the idempotent-duplicate signal.
  Durable beta.160 hazards: (1) `graph.ingest.query.entity` replies the
  `{entity, kvRevision}` ENVELOPE — a bare-EntityState decode reads as silently EMPTY
  (this parked all 18 journeys once; readers must use `graph.ExactEntityReader`, pinned
  offline; CHECK RESPONSE SHAPES, not just subjects, on every migration); (2) a missing
  entity is a classified not-found on authority reads (the seam maps it to empty for
  emptiness-gated callers); (3) rule `add_triple` is must-exist + tuple-set-valued and
  `remove_triple` is a revision-fenced reconcile; (4) ports use the strict typed
  `config.kind` envelope (Go `Portable` + JSON), every mutating component declares the
  `semstreams.graph.mutation` v1 requester (built ONLY via
  `graphown.RequesterPortDefinition` — the census caps hand-rolled subjects at ZERO), and
  `ConfigureFromServices` composes AND seals the service set (no post-configure
  construction); (5) agentic-loop needs the `objectstore` storage component
  (AGENT_CONTENT) for trajectory evidence, tool discovery lives at `discovery.tool.list`
  (TOOL stream narrowed to `tool.execute.>`+`tool.result.>`), and adoption of a stable
  tag starts on FRESH NATS storage. semdev's 14 registered tools carry ADR-089
  worst-effect metadata, census-enforced);
  NATS via docker compose (never embedded).
- Mock ladder green before any real-LLM token. Real-LLM runs get watch
  sidecars and evidence-ledger entries.

## Status

**Phase 1 `conversation-channel-seam` (the pure carve) COMPLETE + ARCHIVED, and its
`pull-first-transport` follow-on COMPLETE + ARCHIVED (2026-07-20).** The conversation
half of the arc is carved behind a channel-neutral `Channel` port
(`Post`/`ResolveThread`/`Read` + a neutral `Message`): the GitHub v1 impl in
`internal/forge/conversation`, a shared `internal/intake/admission` core, and a
`conversation-channel` component (`internal/conversationchannel`) owning the
`/semdev approve` comment lane + park-post — carved out of a NARROWED `issue-intake`
(now the issue lane + webhook receiver only). `human.opt.signal`'s writer is the
channel-neutral `conversation-adapter` (G5 pivot); `human.opt.signal` +
`run.change.approved` capability tags → `conversation-channel`. **pull-first-transport**
added the port's `Read` verb + a POLLER so a webhook-unreachable deployment drives the
approval gate by polling each awaiting-approval run's thread (in-memory cursor B-2,
poll↔webhook XOR B-1, `Message.Author` auth H-1, a boot LOUD-WARN on the http_port-0 +
poll-off dead-lane combo); the webhook transport stays byte-identical and an optional
latency accelerator. Both changes: every group red-first, both adversarial reviewers
APPROVE (zero blocking/high), full `task e2e -race` GREEN (incl.
`TestBridgeProofApprovalByPollNoWebhook` on real docker), synced (conversation-channel
MODIFIED, still 12 caps). NL intent is Phase 2 (`nl-conversation-intent`); the
draft-PR review surface is Phase 3.

M0 walking skeleton COMPLETE end-to-end (mock-LLM, real containers), now on
**semstreams beta.154** (on the beta.147 breaking canonical-predicate + entity-ID wave;
beta.149 landed the #551 per-loop executor tool-enforcement fix; beta.150 hardens the
canonical contract fail-closed at graph-write — semdev's vocab already conforms, verified;
beta.153 landed all three filed asks: #568 `OpLengthGte`/`OpLengthLte` (routing-budgets unblocked),
#569 `LoopTerminalReason` from `event.Reason` (reason-aware escalate adoptable), and #566 the
health/flow-getter data-race fix — bumped as a clean compile+vet with the offline ladder, the flipped
#566 tripwire, and 3× green `-race` docker journeys as evidence; beta.154 is ADDITIVE — the ADR-080
push-based lesson substrate (new `agent.lesson.*` canonical predicates, `emit_lesson` builtin executor
[invisible to semdev's loops: every spawn's advertised-tools list is scoped and beta.149 enforcement
rejects unadvertised calls], deterministic brief-assembly injection of ACTIVE lessons scoped by role
tag / entity-ID prefix [wired by default; semdev spawns carry roles so each dispatch issues one benign
`graph.ingest.query.prefix` lesson listing — zero lesson records exist, briefs unchanged, read fails
silent-degrade], the orphan `processor/agentic-memory` retired [semdev never referenced it], and
`MetadataKeyAgentRole` stamped on every dispatched ToolCall [harness-derived role attribution,
additive metadata]) plus the graph-ingest #583 entity-query-cache stale-repopulation race fix — a
direct read-after-write-coherence win for semdev's rules/tools that read entities right after
concurrent writes (the 30s stale-cache window class). The lesson substrate is a NAMED M2+ adoption
opportunity, not a current dependency: a debrief seam distilling parked/failed runs into evidence-cited
`agent.lesson.*` records (born `proposed`, operator-gated to `active`) that future developer-role
dispatches receive at brief assembly — exactly the durable-lesson class M1 run 1 exposed. If semdev
ever runs the lesson lifecycle rulepack, its bootstrap must mirror `lessonRecordProjectionContract`.
OpenSpec changes on the `m0-walking-skeleton-spine` branch (draft PR):
`m0-walking-skeleton-spine` (the arc + evidence spine), `containerized-sandbox-dev-loop`
(the real sandbox + cold clean-room verify), `simplify-m0-execution-rail` (the
rule-native execution rail), and `forge-io-real-lanes` (M2's forge seam made REAL,
IMPLEMENTED except the operator-gated live-forge run [task 5.4]: the `issue-intake`
component owns BOTH webhook halves — the framework RETIRED its github-webhook input in
beta.147, so semdev owns the receiver [HTTP+HMAC+flatten→the semdev-declared GITHUB
stream, delivery-GUID dedup] AND the durable consumer [Normalize → the admission gate →
the admission-record entity (`intake.actor.admitted` finally stamped, content-derived ID
= idempotency backstop) → the coordinator wake]; the wake's TaskID is now the BARE issue
ref and `coordinator/04-stamp-issue-ref` stamps `run.issue.ref` rule-owned (RED/GREEN
journey-verified); the approval gate is operable FROM the issue [`/semdev approve`
comment → the approval adapter → the exact stand-in fact, proven end-to-end by
`TestBridgeProofWebhookIssueToApprovedRun` with NO stand-in writes]; park messages post
as issue comments [the intake component's USER-stream consumer]; and delivery is REAL —
the `local-delivery:` stub is DELETED, `openpr.Delivery` pushes the RECORDED verified
`attempt.commit.sha` [never HEAD] and creates-or-adopts the evidence-bearing PR
[query-by-head FIRST — doubly idempotent], every journey now delivers against a
protocol-faithful local forge double + a REAL bare-git-remote push, and an unconfigured
forge fails closed into the station-failure park. Both reviewers approve zero
blocking/high, all findings applied. The M0-completion live-forge delivery LANDED
2026-07-20 — see the self-target entry + the evidence ledger), and
`self-target-provisioning-and-launch-driver` (M2's dogfood FOUNDATION, groups 1–6
DONE, group 7 verify pending): the run PROVISIONS by CLONING the real target from
its own `run.issue.ref` — history preserved as the in-repo diff base `refs/semdev/base`
so a real PR diffs to the fix ALONE, the fixture path stays byte-identical —
config-selected via the `source.forge` block, fail-closed on ambiguity [design D5];
and `semdev launch <owner/repo#n>` mints one run against a live issue OUTBOUND through
`experiment.Launch` [pull-first — no webhook secret needed; NO Go lifecycle write, G2].
Bridge-proven OFFLINE by `TestBridgeProofSelfTargetForgeCloneToPR` [webhook front →
forge-clone provision → full arc → deliver back to the SAME bare remote → the delivered
diff `main..semdev/<suffix>` is the fix alone, history preserved; zero paid tokens, green
`-race` ~29s]; both reviewers APPROVE zero blocking/high (false-green impossibility
verified against real git). Its task 7.4 = forge-io's 5.4 IS DONE — **M0-COMPLETION
CLAIMED 2026-07-20**: the ONE recorded live-forge delivery, PR
https://github.com/C360Studio/semdev-test/pull/2 (real Gemini all-roles, cloned
`C360Studio/semdev-test#1` → developed → cold-verified → delivered the fix-alone diff
back to main; pull-first `semdev launch` + a stand-in `/semdev approve` through the real
approval adapter; converged attempts=1, ≈$0.178, ~2min; evidence-ledger M0-completion
entry). Both self-target + forge-io-real-lanes are now fully implemented (all tasks) and
ready to verify/archive (sync their deltas). ARCHIVED
(implemented + specs synced): `station-failure-parks` (M2's first unattended-safety
floor: `station.dispatch.failed` harness-stamped on retries-exhausted, the run-/loop-fired
park rules, the dispatch-entity census, the RED-first `TestBridgeProofStationFailureParks`
journey — the class that silently stalled real-LLM run 1 now parks toward the human),
`migrate-semstreams-beta147`, `adopt-per-task-routing-budgets` (#568 per-task attempt
budgets, shipped `79a884a`), `adopt-reason-aware-escalate` (#529/#569 transient grace
via the atomic-mirror classification, shipped `56b30a4` — the rule-engine double-dispatch
race chased, fixed, and pinned), and `integrate-semsource-ab-harness` (the A/B instrument,
shipped `da38652`: condition-gated read tools behind a parity-pinned variant pack + a
fail-closed per-signal launch gate; the semsource-condition plumbing journey proven green
against a live semsource — both reviewers approve; the M1 real driver must mint via
`experiment.Launch`).
`openspec/specs/` now holds the CANONICAL synced capability specs (the stacked
implemented deltas merged, oldest→newest, INCLUDING routing-budgets + reason-aware +
semsource-ab — now 9 caps); the recorded evidence ledger is `docs/evidence-ledger.md`
(G7 — M0 claimed on named bridge proof, M1 not claimed).
**M1 IS CLAIMED (2026-07-19)**: `TestRealLLMJourneyIssueToPR` CONVERGED on run 2 —
the full issue→PR arc with REAL Gemini turns (gemini-3.1-pro-preview, all roles):
model-authored change CLI-validated first try, real diff measured GREEN
in-container, floors passed, Quinn approved, clean-room verify PASSED,
`delivery.pr.ref` (M0 local stub), attempts=1, 84s, ≈$0.18 token-reconciled
(ledger entry + run 1's honest projection failure recorded in
`docs/evidence-ledger.md`). Run 1 taught the includes-test contract gap
(enforced-but-uncommunicated) — fixed with a red-first schema-description pin.
The **first-real-llm-journey** change (groups 1–6 + the paid runs done) landed the real-LLM
launch surface: the issue-content lane (wake carries the admitted issue's authored
text; persona contract makes create_change reasons preserve the ask — the mock had
papered over the model never seeing the issue), the env-gated real-LLM journey
(`test/e2e/realllm_journey_test.go`, `SEMDEV_REAL_LLM=1`; keyless/malformed
declarations fail loud) against the framework's FIRST-CLASS Gemini route
(operator constraint: Anthropic rates unaffordable; provider `gemini` +
`wire_backend: wire`, `gemini-3.1-pro-preview`, `GEMINI_API_KEY` — the
`configs/gemini-example.json` shape; NOTE beta.153 has NO native anthropic
adapter — an Anthropic run would need its OpenAI-compat endpoint, never
`provider:"anthropic"`), `docs/real-llm-runbook.md` (sidecar commands
dry-run-proven), and the Taskfile operator lane `realllm:probe`/`launch`/
`status`, plus the gitignored `.env` dotenv lane for the key (semspec pattern).
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
