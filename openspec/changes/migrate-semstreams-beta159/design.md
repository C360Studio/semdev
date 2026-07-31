# Design — migrate semstreams beta.154 → beta.159 (projection mutation client)

## Context

beta.159 deletes `agentictools.OwnedFactWriter` (the `owned_fact_writer.go` file,
its interface, and `NewNATSOwnedFactWriter`) and replaces the Go fact-write path
with the contract-bound `pkg/projection` mutation client (ADR-056). This is the
sole reason semdev does not compile against beta.159; every other cross-release
change (readiness distribution ADR-083/084, the GraphQL gateway envelope, the
match/inflight primitives) targets other sisters and does not touch semdev's code
(verified by grep: semdev consumes `graph.ingest.query.*`, never graph-index
readiness or the gateway).

**The write path stays flow-based.** The old writer was a thin wrapper over the
NATS subject `graph.mutation.entity.update_with_triples`; the new client writes
over NATS to graph-ingest through a contract. semdev hosts graph-ingest as one
in-process component reached only over NATS (config `components: [graph-ingest,
…]`; the only Go transport is `natsclient.RequestClassified`; zero direct Go calls
into graph-ingest). The migration changes the *contract and subject* of the write,
never *whether* it crosses the bus.

## Goals / Non-Goals

**Goals**
- Compile and run on the beta.159 TAG with the mock ladder and docker journeys
  green.
- Every Go owned-write goes through a `pkg/projection` client bound to a contract
  derived from the existing G5 vocab table.
- The owner-lease gate is enforced on semdev's graph-ingest with recorded ADR-056
  rollout evidence before any paid lane.

**Non-Goals**
- Changing what facts semdev writes, or which Source owns which predicate — the
  vocab table is authoritative and unchanged.
- Migrating rule-written facts. Rules write via the engine's `add_triple`; they
  are not `OwnedFactWriter` callers and are out of scope.
- The untagged `nats.go` v1.52.0 convergence — we pin the TAG (v1.48.0). Only the
  server IMAGE moves (a docker concern independent of the module pin).

## Decisions

### D1 — the vocab table is the contract source (the G5 lever)
`internal/vocab/vocab.go` already declares exactly one writer (Source) per
predicate (G5, `TestSingleWriterPerPredicate`). That table IS the projection
contract set: a `projection.Contract` per Go-writer Source, its `Groups` the
predicates that Source owns. Contracts are DERIVED from the table, not hand-authored,
and a conformance census (D8) asserts they match — so the runtime owner claim and
the checked-in writer census cannot drift.

### D2 — owner boundary = the Go-writer Source; rule writers untouched
The ~29 Sources split into Go writers (tools, stations, adapters — they used
`OwnedFactWriter`) and rule writers (`dev-route-rule`, `park-rule`,
`conversation-spawn-rule`, `issue-ref-rule`, `dev-*-rule`,
`sandbox-provision-rule` — they write via `add_triple`). ONLY the Go writers
become projection owners. The rule writers are engine-owned and unchanged.
(`brownfield-spec-projector` is NOT a rule writer — an earlier draft listed it as
one. It is a Go projector with no caller, handled by the UNWIRED exclusion below.)

The split is censused in the direction that is mechanically checkable: every vocab
Source is classified exactly once, and no exclusion entry is stale
(`TestProjectionContractsMatchVocabWriters`, `TestExclusionSetsAreNotStale`).
**A Go-write-site SCANNER — proving no Go code writes under a rule Source — does
not exist and is a named follow-on.** Doing that grep by hand is what surfaced D2b
below, so it would pay for itself; it is out of scope here only because it needs a
real AST/scan pass, not a substring match.

Go-writer owners (from the vocab census): `admission-check`, `approval-adapter`,
`conversation-adapter`, `conversation-classifier`, `create-change-author-tool`,
`experiment-intake`, `floor-tools`, `measurement-harness`, `open-pr`,
`openspec-validate-harness`, `patch-committer`, `reviewer-quinn`, `route-mirror`,
`sandbox-provisioner`, `station-harness`, `task-projector`, `verify-harness` —
**17 owners, 19 contracts** (D2a splits two owners by entity class).

