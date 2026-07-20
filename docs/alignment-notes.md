# Framework-Alignment Notes (G1)

Every Go component or tool semdev adds must first clear the primitive-first gate
(constitution G1): before writing Go, prove a rule, a persona, a fact, or an
existing semstreams component cannot express the behavior. This file is the
record of that proof — one section per entry in `internal/registry`
(`registry.Entries`). The G1 conformance pin fails the build if a semdev-added
component has no note here, so the note is not optional documentation; it is part
of the addition.

Ported and hardened from semteams' tool-accretion review discipline
(`cmd/semteams/tools/README.md`): semteams kept it as review culture; semdev pins
it.

## Note format

Each note is a level-2 heading whose text is the `AlignmentNote` anchor of its
registry entry, followed by:

- **Primitive considered** — the rule / persona / fact / existing component that
  was evaluated first.
- **Why it cannot express this** — the specific gap that forces Go.
- **Registry entry** — the `registry.Entries` name and kind this note backs.
- **Change** — the OpenSpec change slug that introduced it.

Template:

```markdown
## <anchor>

- **Primitive considered:** …
- **Why it cannot express this:** …
- **Registry entry:** `<name>` (`component` | `tool`)
- **Change:** <change-slug>
```

## deterministic-station-component

- **Primitive considered:** the forced single-turn coordinator loop each
  deterministic station used to ride — a rule `publish_agent` with
  `tool_choice=function` spawning an agentic loop whose only act is to call one
  harness tool and StopLoop; or a rule alone.
- **Why it cannot express this:** a rule can ROUTE facts but cannot invoke Go, so
  the deterministic station work (record delivery, run the floors, cold-verify,
  project, validate, provision) still needs a Go executor. The forced-turn tool
  path makes that executor a MODEL turn — under a real LLM a paid call that
  decides nothing, since the outcome is harness-derived (G3) — and is
  context-starved by construction (R6). The framework-aligned answer is the
  gated-DAG publish→component pattern (`internal/station`, mirroring
  `processor/research-graph-route`): a rule fires a plain `publish`, the rule
  engine emits it on core NATS (semdev's rule component declares no matching
  JetStream output port), and a registered processor turns the reference into
  deterministic work with zero model turns. Each concrete station calls the same
  core the (transitional) tool did, so no fact writer gains a second owner (G5),
  and fires no lifecycle transition (G2).
- **Registry entry:** `delivery-station`, `projection-station`, `validation-station`
  (`component`) — the R6 stations that need no shared-runspace DI seam (their
  dependencies build from the NATS client; the validation station additionally shells
  the `openspec` CLI via a plain os/exec runner). `floors-station` (`component`)
  additionally captures boot's SHARED `runspace.Checkouts` (the same process-local map
  the dev-loop tools use — it reads the developer's authored attempt off the run's
  checkout) via the `RegisterAll(reg, checkouts, sandboxes, sourceDir)` DI seam.
  `verify-station` (`component`) joins the same seam: it clones the run's COMMITTED
  artifact off that shared checkout (`CloneForVerify`) and cold-proves it, stamping
  `verify.result` on the run. `provision-station` (`component`) captures BOTH shared
  instances — it materializes the run's checkout AND stands up the WARM dev container
  the `measure_task` tool later Execs into (one run, one container, so the map must be
  shared) — plus the operator-configured run source dir; it stamps `sandbox.ready`/
  `sandbox.blocked` on the run.
- **Change:** simplify-m0-execution-rail

## issue-intake-component

- **Primitive considered:** a rule pack over the GITHUB stream, or configuring a
  framework webhook input.
- **Why it cannot express this:** the framework RETIRED its github-webhook input
  in the beta.147 boundary wave (ADR-075; the cutover checklist transfers the
  receiver, the payload shapes, and the flattening to semdev) — there is no
  framework input to configure, and NOTHING publishes `github.event.*` without
  one. A rule cannot terminate HTTP, validate an HMAC, decode a host payload,
  make the collaborator-permission network call the admission gate requires, or
  build a prompt-bearing coordinator wake. The component owns both halves of
  the lane: the receiver (HMAC → filter → flatten → publish onto the
  semdev-declared GITHUB stream, delivery-GUID msg-id dedup) and the durable
  consumer (Normalize → the shared `admission.Decide` gate → admission record →
  `intake.CoordinatorTask` wake). NARROWED by conversation-channel-seam to the
  ISSUE lane: the receiver still flattens comment events (GitHub delivers all
  event types to one URL), but the comment-approval + park-post lanes moved to
  the conversation-channel component. It adds NO admission logic of its own,
  stamps no lifecycle fact (G2 — the wake is the host-way front door; the mint
  rule fires the transition), and records only what the gate itself derived (G3).
- **Registry entry:** `issue-intake` (`component`)
- **Change:** forge-io-real-lanes (narrowed by conversation-channel-seam)

## conversation-channel-component

