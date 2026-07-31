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
`conversation-spawn-rule`, `issue-ref-rule`, `dev-*-rule`, `sandbox-provision-rule`,
`brownfield-spec-projector` — they write via `add_triple`). ONLY the Go writers
become projection owners. The rule writers are engine-owned and unchanged. A
grep-census pins the split so a future Go write under a rule Source is caught.

Go-writer owners (from the vocab census; each = one contract):
`approval-adapter`, `conversation-adapter`, `conversation-classifier`,
`create-change-author-tool`, `measurement-harness`, `task-projector`,
`route-mirror`, `floor-tools`, `sandbox-provisioner`, `reviewer-quinn`,
`verify-harness`, `station-harness`, `patch-committer`, `open-pr`,
`openspec-validate-harness`, `openspec-archive-harness`, `experiment-intake`,
`evidence-ledger`, `admission-check`.

### D3 — write mode per predicate (a mode error is silent, so it is censused)
Three modes, mapped from the fact's existing semantics:
- **`replace-owned`** — the replace-by-predicate writes (the majority:
  `measurement.result`, `run.change.decision`, the gate/route facts, `task.spec`).
  `ReplaceOwned` reconciles the complete owned set for a contract group.
- **`append-evidence`** — the append-set ledgers: `conversation.intent.classified`
  (the classifier dedup ledger) and the `route.*` mirror append sets. Binding one
  of these as `replace-owned` would DROP prior entries silently — the census
  asserts every declared append-set predicate carries `append-evidence`.
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

### D5 — observe-only first, then enforce (the gate is opt-in, and self-evidencing)
graph-ingest's owner-lease check runs on BOTH `create_with_triples` and
`update_with_triples`, but ONLY when `enforce_owner_lease=true` (ADR-056 PR-5).
**The default posture is OBSERVE-ONLY (PR-3):** an un-tokened or stale-token write
is METERED (`owner_lease_mismatch_total`) and Warn-logged, NOT rejected
(`graph/mutation_responses.go:122-131`). This gives a safe two-phase landing:

- **Phase A (this change's default):** migrate the write path so every owned write
  carries its owner token, and leave enforcement OFF. semdev compiles and runs;
  the observe-only meter reports mismatches without failing any write. This is the
  state the change lands in.
- **Phase B (deliberate flip):** set `enforce_owner_lease=true` on semdev's one
  hosted graph-ingest component ONLY after `owner_lease_mismatch_total` is provably
  ZERO across a bounded observation window on a mock-ladder run. Single-binary makes
  the "every serving instance enforces" precondition one flag on one component.

The observe-only meter IS the ADR-056 rollout evidence — it directly measures
whether any write still lacks a valid token. Zero-mismatch across the window is the
fail-closed gate for the paid lane; a non-zero meter names exactly which owner path
is still un-tokened (e.g. a create path not yet migrated). Enforcement is NOT flipped
speculatively.

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
