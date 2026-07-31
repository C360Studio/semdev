## ADDED Requirements

### Requirement: Go fact-writes go through a contract-bound projection owner

Every Go component that writes owned facts SHALL do so through a `pkg/projection`
mutation client bound to a declared `projection.Contract`, never through an ad-hoc
graph mutation. The write still travels over NATS to graph-ingest; what the
contract adds is a declared owner identity, a per-predicate write mode, and an
owner-token fence — so an owned write cannot originate from an unbound writer.

Each contract's predicate set SHALL be derived from the checked-in single-writer
vocabulary table, so the projection owners and the G5 writer census cannot drift:
a predicate owned by a Source in the table is owned by that Source's projection
contract, and no other.

The write mode SHALL match the fact's semantics: a replace-by-predicate fact is a
`replace-owned` group, an append-only ledger is an `append-evidence` group, and a
primary-subject creation fact is a birth predicate. A mode mismatch is silent at
compile time — an append ledger bound as replace-owned drops prior entries — so a
conformance census SHALL assert every contract's mode against the fact's declared
pattern.

#### Scenario: An owned write is refused from an unbound writer
- **WHEN** a Go path attempts an owned write without a bound projection contract
- **THEN** the write does not compile or is rejected, never silently applied

#### Scenario: Contracts match the single-writer census
- **WHEN** the contract-census conformance test runs
- **THEN** every projection contract's owner and predicates match the vocab writer table exactly

#### Scenario: An append ledger is bound append-evidence, not replace-owned
- **WHEN** a predicate is a declared append-set ledger
- **THEN** its contract group mode is `append-evidence` and the census fails on any other mode

### Requirement: Owned writes are owner-lease enforced with recorded rollout evidence

graph-ingest SHALL enforce the owner lease (`enforce_owner_lease=true`) for the
subjects that serve owned mutations, so a write routed to a non-enforcing instance
cannot bypass the owner-token fence. Because semdev hosts exactly one in-process
graph-ingest component reached only over NATS, this is one configuration on one
component.

Before any paid or live run against the enforcing configuration, the ADR-056
rollout evidence SHALL be recorded: enforcement enabled on the serving component,
the owner heartbeat live before any owner writes, and owner-lease mismatch metrics
zero across a bounded observation window. Missing evidence is a fail-closed
blocker, not a warning.

#### Scenario: Enforcement is on before the owner writes
- **WHEN** the runtime binds an owning projection client
- **THEN** the owner heartbeat is live and graph-ingest enforcement is enabled first

#### Scenario: Rollout evidence gates the paid lane
- **WHEN** a paid or live run is attempted without the recorded owner-lease evidence
- **THEN** it is refused as a fail-closed blocker

## MODIFIED Requirements

### Requirement: Single writer for measurement facts

`measurement.result` SHALL have exactly one writer — the harness that executed
the command — recorded in the checked-in writers table AND realized as that
harness's `pkg/projection` contract owner. The vocabulary table remains the single
source of truth for who owns the predicate; the projection contract is the
runtime realization of that same claim, and the two SHALL NOT diverge.

#### Scenario: Writers census maps measurement.result to one writer
- **WHEN** the writers-census conformance test runs
- **THEN** `measurement.result` maps to exactly one writing harness

#### Scenario: The measurement writer is that harness's projection owner
- **WHEN** the contract-census conformance test runs
- **THEN** `measurement.result`'s vocab writer and its projection-contract owner are the same identity
