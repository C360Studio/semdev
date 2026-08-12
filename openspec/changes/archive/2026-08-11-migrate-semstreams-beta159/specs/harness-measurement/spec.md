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

### Requirement: Owned-write coverage is proven positively; the lease stays observe-only

graph-ingest SHALL run the owner lease observe-only for the life of the beta.159
pin: `enforce_owner_lease` explicitly false in every shipped config, never
absent — an absent key silently inherits whatever the framework default becomes.
The originally planned enforcement flip is VOID as-built (design D5, 2026-08-11):
semstreams' announced final refactor phase removes the ownership/lease mechanism,
so the posture question transfers to the next-tag migration change, re-asked
against ownership's replacement.

Because the lease meter cannot see an un-tokened write (it counts stale tokens,
not missing ones), owned-write coverage SHALL be proven positively and offline
instead: every declared owner binds at boot BEFORE any component or tool that
writes is registered, and no Go call site names a graph-mutation subject outside
the owning seam, with sanctioned exceptions named individually and capped.

#### Scenario: The observe-only posture is explicit and pinned
- **WHEN** a shipped config declares the graph-ingest component
- **THEN** `enforce_owner_lease` is present and false
- **AND** an absent key or a true value fails the offline conformance pin

#### Scenario: An unbound owner fails at boot, not at first write
- **WHEN** the runtime boots and a declared owner has no bound mutation client
- **THEN** boot fails naming the owner, before anything that writes is registered

#### Scenario: A hand-rolled mutation subject fails the census
- **WHEN** a Go call site outside the owning seam names a `graph.mutation.*` subject beyond the named sanctioned exceptions
- **THEN** the offline census fails, naming the file and line

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