**UNWIRED (as-built correction): `evidence-ledger`, `openspec-archive-harness`,
and `brownfield-spec-projector` get NO contract.** Their predicates
(`evidence.ledger.run`, `openspec.change.archived`, `openspec.spec.document`) are
DECLARED in vocab but have no Go write site today — `internal/ledger` is
schema-only, no archive harness exists, and `brownfield.Triples` has no caller.
`human.opt.signal` is the same case under a live owner (`conversation-adapter`):
the ask_human reply/resume lane is unimplemented. An ownership claim with no writer
buys nothing under observe-only and, under enforcement, would REJECT the very
writer that eventually lands (it would have to bind to the claiming owner first).
The exclusions are explicit (`graphown.unwiredPredicates`, each with its reason)
and the census asserts the three-way partition — Go-writer / rule-writer / unwired
— so a predicate cannot fall through unclassified.

### D2a — one contract per (owner, ENTITY CLASS) — as-built correction
**The original D2 "each owner = one contract" is WRONG and would not have
run.** A `projection.Contract` carries ONE `EntityPattern`, and the mutation client
REJECTS any write whose entity falls outside it
(`mutation_client.go:validateEntityIDForContract`). semdev writes onto three entity
classes, not one:

| class | pattern | what lands there |
|---|---|---|
| run | `*.*.agent.chain.execution.*` | the harness fact packages the routing rules read |
| agentic loop | `*.*.agent.agentic-loop.execution.*` | route mirrors, the authored marker, the classifier's recorded marker |
| admission record | `*.*.forge.intake.event.*` | the birth facts (`admission-check`) |

Two owners write onto BOTH the run and the loop —
`create-change-author-tool` (change blob on the run, `openspec.change.authored`
marker on the authoring loop) and `conversation-classifier`
(`conversation.intent.*` on the run, `conversation.classifier.recorded` on the
classifier loop) — so each derives TWO contracts, named `<owner>-run` /
`<owner>-loop`. `route-mirror` is loop-ONLY (all seven `route.*` predicates land on
the firing loop, never the run), which the one-run-pattern shape got wrong for
every one of its writes. `station-harness` stamps `station.dispatch.failed` on
whatever entity was dispatched — a run for run-triggered stations, a loop for
loop-triggered ones — so it claims the widened `*.*.agent.*.execution.*`; safe
because it is that predicate's only writer, so the wider cell collides with no
other owner.

No call site hardcodes a contract name: `graphown.ContractFor(owner, entityID)`
resolves it by matching the entity against the owner's contracts, and returns a
NAMED ERROR when the owner has no contract for that entity class. That is what
makes the classification load-bearing — see D3a.

**New failure mode this introduces (group-4 requirement):** the write path gains an
ENTITY-PATTERN GATE it did not have before. `station.dispatch.failed` is the
exposed case — `station.go:503` stamps on `req.EntityID`, the firing entity of a
`publish` rule, and all eight station-dispatch rules declare the watch-all pattern
`*.*.*.*.*.*`; the "it is always an agent-execution entity" invariant lives only in
each rule's CONDITIONS and is pinned nowhere. If a station's conditions ever admit
another entity class, `ContractFor` errors before the write and
`stampDispatchFailed`'s existing write-failure `logger.Error` never runs — a
silently lost terminal fact, which is a run that stalls instead of parking. Group 4
must (a) surface the `ContractFor` error on that path with the same Error log +
`c.errors` bump as a write failure, and (b) add a conformance pin that every
station-dispatch rule confines its firing entity to the run or loop class.

### D2b — the rule engine also REMOVES facts a Go owner claims (decided, not hidden)
`configs/rules/conversation/04-classifier-terminal-release.json` issues
`remove_triple` for `conversation.pending.{author,body,message-id}` on the RUN — the
exact predicates and entity class `conversation-adapter` now claims as an exclusive
replace-owned group. So D2's Go-vs-rule split is a false dichotomy for this
predicate set: it is STAMPED by Go and MUTATED by the engine.

Nothing breaks today, and the reason is narrow: the engine's removal goes over
`graph.mutation.triple.remove` (`processor/rule/triple_mutator.go`), and
`handleTripleRemove` performs no `checkOwnerLease`. That is an accident of which
lanes ADR-056 gated, not a property we designed for. The census cannot see it
either — vocab records the STAMPING writer, not the removing one.

**Decision: claim the cell, document the rule-lane exemption here and in the
package doc, and do NOT paper it over as "one writer".** The alternatives were
weighed: an `ownership.CoordinationWaiver` is the framework-blessed shape but adds
a surface with no enforcement behind it today; moving the slot-clear to an
owner-side `ReplaceOwned(Desired: nil)` would put a Go reconciler on a lifecycle
transition (G2) and destroy the rule's load-bearing four-revision ordering
(author → body → message-id → marker, each its own KV revision, which is what makes
a concurrent bridge write untearable). Re-evaluate if ADR-056 ever gates the remove
lane — at which point this becomes a hard break, not a documentation note.

