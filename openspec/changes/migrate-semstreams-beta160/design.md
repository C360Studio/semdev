# Design — migrate-semstreams-beta160

## Context

See proposal.md for motivation. The relevant as-built state:

- All semdev Go fact-writes flow through `internal/graphown` (beta.159's seam):
  call sites hold `*graphown.Writer`, tools take the concrete type (D3b), and
  `ContractFor(owner, entityID)` resolves 19 contracts across 17 writers by
  entity match. The seam was built so an upstream write-path change lands as
  graphown-internals + boot rewrite, not a call-site sweep. This wave is that
  change arriving.
- beta.160 deletes `pkg/ownership` (ADR-091). `projection.MutationClient`
  survives as a plain contract-validating client
  (`NewMutationClient({NATS, Contracts, Timeout})`): no registration, no
  tokens, no heartbeats, no liveness. Contracts "validate caller intent; they
  do not reserve predicates or prevent other writers."
- `Reconcile` is internally revision-fenced: the client reads the entity
  (`ReadAuthoritative`), then issues the wire reconcile with
  `ExpectedRevision` set to the read revision. It performs one request and
  does not retry; a lost race surfaces as `MutationRevisionConflict`.
- The rule engine writes EACH rule action as its own KV revision on the target
  entity (established crux fact). Run and loop entities therefore have
  constant revision churn from rule writes that touch DIFFERENT predicates
  than any in-flight Go write.
- The user directive for this migration: no shims, no deprecated code left
  behind. The framework itself ships zero compatibility surfaces.

## Goals / Non-Goals

**Goals:**

- Compile, boot, and run the full journey ladder green on beta.160 with the
  ownership machinery deleted outright.
- Preserve the two proofs that made beta.159 safe: the D3b
  contract-resolution proof (misclassed predicate fails loudly at the tool)
  and the positive write-coverage census (no hand-rolled mutation subjects).
- Resolve the questions beta.159 parked for this wave (enforcement posture,
  clean shutdown/Resign) — both moot with the mechanism removed — and record
  the upstream artifact (tag + candidate-proof release) in the evidence
  ledger.
- Adopt tool effect metadata (semdev is the named audience) with a census so
  it cannot rot.

**Non-Goals:**

- Adopting the `reconcile_predicates` rule action (rules keep
  `add_triple`/`remove_triple`, both of which survive; semdev never shipped
  `replace_owned`).
- Adopting the lesson substrate (user decision 2026-08-11: deferred until
  after this wave lands and basics are proven — this change is the wave, not
  the proof period).
- Native `append-evidence` adoption for the ledger (named follow-on, stays
  parked).
- The security change (token argv / `git add -A` / devcontainer traversal),
  Phase 3, and paid-run work — separate changes.

## Decisions

### D1 — graphown keeps its facade; binding dies without replacement

One `projection.NewMutationClient` is constructed at boot carrying ALL
contracts. `graphown.Clients` becomes a thin resolver from writer name to
`*graphown.Writer` over that single client; `ContractFor` and the 19-contract
table survive unchanged as LOCAL intent validation. Deleted with no
replacement: `binding.go`'s registry/heartbeater/bind machinery, the
in-process claim ledger (`ErrOwnersAlreadyBoundInProcess`), the failed-boot
claim-release logic at all three boot layers, and the bind-only-what-you-write
discipline (its hazard — destructive registration — no longer exists).

*Alternative considered*: dissolving graphown entirely and calling
`MutationClient` directly from call sites. Rejected: the facade is what makes
the D3b proof real (tools take the concrete `*graphown.Writer`, so contract
resolution cannot be bypassed by a substituted double), and it kept this
migration a two-file rewrite instead of a 24-site sweep. The facade is
load-bearing architecture, not a shim.

### D2 — revision conflicts are bounded-retried inside the seam, loud on exhaustion

`graphown.Writer.Replace` retries `Reconcile` a bounded number of times
(house value: 3 attempts total) on `MutationRevisionConflict` ONLY — every
other classified kind surfaces immediately. Rationale: revision fencing is
per-ENTITY while G5 gives every predicate exactly one writer, so a conflict on
a semdev write is interleaving noise (a rule action bumped the entity between
the client's internal read and its write), never a semantic collision on the
group. Each retry re-enters `Reconcile`, which re-reads and re-fences.
Exhaustion returns the classified error to the caller unchanged, where the
existing station retry/park machinery owns it — the seam never converts
exhaustion into silence.

*Alternative considered*: no retry, park on first conflict. Rejected: run
entities take constant rule-write churn; parking on interleaving noise would
make every journey flaky and violate the park-toward-the-human bar (a human
would be summoned for a non-event).

*Pin*: red-first unit pin with a double returning revision-conflict twice then
success (write lands, correct final Desired); a second pin that three
conflicts surface `MutationRevisionConflict` loudly.

### D3 — intake birth uses strict Create; conflict is the idempotent-duplicate path

The retired `create_with_triples` raw request becomes
`MutationClient.Create` under the admission contract (birth predicates).
`MutationConflict` means the content-derived admission entity already exists:
log at info with the entity ID, count it, and do NOT re-fire the coordinator
wake — the run already woke once. This makes the content-derived-ID
idempotency backstop (previously implicit) an explicit, pinned behavior.
Every other classified kind fails the delivery loudly as today.

*Pin*: red-first — a doubled Create returning conflict yields no second wake
publish and no error.

### D4 — every semdev component that holds a writer declares the requester port

Config-side: graph-ingest's mutation input becomes the canonical typed port
(`kind: nats-request`, `interface: {type: semstreams.graph.mutation, version:
v1}`, `subject: graph.mutation.>`), copied from the framework's shipped
`configs/agentic.json` shape. Go-side and config-side, a requester OUTPUT is
declared on every component that originates graph mutations: the framework's
own requesters present in semdev's config (`rule`, `agentic-tools`,
`agentic-loop`) per the framework pattern, and semdev's writers
(`issue-intake`, `conversation-channel`, the six stations) on their
`PortDefinition` outputs. Declarations are honest even where static flow
validation might not force them — the upstream contract says every mutating
component declares, and undeclared-but-writing is exactly the class of quiet
drift this repo's censuses exist to kill.

All port blocks (Go literals and JSON) move to the strict envelope in the
same group — there is no mixed-shape intermediate state.

### D5 — services/streams/config hygiene in one sweep

Inner `name` fields leave the services block (strict decode rejects unknown
fields). TOOL stream narrows to `tool.execute.>` + `tool.result.>` per the
discovery cutover; semdev declares no `tool.list` port and calls the subject
nowhere, so the new `discovery.tool.list` default is inherited with zero
edits. Top-level config `version` bumps so the file beats any KV-selected
predecessor. `max_bytes`/`discard` declarations survive as-is
(`TestEveryStreamDeclaresItsBounds` keeps pinning them).

### D6 — effect classification lives at registration, censused at source level

Each tool registered by `RegisterTools` declares its worst-effect class per
the adopter note's two rules (a metered external read is `read_only`;
mediation does not launder effect, but one hop is not external). Initial
classification: `open_pr` is `external_effect` (pushes and creates PRs on a
real forge); graph-writing and sandbox/workspace-mutating tools
(`create_change`, `apply_patch`, `measure_task`, `check_floors`,
`submit_review`, `verify_artifact`, `provision_sandbox`, `project_tasks`,
`classify_intent`, `validate_change`) are `mutating`; pure readers
(semsource proxy reads) are `read_only`. The census test walks semdev's
registration table and fails on an absent or unrecognized value — mirroring
the framework's own source-level check, which explicitly does not cover
adopter tools. Effect metadata is descriptive (Rule 5): it changes no gate in
either direction.