- **Primitive considered:** keeping the human approval + park-post lanes inside
  issue-intake, or expressing them as rules over the GITHUB/USER streams.
- **Why it cannot express this:** the conversation half is a code-host adapter
  concern (post a message to a thread, normalize an inbound comment, run the
  collaborator-permission network call the approval authorize requires) that a
  rule cannot perform — the same reasons issue-intake exists. Carving it into a
  dedicated component (conversation-channel-seam D8) puts it behind the
  channel-neutral `Channel` port so a second channel composes without touching
  the arc: it owns the comment-approval consumer (github.event.comment → neutral
  Message → `admission.Authorize` → the stand-in `run.change.approved` fact the
  resume rule reads) and the park-post consumer (user.response.> → `Channel.Post`).
  It shares the `admission` decision + resolver core with issue-intake (a pure
  reference, not a second writer — G5), fires no lifecycle transition (G2 — the
  resume rule owns the transition), and stamps only what the human's authorized
  command derived (G3).
- **Registry entry:** `conversation-channel` (`component`)
- **Change:** conversation-channel-seam

## create-change-author-tool

- **Primitive considered:** a rule that authors the OpenSpec change directly, or
  the reused framework `decide`/agentic tools.
- **Why it cannot express this:** authoring a change means turning a model's
  structured content into the full `openspec.change.*` fact set via the OpenSpec
  format engine (`internal/openspec`), then stamping it atomically on the run
  entity. That is a deterministic mapping + a graph write no rule or generic tool
  performs; the engine mapping is code (ported, dep-free), and the tool is the
  single G5 writer of `openspec.change.*`. Its schema takes content only (G3).
- **Registry entry:** `create_change` (`tool`)
- **Change:** m0-walking-skeleton-spine

## render-openspec-hydrate-tool

- **Primitive considered:** a rule that reads `$entity.triple.openspec.change.*`
  and `publish`es a rendered document to an `output/file` component (the D1
  "rule→publish→output/file" hydrate path).
- **Why it cannot express this:** rendering a change means reconstructing the
  nested OpenSpec model (proposal, per-capability deltas, ordered tasks) from the
  stored `openspec.change.document` blob and re-serializing it to canonical
  markdown (`changefacts.Hydrate` + `RenderChangeFolder`). A rule's
  `$`-templating substitutes single predicate values into a fixed string; it
  cannot JSON-decode the document, re-group deltas by capability, or order tasks
  by index. That reconstruction is deterministic Go in `internal/openspec`;
  the tool is the thin read adapter (a `changefacts.Reader`, query-only) that
  hands the run's facts to it. Read-only: it stamps no facts (no G5 writer) and
  its schema takes only the change slug (G3, trivially).
- **Registry entry:** `render_openspec` (`tool`)
- **Change:** m0-walking-skeleton-spine

## write-change-workspace-tool

- **Primitive considered:** a rule that reads `$entity.triple.openspec.change.*`
  and `publish`es to an `output/file` component to drop the change folder on disk.
- **Why it cannot express this:** materializing an OpenSpec change is a multi-file
  filesystem write — `proposal.md`, `tasks.md`, and one `specs/<capability>/spec.md`
  per delta — hydrated from the `openspec.change.document` blob and
  serialized by the format engine's `WriteChange`, which also prunes stale managed
  files. A rule's single-value `publish` cannot re-group deltas by capability, walk
  a variable set of capability files, or drive the authoritative directory
  rewrite. It is a graph-READ tool (a `changefacts.Reader`) plus the deterministic
  `WriteChange`; it stamps no facts (no G5 writer) and its schema takes only the
  change slug (G3). WHERE it writes is the run's checkout, resolved through an
  injected `WorkspaceResolver` seam rather than any workspace state of its own (B1).
- **Registry entry:** `write_change` (`tool`)
- **Change:** m0-walking-skeleton-spine

## validate-change-cli-oracle

- **Primitive considered:** a rule/persona that judges the change's validity from
  its facts, or re-implementing the OpenSpec validation rules in Go.
- **Why it cannot express this:** the honest "compatible" claim (D11/D14) requires
  the SPONSOR's own validator to bless the change — semdev must shell the real
  `openspec validate` CLI and read its exit code, not re-implement or LLM-judge it
  (re-implementing invites drift and forfeits the compatibility claim). Doing that
  means hydrating the change, materializing it to a throwaway workspace, running an
  external process, and stamping `openspec.validated` from the real exit status. No
  rule can run a subprocess or read an exit code, and G3 forbids a model supplying
  the verdict. This is the measurement-harness shape: the schema takes content only
  (the slug); the tool's Go (the harness that ran the command) stamps the result.
  It is the single G5 writer of `openspec.validated`.
- **Registry entry:** `validate_change` (`tool`)
- **Change:** m0-walking-skeleton-spine

## archive-change-cli-oracle

- **Primitive considered:** a rule that folds a merged change's deltas into the
  living specs, or re-implementing `openspec archive`'s spec-merge in Go.