### D3 — write mode per predicate — CONSERVATIVE (byte-identical behavior)
**Decision: every `ReplaceTriples` write maps to `replace-owned`; `append-evidence`
is adopted NOWHERE in this change.** semdev has no native-append call today — every
write goes through the old `ReplaceTriples`, and the "append-mirror" ledgers
(`conversation.intent.classified`, `route.attempt.instance`, …) are SIMULATED
append via read-all-then-write-full-set. Mapping those to `ReplaceOwned(Desired =
the full set the code already computes)` preserves byte-identical behavior; the code
already reads-all-then-writes-the-full-set, so the desired set is exactly what it
computes today. Adopting native `append-evidence` would CHANGE per-ledger semantics
(and a misclassification is silent data loss), so it is a deliberate per-ledger
follow-on, out of scope here. The point of this change is to compile + conform, not
to re-architect the ledgers.

Two modes in scope:
- **`replace-owned`** — every `ReplaceTriples` site. Where the code passed
  `removePredicates`, the group's predicates are those; where it read-all-then-
  wrote-full-set, `Desired` is that full set. Reconciles the complete owned set.
- **birth / `CreateWithTriples`** — primary-subject entity creation. OQ2 RESOLVED:
  the ONLY create owner is `admission-check` (the admission-record entity, via
  `RecordAdmission` → semdev's existing `EntityCreator.CreateEntityWithTriples`
  seam). `experiment-intake` is NOT a creator — it upserts a single condition label
  onto an already-minted run (`StampCondition`, replace-owned). The run entity
  itself is minted by the framework (`agentrun.Mint`), out of scope. The admission
  create uses a SEPARATE path (`natsEntityCreator` over the surviving
  `graph.CreateEntityWithTriplesRequest`) that still compiles, so it is untouched by
  the compile break — but under enforcement (Phase B, D5) an un-tokened create is
  rejected, so it migrates to `projection.EntityCreator.CreateWithTriples` BEFORE
  enforcement flips. During Phase A observe-only it may stay as-is (the meter shows
  it un-tokened, which is the signal to migrate it).

**D3c — the create migration is DEFERRED (as-built).** Beyond D5's correction that it
buys no lease coverage, `CreateWithTriples` actively swallows the signal the intake
lane is built on: it returns plain success when the entity already exists and
`createFactsMatch` holds (`mutation_client.go:586-590`). semdev's `natsEntityCreator`
exists *specifically* to surface `ErrorCodeEntityExists`, which `RecordAdmission`
turns into `ErrAlreadyRecorded` → SKIP THE WAKE. Losing it double-mints a run on a
webhook redelivery. The swap would pass today only because `sameFullTriple` compares
`Timestamp` and the record stamps a fresh one per call — an accident, not a contract.
Landing it safely needs an explicit created-vs-already-existed signal; filed as the
follow-on, not forced here.

### D3a — the GROUP is the write's blast radius (the silent-loss axis)
`ReplaceOwned` is **not** a drop-in for `ReplaceTriples`. The old writer sent
`RemoveTriples` = the caller's own explicit remove list. `ReplaceOwned` sends
`RemoveTriples` = **the entire selected group** (`mutation_client.go:714`) and then
adds `Desired`. So every predicate in the group that `Desired` does not re-supply
is DELETED from that entity. Group granularity, not mode, is where a conservative
migration silently loses facts.

Each contract therefore carries exactly ONE group (`"owned"`), scoped to one
(owner, entity class) — and the mapping is byte-identical only because every call
site already reconciles its COMPLETE owned set for the entity it targets. Verified
site by site:

- `sandbox-provisioner` — `ready` writes {ready, attestation.image,
  attestation.tier} and removed `[blocked]`; `block` writes {blocked} and removed
  `[ready, image, tier]`. Both already reconcile all four. Group-wipe reproduces
  each exactly.
- `create-change-author-tool` — removed exactly `[document, slug, revision]`, its
  whole run-class group; the loop marker is its own class (D2a).
- `floor-tools` — `RunFloors` writes all three findings; `clearFindings` becomes
  `Desired: nil`, which clears the group (what the prefix-read + remove did).
