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
  nested OpenSpec model (proposal, per-capability deltas, ordered tasks) from a
  flat fact set and re-serializing it to canonical markdown — the inverse of the
  format engine's `Facts()` (`ChangeFromFacts` + `RenderChangeFolder`). A rule's
  `$`-templating substitutes single predicate values into a fixed string; it
  cannot re-group deltas by capability, order tasks by index, or JSON-decode the
  scenario arrays. That reconstruction is deterministic Go in `internal/openspec`;
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
  per delta — reconstructed from the flat fact set (`ChangeFromFacts`) and
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
- **Why it cannot express this:** the verdict must be FLOORED by the harness facts,
  and that floor is `measurement.CanApprove` — "every REQUIRED task (each projected
  `task.spec.<i>`) has exactly one passing measurement, re-derived from the raw exit
  evidence." No rule can express it: a rule matches a triple, it cannot enumerate an
  unknown number of required tasks, count exactly-one-measurement-each, and re-derive
  a pass from `ran`/`exit_code`/`timed_out`. Nor can the persona own the outcome
  (G3/D4): a false success claim must not earn approval however confidently the work
  describes itself, so approval cannot be a model-supplied field — it is DERIVED here
  (`approved ⟺ CanApprove(required, observed) ∧ no findings`). The tool reconstructs
  the measurements via `measurement.ResultsFromFacts` (fails CLOSED on unparseable
  evidence — a fact it cannot read is a failure, never a defaulted approve), reads
  the required set from `task.spec.*`, and stamps the single `review.verdict`.
- **Additive-only, structurally (7.3):** Quinn's only input is FINDINGS (required
  changes); an open finding blocks approval, but a finding can never weaken
  `task.spec` because this tool's single writer is `reviewer-quinn` and it stamps
  ONLY `review.verdict` — it holds no writer for `task.spec` (G5 single-writer makes
  the "findings never relax the spec" scenario impossible, not merely disallowed).
- **Fact shape:** `review.verdict` is an EXACT predicate (not a namespace) — one
  current verdict per run, upserted latest-wins (a re-review after fixes replaces it,
  the graph's replace-per-`(subject,predicate)`), which is what the `open_pr` gate
  rule reads. Contrast `measurement.result.*` (per-task namespace): a verdict is
  singular per run, a measurement is per-task. Fires no transition (G2) — the gate is
  a rule on `review.verdict` (wired with the coordinator spawn rules + clean-room
  `verify.result` at a later group). Single G5 writer of `review.verdict`
  (`reviewer-quinn`).
- **Registry entry:** `submit_review` (`tool`)
- **Change:** m0-walking-skeleton-spine

## brownfield-spec-projector

- **Primitive considered:** a rule/persona that reads a target repo's
  `openspec/specs/` and an LLM that interprets the artifacts into facts on the
  ingest path.
- **Why it cannot express this:** ingest MUST be deterministic and model-free
  (G3) — a model interpreting the artifacts is exactly the LLM-supplied-fact class
  the constitution forbids on the measurement/ingest path. Parsing OpenSpec
  markdown into the nested spec model and projecting it to `openspec.spec.*` facts
  is the format engine's `ParseSpec`/`Facts()` (ported, dep-free); the projector
  is the thin deterministic Go that walks the specs tree, applies the single
  owner, and retains each source file's raw bytes by content-hash reference for
  provenance. No rule can walk a directory, hash bytes, or re-group a spec's
  requirements. It is the single G5 writer of `openspec.spec.*`.
- **Registry entry:** none yet — at M0 this is the library projector core with its
  red-first pins; its registered ingest component (which wires it onto the raw
  lane and stamps the facts on their spec entities) and the matching
  `registry.Entries` entry land with the runtime boot path (group 11).
- **Change:** m0-walking-skeleton-spine