- **Why it cannot express this:** the loop-closer that keeps the target repo's
  specs truthful (G10) must be the SPONSOR's own archiver (the second CLI oracle
  alongside `validate`, D11/D14) — semdev shells `openspec archive <change> -y
  --json` and stamps `openspec.archived` from the real exit code, never a
  re-implementation (which would drift from the CLI and forfeit the compatibility
  claim). Verified invocation: it moves the change to
  `openspec/changes/archive/<date>-<slug>/` and folds its deltas into
  `openspec/specs/` (`"specsUpdated": true`). Key contrast with `validate_change`:
  the validator runs against a **throwaway temp** materialization (a read-only
  oracle over the graph), but the archiver **mutates the real checkout** — it
  rewrites the repo's living specs, which are then committed — so at M1 it runs
  against the run's actual workspace, not a temp. Harness-stamped (G3: exit code,
  not a model outcome); single G5 writer of `openspec.archived`
  (`openspec-archive-harness`).
- **Registry entry:** none yet — **DESIGN-ONLY at M0** (this note declares the
  shell path). The live archive call and its merge-event trigger land at M1: the
  `archive_change` action + `openspec.archived` fact + the disabled loop-closer
  rule (`configs/rules/run-lifecycle/04-archive-change-loop-closer.json`) are
  already declared (task 3.9), and M1 wires the forge-io PR-merged trigger, enables
  the rule, and registers the archive tool (mirroring `validate_change` over
  `internal/cliexec`). The M0 mock journey terminates at `open_pr` (`pr.ref`).
- **Change:** m0-walking-skeleton-spine

## github-list-comments-tool

- **Primitive considered:** the framework's existing `github_read` tools (which
  cover `github_get_issue`/`github_get_pr` but not comment threads) and a rule.
- **Why it cannot express this:** reading an issue/PR's comment thread is a GitHub
  REST call (`GET /repos/{o}/{r}/issues/{n}/comments`) the framework's github tools
  do not expose (inventory, task 5.5) and no rule can make. It is the thin comment
  read semdev adds over its own GitHub client — read-only, host-specific by
  construction like the framework's `github_*` tools, stamping no facts (no G5
  writer) with a coordinate-only schema (G3). The arc consumes the returned
  thread, not the API.
- **Registry entry:** `github_list_comments` (`tool`)
- **Change:** m0-walking-skeleton-spine

## project-tasks-tool

- **Primitive considered:** a rule that reads the approved change's task facts and
  writes `task.spec`, or a persona that "projects" the tasks.
- **Why it cannot express this:** projection reads MANY execution-rich task facts
  off the run entity (`openspec.change.<slug>.task.<i>.*`), reconstructs each into a
  typed task honoring the nil-vs-authored-empty presence distinction, enforces the
  Karpathy schema and clamps the iteration budget (`devtask.Project`), and stamps
  the result as the IMMUTABLE `task.spec.*` — a multi-fact read + transform + typed
  write no rule can perform (a rule matches triples and emits a triple; it cannot
  loop over an unknown number of tasks, parse JSON arrays, or run schema logic). It
  plans nothing (create_change authored the tasks); it enforces and freezes them,
  and refuses to re-project onto a run that already carries `task.spec` (the dev
  loop CONVERGES on the facts, it does not redefine them — G2: the tool surfaces a
  schema gap as an error and parks toward the human via a rule, it fires no
  transition). Stamps no outcome (G3): `task.spec` carries the task definition, not
  a result. Single G5 writer of `task.spec.*` (`task-projector`).
- **Registry entry:** `project_tasks` (`tool`)
- **Change:** m0-walking-skeleton-spine

## measurement-tool

- **Primitive considered:** the framework's `bash` executor run directly by the dev
  loop, or a rule/persona that reads the model's "tests pass" claim and records the
  outcome.
- **Why it cannot express this:** the measurement gate's whole value is that the
  outcome is MEASURED, not asserted (G3). That means: read the IMMUTABLE
  `task.spec.<i>.test_command` off the run entity (the projector froze it, so the
  model cannot substitute a friendlier command), run it in the run's workspace,
  and stamp `measurement.result.<i>` DERIVED from the real exit code — a non-zero
  exit records failure regardless of the command's stdout or any model text, and a
  command that never started (missing binary — a zero-value result with a runner
  error) records `ran=false`/`passed=false` so it cannot false-green on the exit-0
  a never-run process reports. No rule can run a subprocess or read an exit code.
  The framework's `bash` tool captures an OS exit code (the mapped primitive), but
  its schema takes a MODEL-SUPPLIED `command` — the exact G3 hazard this tool exists
  to remove — and it neither reads the frozen command from the graph nor
  derives-and-stamps the fact on the run entity. So semdev reuses only the
  exit-code-capture seam it already owns (`internal/cliexec`, shared with
  `validate_change` — a thin os/exec wrapper, not a re-created exec subsystem) and
  adds the frozen-command read, the outcome-free schema, and the fact stamp (D9: the
  OS exit-code→fact path is the native G3 shape; this tool is the thin harness that
  wires it to the graph). It accepts no caller outcome (G3 — the schema takes only
  the task index) and fires no transition (G2 — the reviewer reads the fact, a rule
  advances the run).