- `measurement-harness`, `task-projector`, `reviewer-quinn`,
  `conversation-classifier`, `conversation-adapter` — each writes its full flat
  package in one pass (the classifier re-supplies the whole
  `conversation.intent.classified` ledger, which is why the "append-mirror"
  survives replace-owned).
- `route-mirror` — the ONE case where two sites of one (owner, class) write
  DIFFERENT subsets: `check_floors` stamps the floors mirror on `L_n`,
  `submit_review` stamps the review mirror on the review loop. Byte-safe ONLY
  because those are **disjoint entity populations** — no floors loop ever carries
  `route.review.verdict`, no review loop ever carries `route.attempt.passed` — so
  each site's group-wipe clears only predicates never present on its entity. Note
  the migration STRICTLY WIDENS this site's radius: `submit_review` removed 2
  predicates before, and removes all 7 after. And `ownedFactsMatch` verifies the
  group EQUALS `Desired`, so a wrongful deletion returns `CommitVerified` — silent
  loss with a green receipt. The invariant lives in the RULE CONFIGS (check_floors
  is dispatched off a `role=developer` loop; `submit_review` is advertised only to
  `role=reviewer` spawns), so it is pinned there by
  `TestRouteMirrorSitesWriteDisjointEntityPopulations`.

- **`check_floors`'s mirror has a second, narrower precondition (M2).** Its old
  remove list did NOT include `route.attempt.instance` or
  `route.transient.instance`; those survived a write by replace-by-predicate only
  when the add supplied at least one object. So on an EMPTY read of `attemptObjs` /
  `transientObjs`, the old write left the prior mirror intact and the new group-wipe
  DELETES it. `route.attempt.instance` is what the retry/escalate routes count with
  `length_*`, so a zeroed count reads as budget-unexhausted and the run retries past
  its budget — on paid tokens. It holds today because the dispatch rule appends an
  instance at spawn (so `attemptObjs` is non-empty by construction) and both sets are
  monotonically non-shrinking. That is exactly the class of "holds by construction"
  precondition this migration converts into a data-loss axis, so group 4 either pins
  it red (a test where the read returns empty) or faults loudly at the site, matching
  the D7 budget posture two lines above it in `checkfloors.go`.

**Coverage of this audit:** the eight owners above. The remaining six migrated in
group 4 — `validatechange`'s clear, `applypatch`, `openpr`, `experiment`,
`approval-adapter`, `station-harness` — are all single-predicate groups, so the
group-wipe is trivially the predicate's own replace. Checked, not assumed.

Splitting `route-mirror` into per-site groups is NOT available as a safety net:
`Contract.Validate` forbids one predicate in two groups, and the two sites overlap
on `route.task.budget` + `route.attempt.instance`. Nor may a site split into two
`ReplaceOwned` calls — that would break the pinned D7 atomicity invariant (the
routes must never see the attempt count without the budget, or `unclean`'s inputs
without the transient classification).

