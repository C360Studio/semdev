# Tasks: migrate-semstreams-beta159

Discipline (project law): red-first per group (G6); the fact-write contract stays
G3/G5-clean (no outcome params, one writer per predicate); both reviewers (go +
semstreams) before each group commit; no paid token on the mock ladder. The
semstreams module is pinned to the beta.159 TAG (never an untagged commit).
Mirrors the archived migrate-semstreams-beta147 sweep.

## 1. Pin the framework + confirm the blast radius

- [x] 1.1 Pinned `v1.0.0-beta.159` + `go mod tidy`. `nats.go` STAYS v1.48.0
  (confirmed — no untagged pin). RED baseline captured: 18 `undefined:
  agentictools.OwnedFactWriter` across 10 production files (`go build ./...`); test
  files add the rest of the 25.
- [x] 1.2 API grounded against the pinned cache
  (`~/go/pkg/mod/...@v1.0.0-beta.159`): `Contract{Name,MessageType,EntityPattern,
  Groups,BirthPredicates,ForeignEdges}`; `PredicateGroup{Name,Mode,Predicates}`;
  `WriteMode` ∈ {`replace-owned`,`cas-transition`,`append-evidence`}; the four role
  interfaces + their `*Mutation` payloads (`CreateMutation`, `ReplaceOwnedMutation
  {Group,Desired}`, `AppendEvidenceMutation{Evidence}`); `ownership.EnsureBuckets`
  → `registry.NewHeartbeater(interval)` → `go hb.Run(ctx)`; `BindMutationClient
  (cfg)` / `BindAndHeartbeat`; `ErrOwnerAlreadyBound`. Resolved OQ2 (only
  `admission-check` creates) and OQ3 (enforcement opt-in, observe-only default —
  `owner_lease_mismatch_total` metered, not rejected).

## 2. Derive the contract set from the vocab table (design D1, D2, D3)

The censuses live in `internal/graphown/contracts_test.go`, NOT `test/conformance`:
they pin graphown's own derivation, and the conformance package transitively
imports the tool packages that do not compile until group 4 lands — a census that
cannot run during the migration it guards is not a guard.

- [x] 2.1 `TestProjectionContractsMatchVocabWriters` — a contract-census that
  derives the expected owner→predicates set from `internal/vocab` and asserts the
  three-way partition is exact: every product predicate is covered ONCE, by a
  Go-writer contract, an excluded rule-writer Source, or the explicit
  declared-but-unwired set — never two, never none. Bidirectional (a contract
  owning a predicate vocab does not, or a stale unwired entry, fails). RED verified
  by planting a predicate with no entity-class entry.
- [x] 2.2 `TestContractModesAreConservative` — every group is `replace-owned` and
  named `owned`; birth predicates appear only under sanctioned create owners; NO
  group is `append-evidence` (deferred, D3). RED verified by planting an
  `append-evidence` mode.
- [x] 2.3 Author the contracts (`internal/graphown`): **19 contracts across 17
  owners** — one per (owner, ENTITY CLASS) per the D2a as-built correction, all
  groups `replace-owned` (D3 conservative), `BirthPredicates` for `admission-check`
  only (OQ2). Derivation fails CLOSED on a Go-writer predicate with no entity-class
  entry AND on one listed both unwired and entity-classed (so wiring a writer up
  cannot silently no-op). **OQ1 CORRECTED: 18 contracts carry a replace-owned
  group; `admission-check` is birth-only — it registers NO claim, mints the ZERO
  token, and needs no heartbeater** (`contract.go:258-260`, `contract.go:167`).
  `graphown.OwningOwners()` is the 16-owner set that actually binds a lease;
  `Owners()` is all 17.
- [x] 2.6 (added) `TestExclusionSetsAreNotStale` — reverse-direction staleness on
  every hand-maintained table (`unwiredPredicates`, `entityClass`,
  `ruleWriterSources`, `createOwners`): an entry naming a predicate or Source vocab
  no longer declares fails. Closes the gap where a stale rule Source silently
  un-owns a future Go predicate of the same name. RED verified by planting.