- **Fact shape:** `measurement.result` is a per-task OWNED namespace
  (`measurement.result.<i>.{command,ran,exit_code,timed_out,passed}`), NOT a single
  appended predicate. The graph merges replace-per-`(subject,predicate)`
  (`graph.MergeTriples`), so a single exact predicate would let a second task's
  measurement clobber the first; keying the task index into the predicate gives each
  task its own owned sub-package, upserted independently (re-measuring a task
  replaces its own facts — a measurement is a task's CURRENT outcome, not an
  accumulating log; attempt history is `task.attempt`'s separate writer). This
  mirrors the `task.spec.*` namespace and yields the "one measurement per required
  task" shape `measurement.CanApprove` expects. Single G5 writer of
  `measurement.result.*` (`measurement-harness`).
- **Registry entry:** `measure_task` (`tool`)
- **Change:** m0-walking-skeleton-spine

## submit-review-tool

- **Primitive considered:** a rule that reads `measurement.result` and stamps
  `review.verdict`, or the reviewer persona (Quinn) deciding the verdict directly.
- **Why it cannot express this:** review runs PER TASK and its verdict must be
  FLOORED by that task's harness fact, and that floor is `measurement.CanApprove`
  over the single reviewed task — "the reviewed `task.spec.<i>` has exactly one
  passing measurement, re-derived from the raw exit evidence." No rule can express
  it: a rule matches a triple, it cannot re-derive a pass from `ran`/`exit_code`/
  `timed_out`. Nor can the persona own the outcome (G3/D4): a false success claim
  must not earn approval however confidently the work describes itself, so approval
  cannot be a model-supplied field — it is DERIVED here (`approved ⟺
  CanApprove([task], observed) ∧ no findings against that task`). The tool
  reconstructs the measurements via `measurement.ResultsFromFacts` (fails CLOSED on
  unparseable evidence — a fact it cannot read is a failure, never a defaulted
  approve), confirms the reviewed index is a projected `task.spec.<i>`, and stamps
  the per-task `review.verdict.<i>`.
- **Adversarial + additive-only, structurally (7.3):** Quinn reviews each task
  adversarially (refutes the attempt), and its only input beyond the task selector is
  FINDINGS (required changes); an open finding blocks THAT task's approval, but a
  finding can never weaken `task.spec` because this tool's single writer is
  `reviewer-quinn` and it stamps ONLY `review.verdict.<i>` — it holds no writer for
  `task.spec` (G5 single-writer makes the "findings never relax the spec" scenario
  impossible, not merely disallowed).
- **Fact shape:** `review.verdict.*` is a per-task NAMESPACE (mirroring
  `measurement.result.*`): one verdict per task, each its own predicate
  (`review.verdict.<i>`), upserted latest-wins on a re-review (the graph's
  replace-per-`(subject,predicate)`) so re-reviewing one task never clobbers
  another's verdict. Review moved INTO the per-task dev loop — adversarial review on
  every unit of work, not just the PR (design D16). Fires no transition (G2) — the
  `open_pr` gate is a rule that ROLLS UP every `review.verdict.*` (approved) with the
  clean-room `verify.result` + `openspec.validated` (wired with the coordinator spawn
  rules at a later group). Single G5 writer of `review.verdict.*` (`reviewer-quinn`).
- **Registry entry:** `submit_review` (`tool`)
- **Change:** m0-walking-skeleton-spine

## classify-intent-tool

- **Primitive considered:** the framework `decide` tool (semdev already reads
  intent with it — the coordinator persona reads an issue and returns one action
  from a closed taxonomy, `decide` stamps `coordinator.decision.next-action`, a
  rule routes), or a rule / persona owning the approval directly.
- **Why it cannot express this:** a rule cannot read natural language, so the
  classification needs a model turn — but `decide` is the wrong model turn on two
  counts. (a) It carries NO message grounding: its args are
  action/reason/subtopics/retry_hint, so it cannot bind the intent to the ONE
  authorized message the harness must re-authorize (D2 — an LLM-supplied author
  would be an approval-injection hole). (b) It stamps under Source
  `coordinator-decide` on the coordinator's routing lane; reusing it for the
  approval gate would give `conversation.intent.*` a SECOND writer and collide
  with the coordinator's own decisions (G5). The grounding fields (id + author
  copied from `conversation.pending.*`) plus a distinct fact/writer
  (`conversation-classifier`) force a separate tool. It is the `decide` SHAPE —
  a routing classification the harness records — NOT the `submit_review` shape:
  approval has no executable ground-truth, so no measurement floor is possible
  and the tool takes no outcome field (G3). The consequential gate fact
  (`run.change.approved`/`rejected`) is stamped deterministically downstream by
  `approval-adapter` (one G5 writer), never by this tool; it fires no lifecycle
  transition (G2). It subject-overrides to the RUN (not the classifier loop the
  framework `decide` would target) because the dedup and the routing rule both
  read the run (a rule templates only the firing entity's own triples — H2).
- **Registry entry:** `classify_intent` (`tool`)
- **Change:** nl-conversation-intent

## verify-artifact-tool

- **Primitive considered:** a rule that reads a build/test fact and stamps
  `verify.result`, or reusing the framework's sandbox HTTP client / `bash` tool
  directly to "run the tests."
- **Why it cannot express this:** the clean-room gate (G4) must PROVE the delivered
  artifact cold — provision fresh isolation with a distinct build-cache home (the
  universal G4 control, D5), resolve/build from the artifact's OWN declarations, run
  its OWN tests, and derive Pass/Fail/Retry from what happened. No rule can provision
  a sandbox, run a subprocess, or read an exit code. The framework ships only an HTTP
  client to an external sandbox and a git-diff tripwire (detection, not containment);
  `pkg/sandbox` is proposal-only — so semdev owns a thin `cleanroom.Runner` seam
  (Up/Exec/Down, mirroring semteams' sandboxmanager). The M0 run path is a
  `ContainerRunner` (per-run docker container from the operator-declared image,
  fresh cache volume per run — the `containerized-sandbox-dev-loop` change, revising
  D5); `LocalRunner` (host cache-home isolation) and `MockRunner` are the
  unit-test / no-docker shims behind the same seam. The tool wires that Runner + the
  reproducibility manifest
  (`internal/harness`) + the pure `verify.Decide` (`internal/verify`) and stamps
  `verify.result`. It stamps no caller outcome (G3 — the schema takes NO arguments;
  the model may only trigger the proof) and fires no transition (G2 — the open_pr
  gate is a rule on `verify.result`).
- **The transport-vs-genuine line (the make-or-break detail):** `verify.Decide` is
  correct only if the harness sets `Completed=false` for ANY transport/infra fault, so
  the tool draws that line — a failed provision, a step that could not run, or a
  resolve failure the `cleanroom.ClassifyResolve` classifier reads as network-class
  (registry unreachable, TLS/DNS/proxy, timeout) all yield **Retry**, never a terminal
  reject of a good artifact. A GENUINE resolve failure (a missing/fabricated coordinate
  the fresh cache could not mask) is `Resolved=false` → **Fail**: the
  cache-masked-fabrication reject. Neither classification can produce a false-green (a
  failed resolve is never Pass), so the classifier only decides Retry-vs-Fail; its
  default for an unrecognized non-zero resolve is genuine (fail closed on the artifact).
- **Fact shape:** `verify.result` is an EXACT scalar (pass/fail/retry) — one clean-room
  verdict per run, upserted latest-wins (a retry re-run replaces it), which the open_pr
  gate rule reads. Singular per run (the whole artifact proven as one unit), like
  `review.verdict`. Single G5 writer of `verify.result` (`verify-harness`).
- **Registry entry:** `verify_artifact` (`tool`)
- **Change:** m0-walking-skeleton-spine

## floor-tools-wrapper

- **Primitive considered:** a rule that inspects an attempt and stamps
  `floor.finding`, or trusting the persona's own claim that its test is real.
- **Why it cannot express this:** the floors are STRUCTURAL source analysis — parse
  every authored `.go` file, walk the AST to decide whether a test exists, whether it
  asserts on computed behavior (vs a constant tautology), whether it ships a stub, and
  whether it "tests" only a mock of the target symbols. No rule can parse Go and walk
  an AST; and the whole point (S1) is that a persona cannot be trusted to answer these
  fabrication-shaped questions about its OWN work, so the verdict must be a
  deterministic harness computation, never a model claim (G3 — the schema takes only
  the task index; `floor.finding` carries a harness-derived `passed`). The pure floor
  library (`internal/floors`) does the analysis; this tool only WRAPS it with
  fact-stamping (the floors package writes no facts, which keeps them offline-testable
  and G5-clean). It records the findings and reports whether any rejected; it fires no
  transition (G2) — the loop-gate that blocks advance-to-review on a rejecting
  `floor.finding` is a rule (task 6.6, wired with the bounded dev loop).
- **Fact shape:** `floor.finding` is a per-(task, floor) OWNED namespace
  (`floor.finding.<taskIndex>.<floorName>.{passed,detail}` plus a per-task
  `floor.finding.<taskIndex>.attempt`), NOT a single appended predicate. The graph
  merges replace-per-`(subject,predicate)`, so one exact predicate would hold a single
  finding; keying both the task index and the floor name into the predicate gives each
  floor its own sub-package, re-stamped each attempt (latest-attempt-wins) so a task's
  current floor verdicts never clobber another task's. The floor set is fixed (the five
  floors), so the sub-keys upsert without a clear. Mirrors `measurement.result.*`;
  attempt history is `task.attempt`'s writer. Single G5 writer of `floor.finding.*`
  (`floor-tools`).
- **Current-attempt binding (Codex P1):** a finding without an attempt identity is a
  stale-false-green route — a prior attempt's all-pass set is indistinguishable from
  the current one after the loop authors a new attempt, or after a `check_floors` that
  cannot resolve the checkout leaves the old passes readable. So the set is bound to
  `floor.finding.<taskIndex>.attempt` = `floors.AttemptID` (a content hash of the
  evaluated source; changes iff the source changes), and a resolve/check failure FAILS
  CLOSED by clearing the task's `floor.finding.*` package (not leaving old passes
  readable). Forward contract for the 6.6 gate: read a finding as a current pass ONLY
  when its `attempt` matches the run's current attempt AND every floor passed —
  never on the mere absence of a rejection (`AttemptID` is shared so the loop/gate
  recompute the same id and cannot drift).
