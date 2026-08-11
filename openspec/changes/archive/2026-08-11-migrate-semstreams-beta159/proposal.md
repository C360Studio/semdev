# Migrate semstreams beta.154 → beta.159 (adopt the projection mutation client)

## Why

beta.159 deletes `agentictools.OwnedFactWriter` — semdev's ONLY Go fact-write
path — and replaces it with the contract-bound `pkg/projection` mutation client
(ADR-056). semdev does not compile against beta.159: 25 files, 22 `ReplaceTriples`
calls, 2 `ReadOwnedPredicates`. The migration makes every Go writer a declared
projection owner, which is strictly MORE flow-disciplined (writes still go over
NATS to graph-ingest; only the contract and subject change), and semdev's existing
G5 single-writer-per-predicate vocab table is the contract source.

## What Changes

- **BREAKING (framework):** `agentictools.OwnedFactWriter` /
  `NewNATSOwnedFactWriter` / `ReplaceTriples` are gone. Every Go write moves to a
  `pkg/projection` mutation client bound to a `projection.Contract`.
- **Projection contracts derived from the G5 vocab table.** Each Go-writer Source
  (`approval-adapter`, `conversation-classifier`, `conversation-adapter`,
  `create-change-author-tool`, `measurement-harness`, `task-projector`,
  `route-mirror`, `sandbox-provisioner`, `experiment-intake`, the review/verify/
  delivery harnesses, …) becomes one projection owner. Rule-writer Sources
  (`dev-route-rule`, `park-rule`, `conversation-spawn-rule`, …) are UNCHANGED —
  they write via the engine's `add_triple`, not the Go client.
- **Write mode per predicate:** replace-by-predicate writes → `ReplaceOwned`
  groups; the append-set ledgers (`conversation.intent.classified`, the
  `route.*` mirror) → `AppendEvidence`; entity creation (the admission record, the
  run mint) → `BirthPredicates` + `CreateWithTriples`. A mode error is silent
  (a ledger bound as replace-owned drops entries), so the vocab census guards it.
- **Composition-root binding.** An `ownership` registry + a process-lifetime
  heartbeater are stood up once in boot; each owner binds its full contract set in
  ONE `BindMutationClient`/`BindAndHeartbeat` call (the one-registration-per-
  registry-lifetime invariant — no incremental binds).
- **The graph-ingest owner-lease gate** (`enforce_owner_lease=true`). semdev hosts
  exactly one in-process graph-ingest component reached only over NATS, so "every
  serving instance enforces" is one config flag; the ADR-056 rollout evidence
  (heartbeat live, zero mismatch) is recorded before the paid lane runs.
- **NATS substrate (footnote, folded in):** pin the semstreams module to the
  beta.159 TAG (`nats.go` stays v1.48.0 — unchanged); adopt `nats:2.14-alpine` to
  match where the house is converging; pin the floating `natsio/nats-box:latest`.
- **Test doubles** (mock/e2e/unit) move to the new client shape.

## Impact

- **Capability:** `harness-measurement` (MODIFIED — the fact-write contract for
  the measurement/harness tools). No new capability.
- **Code:** the 25 Go-writer files, `internal/vocab` (contract derivation),
  `internal/boot` (registry + heartbeater + owner binds), the shipped configs
  (owner-lease flag), `docker/compose/nats.yml` + `Taskfile.yml` (image pins).
- **Sequencing:** lands BEFORE the parked nl-conversation-intent group 8
  (8.3/8.4/8.6) — its committed code (`stampDecision`, the apply consumer) writes
  through the old writer, so migrating first puts group 8 on the new substrate
  instead of migrating it twice. The parked `surface-delivery-evidence-recap`
  branch merges back after both.
- **Deployment:** a real behavior change on writes — every owned write now carries
  an owner token and is lease-checked at graph-ingest. Fail-closed if the gate is
  half-configured, which the single-binary topology makes trivial to satisfy.