- [x] 2.7 (added) `TestAgentExecPatternSpansBothExecutionClasses` — pins the ONE
  thing the widened station pattern exists for, which the round-trip test cannot see
  (its representative for that class is a RUN entity). A narrowing to
  `*.*.agent.chain.*.*` un-owns the loop-fired station park; RED verified by
  planting exactly that. Also pins that Run/Loop patterns stay mutually exclusive.
- [x] 2.8 (added) `TestRouteMirrorSitesWriteDisjointEntityPopulations` — the
  route-mirror precondition (D3a) pinned where it actually lives, the rule configs:
  `submit_review` is advertised only to `role=reviewer` spawns, and the floors
  station is dispatched only from a `role=developer` loop. Fails loudly if either
  half matches nothing (a vacuous pin is worse than none).
- [x] 2.9 (added) `TestBirthOnlyOwnersOwnNothing` — pins the framework consequence
  three artifacts previously got wrong: a birth-only contract registers no claim,
  so `ErrOwnerAlreadyBound` never fires for it and group 3's one-bind pin must scope
  to `OwningOwners()`.
- [x] 2.4 (added, D2a) `TestContractsClaimTheEntityClassTheirWritersStamp` +
  `TestContractForRejectsAnUnownedEntityClass` — every contract claims a DECLARED
  entity class and `ContractFor` round-trips a representative entity of that class;
  an owner asked for a class it does not claim gets a named error.
  **Documented limit (D3b): these do NOT prove a predicate is classed onto the
  RIGHT entity** — planting the original all-run-pattern defect passes them. The
  behavioral proof is task 4's `ContractFor` call-site wiring.
- [x] 2.5 (added) `TestEveryOwnerDerivesWithoutSelfOverlap` — each owner's FULL
  contract set through `projection.Derive`, the same validation
  `BindMutationClient` runs at boot: catches an invalid contract and a per-owner
  cell self-overlap that would take the owner's whole write path down at boot.

## 3. Composition-root binding (design D4, D5)

- [ ] 3.1 `TestOwnerBindsAreAggregatedAndOnce` — NOT WRITTEN. Its two halves split
  cleanly, and only the offline half is currently covered:
  - **Aggregation (covered):** each owner's FULL contract set derives in one call —
    `TestEveryOwnerDerivesWithoutSelfOverlap` runs the real `projection.Derive` per
    owner, which is exactly what `BindMutationClient` runs. `BindAll` structurally
    binds once per owner by iterating the de-duplicated `Owners()` set.
  - **Rejection (NOT covered):** a second same-owner bind returning
    `ErrOwnerAlreadyBound` needs a LIVE registry (`EnsureBuckets`), so it belongs
    with the docker journeys in group 7. **Scope that assertion to
    `graphown.OwningOwners()`** — a birth-only owner returns before `RegisterOwner`,
    so its second bind succeeds silently and an all-17 assertion would be wrong by
    one (task 2.9 pins why).
- [x] 3.2 Implemented as `graphown.BindAll` (`internal/graphown/binding.go`),
  called from BOTH entry points — `boot/runtime.go` (before `RegisterAll`, so no
  component or tool that writes facts is registered against an unbound owner) and
  `boot/launch.go` (the launch driver stamps an owned fact too). `graphown.Clients`
  maps a vocab Source to its bound client; a nil `*Clients` yields nil writers, so
  the schema-scanning censuses keep the loud-fail posture the nil OwnedFactWriter
  gave. Original text: `ownership.EnsureBuckets` → registry;
  one process-lifetime `Heartbeater` (`go hb.Run(appCtx)`, tied to shutdown);
  bind every Go-writer owner once (`BindMutationClient`/`BindAndHeartbeat`).
  Each owner exposes only its narrow role interface(s) to its call sites.
  **NOT a 1:1 swap of the shared `changeWriter`** (`boot.go:149-151`, injected into
  5 tools): each owner needs its OWN bound client, and three call sites write as
  TWO owners each — `check_floors` (`floor-tools` + `route-mirror`),
  `submit_review` (`reviewer-quinn` + `route-mirror`), `approval.go`
  (`approval-adapter` for `stampDecision` + `conversation-adapter` for the
  `conversation.pending.*` write at `:501`). `MutationClientConfig.Owner` is
  singular, so those take two narrow clients, not one.
