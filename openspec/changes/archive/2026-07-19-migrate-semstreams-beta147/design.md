# Design — migrate to semstreams beta.147

## Context

beta.147 enforces two contracts that break semdev:

1. **Canonical predicate contract** (`vocabulary/predicate_contract.go`): a
   predicate is exactly three lower-kebab segments `domain.category.property`, each
   `[a-z][a-z0-9]*(-[a-z0-9]+)*`, ≤64 bytes/segment. Enforced at rule-authoring
   (`RuleProcessor.ValidateDefinition` → `validateConditionFields` /
   `validateActionLists`, run UNCONDITIONALLY on every rule load) and at graph-write
   (entity-state contract). `RequireDeclaredPredicate` hard-fails on non-canonical
   shape first, then on canonical-but-undeclared. Condition fields may instead use an
   explicit `$message.*`/`$state.*`/`$entity.*` namespace (skips the check); action
   mutation predicates have **no** escape — they must be canonical AND declared.
2. **Canonical entity-ID contract** (gh#531): six positions
   `org.platform.domain.system.type.instance`, `[A-Za-z0-9][A-Za-z0-9_-]*` each;
   `entity_watch_patterns` → `entity_watch_buckets`; per-rule `entity.pattern`.

Nothing fails silently — every violation is a boot-time error. The migration is
boot-driven: fix the first error, re-boot, repeat.

## Decisions

### D1. Drop the per-index/per-key segment at M0 (single-task, reshape R9)

semdev is honestly single-task at M0. The `.<i>` task index and `.<slug>` change key
exist only for an M1 multi-task/multi-change future. At M0 they are always `0` / the
one change, so DROP them from the predicate and recover a clean 3-segment name. The
multi-key future moves the key into the ENTITY ID (a per-task/per-change entity), an
explicit M1 seam — not per-index predicates. This keeps the vocab simple-yet-detailed.

### D2. Canonical predicate mapping (the load-bearing table)

| old | new (canonical) | writer (G5 unchanged) |
|-----|-----------------|-----------------------|
| `intake.actor` | `intake.actor.login` | admission-check |
| `intake.admitted` | `intake.actor.admitted` | admission-check |
| `run.issue_ref` | `run.issue.ref` | issue-intake-adapter |
| `run.change_approved` | `run.change.approved` | approval-adapter |
| `human.signal` | `human.opt.signal` | comment-adapter |
| `run.awaiting_human` | `run.awaiting.human` | park-rule |
| `run.dev_kickoff` | `run.dev.kickoff` | dev-rewake-rule |
| `run.projection_kickoff` | `run.projection.kickoff` | dev-projection-rule |
| `pr.ref` | `delivery.pr.ref` | open-pr |
| `openspec.validated` | `openspec.change.validated` | openspec-validate-harness |
| `openspec.archived` | `openspec.change.archived` | openspec-archive-harness |
| `openspec.change.<slug>.*` | `openspec.change.document` (scalar, D3) | create-change-author-tool |
| `openspec.spec.<cap>.*` | `openspec.spec.document` (scalar, D3; brownfield/group 9) | brownfield-spec-projector |
| `task.spec.<i>.<field>` | `task.spec.<field>` (goal/budget/assumptions/non-goals/target-files/test-command) | task-projector |
| `task.attempt.<i>` | `task.attempt.instance` | dev-dispatch-rule |
| `attempt.commit` | `attempt.commit.sha` | patch-committer |
| `floor.finding.<i>.rejected` | `floor.finding.rejected` (aggregate) | floor-tools |
| `floor.finding.<i>.<floor>.<field>` | `floor.finding.detail` (scalar, D4) | floor-tools |
| `floor.finding.<i>.attempt` | `floor.finding.attempt` | floor-tools |
| `measurement.result.<i>.<field>` | `measurement.result.<field>` (passed/command/commit/ran/exit-code/timed-out) | measurement-harness |
| `review.verdict.<i>` | `review.verdict.value` | reviewer-quinn |
| `review.findings.<i>` | `review.findings.value` | reviewer-quinn |
| `verify.result` | `verify.cleanroom.result` | verify-harness |
| `evidence.run` | `evidence.ledger.run` | evidence-ledger |
| `sandbox.provisioned` | `sandbox.provision.marker` | sandbox-provision-rule |
| `sandbox.ready` | `sandbox.provision.ready` | sandbox-provisioner |
| `sandbox.blocked` | `sandbox.provision.blocked` | sandbox-provisioner |
| `sandbox.attestation.<field>` | `sandbox.attestation.<field>` (already 3-seg) | sandbox-provisioner |
| `dev.dispatched` | `dev.developer.dispatched` | dev-dispatch-rule |
| `dev.floors_dispatched` | `dev.floors.dispatched` | dev-floors-rule |
| `route.passed` | `route.attempt.passed` | route-mirror |
| `route.rejected` | `route.attempt.rejected` | route-mirror |
| `route.verdict` | `route.review.verdict` | route-mirror |
| `route.attempt.<i>` | `route.attempt.instance` | route-mirror |
| `route.not_clean` | `route.attempt.unclean` | dev-route-rule |
| `route.routed` | `route.attempt.routed` | dev-route-rule |
| `delivery.routed` | `delivery.route.routed` | dev-route-rule |

Names are refined against the validator during boot-driven execution; the SCHEME
(drop-key, kebab, pad-to-3, keep-writer) is fixed. Segments must not start with a
digit and carry no underscore.

### D3. Store the OpenSpec change (and brownfield spec) as a scalar document

`openspec.change.<slug>.delta.<cap>.<rid>.<field>` is 6+ segments — the deep flatten
cannot be canonicalized. Replace it: `create_change` serializes the whole
`openspec.Change` to ONE scalar (`openspec.change.document`, JSON), plus the existing
slug pointer as `openspec.change.slug`. `changefacts.Hydrate` deserializes the scalar
instead of reconstructing from a triple tree. This is simpler AND more honest — an
OpenSpec change is one artifact, not hundreds of independent facts. It also shrinks
the vocabulary (G9). The brownfield `openspec.spec.<cap>.*` gets the same treatment
when group 9 lands (deferred; note it, don't build it now).

### D4. Flatten per-floor findings to aggregate + detail scalar

`floor.finding.<i>.<floor>.<field>` collapses to `floor.finding.rejected` (the
aggregate bool the route reads) plus `floor.finding.detail` (a scalar carrying the
per-floor prose for the human/re-entry prompt). The route only ever reads the
aggregate; the detail was never matched in a condition.

### D5. Declare canonical predicates in a semdev vocab registry `init()`

Add `internal/vocab.Register()` (or an `init()`) that calls the framework's
`vocabulary.RegisterPredicate` for every canonical semdev predicate (with its
writer/capability metadata). `RegisterPredicate` requires canonical shape, so D2's
names are the gate. semdev's boot imports this package so the `init()` runs before
rule load. For the framework predicates semdev READS (`agent.loop.role`,
`agent.run.phase`, `coordinator.decision.next-action`, …), import the framework vocab
package that declares them (blank import in boot) so they resolve; if a framework
predicate was renamed in the wave, use the new name (discovered boot-driven).
gh#546 (dynamic `TryRegister`) is the upstream path if we ever need runtime
declaration — not needed at M0 (fixed vocabulary).

### D6. Rule condition fields — declared or explicitly namespaced

A bare condition field must be a declared canonical predicate. semdev's own facts are
declared (D5), so `route.attempt.passed` etc. resolve bare. A field that is a
MESSAGE/loop field the vocabulary does not own (e.g. `agent.loop.role` if the
framework does not declare it) uses the explicit `$state.*`/`$message.*` namespace.
Framework-declared entity predicates resolve bare once their declarer is imported (D5).

### D7. Entity-ID contract

Audit every semdev-constructed entity ID against the six-position contract (run IDs
`c360.semdev-001.agent.chain.execution.<uuid>` are already 6-part — verify all
others). Replace `entity_watch_patterns` with
`entity_watch_buckets: {"ENTITY_STATES": ["*.*.*.*.*.*"]}` in the bootstrap; give
each entity-scoped rule an `entity.pattern`. Use `pkg/types.ValidateEntityID` where
semdev validates IDs.

### D8. Package boundary — github-webhook (done)

`input/github-webhook` removed (ADR-075); re-homed to semdev-owned
`internal/forge/githubwebhook` (types-only issue cluster; PR/comment/review deferred
with their flows). Done in group 1.

## Migration order (boot-driven)

1. Bump + package boundary + tripwire flip (done) → build + unit green.
2. Canonicalize `internal/vocab` + the vocab registry `init()` (D2/D5).
3. Update every Go writer/reader to the canonical constants; blob the change (D3);
   flatten findings (D4). Compile green.
4. Rewrite the 27 rule packs (conditions D6, actions D2) + entity config (D7).
5. Update unit + conformance fixtures/pins; unit ladder green.
6. Boot-driven e2e: work the config-validation error list to zero; all four journeys
   green under real docker.
7. Docs/spec re-projection (G10); archive.

## Non-goals / deferred

- The restart-recovery park (#530 gate now open, stale-revision guard unverified) —
  separate follow-up, not this change.
- M1 multi-task/multi-change keying (index in the entity ID) — explicit seam.
- Brownfield `openspec.spec` scalar projection — with group 9.
- graph-index replacement-semantics tuning beyond boot-clean.
