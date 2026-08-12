## Context

semdev turns a GitHub issue into a reviewed, clean-room-verified PR, built
almost entirely from semstreams primitives (rules, personas, facts) with
deterministic tools only where the constitution's G1 gate admits them. This is
the founding change: the M0 walking-skeleton spine, on a mock LLM against a
light fixture repo, at zero paid tokens (G6).

Research against the current framework (semstreams `v1.0.0-beta.141`) and the
two donors established the ground this design stands on:

- **Five of seven capabilities are semstreams *reuse*, not new Go.** The bounded
  dev loop (`agentic-loop`), the exit-code→fact measurement path (`agentic-tools`
  `bash`), the run entity (`agentic/agentrun`), rule-owned lifecycle, and the
  GitHub tools/webhook all exist today. New Go is a thin set of adapters.
- **The four engine gaps semteams suspected at beta.115 are all closed**
  (guaranteed re-evaluation, re-entrant iteration budgets, foreign-bucket watch,
  cap-exhaust escalate). Fan-out/fan-in also exist (`for_each` + synchronizer /
  gated-DAG). **No upstream ask is warranted for M0.**
- **The make-or-break is the clean-room / harness.** The predecessors' two hard
  e2e scenarios (OSH MAVSDK + Meshtastic drivers) are hard because of *infra*
  (auth'd coordinates, git submodules, source-substitution, vendored native
  blobs, toolchain pins) — not driver logic. The container technology is the
  easy part; cache-home freshness and dependency resolution are the hard,
  container-agnostic parts.
- **OpenSpec compatibility is the sponsor's one hard requirement.** semteams has
  a dep-free, tested, bidirectional format engine to port; semspec contributed
  the graph-first framing ("hydrate from the flow; prose is a side effect") and
  brownfield-ingest governance.

## Goals / Non-Goals

**Goals:**
- Prove every station of the arc connects and the load-bearing pins (G2/G3/G4/G7)
  are real, on a mock LLM, zero paid tokens.
- Establish, once, the architecture the rest of semdev builds on: the
  capability→primitive map, the persona roster, the fact vocabulary + writers
  table, the clean-room Runner + reproducibility-contract harness, and the
  OpenSpec hydrate/ingest seam.
- Keep the new-Go surface minimal and G1-gated (alignment note + registry entry
  per addition).

**Non-Goals:**
- Real-LLM runs, parallelism, liveness/watchdog surface (per proposal).
- **Auto-spawned blocking harness-fix child runs** — the mechanism is designed
  here; the *autonomy* earns its way in later (v1 posture: operator-authority
  park).
- Building the JVM/OSH toolchain profile — M0 exercises only the Go profile; the
  hard profile is M2.
- Any BMAD *process* (phases, story sharding, story-tier gates, CI-execution).

## Decisions

### D1 — Reuse semstreams primitives; new Go is thin adapters (G1)

| Capability | semstreams reuse | New Go (each needs a G1 note + registry entry) |
|---|---|---|
| `run-lifecycle` | rules (phase-as-fact / `pkg/lifecycle` + `lifecycle_transition` actions); `agentic/agentrun` as the run entity | rule packs; persona fragments; the closed action taxonomy |
| `forge-io` | `input/github-webhook`; `github_read`/`github_write` tools | deterministic allowlist admission check; normalized-fact mapping; 1–2 thin comment tools |
| `dev-from-task` | `agentic-loop` (bounded loop); governed facts (ADR-055/056); `project_spec_tasks` pattern (T2) | budget clamp `[1,5]` at stamp; escalate rule; floor checks as pure functions (S1) |
| `harness-measurement` | `agentic-tools` `bash` (OS exit-code → fact — native G3) | measurement tool schema (no outcome field); review-verdict rule wiring |
| `clean-room-verify` | Runner/`SANDBOX_URL` client seam; ADR-067 read-only tripwire | the Runner (Mock/local/CLI); ported `verify.Decide` (pure); resolution-proof tool; the reproducibility-contract harness |
| `evidence-ledger` | trajectory / loop-execution entities; governed state | ledger schema + status vocabulary (port semspec S10) |
| `openspec-io` | rule→`publish`→`output/file` (hydrate); raw-lane→projector→`graph-ingest` (ingest) | ported semteams format engine (library); ingest + hydrate + `WriteChange`-to-workspace tools; brownfield projector; CLI-validate step |