- [x] 3.4 (added, grp3–5 review) The one-bind invariant is enforced ACROSS calls,
  not just within one: `graphown.BindOwners` takes an owner SUBSET and refuses a
  second bind of an owner this process already bound
  (`ErrOwnersAlreadyBoundInProcess`). Root cause it closes: `ownership.NewRegistry`
  mints a fresh incarnation per instance and `RegisterOwner` replaces the epoch
  entry with NO liveness check, while a `MutationClient` captures its token once —
  so a second bind permanently invalidates the first holder's token, and the
  framework's own `ErrOwnerAlreadyBound` cannot see it (per-Registry state).
  Consequences fixed: (a) `semdev launch` bound all 17 owners and would have left a
  live `task serve` writing under a stale lease forever — it now binds ONLY
  `experiment-intake`, the one owner it writes, which the runtime never writes;
  (b) the e2e stand-ins re-bound mid-journey, so every bridge proof ran with a
  broken fence and would have hard-broken at the Phase-B flip — `boundWriter` now
  draws from the runtime's own `GraphOwners()`.
- [x] 3.5 (added, grp3–5 review) `MutationClientConfig.Retry` is
  `natsclient.DefaultRetryConfig()`. The zero value means `MaxRetries: 0` —
  `normalizeRetryConfig` only clamps negatives — which silently dropped the
  no-responders retry the DELETED writer had (`RequestWithRetryClassified`,
  3 attempts, 100 ms→2 s). Without it a graph-ingest restart loses
  `station.dispatch.failed` on the first attempt: the station path can only log, so
  the run stalls instead of parking.
- [x] 3.6 (added, D5 6.1a) Boot census: `Clients.RequireBound(declaredOwners...)`
  runs in `NewRuntime` BEFORE anything that writes is registered, so an owner that
  failed to bind — or a typo'd Source — fails at boot instead of as a nil writer at
  its first write inside a station handler.
- [x] 3.3 `enforce_owner_lease` stays OFF for the landing state (D5 Phase A), pinned
  by `TestOwnerLeaseObserveOnlyOnLanding` over BOTH shipped configs — so enforcement
  is never flipped speculatively. The pin also rejects an ABSENT key: omitting it
  reads as observe-only today but silently inherits whatever the framework default
  becomes. RED verified on both shapes (flipped true, and key removed).

## 4. Rework the write call sites by mode (design D3, D6, D9)

Per owner, red-first at each site; the recorded fact stays byte-identical (Source,
predicate, object) — only the client and method change.

- [x] 4.1 `replace-owned` sites: measuretask, submitreview/checkfloors (route
  mirror replace groups), createchange, projecttasks, provisionsandbox,
  validatechange, verifyartifact, classifyintent (intent facts), approval.go
  (`stampDecision`), apply.go, delivery/openpr, the stations. `ReplaceTriples(add,
  remove)` → `OwnedReplacer.ReplaceOwned(ReplaceOwnedMutation{...Desired})`.
  **Every site names its contract via `graphown.ContractFor(owner, entityID)` —
  NEVER a hardcoded contract string (D3b).** That coupling is the only behavioral
  proof that a predicate's declared entity class is the one its writer stamps; the
  per-tool tests that already assert the target entity are what exercise it.
- [ ] 4.2 (VOID — D3) no `append-evidence` sites. The classifier `.classified`
  ledger and the route mirrors stay `replace-owned` with the full computed set,
  byte-identical to today; native append is a per-ledger follow-on.