- **Registry entry:** `check_floors` (`tool`)
- **Change:** m0-walking-skeleton-spine

## brownfield-spec-projector

- **Primitive considered:** a rule/persona that reads a target repo's
  `openspec/specs/` and an LLM that interprets the artifacts into facts on the
  ingest path.
- **Why it cannot express this:** ingest MUST be deterministic and model-free
  (G3) — a model interpreting the artifacts is exactly the LLM-supplied-fact class
  the constitution forbids on the measurement/ingest path. Parsing OpenSpec
  markdown into the nested spec model is the format engine's `ParseSpec` (ported,
  dep-free); the projector is the thin deterministic Go that walks the specs tree,
  serializes each capability spec to ONE canonical `openspec.spec.document` blob
  (`internal/specfacts`, the beta.150 canonical twin of the `openspec.change.document`
  change blob — the old `openspec.spec.<cap>.<field>` tree is 4+ segments the
  fail-closed graph-write gate rejects), applies the single owner, and retains each
  source file's raw bytes by content-hash reference (carried as `source_ref` inside the
  blob) for provenance. No rule can walk a directory, hash bytes, or re-group a spec's
  requirements. It is the single G5 writer of `openspec.spec.document`.
- **Registry entry:** none yet — at M0 this is the library projector core with its
  red-first pins; its registered ingest component (which wires it onto the raw
  lane and stamps the facts on their spec entities) and the matching
  `registry.Entries` entry land with the runtime boot path (group 11).