*Alternative rejected:* a component per concern (B6). Most of the arc is rule
packs + persona fragments over reused components.

### D2 — Rules own the lifecycle; milestone-fact model, no phase enum (G2)

The run advances by rules matching facts. There is no authoritative `run.phase`
predicate that any Go sets; the run's position is *derived* from which milestone
facts exist (`run.issue_ref`, `run.change_approved`, `task.spec`,
`measurement.result`, `review.verdict`, `verify.result`, `pr.ref`). Terminals are
rule-owned via `agentrun`'s participant + a `lifecycle_transition` rule. An
engine gap parks toward the human, never a Go reconciler.
*Alternative rejected:* a Go state machine (semspec's 5/6-edges-in-Go disease, B3).

### D3 — Personas: BMAD names, none of BMAD's process

Three persona fragments (semdev is a thin pipeline, not an agile team). Personas
are cosmetic prompt fragments attached to roles; **they never write facts** (G5).

| Role | BMAD name | Owns (taxonomy) |
|---|---|---|
| Coordinator | **Sarah** (PO) | `issue_intake`, `create_change`, `open_pr`, `ask_human`, `respond` |
| Developer | **Amelia** (Dev) | `dev_from_task`, drives `verify` (runs commands, declares nothing) |
| Reviewer | **Quinn** (QA — deliberately not Murat/TEA) | semantic-review facet gating `open_pr` |

**Not adopted** (each is a G1/G2 violation if imported): John/PM (PRD phase),
Winston/Architect (`ArchitectureDocument`), Bob/SM (story sharding), Murat/TEA
(story-tier gates), Sally/UX, Paige/tech-writer, `bmad-master`, and any
recovery/wedge-manager role. Rule of thumb for reviewers: **a BMAD name is fine;
a BMAD phase, document, shard, or gate is a violation.**
*Evidence:* `semspec-ui-bmad` proved every BMAD *process* import spawned a failure
class (M:N story wipe, SITL infinite-reject gate, CI-executor over-scope).

### D4 — Measurement is harness-stamped; review gates on facts (G3)

The `bash` executor already captures the OS exit code and the model supplies only
the command — semdev's measurement tool simply must not add an outcome field to
its schema. The reviewer (Quinn) reads `measurement.result`, not the model's
claim, and issues an additive-only per-task `review.verdict.<i>`. No persona owns the outcome.

### D5 — Clean room = a thin swappable Runner seam; isolation is product-supplied

> **SUPERSEDED (M0) by `containerized-sandbox-dev-loop`.** The sibling change moved
> CONTAINER isolation to M0 (not M2): the clean room is now a real per-run `docker`
> container built from an OPERATOR-DECLARED image, and `cleanroom.ContainerRunner` is the
> production `Runner`; `LocalRunner` demoted to a test shim. The "A/B `docker run` at M2"
> deferral and the local-`ExecIsolated` production path below are RETIRED. What still
> holds verbatim: the thin `Runner` seam (`Up`/`Exec`), cache-home freshness as the one
> universal G4 control, the ported pure `verify.Decide` + resolution proof, and leaving
> semspec's `execution-bridge` reconciler (B3) behind. See that change's design SB1–SB8 +
> its Migration Plan for the as-built shape (the dev loop + cold verify run in containers
> end-to-end). This note keeps the two changes coherent (task 9.1).

semstreams ships only an HTTP client to an external sandbox (`SANDBOX_URL`) and a
git-diff *tripwire* (ADR-067, detection not containment); `pkg/sandbox` is
proposal-only. So semdev owns a thin `Runner` seam (`Up`/`Exec`, mirroring
semteams) with `Mock` / local-`ExecIsolated` / CLI implementations. **The one
universal G4 control is cache-home freshness** — every ecosystem has a package
cache that masks a broken build; a fresh cache home per proof is mandatory and
orthogonal to the container choice. We port semspec's **pure `verify.Decide`**
and the **resolution-proof logic**, rule-wired — and explicitly **leave behind**
its `execution-bridge` reconciler shell (B3).
*Alternatives rejected:* devcontainer as the *only* isolator (it doesn't solve
cache-home or source-substitution — A/B it vs `docker run` at M2); semspec's
fresh-cache-in-warm-container (weaker than a fresh environment).

### D6 — Harness is operator-provisioned + init-proven, not dynamically synthesized

We take a little operator-setup DevX to **delete the second agentic team**
semteams needed. `semdev init`: **harvest → propose → prove-cold → commit** a
reproducibility-contract manifest. Init distinguishes **ambient** repo
infrastructure (harvested: toolchain pins, how deps resolve, tier split) from
**task-introduced** deps (the agent adds; verified by a cold `--recursive` clone
of the PR). The manifest holds refs + pins, never re-derived coordinates:
toolchain pins, credentials-as-refs, submodule SHAs, source-substitution map,
native-asset presence, tiers.
*Alternative rejected:* semteams' dynamic sandbox-manager team (extra loop; and
dynamic materialization of arbitrary toolchains is itself the flaky hard thing).

