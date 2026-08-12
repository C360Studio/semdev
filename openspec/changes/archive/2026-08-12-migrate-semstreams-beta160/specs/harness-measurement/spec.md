# harness-measurement — delta for migrate-semstreams-beta160

## REMOVED Requirements

### Requirement: Go fact-writes go through a contract-bound projection owner

**Reason**: semstreams v1.0.0-beta.160 (ADR-091) removes the ownership/lease
mechanism this requirement was written against. Owner identity, the
owner-token fence, and "an owned write cannot originate from an unbound
writer" no longer exist as framework concepts — a projection contract now
validates local caller intent only and reserves nothing.

**Migration**: Replaced by "Go fact-writes go through a contract-validated
projection client" below, which keeps every clause that survives the removal
(the single client seam, the vocab-table derivation, write-verb-matches-
semantics) and adds the classified-outcome discipline the new client
introduces.

### Requirement: Owned-write coverage is proven positively; the lease stays observe-only

**Reason**: The lease is deleted upstream; there is no `enforce_owner_lease`
key to pin, no observe-only posture to hold, and no token for a meter to
count. The posture question beta.159 parked for this wave resolves as moot.

**Migration**: The positive-coverage half — the only half that ever measured
anything real — survives as "Write coverage is proven positively at boot"
below. The lease-posture scenarios are retired with the mechanism.

## ADDED Requirements

### Requirement: Go fact-writes go through a contract-validated projection client

Every Go component that writes graph facts SHALL do so through a
`pkg/projection` mutation client validating against a declared local
`projection.Contract`, never through an ad-hoc graph mutation. The write
still travels over NATS to graph-ingest; what the contract adds is local
intent validation — entity-pattern match, declared predicate groups, and a
write verb per group — so a misdirected write fails loudly at the writer
instead of landing silently.

Each contract's predicate set SHALL be derived from the checked-in
single-writer vocabulary table, so the projection contracts and the G5 writer
census cannot drift: a predicate owned by a Source in the table appears in
that Source's contract, and no other.

The write verb SHALL match the fact's semantics: a replace-by-predicate fact
is a reconcile group, an append-only ledger is an append group, and a
primary-subject creation fact is a birth predicate issued as a strict create.
A verb mismatch is silent at compile time, so a conformance census SHALL
assert every contract's verb against the fact's declared pattern.

The client's classified outcomes SHALL be honored, never reinterpreted:
`entity_not_found`, `revision_mismatch`, and `commit_unknown` are distinct
results, and ambiguity is never treated as success. A revision conflict on a
single-writer group MAY be retried a bounded number of times within the
writing seam; exhaustion SHALL surface the classified error to the caller
unchanged.

#### Scenario: A misclassed predicate fails at the writer, not silently

- **WHEN** a Go write presents a predicate on an entity class outside its
  contract's declared pattern
- **THEN** the write is rejected with a named error before any wire request,
  never silently applied

#### Scenario: Contracts match the single-writer census

- **WHEN** the contract-census conformance test runs
- **THEN** every projection contract's predicates match the vocab writer
  table exactly

#### Scenario: A creation fact is a strict create and a duplicate is idempotent

- **WHEN** a birth write finds its content-derived entity already exists
- **THEN** the writer treats the conflict as the already-admitted duplicate
  path — no error escalation, and no repeated downstream wake

#### Scenario: Revision-conflict retry is bounded and loud on exhaustion

- **WHEN** a reconcile write loses the revision fence more times than the
  bounded retry allows
- **THEN** the classified revision-conflict error reaches the caller
  unchanged, where existing retry/park routing owns it

### Requirement: Write coverage is proven positively at boot

Write coverage SHALL be proven positively and offline: the projection client
carries every declared contract before any component or tool that writes is
registered, and boot fails naming the gap otherwise. No Go call site SHALL
name a `graph.mutation.*` subject outside the owning seam — with zero
sanctioned exceptions — and an offline census SHALL enforce both properties.

#### Scenario: A missing contract fails at boot, not at first write

- **WHEN** the runtime boots and a declared writer has no contract in the
  constructed client
- **THEN** boot fails naming the writer, before anything that writes is
  registered

#### Scenario: A hand-rolled mutation subject fails the census

- **WHEN** a Go call site outside the owning seam names a `graph.mutation.*`
  subject
- **THEN** the offline census fails naming the file and subject

### Requirement: Every registered tool declares a worst-effect classification

Every tool semdev registers into the executor registry SHALL declare a
worst-effect classification (`read_only`, `mutating`, or `external_effect`)
per the framework's effect contract: the value is a worst-effect claim, an
outbound read is `read_only`, and `external_effect` dominates `mutating`. A
source-level census SHALL fail on any semdev-registered tool whose effect is
absent or unrecognized, so a new tool cannot ship unclassified.

Effect metadata is descriptive: it SHALL NOT alter any configured gate
(`approval_required`, `allowed_tools`, per-loop advertised-tool admission)
in either direction.

#### Scenario: An unclassified tool fails the census

- **WHEN** a tool is registered without a valid effect classification
- **THEN** the offline census fails naming the tool

#### Scenario: Discovery serves the declared effect

- **WHEN** the tool catalog is served over the discovery port
- **THEN** each semdev tool carries its declared effect value
