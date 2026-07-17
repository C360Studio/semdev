## Why

semstreams beta.147 is a pre-v1 BREAKING wave (883 files, gh#531/#532/#534–539):
canonical entity-ID contract, a canonical predicate contract enforced at
rule-authoring AND graph-write time, framework package-boundary clean break
(ADR-075), and graph-index replacement semantics. It is a destructive cutover —
no compatibility reader, no in-place migration; incompatible graph state is wiped
and reseeded (semdev already does this per-boot via `resetNATS`).

The framework's sister-repo cutover checklist rated semdev LIGHT-MEDIUM from a
static import grep, but booting semdev on beta.147 surfaced the real scope: the
rule-authoring predicate validator (ADR-036, run unconditionally on every rule
load) rejects semdev's entire non-canonical vocabulary. The break is loud and
fail-fast — every wrong field is a boot-time config-validation error with an
instructive message — so the audit is literally "boot semdev on beta.147 and work
the error list," but the surface is wide: ~35 product predicates, 27 rule packs,
the openspec-change graph projection, the entity-watch config, and every Go
writer/reader/test/fixture.

A confirmed silver lining: **semstreams #530 landed in beta.147.** The
`hasStatefulRuleActions` gate now admits `OnRecovery`, so the restart-recovery
park is unblocked at the gate (the compounding stale-revision guard is unchanged,
so it still needs real-restart verification — tracked separately, not in scope
here). The `TestTripwireOnRecoveryRoutingGate` tripwire is flipped to a regression
guard.

## What Changes

- **BREAKING — canonicalize the semdev fact vocabulary to the 3-segment
  lower-kebab `domain.category.property` contract.** At M0 (honestly single-task,
  reshape R9) the per-index/per-key segment is DROPPED, keeping semdev in the
  simple-yet-detailed lane: `measurement.result.0.passed` → `measurement.result.passed`,
  `task.spec.0.target_files` → `task.spec.target-files`, `review.verdict.0` →
  `review.verdict.value`, `run.awaiting_human` → `run.awaiting.human`, `pr.ref` →
  `delivery.pr.ref`. Multi-task keying (the index) becomes an explicit M1 seam
  (encode it in the entity ID, not the predicate).
- **BREAKING — store the OpenSpec change as a scalar document, not a flattened
  triple tree.** `openspec.change.<slug>.delta.<cap>.<rid>` / `.task.<i>.<field>`
  are 5–6 segments — irreducible to 3 by dropping one key. Replace the deep
  projection with a single canonical scalar the create-change tool serializes and
  `changefacts` deserializes (simpler, canonical-compatible, and honest: the
  Change is one artifact, not hundreds of independent facts).
- **BREAKING — flatten the per-floor finding structure.** `floor.finding.<i>.<floor>.<field>`
  (4–5 segments) collapses to a canonical aggregate (`floor.finding.rejected`) plus
  an optional detail scalar.
- **Declare the canonical semdev predicates** in a semdev vocabulary registry
  `init()` (the framework's `RegisterPredicate` requires canonical shape and is the
  authority `RequireDeclaredPredicate` consults). Import the framework vocab
  package(s) that declare the `agent.*` / `coordinator.*` predicates semdev reads,
  so bare condition references resolve.
- **Rewrite rule conditions and actions.** Conditions referencing message/state
  fields the vocabulary does not own use the explicit `$message.*` / `$state.*`
  namespace; action `add_triple` predicates use the canonical declared literals.
- **Migrate the entity-ID config** — `entity_watch_patterns` → `entity_watch_buckets`,
  add per-rule `entity.pattern` in the six-position declaration language; audit all
  entity IDs against the six-position contract (semdev run IDs are already 6-part).
- **Re-home the removed `input/github-webhook` package** → semdev-owned
  `internal/forge/githubwebhook` (types-only; done).
- **Bump go.mod beta.146 → beta.147**; flip the `#530` tripwire to a regression
  guard (done).

## Impact

- Affected specs: forge-io, run-lifecycle, dev-from-task, harness-measurement,
  openspec-io, clean-room-verify, sandbox (predicate renames; the openspec-io
  change projection is redesigned).
- Affected code: `internal/vocab`, `internal/changefacts`, every tool writer/reader
  (`applypatch`, `measuretask`, `checkfloors`, `submitreview`, `openpr`,
  `projecttasks`, `validatechange`, `provisionsandbox`, `createchange`,
  `hydratechange`), all 27 rule packs, `configs/semdev-bootstrap.json`, and every
  unit/conformance/e2e test + fixture.
- Destructive: incompatible graph state is wiped (per-boot `resetNATS` already does
  this). No data migration.
- Guardrails: G5 (single writer per canonical predicate preserved), G9 (the rename
  does not add predicates — it renames the existing set; the change blob and
  finding flatten REDUCE vocabulary), G10 (docs/specs re-projected), G2/G3 unchanged.