### D7 — pins retire with their mechanism, censuses re-base

Deleted alongside the code they pinned: `TestOwnerLeaseObserveOnlyOnLanding`
(no lease key exists to assert), `TestInProcessClaimLedger`, the
one-bind-per-owner pin. Re-based, not deleted: the boot census (contracts
complete before anything that writes is registered — was `RequireBound`), the
no-hand-rolled-mutation-subject grep census (sanctioned-exception cap drops
to 0: the intake raw subject was the last exception and it migrates to the
client), and the contract↔vocab-table conformance census. Survives unchanged:
`TestWriteTodosStaysSkipped` (verified: `write_todos` remains a valid
`SkipBuiltins` key in beta.160).

## Risks / Trade-offs

- [Reconcile doubles wire ops per write (internal read + reconcile), shifting
  journey timing] → journeys already assert with latency-tolerant windows;
  the e2e gate runs 3× to catch flake regressions before claiming green.
- [Bounded retry under heavy rule churn could still exhaust on a hot entity]
  → exhaustion is loud and classified; the station retry/park machinery
  already owns transient-vs-terminal routing (reason-aware escalate). If a
  journey shows systematic exhaustion, that is a real finding about write
  placement, not a reason to raise the bound silently.
- [Static flow validation semantics are documented only by the framework's
  shipped configs and tests] → iterate against real boot; the framework's
  `test/shipped_graph_mutation_ports_test.go` and `configs/agentic.json` are
  the reference shapes; deviations surface as boot rejections, not silent
  drift.