### D3b — what the census can and cannot prove (honest limits)
The contract censuses (D8) catch mode drift, phantom/unclassified predicates, and
per-owner self-overlap. They **cannot** catch a predicate classed onto the wrong
entity: re-classing every loop predicate to the run pattern still passes every
census and `Contract.Validate` — verified by planting exactly that — and then
hard-fails at runtime on every loop-entity write. The real proof is behavioral and
lands with the call sites: every migrated write resolves its contract through
`graphown.ContractFor(owner, entityID)` rather than a hardcoded name, so a
misclassified predicate errors at the site that writes it and the per-tool unit
tests that already assert which entity each fact lands on (classifyintent's
"the mirror must be written to the loop, not merely carry it as a Subject",
checkfloors' loop-entity mirror pin) go red. **Task 4 requirement: no call site may
hardcode a contract name.**

### D4 — composition-root binding (one registry, one heartbeater, one bind per owner)
In boot (`internal/boot`): after NATS and ownership storage are ready but before
any owner writes, `ownership.EnsureBuckets` builds the registry and one
process-lifetime `Heartbeater` (`registry.NewHeartbeater`; `go hb.Run(appCtx)`).
Each Go-writer owner binds ONCE via `BindMutationClient` (or `BindAndHeartbeat`)
with its COMPLETE contract set — the one-registration-per-registry-lifetime
invariant forbids incremental or partial binds, and a second same-owner bind is
rejected with `ErrOwnerAlreadyBound`. `appCtx` cancellation on shutdown owns the
heartbeater lifecycle (the client does not stop it). Append-only owners may bind
with a nil heartbeater; any owner with a `replace-owned`/`cas-transition` group
requires the live heartbeater.

Each call site depends on the NARROW interface for its role
(`EntityCreator` / `OwnedReplacer` / `EvidenceAppender` / `AuthoritativeReader`),
not the concrete client — least authority per component.

### D5 — observe-only first, then enforce — and the meter does NOT measure coverage
graph-ingest's owner-lease check runs on BOTH `create_with_triples` and
`update_with_triples`. **An earlier draft of this decision was wrong about what it
observes, and the group-6 gate built on it was vacuous.** The corrected mechanism,
read from `processor/graph-ingest/component.go:2137-2180`:

```go
// Two-state skip (return nil — never reject):
//   - ownerToken == ""  → legacy/unowned writer (the single agreed skip signal).
if ownerToken == "" { return nil }
```

- An **un-tokened** write is skipped unconditionally, BEFORE any
  `EnforceOwnerLease` branch. It is never rejected — in either posture — and never
  metered.
- `owner_lease_mismatch_total` increments only inside `ownerToken != expected`,
  i.e. only when a token IS presented and is STALE.
- So the meter measures **token staleness, not token coverage.** It reads zero when
  everything is migrated AND when nothing is. A missed call site in group 4 keeps
  writing un-tokened, forever, silently.

This also settles the create path: `mutation_client.go:316-326` mints a token only
when a written predicate sits in an OWNING group, and a birth-only contract has
none (`Contract.Validate` rejects a birth predicate that overlaps a group). So
`admission-check` is permanently un-tokened BY DESIGN, and migrating it to
`projection.EntityCreator.CreateWithTriples` buys **zero** enforcement coverage.
It is still worth doing — the client validates the entity pattern and authorizes
the birth predicates — but not for the reason D3 originally gave.

The two-phase landing stands; only its evidence changes:

- **Phase A (this change's default):** migrate the write path so every OWNED write
  carries its owner token, and leave enforcement OFF.
- **Phase B (deliberate flip):** set `enforce_owner_lease=true` on semdev's one
  hosted graph-ingest component. Single-binary makes the "every serving instance
  enforces" precondition one flag on one component.

**The Phase-B gate is a POSITIVE coverage check, not a zero-meter reading** (group
6, rewritten). Coverage must be proven by construction, offline:

1. Every owner in `graphown.OwningOwners()` holds a live claim in the epoch after
   boot (a bind census — an owner that failed to register is a whole un-tokened
   write path).
2. No surviving Go call site writes owned facts outside a bound client — proven by
   the compile break itself (`OwnedFactWriter` is deleted, so there is no other
   write surface) plus a grep census that no new raw `update_with_triples` request
   is hand-rolled.
3. The mismatch meter is still read, but as what it is: a **staleness** signal
   (the multi-process incarnation hazard below), not a coverage signal.

**Multi-process hazard — CONFIRMED and partly mitigated (D5a).** A second
registration of an owner someone already holds mints a new incarnation and replaces
the epoch entry with NO liveness check (`registry.go:379-390`), while a
`MutationClient` captures its token once and never refreshes it — so the first
holder is permanently fenced out. The framework's `ErrOwnerAlreadyBound` cannot
catch it (per-Registry state).

Mitigations landed: (1) `BindOwners` takes an owner SUBSET and a process binds only
what it writes — `semdev launch` binds `experiment-intake` alone, not all 17, so a
running `task serve` is untouched (and the runtime never writes
`experiment.run.condition`, so even that overlap is inert); (2) an in-process guard
rejects a second bind (`ErrOwnersAlreadyBoundInProcess`), which is what forced the
e2e stand-ins to reuse the runtime's clients instead of re-binding mid-journey.

Residual, for group 6: the CROSS-process case cannot be enforced from in-tree. The
flip therefore still requires single-writer-process discipline. `registry.WatchRevival`
(`pkg/ownership/revival.go`) is the framework's producer-side quiesce and would let a
superseded process DETECT that it was fenced out — not adopted here (it is a behavior
addition beyond the migration), and named as a group-6 follow-on. Relatedly,
`Heartbeater.Run` swallows every tick failure (`heartbeat.go:108-112`, Warn only),
so a persistently failing heartbeat is log-only: worth a meter before the flip.

### D6 — the read path
`OwnedFactWriter.ReadOwnedPredicates(ctx, id, prefix)` (2 sites: checkfloors'
finding read-back, projecttasks' task.spec immutability check) maps to
`AuthoritativeReader.ReadAuthoritative(ctx, id)` + a local prefix filter (the
client returns the full `*graph.EntityState`). semdev's OWN `changefacts` reader
(over `graph.ingest.query.entity`) is a separate type, NOT the deleted writer, and
is UNCHANGED — reads that do not need the projection client's read-your-writes
guarantee stay on it.

### D7 — NATS substrate (folded in, minimal)
- Pin the semstreams module to the beta.159 TAG. `nats.go` stays v1.48.0 (beta.159
  requires exactly that) — no Go NATS dep change, and NO pin to an untagged commit.
- `docker/compose/nats.yml`: `nats:2.10-alpine` → `nats:2.14-alpine`, matching the
  house convergence substrate. Pinned by `TestNatsImageMatchesHouse` (or fold into
  the existing compose pin test).
- `Taskfile.yml`: the floating `natsio/nats-box:latest` → a pinned tag, so the
  sidecar substrate stops floating.

### D8 — the contract-census conformance pin (the anti-drift guard)
A conformance test derives the expected contract set FROM the vocab table and
asserts the bound projection contracts match: same owner per predicate, and the
mode matches the predicate's declared pattern (replace vs append-set vs birth).
This is the guard that a mode mismatch (silent, data-dropping) or an owner drift
cannot pass green. Mirrors `TestSingleWriterPerPredicate`'s bidirectional shape.

### D9 — test-double strategy
The unit fakes (`fakeWriter`, `fakeGraph`) that implement `OwnedFactWriter` move to
implement the narrow role interfaces they exercise (`OwnedReplacer` /
`EvidenceAppender` / `EntityCreator` / `AuthoritativeReader`). The stateful
`fakeGraph` (group 8) already models replace-by-predicate; it gains the
`ReplaceOwned`/`AppendEvidence` method shapes. The e2e journeys run against the
real bound clients through the real graph-ingest component — no double there.

## Sequencing

This lands BEFORE the parked nl-conversation-intent group 8 (8.3/8.4/8.6). Group
8's committed code (`approval.stampDecision`, the apply consumer) writes through
the old `OwnedFactWriter`, so migrating first puts the remaining group-8 work on
the new substrate rather than migrating it twice. The parked
`surface-delivery-evidence-recap` branch (touches `openpr/delivery.go`) merges back
after both. Group discipline mirrors the archived `migrate-semstreams-beta147`
sweep: red-first per group, both reviewers (go + semstreams) before each commit.

## Risks / Trade-offs

- **[Silent mode misclassification]** — an append-set ledger bound `replace-owned`
  drops entries; the classifier dedup or the route mirror would corrupt with no
  compile error. → the D8 census asserts mode against the declared pattern,
  verified red against a planted mismatch.
- **[Owner-lease half-configured]** — enforcement on one path but not another would
  fail writes closed. Single-binary bounds this to one flag; the D5 evidence gate
  catches a mis-set before the paid lane.
- **[The one-registration invariant]** — an incremental/partial owner bind is
  rejected at runtime, not compile time. → aggregate each owner's full contract at
  the composition root; a boot-time bind failure is loud and fail-closed.
- **[Heartbeater lifecycle]** — a dead heartbeat expires owner tokens mid-run,
  failing writes. → one process-lifetime heartbeater tied to `appCtx`, started
  before any owner writes; reuse across all owners.

## Open Questions

- **OQ1** — is any semdev owner append-only enough to bind with a nil heartbeater,
  or do they all carry a replace-owned group (requiring the heartbeater)? Resolve
  during D2 contract derivation. (Leaning: the two append-set owners —
  `conversation-classifier`'s `.classified` ledger and `route-mirror` — also carry
  replace-owned groups, so a shared heartbeater is needed regardless.)
- **OQ2 — RESOLVED (group-1 API grounding).** `experiment-intake` is a replace-owned
  upsert, not a creator; the run entity is framework-minted (`agentrun.Mint`); the
  only create owner is `admission-check`. See D3.
- **OQ3 — RESOLVED (group-1 API grounding).** Owner-lease enforcement is opt-in and
  defaults to observe-only (meter + warn, no reject), so the migration lands safely
  in observe-only and flips enforcement only on zero-mismatch evidence. See D5.