- [ ] 4.3 create site — **DEFERRED, with cause.** The admission record is the only
  create (OQ2). Migrating it to `projection.EntityCreator.CreateWithTriples` was
  planned for contract validation (D5 corrected the enforcement rationale: a
  birth-only contract is permanently un-tokened, so it buys ZERO lease coverage).
  **It is deferred because the projection client SWALLOWS the signal intake depends
  on.** `intake/component.go`'s `natsEntityCreator` exists precisely so
  `ErrorCodeEntityExists` reaches the caller — `RecordAdmission` maps it to
  `ErrAlreadyRecorded`, and the caller SKIPS the wake; without that signal a webhook
  redelivery double-mints a run and double-spends paid tokens.
  `CreateWithTriples` returns `(receipt, nil)` — plain success — when the entity
  already exists and `createFactsMatch` is true (`mutation_client.go:586-590`).
  It would work today only by ACCIDENT: `sameFullTriple` compares `Timestamp`
  (`mutation_client.go:1358-1371`) and `RecordAdmission` stamps a fresh
  `time.Now().UTC()` per call, so a redelivery currently mismatches and falls to the
  error path where the classified code survives. Make the record's facts
  deterministic — an entirely reasonable-looking change for a content-addressed
  idempotent record — and the redelivery silently becomes a double-run.
  **To land it safely** the intake lane needs an explicit already-existed signal that
  does not ride on timestamp inequality: either an upstream ask for
  `CreateWithTriples` to distinguish created-vs-already-existed in the receipt, or a
  deliberate in-tree pre-check. Not worth doing blind for zero enforcement gain.
- [x] 4.4 read-back sites: `ReadOwnedPredicates(id, prefix)` (checkfloors,
  projecttasks) → `AuthoritativeReader.ReadAuthoritative(id)` + local prefix filter.
  **Carry-forward:** `ReadOwnedPredicates` was prefix-SCOPED; `ReadAuthoritative`
  returns the FULL `*graph.EntityState`. `projecttasks.go:67-73` gates immutability
  on `len(existing) > 0` — drop the local `task.spec.` filter and every run has
  triples, so `project_tasks` refuses universally. Leave semdev's own `changefacts`
  reader untouched.
- [x] 4.6 (added, D3a/M2) BOTH route-mirror sites now FAULT LOUDLY on an empty
  `task.attempt.instance` read, matching the D7 budget posture immediately below
  each: `check_floors` (`RunFloors`) and — added after the grp3–5 review flagged it
  as the more reachable twin — `submit_review` (whose mirror error returns WITHOUT
  StopLoop, so repeated mirror writes on one review loop are the designed path).
  The old remove lists excluded `route.attempt.instance`, so an empty read was a
  no-op; the group wipe DELETES it, a zeroed count reads as budget-unexhausted, and
  the deletion returns `CommitVerified` so nothing downstream can tell. Pinned by
  `TestCheckFloorsFaultsOnEmptyAttemptSet` (RED verified by removing the guard); two
  fixtures that seeded no attempt fact were corrected — a running loop always has
  one (G8).
- [x] 4.7 (added, D2a) BOTH halves done. `station.stampDispatchFailed` surfaces a
  `ContractFor` failure with the same `logger.Error` + `c.errors` bump as a write
  failure — the path can only log, so a silent return is a lost terminal fact and a
  run that stalls instead of parking. AND
  `TestStationDispatchRulesConfineTheFiringEntityClass` pins that every one of the 8
  station-dispatch rules confines its firing entity to ONE entity class that
  station-harness claims — derived from the entity class of the predicates each rule
  conditions on, so it stays honest as the vocabulary moves (all 8 declare the
  watch-all pattern `*.*.*.*.*.*`; the invariant lived only in their conditions).
  RED verified twice: dropping a rule's class-bearing conditions, and planting
  contradictory run+loop conditions in one rule.
- [x] 4.5 Test doubles: `fakeWriter`/`fakeGraph` implement the narrow role
  interfaces they exercise; the stateful `fakeGraph` keeps replace-by-predicate +
  gains append semantics. All unit suites green.