### D7 — Language-agnostic harness: schema + Runner common; ecosystems are profiles (data)

The reproducibility-contract *schema* and the *Runner* are common; per-language
specifics live in declarative **profiles** (a detector + conventions), not a
component per language (anti-B6). OSH/Java is just the hardest profile.

| Profile | toolchain | cache-home (universal control) | resolve | test | hard fields |
|---|---|---|---|---|---|
| **Go** (M0) | `go 1.26` | `GOMODCACHE`/`GOCACHE` | `go mod download` | `go test ./...` | — |
| TS/JS | `node`+pnpm | pnpm store | `pnpm i --frozen-lockfile` | `pnpm test` | — |
| Rust | `rustc` | `CARGO_HOME` | `cargo fetch` | `cargo test` | — |
| Python | `python`+uv | uv/pip cache | `uv sync` | `pytest` | — |
| JVM/OSH (M2) | `jdk 17`+`gradle 8.10.2` | `GRADLE_USER_HOME`+m2 | `gradlew --refresh-dependencies` | `gradlew test` | creds-refs, submodules, source-substitution, native blobs |

Greenfield repo = scaffold from the declared profile and prove even a hello-world
builds cold. Init auto-detects the profile from repo markers (`go.mod`,
`Cargo.toml`, `package.json`, `pyproject.toml`, `build.gradle`).

### D8 — Verification-capability-first readiness gate

Before dev builds toward a claim, a deterministic T5 check reads the harness
manifest: is there a `sandbox`-scope tier that proves it? **Yes** → build, harness
stamps (G3), clean room verifies (G4). **Only `operator-ci`** → build, but the
proof is deferred-and-noted (G7), never gated in-sandbox, never faked. **No tier**
→ park toward the human. The sandbox/operator-CI line is *harvested from the
repo's own test config* (OSH already excludes its SITL tests) — the operator
barely draws it.

### D9 — Harness evolution is a harness-fix change through semdev's own arc