- [Foundation B trajectory/eviction changes could shift e2e observability
  assumptions (terminal aggregates now released)] → the journey ladder is the
  arbiter; any watcher reading trajectory state after terminal must be found
  by the red run, fixed against the new contract, and pinned.
- [Fresh-NATS adoption premise on persistent deployments] → dev/e2e reset per
  run already; the real-LLM runbook gains the fresh-storage note so a live
  deployment cannot adopt over retained state by accident.

## As-built findings (task 1.3 — what the recon did not predict)

1. **`MutationClientConfig` lost its `Retry` field.** The beta.159 seam rode
   out graph-ingest blips via `natsclient.DefaultRetryConfig()`; the beta.160
   client performs ONE classified request per operation. D2's bounded retry
   therefore covers the transport kinds too — `unavailable` (never landed) and
   `commit-unknown` (may have landed; a reconcile is an idempotent full-group
   set, and a create that landed converges to the conflict signal the caller
   already handles). Pinned by `TestReplaceRetriesTransportKinds`.
2. **The authority read classifies a MISSING entity as not-found** where the
   old surface returned an empty entity. `ReadOwnedPredicates` maps
   `MutationNotFound` to an empty read (the emptiness-gating callers treat
   "not born yet" as "nothing owned"); every other failure stays loud. Pinned
   by `TestReadOwnedPredicatesMapsNotFoundToEmpty`.
3. **The framework exports no Go constants for the mutation port contract** —
   its own configs use string literals. semdev names them ONCE in
   `graphown.RequesterPortDefinition` (+ `MutationSubjectFamily`), which keeps
   the no-hand-rolled-subject census exact: the literal appears only inside
   graphown. The canonical requester port carries `required: true` (matching
   the framework's shipped configs).
4. **The rule engine's lanes changed shape underneath `add_triple`/
   `remove_triple`**: add is now must-exist and SET-VALUED over exact tuples
   (a same-predicate different-object add still appends — the parks pin
   updated to assert exactly that); remove is now an internal read +
   revision-fenced reconcile. Watch item for the docker journeys: a rule
   remove losing a revision race is a best-effort action failure (log +
   counter), where the old lane could not lose one.
5. **The birth lane gains a first-class seam sibling**: `graphown.Creator`
   (strict Create with the same unavoidable `ContractFor` resolution),
   replacing intake's raw `create_with_triples` adapter. `MutationConflict` is
   surfaced as-is — `graphown.IsConflict` is the callers' duplicate signal —
   and the existing no-double-wake/recovery-republish pins now run through it
   (red-verified: neutering `IsConflict` fails
   `TestRecordedButRunlessRedeliveryRepublishesTheWake` loudly).
6. **The shipped-config version pair is load-bearing**: the mock bootstrap must
   version-beat the live config (they share one KV entry). The cutover bumps
   them to 0.32.1 / 0.32.0, preserving the strict ordering the conformance pin
   asserts.

## Migration Plan

Single-PR migration on the working branch (matching every prior semstreams
bump): bump + rewrite + config cutover land as one reviewed, journey-proven
unit; groups are commit boundaries, not deployment stages. Rollback:
`go get github.com/c360studio/semstreams@v1.0.0-beta.159 && go mod tidy` plus
reverting the migration commits — but note beta.159 state in NATS is not
forward-compatible; any rollback also re-provisions storage (dev/e2e: `task
nats:reset`).
