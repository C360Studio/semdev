# standards-via-lessons — design

## Context (verified against the code, 2026-08-16)

Two exploration passes ground this design — semdev seams and the beta.160
substrate mechanics. Load-bearing facts:

- **Injection is already live and needs ZERO semdev code.** `agentic-loop`
  wires `SetLessonReader` unconditionally when NATS is present
  (framework `component.go:290-296`, no config knob); per dispatch the scope
  is exactly `tag:<TaskMessage.Role>` (`lessons.go:157-163`) and semdev's
  spawn roles are `coordinator|developer|reviewer|conversation`
  (rule files under `configs/rules/`). `Scope.EntityIDs` is unused at
  beta.160 — role tags are the ONLY scoping axis. Matcher bounds: K=10
  (max 25), 4KB byte budget, severity DESC → created-at DESC ordering;
  active-only. Rendered block: `[Lessons — …]` + one line per lesson with its
  entity ID, joined into the system prompt once per loop start.
- **Birth is importable and idempotent.** `agentictools.NewNATSLessonStore`
  (exported) creates `{org}.{platform}.agent.lesson.record.{uuid}` entities
  via strict `graph.mutation.entity.create`; on EntityExists it verifies the
  four identity fields (category, applies_to, summary, evidence) and returns
  `created=false` — idempotent re-birth for free. Birth is NOT contract-bound
  (CreateEntityRequest has no contract field); only lifecycle reconcile is.
  Injection-form bound = 320 bytes, reject-never-truncate; evidence ≥1 valid
  6-part entity ID; applies-to grammar `tag:<token>` | `id:<≥3 segments>`.
- **Promotion is Lane 1 only.** `agentictools.LessonCurator` (exported;
  `NewLessonCurator(writer, reader, logger)` where one
  `*projection.MutationClient` satisfies both interfaces) refuses to promote
  unless EVERY cited evidence entity resolves; Retire/Supersede reconcile the
  full `lesson-lifecycle` group (status, superseded-by, retired-at) so
  sibling lifecycle predicates cannot survive a transition. The reference
  rule pack is optional Lane-2 mechanics only — the framework README states
  promotion MUST route through the curator (no evidence check exists in the
  rule lane), and ships no promote tool.
- **The contract must be MIRRORED.** The lesson projection contract is
  `builtinprojection.LessonRecordContractName = "agentic.lesson-record"`
  (message type `agentic.agent_lesson.v1`, pattern
  `*.*.agent.lesson.record.*`, birth-predicates list + one reconcile group
  `lesson-lifecycle` = status/superseded-by/retired-at) — in an INTERNAL
  framework package semdev cannot import. semdev hand-mirrors it in
  `internal/graphown` (the CLAUDE.md-recorded precondition; precedent: the
  `write_todos` builtin-skip note in `boot/runtime.go:625-644`).
- **Provision pipeline slot**: `provisionsandbox.Provision()` steps are
  alreadyReady → dockerCheck → Sources.Resolve → Checkouts.Materialize →
  Manifests.Resolve → ProveBaseline → warm → ready. The sync step slots
  after Materialize (checkout root in hand) and before ProveBaseline, as a
  new narrow seam on `ProvisionDeps` (`runspace/manifests.go` is the shape
  precedent). Provision writes via `graphown.Clients.Writer(owner)`;
  strict-birth `Create` is gated by `graphown.createOwners` (today only
  `admission-check`).
- **Floors are pure/structural** (`internal/floors` reads source, never runs
  it), evaluated by the floors station via `checkfloors.RunFloors`; the
  existing run-a-command-in-the-sandbox-and-stamp shape is
  `measuretask.go` (`sandboxes.Resolve` → `runner.Exec(sh -c)` →
  `writer.Replace`).
- **Quinn's brief** = persona fragment (`reviewer/00-identity.md`) ∥ injected
  lessons ∥ the spawn prompt; the verdict is DERIVED (`submit_review` schema
  has no outcome field): approved ⇔ measurement pass AND zero findings. So a
  must-standard violation blocks approval simply by BEING a finding — no
  verdict mechanics change at all.

## Goals / Non-goals

Goals: the five repo-standards requirements + the two modified capabilities,
with human authoring trivial, zero new injection plumbing, and the lesson
substrate adopted through its validated paths only.

Non-goals: AGENTS.md free-text ingestion; the debrief/review-pattern
promotion loop; `emit_lesson` advertisement to any loop; any `id:`-scoped
lessons (roles are the v1 axis); K/byte-budget tuning (framework-fixed).

## D1 — the standards file: `.semdev/standards.yaml`, strict parse

One conventional path. YAML (comments + multiline for humans; JSON stays a
non-goal). Shape:

```yaml
version: 1
standards:
  - id: eng-test-traceability        # kebab token, unique in file
    text: "Every new test must reference the scenario it verifies."
    severity: must                   # must | should | may
    roles: [developer, reviewer]     # optional; default = both
checks:
  - name: go-vet
    command: go vet ./...
    required: true                   # required gates; optional surfaces
```