- **Change:** m0-walking-skeleton-spine

## sandbox-substrate (operator-declared image · cold-prove · secrets)

- **Primitive considered:** a rule/persona that "sets up the environment" — an LLM
  or reconciler that harvests/infers a toolchain and stamps a readiness fact, plus
  a fact carrying a resolved secret for a rule to inject.
- **Why it cannot express this:** provisioning a real, isolated, cold-reproducible
  sandbox is the make-or-break infra both predecessors died on, and none of it is
  rule-expressible. `internal/cleanroom.BuildImage` builds the OPERATOR-declared
  image (`harness.LocateImage` finds the committed `Dockerfile`/devcontainer;
  `--iidfile` captures the digest pin) — semdev never harvests/infers/synthesizes a
  toolchain (SB2). `internal/coldproof` provisions a fresh per-run container and
  proves the repo resolves its base deps and BUILDS cold BEFORE the dev loop relies
  on it (SB4.1) — the shared cold-build core (`Gather`) that both the provision-time
  baseline and the final `verify_artifact` route through `verify.Decide`, so a
  fabrication reads identically in both. `internal/harness` is the reshaped RUN
  contract (the declared image + resolve/build/test commands + tier split + secret
  refs — no toolchain modeling; the dropped source-substitution/native-asset fields
  were semspec's harness-injected-resolution grave, now structurally inexpressible).
- **Secrets (SB2c/G7):** `internal/secrets` is the governed named-creds-ref store
  (git-ignored `.env` at M0), the leak-guard Scrubber (a secret VALUE is redacted
  from every surfaced detail — no value in a log, tool result, or fact), and the
  run-time injection channel (a `-e NAME` pass-through with the value in the docker
  process's own environment, off its argv). A missing/empty required ref fails
  CLOSED toward the operator (no warm fallback). It writes no facts and injects only
  values it resolved at run time — never a value round-tripped through a manifest or
  fact (`SecretRefs` is names-only).
- **G2/G3:** these are synchronous compute cores returning values for a future
  provisioning RULE (group 5) to route on — they stamp no fact and fire no
  transition here; the harness derives every outcome (a cold build's real exit
  status), never a model claim.
- **Registry entry:** none — libraries behind the `verify_artifact`/cleanroom seam
  (like `LocalRunner`/`MockRunner`); the provisioning station that stamps readiness
  facts + its vocab land in a later group of the change.
- **Change:** containerized-sandbox-dev-loop

## provision-sandbox-tool

- **Primitive considered:** a rule that "provisions the sandbox" and stamps a
  readiness fact, or reusing the framework sandbox HTTP client / a `bash` tool to
  "set up and check the environment."
- **Why it cannot express this:** the provision-and-prove-cold station (SB2/SB4/SB7)
  must MATERIALIZE the run's checkout, BUILD the operator-declared image, and PROVE
  the repo resolves its base deps and builds cold in a fresh per-run container —
  then derive readiness from what actually happened. No rule can copy a working
  tree, build an image, run a subprocess, or read a cold-build exit code; and the
  whole make-or-break lesson is that readiness must be PROVEN, never asserted
  (semspec stamped "execution verified" over zero executions; semteams only checked
  `--version` over a warm cache). So a thin tool wires the group-2/3/4 substrate it
  cannot itself replace: the `runspace` Sources+Checkouts seams (materialize the
  fresh per-run copy), `runspace.Manifests` (the committed declared image + run
  fields — no harvest, SB2), and `coldproof.ProveBaseline` (build + cold-prove
  through the shared `verify.Decide`). It stamps the DERIVED readiness/attestation
  (G3 — the schema takes NO arguments; the model may only trigger the proof) and
  fires no transition (G2 — the provision rule forces it, a readiness-gate rule
  reads `sandbox.ready`, a park rule reads `sandbox.blocked`).
- **Fail-closed (SB5):** every non-proof path is a BLOCK, never a silent skip —
  absent docker parks the human; an undeclared/unbuildable image or a repo that will
  not build cold parks the operator; a claim only an operator-ci/lab tier can prove
  is deferred toward the operator (never gated in-sandbox). It writes `sandbox.ready`
  only when the environment built the repo cold AND a sandbox-scope tier proves the
  claim; otherwise `sandbox.blocked` carries the reason. The idempotency guard (read
  `sandbox.ready` before materializing) protects a replay from a destructive
  re-materialize that would wipe in-progress `apply_patch` work.
- **Fact shape:** the readiness package the rule owns (`sandbox.provisioned`, the
  fired-once kickoff marker) is SPLIT from the package the harness owns
  (`sandbox.ready`, `sandbox.blocked`, `sandbox.attestation.image/.tier`) so no
  predicate has two writers (G5). The image digest + proven tier are harness-derived
  (from the baseline / the committed manifest), never model-supplied. Single G5
  writer of the `sandbox.ready/.blocked/.attestation.*` package (`sandbox-provisioner`).
- **Registry entry:** `provision_sandbox` (`tool`)
- **Change:** containerized-sandbox-dev-loop

## apply-patch-tool

- **Primitive considered:** a persona that "edits the files" and reports success, or
  reusing the framework `bash`/file-write tools to let the developer mutate the tree.
- **Why it cannot express this:** authoring code was the mechanism semdev entirely
  lacked (both predecessors let the model CLAIM a fix rather than land one). No rule
  can apply a unified diff or read a git-apply exit code; and a general file-write
  tool is exactly the "model writes wherever it wants, then asserts it works" hole —
  it would let the developer escape the checkout AND supply its own outcome. So a thin
  tool (`internal/tools/applypatch`) wraps the `runspace.Patcher` seam: the developer
  emits a unified diff, the harness PATH-GUARDS every touched file to inside the run's
  checkout (`safeJoin` — a `..`/absolute target is rejected before git runs; `git apply`'s
  own escape protection is the backstop, not the guard) and applies it with `git apply
  -p1`. The checkout is the host dir bind-mounted at the container's `/work`, so applying
  on the host root IS authoring in the sandbox the dev loop (measure/floors, in-container
  at g7) then reads.
- **G3 (the load-bearing property):** the schema takes ONLY the diff — never a pass/fail
  or "it works" field. The developer supplies the intelligence (the change); whether the
  task then passes is a SEPARATE harness measurement (`measure_task`), so a developer can
  never assert its own change works. A rejected diff (escape, malformed, or a clean-apply
  conflict) is a tool error the developer re-authors from, not a silent success.
- **Fact shape:** none — apply_patch stamps NO graph fact and fires no transition (G2).
  It is a pure checkout mutation; the dev-loop bookkeeping (`task.attempt`) and the
  measured outcome (`measurement.result`) are stamped by their own harnesses in the loop
  (g7). No G5 writer (it writes no fact).
- **Registry entry:** `apply_patch` (`tool`)
- **Change:** containerized-sandbox-dev-loop

## read-workspace-tool

- **Primitive considered:** templating the checkout's file contents into the developer/
  reviewer prompt via a rule's `$entity.triple.<pred>` substitution, or reusing a framework
  file-read/`bash` tool.
- **Why it cannot express this:** the reshape makes the developer (Amelia) and reviewer
  (Quinn) loops bounded MULTI-TURN (`tool_choice: auto`), so they must READ the artifact
  before authoring/judging. File contents are not triples — a rule cannot template them, and
  it cannot populate `TaskMessage.Context` (audit-verified), so prompt templating + a read
  tool is the whole channel. A general file-read/`bash` tool is the "model reads (and could
  write) anywhere on the host" hole both predecessors died on — it would let a loop escape
  the run's checkout. So a thin tool (`internal/tools/readworkspace`) wraps the checkout seam:
  it resolves the run's `runspace.Checkouts.Root`, PATH-GUARDS the requested path to inside
  the checkout (`runspace.SafeJoin` — the read sibling of apply_patch's write guard), reads
  read-only, and paginates under the component's 32KB `ToolResultMaxBytes`.
- **G3 (the load-bearing property):** read-only — it stamps no fact and takes no outcome; it
  returns bytes. A path escape/absent file is an invalid-args tool error the loop re-reads
  from, not a silent success.
- **Fact shape:** none — it writes no graph fact and fires no transition (G2). No G5 writer.
- **Registry entry:** `read_workspace` (`tool`)
- **Change:** simplify-m0-execution-rail

## read-diff-tool

- **Primitive considered:** templating the cumulative diff into Quinn's prompt, or letting
  the reviewer shell `git diff` via a `bash` tool.
- **Why it cannot express this:** the reviewer reviews the AUTHORED CHANGE, which is the
  `git diff <base>..<committed attempt>` of the run's checkout — not a triple a rule can
  template, and (as with read_workspace) `TaskMessage.Context` cannot be populated. A `bash`
  tool is the host-escape hole. So a thin tool (`internal/tools/readdiff`) wraps a checkout
  seam (`runspace.Checkouts.Diff`): it resolves the run's checkout, finds the pristine base
  (`git rev-list --max-parents=0 HEAD`), and returns `git diff base..HEAD` — HEAD is the
  latest committed attempt (`attempt.commit`) under the one-in-flight serialization invariant,
  so what Quinn reviews is exactly the committed tree the cold verify proves.
- **G3 (the load-bearing property):** read-only, no arguments, stamps no fact.
- **Fact shape:** none — it writes no graph fact and fires no transition (G2). No G5 writer.
- **Registry entry:** `read_diff` (`tool`)
- **Change:** simplify-m0-execution-rail

## open-pr-tool

- **Primitive considered:** a rule stamping `pr.ref` directly, or the (nonexistent)
  `pr-delivery-adapter` writer the vocab reserved.
- **Why it cannot express this:** delivery is an OUTWARD action — at M2 it opens a live
  forge PR (a network call to GitHub/GitLab, a real URL), which no rule can perform. The tool
  is the seam that action lives behind; at M0 it records a deterministic LOCAL delivery stub
  so the arc has an honest terminal, and the forge-io adapter swaps in the live PR at M2
  (same predicate, same single writer role). **M0 honesty (not a placeholder-pass):** the
  coherence gate that reaches open_pr GENUINELY passed — a real cold-container verify=pass, a
  real approved verdict, a real `openspec.validated`; only the delivery TARGET is a stub, a
  declared M0 non-goal. semspec faked the verify OUTCOME (a placeholder that WAS the pass);
  here the outcome is real and only the transport is stubbed. G3: the schema takes no
  arguments — the harness forms the ref, the model only triggers delivery.
- **Fact shape:** the single `pr.ref` scalar on the run (G2 — no transition; a run-closing
  rule reads it). Its vocab writer was reconciled from the placeholder `pr-delivery-adapter`
  to `open-pr`, the tool that actually stamps it (G5/G10).
- **Registry entry:** `open_pr` (`tool`)
- **Change:** containerized-sandbox-dev-loop

## semsource-read-proxy-tools

**Tools: `code_context` · `code_impact` · `code_search` · `doc_context` (capability semsource-ab)**

Why a tool and not a rule/persona/fact (G1): the semsource A/B condition
(integrate-semsource-ab-harness) gives the developer loop OPTIONAL read access
to semsource's semantic knowledge graph. The framework has NO MCP client seam
(verified against the module cache: every "MCP" hit is gateway-side and the
module carries no modelcontextprotocol dependency) and no generic HTTP-proxy
tool; the sanctioned extension point is the executor registry. Each proxy is a
thin read over semsource's documented public HTTP surface
(`POST /code-context/<verb>`, `POST /doc-context/context`) with the exact
product-surface tool names, so semsource's own docs and prompts transfer.

Alignment posture:

- **Read-only** — no facts stamped (no G5 writer), schemas take a single
  `query` parameter (no outcome fields, G3), results return as tool content.
  NO semsource fact ever enters semdev's graph (G9 untouched).
- **Always registered, conditionally advertised** (the D1/D2 asymmetry):
  registration is unconditional — schema-only with a LITERAL-nil client absent
  a configured endpoint, failing loudly if executed (the github_list_comments
  precedent) — so the G3 schema census sees every schema. ADVERTISEMENT is the
  condition lever: only the semsource-condition variant dispatch pack appends
  the four names to the developer allowlist, and post-#551 an unadvertised
  tool cannot be called.
- **Fail-loud** — a nil-client execution and any upstream fault return an
  explicit tool errResult (trajectory-visible, D4); never an empty success,
  never a silent fallback.