A harness gap yields a harness-fix change (`target_file: .semdev/harness.yaml`)
through the same arc: same two gates, same cold readiness proof. Two trigger
policies over one mechanism — **manual `/semdev update`** (v1 default) and
**auto-spawned blocking child run** (earns its way in; parent blocks on the
child's terminal fact via `agentrun` ancestry + a resume rule, G2). Guards:
(1) the child runs against a **minimal bootstrap harness** (readiness proof only)
so it cannot hit the same gap and recurse; (2) an agent may *propose* a harness
change but a **floor flags any gate-weakening** (tier downgrade, looser proof) and
the operator approves — the verified cannot silently lower its own bar.

### D10 — forge-io: reuse GitHub tools; arc reads normalized facts; allowlist at the door

No forge-abstraction component (B6). Reuse `github-webhook` + `github_read`/
`github_write`; the arc depends only on normalized issue/PR/comment facts, so the
GitHub tools are the copy-pattern for other hosts. Add 1–2 thin comment tools for
`ask_human`/`respond`. **Intake admission is a deterministic, zero-token gate**:
only a repo collaborator or allowlisted actor, opted in by a `semdev` label/
command, creates a run — everything else is rejected before a token is spent. This
is not just anti-spam; it keeps semdev inside the *non-adversarial* posture the
sandbox threat model assumes.

### D11 — openspec-io: port the format engine; hydrate + ingest + CLI-oracle

Port semteams' `cmd/semteams/openspec/` as a **dep-free Go library** (parse /
render / `ReadChange`/`WriteChange` / `Facts()`↔`FromFacts`) — **no new format
Go**; re-vocabulary to semdev predicates (G9). Hydrate-out renders artifacts from
graph facts (G10-truthful). Ingest-in parses brownfield artifacts to facts
deterministically (G3-safe), preserving raw bytes by reference for provenance.
**Shell out to the real OpenSpec CLI** (`validate <change> --strict --json
--no-interactive` — target the change explicitly; the bare form is
interactive-only and fails non-interactively — and `archive <change> -y --json`)
as the compatibility oracle — the honest "compatible" claim.
Build the one thing semteams deferred: the `WriteChange`-to-workspace tool for the
PR. Round-trip fidelity (parse ≈ render⁻¹) is the compat test; semdev's own repo
is the first fixture.

### D12 — Fact vocabulary (G9) + single writers (G5)

The predicates this change introduces, each with exactly one writer. This table
is the checked-in artifact the G5/G9 pins compare against.

| Predicate | Single writer | Capability |
|---|---|---|
| `intake.actor`, `intake.admitted` | deterministic admission check | forge-io |
| `run.issue_ref` | issue-intake adapter | forge-io |
| `run.change_approved` | approval adapter (from human signal) | forge-io |
| `human.signal` | comment adapter (from human reply) | forge-io |
| `run.awaiting_human` | park rule | run-lifecycle |
| `pr.ref` | open_pr tool (M0 local-delivery stub; forge-io adapter at M2) | forge-io |
| `openspec.change.*` | create_change author tool | openspec-io |
| `openspec.validated` | harness running `openspec validate` | openspec-io |
| `openspec.archived` | harness running `openspec archive` | openspec-io |
| `task.spec` | task projector | dev-from-task |
| `task.attempt` | dev-loop harness | dev-from-task |
| `floor.finding` | floor tools | dev-from-task |
| `measurement.result` | executing harness | harness-measurement |
| `review.verdict.*` | reviewer (Quinn) | harness-measurement |
| `verify.result` | verify harness | clean-room-verify |
| `evidence.run` | evidence ledger | evidence-ledger |

### D13 — Upstream asks (two forge-io payload gaps + one runtime deadlock)

The four beta.115-era suspicions are closed; fan-out/fan-in exist. Two real gaps
surfaced building forge-io intake, both in the `github_webhook` input's
**flattened** published payload (it drops fields the raw GitHub webhook carries);
a third — a framework shutdown deadlock — surfaced standing up the agentic-execution
plane (UA-3 below). Per the constitution the move is: file the upstream ask, scope
M0 to what is safely supportable, record it — never a silent Go workaround (B7/G2).

- **UA-1 — `labeled` action drops the specific added label.** `IssuePayload.Labels`
  is the AGGREGATE current set, not the single label the sender just added, so a
  "labeled `semdev` after opening" opt-in cannot be attributed to the labeler.
  Treating the aggregate as the actor's signal would be the privilege-confusion
  hole (an authorized actor's unrelated `labeled` event inheriting a foreign
  `semdev`). Ask: publish the added `label` on `labeled`/`unlabeled` actions.
  **Filed: C360Studio/semstreams#493.**
- **UA-2 — comment events drop the issue number/URL and comment id.**
  `CommentPayload` is `{body, author}` only, so a `/semdev` comment or a human
  reply cannot be tied to its run. Ask: carry the parent issue/PR number (and
  comment id) on `github.event.comment` (`IssueEvent`/`PREvent` already carry
  `Number`; mirror it on `CommentEvent`). **Filed: C360Studio/semstreams#494.**