Strict parse, fail-closed (the spec's malformed-parks scenario): unknown
fields, duplicate ids, invalid severity/roles, empty text, or a non-1 version
all reject with the exact defect named; the parser is a pure function with
red-first table tests. Roles are validated against the two injectable roles
(`developer`, `reviewer`) — a standard scoped to anything else is a parse
error (fail-closed beats silently-never-injected).

## D2 — the source entity: honest evidence, 3 new predicates (G9)

Evidence must cite a graph entity that exists before Promote resolves it, and
provenance must be honest — the standard derives from the FILE, not the run.
The sync step therefore births one source entity per provisioned
standards-file content:

- Entity: `{org}.{platform}.repo.standards.source.{digest12}` (content
  digest — same file bytes ⇒ same entity, cross-run idempotent).
- Predicates (the change's entire G9 cost, single writer `standards-sync`):
  `repo.standards.digest` (full sha256), `repo.standards.path`
  (repo-relative path), `repo.standards.repo` (owner/repo — the retirement
  scope key, D5).
- Birth is strict `Create` via the existing graphown `Creator` lane
  (`createOwners` += `standards-sync`); EntityExists = the idempotent
  duplicate signal, exactly the admission-record pattern.

Rejected alternative: citing the RUN entity (zero new vocab but dishonest
provenance, and retirement scoping would have nothing to key on).

## D3 — birth via the framework store, mapped deterministically

The sync step reuses `agentictools.NewNATSLessonStore` (exported, idempotent,
identity-checked) rather than re-deriving birth mechanics. Mapping per
standard:

| lesson field | value |
|---|---|
| category | `repo-standard` (open taxonomy, rule-matchable) |
| polarity | `best_practice` |
| severity | must→`critical`, should→`warning`, may→`info` |
| summary | the standard's normative text |
| detail | `<id> — declared in <path> @ <digest12> of <owner/repo>` |
| injection-form | `[std:<id>] MUST/SHOULD/MAY <text>` (≤320B enforced pre-birth, reject naming the id) |
| evidence | the D2 source entity ID |
| applies-to | `tag:developer` / `tag:reviewer` per roles (default both) |
| status | born `proposed` (the store's invariant) |

Identity: UUIDv5 over the same four fields the store's conflict check reads
(category, sorted applies-to, summary, sorted evidence), under a
semdev-standards namespace UUID — so the store's EntityExists verification
holds and re-sync of an unchanged file is a no-op. Because summary, applies-to
and evidence (the source digest) are identity inputs: editing a standard's
text, roles, or the file at all births a NEW record and retires the old
(D5) — records are immutable snapshots, never edited in place.

## D4 — promotion: the explicit policy, curator Lane 1, in the sync step

After birth the sync step calls `LessonCurator.Promote` for every
file-derived record still `proposed`. The policy is named in code and in the
alignment note: *repo-file-derived standards auto-promote because the git
commit / PR review of the standards file is the human gate* (the curator doc
explicitly sanctions a product auto-promotion policy). Scope guard: the sync
promotes ONLY records it just ensured exist from the file it just parsed —
it never lists-and-promotes, so no other proposed lesson can ride the policy
(the spec's non-file-lesson scenario).

G2 analysis: lesson lifecycle transitions here are Go-driven BY FRAMEWORK
DESIGN — the rule lane cannot resolve evidence and the framework ships the
curator as the validated path (its README mandates Lane 1 for promotion).
This is not a product reconciler compensating for an engine gap; it is the
engine's own sanctioned surface. Recorded in the framework-alignment note.

The curator is constructed once at boot from the shared graphown mutation
client — which requires D6's contract mirror in the client's contract set.

## D5 — retirement: removed standards retire, scoped to the repo

After promote, the sync lists `agent.lesson.record` entities (the same
prefix query the injector uses), filters `category == repo-standard`, and
resolves each candidate's evidence → source entity → `repo.standards.repo`.
For candidates whose repo matches the provisioned repo and whose entity ID is
NOT in the file's freshly-computed expected set and whose status is `active`
or `proposed`: `LessonCurator.Retire`. Cross-repo isolation is the
`repo.standards.repo` comparison (a multi-target deployment never
cross-retires); volume is bounded by the substrate's expectations (well under
one page). Retirement is history-preserving (status flip + retired-at, per
the reconcile group) — nothing is deleted.

## D6 — the contract mirror + bootstrap (the CLAUDE.md precondition)

`internal/graphown` gains the hand-mirrored `agentic.lesson-record` contract:
message type `agentic.agent_lesson.v1`, pattern `*.*.agent.lesson.record.*`,
the birth-predicate list, and the single reconcile group `lesson-lifecycle`
(`agent.lesson.status`, `agent.lesson.superseded-by`,
`agent.lesson.retired-at`), exposed as `graphown.LessonRecordMirror()` and
appended by `AllContracts()` — NOT inside the memoized `Contracts()`
derivation, which stays the pure vocab census (appending there would poison
the owner/census pins; go-review R4 records the as-built shape). A conformance pin locks the mirror's literal values
(name, group, predicate set) with a comment naming the upstream source file,
and the e2e journey (D9) is the behavioral proof the mirror matches the wire.
The D2 source-entity contract derives from the normal vocab + entityClass
tables (new entity class + pattern const).

## D7 — the checks lane: floors extension, base-ref read, in-container exec

`checkfloors.RunFloors` gains a repo-checks stage with two new narrow deps
(the warm-sandbox resolver + container runner — the measuretask shape):

- **Where checks come from**: the standards file at the run's base revision —
  `git show refs/semdev/base:.semdev/standards.yaml` in the checkout. Reading
  the base ref (which provisioning already maintains as the diff anchor)
  makes the checks the repo's law AS PROVISIONED: an attempt cannot weaken or
  drop a required check even if a human approved the standards file into
  `target_files`. Absent file at base = zero checks, floors unchanged.
- **Execution**: per check, `runner.Exec(sb, ["sh","-c", command])` in the
  run's warm sandbox (SB2 — repo-authored commands only ever run
  in-container), bounded by the existing measure-exec timeout discipline.
- **Stamping**: each check appends a `floor.finding` via the existing
  findings writer — floor name `repo-check:<name>`, the command's real exit
  status in the detail (G3; the harness ran it, the harness stamps it).
  `required: true` + non-zero exit = a rejecting finding (routes exactly like
  a built-in floor); non-required failures stamp non-rejecting findings.
- The floors station's "no model turn" property is untouched — checks are
  deterministic subprocess runs.
- Parse errors of the base-ref file at floors time fail the floors turn
  loudly (the malformed file would already have parked at provision for the
  HEAD copy; the base-ref copy differing malformed is a pathological state
  that must not silently pass).

## D8 — personas: the judgment lane

- `configs/personas/fragments/reviewer/10-standards-contract.md` (new): when
  `[std:<id>]` entries appear in the brief, review the attempt against each;
  a violated MUST is a finding and the finding text MUST cite the `std:<id>`;
  standards tighten, never weaken, the task spec or the measurement floor.
- `configs/personas/fragments/developer/10-standards.md` (new): `[std:<id>]`
  entries are the target repo's law for this work; MUST entries are
  non-negotiable constraints on authored code.
- No verdict mechanics change: `submit_review` already derives
  `changes_requested` from any finding's existence — a must-citing finding
  blocks approval with zero schema/tool change (G3 intact).

## D9 — evidence plan (the bridge proof)

New journey `TestBridgeProofRepoStandardsReachBriefsAndGate` (red-first per
group where the shape allows): a fixture repo carrying a stripped
standards file (one developer must, one reviewer must, one may, one required
check, one non-required check) →

1. provision births + activates exactly the declared records (graph asserts:
   status active, evidence resolves, idempotent on re-run);
2. the developer spawn's captured mock prompt contains the developer
   `[std:]` lines and NOT the reviewer-only one; the reviewer's contains the
   reviewer set (the mock-LLM harness captures prompts);
3. the required check runs in-container and its finding gates exactly like a
   floor (a fixture where the check fails → floors reject → no review);
4. the arc completes green when standards are satisfied.

Plus unit/integration pins per group (parser tables, idempotent re-sync,
oversize rejection, retirement + cross-repo isolation, base-ref check
immunity, contract-mirror literals) and the G8 scan extension: the
fixture-vocabulary conformance walk gains `.yaml`/`.yml` so standards
fixtures cannot smuggle coaching (B10).

## Sequencing / merge notes

- Independent of PR #7 at the code level (disjoint files). At SPEC-SYNC
  level both changes modify `dev-from-task`'s floors requirement — whichever
  archives second re-merges the requirement text over the other's synced
  form. Recorded here so the second archive expects the conflict.
- semstreams stays at beta.160; if the beta.161 bump lands first, re-verify
  the four upstream anchors (store, curator, contract literals, scope
  derivation) against the new module cache before group 3.
- The B10 posture hardens the fixture side only; live repos may write
  anything — injection bounds and strict parse are the containment.

## Risks / trade-offs

- **Prompt-injection surface**: standards text is repo-authored and enters
  agent briefs verbatim. Bounded by: the 320B/record + 4KB/brief caps, strict
  parse, and — the real backstop — every consequential outcome staying
  harness-gated (measurement, floors, clean-room verify are immune to brief
  content; a hostile standard can waste a run, not forge evidence). Noted in
  the alignment note; a lexical deny-list would be theater and is omitted.
- **K=10 ceiling**: >10 standards per role and the lowest-severity tail drops
  silently at injection (deterministically). The sync WARNS at birth when a
  role's active set exceeds K — loud at authoring time, not at injection.
- **Category collision**: `repo-standard` is an open shared taxonomy; a
  future non-semdev writer using the same category would enter D5's candidate
  set — the repo-scope evidence check is the isolation, and the retirement
  filter refuses candidates whose evidence lacks a resolvable
  `repo.standards.repo`.
- **Contract-mirror drift**: an upstream rename of the contract/group breaks
  Promote loudly (contract lookup fails — fail-closed, not silent); the
  bump-time re-verify covers it.
