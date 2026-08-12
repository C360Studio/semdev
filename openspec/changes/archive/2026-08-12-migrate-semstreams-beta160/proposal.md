# Migrate to semstreams v1.0.0-beta.160

## Why

semstreams v1.0.0-beta.160 is the announced FINAL pre-v1 breaking wave (133
commits; tag SHA `8403a221` doubles as its `candidate-proof` release). It
REMOVES the ownership/lease mechanism (ADR-091), retires the raw mutation wire
semdev's intake uses, changes `component.PortDefinition`'s Go and JSON shape,
makes service config decoding strict, and moves tool discovery to a typed
request port. semdev cannot compile or boot against it unmigrated, and staying
pinned to beta.159 forfeits the stable migration target every future tag builds
on. The beta.159 change deliberately parked its enforcement-posture and
clean-shutdown questions for this wave; this change resolves them (both moot —
the mechanism is gone).

## What Changes

- **BREAKING (upstream)**: bump `github.com/c360studio/semstreams` from
  v1.0.0-beta.159 to v1.0.0-beta.160.
- **`internal/graphown` rewrite, no shims**: ownership binding
  (`EnsureBuckets`/`Registry`/`Heartbeater`/`BindMutationClient`), the
  in-process claim ledger, and the failed-boot claim-release logic are DELETED
  outright. One `projection.NewMutationClient` carries all contracts (the
  destructive-registration hazard no longer exists). The 19-contract table and
  `ContractFor` entity-match resolution (the D3b proof) are KEPT as local
  intent validation.
- **Write verb cutover**: `ReplaceOwned` → `Reconcile` (same group-blast-radius
  semantics), `ReadOwnedPredicates` → `ReadAuthoritative`
  (`*graph.ExactEntity`), error mapping onto the seven classified
  `MutationErrorKind`s. New: an explicit revision-conflict policy —
  `Reconcile` is now internally revision-fenced and does not retry.
- **Intake birth write**: retired `graph.mutation.entity.create_with_triples`
  raw subject → `MutationClient.Create` (strict create-or-conflict; conflict is
  the idempotent-duplicate path our content-derived IDs already imply).
- **Config + port cutover**: every component ports block moves to the strict
  envelope (`config: {kind, ...}`); graph-ingest's mutation input becomes the
  canonical typed `nats-request` port (`semstreams.graph.mutation` v1);
  requester outputs declared per the framework's shipped-config pattern; the
  services block drops inner `name` fields (strict decode rejects them); TOOL
  stream narrows `tool.>` → `tool.execute.>` + `tool.result.>`; top-level
  config version bumps. Three Go components' `PortDefinition` literals move to
  typed `Portable` configs.
- **Pin lifecycle**: `TestOwnerLeaseObserveOnlyOnLanding`,
  `TestInProcessClaimLedger`, and the one-bind pin retire WITH the mechanism;
  the positive-coverage census and hand-rolled-subject cap survive re-based on
  contracts; `TestWriteTodosStaysSkipped` survives unchanged.
- **Additive adoption**: semdev's registered tools gain worst-effect
  classifications (`adopter-tool-effect-metadata` names semdev as its
  audience), with a source-level census so a new tool cannot ship
  unclassified.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `harness-measurement`: the two beta.159 write-path requirements change with
  the upstream removal — "Go fact-writes go through a contract-bound projection
  owner" loses its owner-identity/token-fence clauses (contracts validate local
  intent; they no longer reserve predicates), and "Owned-write coverage is
  proven positively; the lease stays observe-only" retires its lease posture
  while KEEPING the positive coverage census (contracts complete at boot,
  no hand-rolled mutation subjects). One ADDED requirement: every semdev tool
  declares a worst-effect classification, census-enforced. Writers per G5:
  effect metadata is stamped by tool registration (Go, semdev-owned); no new
  graph predicate is introduced.

## Impact

- **Code**: `internal/graphown/*` (rewrite), `internal/boot/{runtime,launch,boot}.go`
  (construction replaces binding; claim-release deletion), `internal/intake/component.go`
  (birth write), three component `PortDefinition` sites (`internal/intake`,
  `internal/conversationchannel`, `internal/station`), every graphown test
  double (`ReplaceOwned` → `Reconcile` method shape), retired pins.
- **Config**: `configs/semdev-bootstrap.json`, `configs/semdev-live-gemini.json`
  (ports envelope, canonical mutation port, services strip, stream narrowing,
  version bump), plus any journey-local configs.
- **Unaffected, verified**: rule packs (`add_triple`/`remove_triple` survive;
  semdev never used `replace_owned`), value-only `graph.ingest.query.entity`
  reads (subject survives), `SkipBuiltins: ["write_todos"]` (still a valid
  key), graph-query surface (only `PrefixQueryResponse`, which survives),
  lesson substrate (`emit_lesson` survives; adoption stays deferred per the
  2026-08-11 user decision).
- **Operations**: adoption of the stable tag starts on freshly provisioned
  NATS storage (`task nats:reset` covers dev/e2e; a note lands in the runbook
  for any persistent deployment).