- **UA-3 — `ComponentManager` leaks a `cm.mu` reader on a health-check timeout,
  deadlocking `Stop`.** `performDetailedHealthCheck` spawns a goroutine that
  `RLock`s `cm.mu` and, on its 50 ms timeout branch, abandons it without the
  matching `RUnlock` — a permanent leaked reader after which every
  `updateComponentState` writer and `stopAllComponents`' `RLock` block forever.
  Surfaced adding the agentic-execution plane: 100% reproducible on a cold boot
  (fresh JetStream), never with the three fast-starting substrate components
  alone; the trigger is `cm.mu` contention during the slow first-boot startup
  window. Present through beta.144 (a framework bump does not fix it). **Filed:
  C360Studio/semstreams#508.** M0 mitigation (not a workaround for the bug — a
  bound so the process cannot hang): `boot.Runtime.Stop` runs `svcMgr.StopAll`
  under an OUTER deadline (`timeout + stopServicesGrace`) and, on a wedge, logs +
  abandons it and still closes the config manager and NATS, so a SIGINT on the
  live binary always returns. The boot smoke test's teardown is best-effort (it
  proves clean *startup*, not shutdown). Remove the bound once #508 lands.
- **UA-4 — no warn-free scalar field-to-field comparison in expression-rule
  conditions.** Comparing one triple against another on the same entity has only
  one scalar form — `$entity.triple.<pred>` in a condition `value` — and it floods a
  `"likely silent-pass bug"` WARN on every entity that lacks `<pred>` (the rule is
  evaluated, and its values substituted, against every changed entity incl.
  agentic-loop steps; substitution is eager over all conditions before evaluation,
  so a guard condition cannot short-circuit it; the graceful `.length`/`.triples`
  forms don't warn but are count/array, not scalar equality). Surfaced wiring the
  D15 #0 change-approval gate freshness. **Filed: C360Studio/semstreams#519.** M0
  handling: the gate stays presence-only and the reachable content-freshness guard
  lives in `project_tasks` (Go, warn-free); a tripwire pin
  (`TestChangeApprovalGateFreshnessForwardContract`) blocks re-introducing the
  warn-flooding form until #519 lands or a rework path forces the gate to adopt a
  warn-free compare (see D15 #0).

**M0 scope decision:** intake triggers on issue **`opened`** only — the one flow
the flattened payload fully supports (actor == opener; initial labels + body are
the opener's; issue number present). Until UA-1/UA-2 land: a non-`opened` issue
action carries no attributable opt-in (creates no run), and the comment-driven
paths (labeled-after-open opt-in; `respond` reply routing, group 5.7) are
designed but their live wiring waits on the field — park toward the human
meanwhile, mirroring the archive M1-deferral. `ask_human` posting a comment
(`github_add_comment`) is unaffected; only reply *routing* waits on UA-2.

### D14 — The taxonomy mirrors the proven OpenSpec lifecycle (structure + HITL + substrate)

semdev does not invent a workflow; it wraps the proven OpenSpec lifecycle
(`new → apply → verify → archive`) with a closed-taxonomy **structure**, **human
gates + observability**, and a **graph-backed fact substrate**. The test of the
taxonomy is that every OpenSpec checkpoint maps to a semdev action or fact:

| OpenSpec checkpoint | semdev action / fact | Kind |
|---|---|---|
| `new` | `create_change` → `openspec.change.*` | LLM-authored, hydrated from graph |
| `validate` | validate sub-step → `openspec.validated` | deterministic CLI oracle |
| `apply` | `dev_from_task` → `task.spec`/`task.attempt` | bounded loop |
| `verify` (coherence) | *structural* — `openspec.validated` + derived completion + every `review.verdict.*` | no separate action |
| — (outcome, G4) | `verify` → `verify.result` | deterministic clean-room harness |
| `archive` | `archive_change` → `openspec.archived` | deterministic CLI oracle (M1-wired) |

semdev's own additions to the wrapper are `issue_intake` (front door),
`open_pr` (delivery — OpenSpec is local-only), and `ask_human`/`respond` (HITL).

Two forks were resolved here. **Verify is split**: the `verify` action means
clean-room *outcome* verification only; OpenSpec's coherence-verify collapses
into the structural triad above, because `task.spec` is the approved change
projected immutably and cannot drift from it (a graph property OpenSpec's manual
step compensates for by hand). **`validate` and `archive` are the two
deterministic CLI oracles** semdev shells rather than re-implements (D11) — the
honest "the sponsor's own tool blessed this" claim on both the way in and the way
out. `archive_change` is designed at M0 (action + fact + rule) and wired to a
merge-event trigger at M1; the M0 mock journey terminates at `open_pr`.
*Alternative rejected:* a parallel semdev verifier/archiver (re-implements the
proven oracle, invites drift, and forfeits the compatibility claim).

### D15 — Run-lifecycle bones: park semantics + the forward contracts groups 4/5 honor

Group 3 lands the run-lifecycle *bones* — the taxonomy, personas, agentrun
registration, the two-human-gate transitions (rules 01/02), the park rule (03),
and the archive stub (04). Per-action spawn rules live with their capability
packs; run-creation-on-intake is `forge-io` (group 5); live firing is proven in
the group-11 journey. Four contracts fall out of that split and are pinned here
so a later group cannot break them silently:

0. **Re-authored changes must be re-validated before a consumer reads
   `openspec.validated`. — CLOSED at the reachable consumer (`project_tasks`); gate
   DEFERRED (content-revision binding).** `create_change` REPLACES `openspec.change.*`
   on a re-author, but G5 forbids it (or any tool but the validate harness) from
   touching `openspec.validated`, and `validate_change` is not invoked on re-author,
   so no writer clears the marker when the content changes. A slug-valued marker
   cannot self-detect a re-author, so a stale pass could gate a superseded version
   silently. **Resolution — bind the marker to CONTENT, not the slug:**
   `create_change` computes a deterministic content revision over the authored facts
   (sha256, length-prefixed sorted `(predicate,object)` pairs, `sha256:`-prefixed so
   the rule engine never compares it numerically) and stamps it slug-scoped at
   `openspec.change.<slug>.revision`, in its own `openspec.change.*` namespace
   (G5/G9 clean, no new vocabulary), in the same atomic replace as the content.
   `validate_change` on PASS echoes THIS slug's revision into `openspec.validated`
   (single compute site — echo, never recompute — so read/write cannot drift; fails
   closed without a revision). `project_tasks` then freezes `task.spec` only when
   `openspec.validated == openspec.change.<slug>.revision` — so a re-authored,
   unvalidated, or alternate change is refused. A re-author bumps the revision, so
   the stale marker no longer matches and `project_tasks` refuses until
   `validate_change` re-runs against the new content (the edge-triggered validate
   rule, coordinator/03, fires on each new authoring loop's marker).

   **Gate side deferred.** Ideally the change-approval GATE would carry the same
   freshness (`openspec.validated` must equal the current content revision). But the
   gate is slug-blind (it fires on the run and cannot wildcard a slug), so it needs a
   slug-independent field-to-field compare, and the only engine form
   (`$entity.triple.<pred>` in a condition `value`) floods a "likely silent-pass bug"
   WARN on every entity that lacks the predicate — the gate is evaluated against all
   changed entities, incl. agentic-loop steps (semstreams **#519**, filed). At M0 the
   gate's stale-firing is UNREACHABLE (no live path re-authors a change while the run
   is `executing` — there is no `awaiting_approval → executing` rework path yet), so
   the gate stays presence-only and `project_tasks` carries the reachable guard. When
   a rework path lands, the gate MUST adopt the freshness compare via a warn-free form
   (the #519 fix, or the graceful `.triples` form). Tripwire:
   `TestChangeApprovalGateFreshnessForwardContract` keeps the gate presence-guarded
   and blocks re-introducing the warn-flooding `$entity.triple` value form until #519.
   Red-first pins for the live guards: `TestProjectRejectsReauthoredUnrevalidatedChange`
   (project_tasks refuses a re-authored-since-validation change),
   `TestCreateChangeRevisionTracksContent` (revision changes iff content changes),
   `TestValidateFailsWithoutRevision` (validate fails closed without a revision).
   Surfaced by the 4.4 semstreams-review and re-flagged live by Codex on PR #1.

1. **Run-scoped facts.** Rules fire against the *firing entity's* triples, and
   the lifecycle rules fire on the run (they match `agent.run.phase`). So the
   milestone facts those rules gate on — `openspec.validated` (group 4),
   `run.change_approved` (group 5) — MUST be stamped on the **run entity**
   (subject = `agent.run.entity_id`, the loop→run subject-override the park rule
   models), never on the loop or a change entity, or the rule never fires and the
   run wedges. Groups 4/5 should carry a red-first pin for this.
2. **Park is marker-only.** The park rule (03) records `run.awaiting_human` and
   does NOT reflect the phase — deliberately, to avoid conflating with the
   change-approval `awaiting_approval` gate (a run can await a human for either
   reason, distinguished by the fact, not the phase). Consequently: (a) any rule
   that advances or terminates a run MUST exclude `run.awaiting_human`-present
   runs, or a parked run gets swept; (b) the `respond` handler (group 5) MUST
   remove `run.awaiting_human` on resume so a resumed run is not read as parked.
   Rules 01/02 carry the `run.awaiting_human length_eq 0` guard, and a
   conformance pin (`TestLifecycleTransitionRulesExcludeParkedRuns`) fails the
   build if any active `lifecycle_transition` rule omits it — the only exemption
   is a rule that itself clears the marker (the group-5 resume-from-park rule).
3. **Entry transition.** There is no `dispatched→executing` rule yet; a minted run
   (phase `dispatched`) reaches `executing` only once group 5 wires the entry
   transition (the semteams `agent-run/02` analog, keyed on the dispatch/handoff
   signal). Rules 01/02 are inert until it lands — expected.
4. **Runtime boot parity.** `boot.RegisterLifecycle` (agentrun registration) and
   the rule/persona/tool config load are not in the static `RegisterAll` seam
   (they need a live Manager); group 11 wires them into a shared runtime-boot path
   that BOTH binaries call, guarded by a parity pin — the same half-wired-binary
   class `boot.RegisterAll` already prevents for components.

### D16 — Flow-validation decisions (review is per-task and adversarial)

Stepping back over the whole issue→PR arc (before wiring the branch rules)
surfaced five decisions. Four confirm the design already written; one changes it.

- **Adversarial review runs per unit of work, not just at the PR (the change).**
  The proven agentic-dev pattern — and this project's own (red-first pins +
  reviewer agents per increment) — is to refute each unit before accepting it.
  So review moves INTO the per-task dev loop: `submit_review` becomes per-task
  (`submit_review(task_index)` → `review.verdict.<i>`), Quinn refuting each
  attempt, still floored by that task's `measurement.result` (G3 — the reviewer
  adds only blocking findings, never turns a failing test green). `review.verdict`
  becomes the namespace `review.verdict.*` (mirroring `measurement.result.*`),
  and the `open_pr` coherence gate rolls up `verify.result eq pass` ∧ every
  `review.verdict.* eq approved` ∧ `openspec.validated ne ""`. The run-level
  `submit_review` shipped first (`c245d27`); this refactors it. *Alternative
  rejected:* a single run-level review — it defers the first adversarial read to
  the whole change, when a per-task read catches a bad unit before the next is
  built on it. The human PR review (gate 2) remains, as the cross-change check.
- **No auto-close (D1).** A "no" or a "maybe" both route `ask_human` carrying a
  recommendation with citations/refs (a recommend-close note, or a follow-up
  question); the human closes. semdev never closes an issue itself, so no
  `decline` action is added — the taxonomy stays closed.
- **Triage stays folded (D2).** "Is it a valid ask?" is the coordinator's first
  `decide`, not a separate research role at M0; it must land yes/no/maybe and
  iterate with the author over issue comments (`ask_human ⇄ respond`, whose live
  routing waits on semstreams #494 — D13 UA-2).
- **Retry loops are the norm (D4).** Floors reject, measurement fail, and a
  `changes_requested` verdict all re-enter a fresh attempt on the same task
  within budget; exhaustion parks (`ask_human`), never a silent fail — already
  the shape of the "Bounded dev loop with escalation" requirement.
- **Rework re-validates (D5).** Already the D15 #0 forward-contract: a re-authored
  change re-runs `validate_change` before the approval gate re-reads
  `openspec.validated`.

## Risks / Trade-offs

- **Cache-home freshness is the silent G4 killer** → per-run fresh cache home +
  the cache-masked-fabrication-reject pin, red-first at M0. Choosing devcontainers
  does *not* cover this.
- **Source-substitution / auth for unpublished deps (M2, container-agnostic)** →
  port the writable-composite overlay + creds-as-refs; without it the OSH builds
  die at dependency resolution.
- **Proving live/heavy behavior in-sandbox is unwinnable** → tier it out; harvest
  the repo's own test exclusions; never gate on evidence the sandbox can't produce.
- **An agent editing the verification harness (Goodhart)** → human approval +
  floor flags any gate-weakening; bootstrap harness bounds recursion (D9).
- **OpenSpec CLI is an external Node dependency in the clean room** → pin the CLI
  version; treat it as the oracle, do not re-implement its rules.
- **semteams sandbox + semspec verify are design-proven, not battle-proven** →
  M0 proves the thin Go slice end-to-end; the hard OSH profile is proven at M2.
- **Multi-persona graph re-projection dropping facts** (the semspec-ui-bmad
  lesson) → minimal 3-persona roster; single-writer facts (G5).
- **Rule re-fire on restart (persisted graph).** The rule engine persists its
  per-(rule,entity) match state in a durable NATS KV bucket (`RULE_STATE`, TTL 0)
  and, on boot, a stale-revision guard skips replayed entities — so a **normal
  restart does NOT re-fire** an already-matching rule (`stateful_evaluator.go`
  `wasMatching` read-back + the "defends across process restarts" guard;
  re-fire-on-recovery is opt-in and none of our rules opt in). The residual is
  **asymmetric bucket loss**: if `RULE_STATE` is wiped/absent while `ENTITY_STATES`
  survives (manual deletion, targeted corruption, entity delete+recreate),
  `wasMatching` resets false and every `publish_agent` **spawn** rule whose trigger
  is *non-self-clearing* re-fires — `publish_agent` is not idempotent (each fire
  mints a fresh loop; no dedup), so you get a duplicate coordinator/author/validate
  loop. `add_triple`/`lifecycle_transition` rules are safe (deterministic-object
  idempotent / transitions-table-guarded). The established fix (semteams
  `agent-run/02`) is a **self-extinguishing top-level condition** whose own action
  flips it false. **`dev-from-task/02` (the dev re-wake) is hardened** — a fired-once
  `run.dev_kickoff` marker + absence guard (pinned by
  `TestDevRewakeIsSelfExtinguishing`). **CARRY-FORWARD (pre-production):**
  `coordinator/02` (create_change spawn), `coordinator/03` (validate spawn), and
  `run-lifecycle/03` (park's `publish`) are the remaining non-self-clearing
  `publish_agent` triggers; give each the same fired-once marker before a real
  deployment (or before any op that could clear `RULE_STATE` independently). Every
  new dev-loop-rail spawn rule MUST follow the self-extinguishing pattern.

## Migration Plan

Greenfield — no data migration. Land order at M0: the checked-in registries and
their conformance pins first (G1 tools registry, G5 writers table, G9 vocabulary
table, G7 ledger schema, the regression-pin manifest), red-first where they guard
a known shape; then the reused-component wiring; then the Go fixture profile end-
to-end journey. Rollback is deletion — nothing depends on the spine yet. Thicken
per the milestone ladder (M1 real-LLM + watch; M2 dogfood + the JVM/OSH profile).

## Open Questions

- Exact new-Go tool inventory for `forge-io` comments (`list_comments`, `get_pr`?)
  — inventory against semstreams' actual tool set during `tasks`.
- Whether the ported OpenSpec format engine needs re-vocabulary work beyond a
  namespace swap to satisfy G9 — likely light, confirm during `tasks`.
- The precise trigger that promotes the auto-spawned blocking harness-fix child
  from "designed" to "enabled" — it needs its own evidence entry (G7) before the
  autonomy turns on.
- The exact OpenSpec CLI `store`/`schema` surface if semdev ever targets
  multi-root workspaces — out of scope for the single-fixture spine.
