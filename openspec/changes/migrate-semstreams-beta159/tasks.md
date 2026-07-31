# Tasks: migrate-semstreams-beta159

Discipline (project law): red-first per group (G6); the fact-write contract stays
G3/G5-clean (no outcome params, one writer per predicate); both reviewers (go +
semstreams) before each group commit; no paid token on the mock ladder. The
semstreams module is pinned to the beta.159 TAG (never an untagged commit).
Mirrors the archived migrate-semstreams-beta147 sweep.

## 1. Pin the framework + confirm the blast radius

- [ ] 1.1 `go get github.com/c360studio/semstreams@v1.0.0-beta.159` + `go mod tidy`.
  Confirm `nats.go` stays v1.48.0 (beta.159 requires exactly that; NO untagged
  pin). Record the exact compile-failure set as the migration's red baseline
  (expected: `agentictools.OwnedFactWriter` undefined across the 25 Go-writer files).
- [ ] 1.2 Read `pkg/projection` + `pkg/ownership` from the pinned module cache and
  record the exact API used: `Contract`/`PredicateGroup`/`WriteMode`
  (replace-owned | append-evidence | cas-transition); the four role interfaces
  (`EntityCreator.CreateWithTriples`, `OwnedReplacer.ReplaceOwned`,
  `EvidenceAppender.AppendEvidence`, `AuthoritativeReader.ReadAuthoritative`);
  `ownership.EnsureBuckets` / `registry.NewHeartbeater` / `BindMutationClient` /
  `BindAndHeartbeat`; `ErrOwnerAlreadyBound`.

## 2. Derive the contract set from the vocab table (design D1, D2, D3)

- [ ] 2.1 RED: `TestProjectionContractsMatchVocabWriters` — a contract-census that
  derives the expected owner→predicates set from `internal/vocab` (Go-writer
  Sources only, rule Sources excluded) and asserts the checked-in contracts match:
  same owner per predicate, bidirectional (a contract owning a predicate the vocab
  table does not, or vice versa, fails). Red before any contract exists.
- [ ] 2.2 RED: `TestAppendSetPredicatesAreAppendEvidence` — every declared
  append-set ledger (`conversation.intent.classified`, the `route.*` mirror set)
  carries `append-evidence`; a planted `replace-owned` on one fails RED (the silent
  data-drop guard, verified against the plant).
- [ ] 2.3 Author the contracts (`internal/vocab` or a new `internal/projection`
  package): one `projection.Contract` per Go-writer owner, `Groups` by mode,
  `BirthPredicates` for the creation owners (admission-check, experiment-intake —
  pending OQ2). Resolve OQ1 (which owners are append-only → nil-heartbeater-eligible).

## 3. Composition-root binding (design D4, D5)

- [ ] 3.1 RED: `TestOwnerBindsAreAggregatedAndOnce` — a boot-path pin that each
  owner binds its FULL contract set in one call, and a second same-owner bind is
  rejected (`ErrOwnerAlreadyBound`). Red before binding exists.
- [ ] 3.2 Implement in `internal/boot`: `ownership.EnsureBuckets` → registry;
  one process-lifetime `Heartbeater` (`go hb.Run(appCtx)`, tied to shutdown);
  bind every Go-writer owner once (`BindMutationClient`/`BindAndHeartbeat`).
  Each owner exposes only its narrow role interface(s) to its call sites.
- [ ] 3.3 Set `enforce_owner_lease=true` on the hosted graph-ingest component in
  the shipped configs. Pin the config carries it (`TestGraphIngestEnforcesOwnerLease`).

## 4. Rework the write call sites by mode (design D3, D6, D9)

Per owner, red-first at each site; the recorded fact stays byte-identical (Source,
predicate, object) — only the client and method change.

- [ ] 4.1 `replace-owned` sites: measuretask, submitreview/checkfloors (route
  mirror replace groups), createchange, projecttasks, provisionsandbox,
  validatechange, verifyartifact, classifyintent (intent facts), approval.go
  (`stampDecision`), apply.go, delivery/openpr, the stations. `ReplaceTriples(add,
  remove)` → `OwnedReplacer.ReplaceOwned(ReplaceOwnedMutation{...Desired})`.
- [ ] 4.2 `append-evidence` sites: the classifier `.classified` ledger append and
  the route `.*` mirror append → `EvidenceAppender.AppendEvidence`.
- [ ] 4.3 create sites: admission record + run mint → `EntityCreator.CreateWithTriples`
  with birth predicates (contingent on OQ2 — if `agentrun.Mint` owns creation,
  scope out).
- [ ] 4.4 read-back sites: `ReadOwnedPredicates(id, prefix)` (checkfloors,
  projecttasks) → `AuthoritativeReader.ReadAuthoritative(id)` + local prefix filter.
  Leave semdev's own `changefacts` reader untouched.
- [ ] 4.5 Test doubles: `fakeWriter`/`fakeGraph` implement the narrow role
  interfaces they exercise; the stateful `fakeGraph` keeps replace-by-predicate +
  gains append semantics. All unit suites green.

## 5. NATS substrate (design D7)

- [ ] 5.1 `docker/compose/nats.yml`: `nats:2.10-alpine` → `nats:2.14-alpine`;
  pin the compose↔config test (`TestNatsPortMatchesComposeDefault` sibling for the
  image).
- [ ] 5.2 `Taskfile.yml`: pin `natsio/nats-box:latest` → a fixed tag.

## 6. Owner-lease rollout evidence (design D5)

- [ ] 6.1 Record the ADR-056 rollout evidence in `docs/evidence-ledger.md`:
  enforcement enabled on the serving graph-ingest, owner heartbeat live before any
  owner write, owner-lease mismatch metric zero across a bounded observation window
  (from a mock-ladder run). Fail-closed blocker for the paid lane.

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