## 5. NATS substrate (design D7)

- [x] 5.1 `docker/compose/nats.yml`: `nats:2.10-alpine` → `nats:2.14-alpine`;
  pin the compose↔config test (`TestNatsPortMatchesComposeDefault` sibling for the
  image).
- [x] 5.2 `Taskfile.yml`: pin `natsio/nats-box:latest` → a fixed tag.

## 6. Enforcement flip, gated on zero-mismatch evidence (design D5 Phase B)

**The meter-based gate this group originally specified was VACUOUS and is
replaced (D5).** `owner_lease_mismatch_total` counts STALE tokens, not MISSING
ones: `checkOwnerLease` returns on `ownerToken == ""` before any enforcement
branch, so an un-tokened write is never metered and never rejected. The old 6.1
("the meter names every still-un-tokened owned write, expected zero once group 4
lands") was inverted — it reads zero whether or not anything migrated, and fed a
paid-lane flip.

- [x] 6.1 Coverage proven POSITIVELY, offline, three ways:
  (a) `Clients.RequireBound(graphown.Owners()...)` runs in `NewRuntime` BEFORE any
  writer is registered, so an owner that failed to bind fails at boot rather than as
  a nil writer at its first write (task 3.6);
  (b) the compile break itself — `OwnedFactWriter` is DELETED, so no alternative
  owned-write surface survives — plus `TestNoCallSiteHandRollsAnOwnedWrite`, which
  fails on any `graph.mutation.*` subject named as a string literal outside
  `internal/graphown`. Exactly ONE sanctioned exception is listed with its reason
  (the admission birth lane, D3c), and the list itself is capped so it cannot rot
  into a blanket waiver. RED verified by planting a raw subject in measuretask;
  (c) the group-4 checklist is closed site by site (4.1/4.4/4.5/4.6/4.7 ticked; 4.2
  void; 4.3 deferred with cause).
- [ ] 6.2 Read `owner_lease_mismatch_total` for what it IS — a STALENESS signal.
  Non-zero means a second process minted a new incarnation
  (`registry.go:379-381`), not that a site is unmigrated. Confirm single-writer-
  process discipline before the flip; consider metering `Heartbeater.Run`'s
  swallowed tick failures (`heartbeat.go:108-112` warns only), since a persistently
  failing heartbeat ages out presence silently.
- [ ] 6.3 ONLY on (6.1) coverage + (6.2) staleness clean: set
  `enforce_owner_lease=true` on the hosted graph-ingest, re-run the ladder GREEN,
  and record the ADR-056 rollout evidence in `docs/evidence-ledger.md` — stating
  honestly WHICH writes are gated (the 16 owning owners) and which are NOT (the
  birth-only admission record, permanently un-tokened by design, D5). Pin the
  enforced config (`TestGraphIngestEnforcesOwnerLease`).

## 7. Verify + review + archive

- [ ] 7.1 Full offline ladder (`task check` / `task validate`) + full `task e2e
  -race` uncached (all four bridge journeys + the NL journeys) + `openspec validate
  --strict`. Zero predicate/entity-contract rejections; zero owner-lease mismatch.
- [ ] 7.2 Adversarial review — BOTH reviewers, zero blocking/high, all findings
  applied. Focus: the contract-census (owner/mode drift), append-vs-replace
  correctness (silent data-drop), the one-registration invariant, the owner-lease
  gate + heartbeater lifecycle, G3 (no outcome params survived), G5 (one writer per
  predicate, now doubly-pinned by vocab + contract), the flow-discipline (writes
  still over NATS, no Go shortcut introduced).
- [ ] 7.3 sync-specs at archive folds the delta (harness-measurement modified;
  no new capability). Bump the CLAUDE.md semstreams pin line to beta.159 and record
  the migration in the evidence ledger.
- [ ] 7.4 Hand off: unpark nl-conversation-intent group 8 onto the new substrate;
  note the `surface-delivery-evidence-recap` branch merges back after.
