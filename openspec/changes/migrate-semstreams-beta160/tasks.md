# Tasks — migrate-semstreams-beta160

## 1. Bump and compile map

- [x] 1.1 `go get github.com/c360studio/semstreams@v1.0.0-beta.160 && go mod tidy`;
      commit the bump alone so the break inventory is reproducible
- [x] 1.2 Enumerate every compile error (`go build ./... 2>&1`), classify each
      against the six migration docs, and verify every replacement symbol
      against the module cache (`~/go/pkg/mod/.../semstreams@v1.0.0-beta.160`),
      never the local checkout (standing directive)
- [x] 1.3 Record any break the recon did not predict as a design note in this
      change before fixing it

## 2. graphown rewrite (D1, D2)

- [x] 2.1 `contracts.go`: strip owner-claim/lease logic and commentary; keep
      the 19-contract table, `ContractFor` entity-match resolution, and the
      contract↔vocab-table census; align `Contract` literals to the beta.160
      struct
- [x] 2.2 Replace `binding.go` with construction: one
      `projection.NewMutationClient({NATS, Contracts: all, Timeout})`; DELETE
      `EnsureBuckets`/registry/heartbeater wiring, the in-process claim ledger,
      and `ErrOwnersAlreadyBoundInProcess` — no shims, no dead exports
- [x] 2.3 `writer.go`: `Replace` → `Reconcile` (same group blast radius —
      carry the beta.159 group-discipline doc comment forward),
      `ReadOwnedPredicates` → `ReadAuthoritative` (adapt to
      `*graph.ExactEntity`, KEEP the local prefix filter —
      `projecttasks.go:67` gates immutability on it), `WriteErrorKind` → the
      seven classified `MutationErrorKind`s
- [x] 2.4 RED-FIRST pin: revision-conflict retry — a double returning
      `revision-conflict` twice then success lands the write with the correct
      final `Desired` (bounded at 3 attempts total)
- [x] 2.5 RED-FIRST pin: retry exhaustion — three conflicts surface
      `MutationRevisionConflict` to the caller unchanged, never silence
- [x] 2.6 Migrate every test double from the `ReplaceOwned` method shape to
      `Reconcile`/`ReadAuthoritative`; assertions on `m.Contract`/`m.Group`
      carry over

## 3. Boot layers and pin lifecycle (D1, D7)

- [x] 3.1 `boot/runtime.go`: `BindAll` → client construction; the boot census
      asserts contracts-complete BEFORE anything that writes is registered
      (re-base the `RequireBound` census, keep its loud-fail naming)
- [x] 3.2 `boot/launch.go` + `boot/boot.go`: same construction swap; DELETE
      the failed-boot claim-release paths at all three layers (the ledger they
      released is gone)
- [x] 3.3 e2e stand-ins: replace `rt.GraphOwners()` draws with the
      contract-resolver equivalent
- [x] 3.4 DELETE `TestOwnerLeaseObserveOnlyOnLanding`,
      `TestInProcessClaimLedger`, and the one-bind-per-owner pin — each dies
      with its mechanism, not before its mechanism
- [x] 3.5 Re-base the no-hand-rolled-mutation-subject census to a sanctioned
      exception cap of ZERO (the intake raw subject migrates in group 4);
      red-verify the census catches a planted violation
- [x] 3.6 Confirm `TestWriteTodosStaysSkipped` still passes unchanged
      (`write_todos` remains a valid `SkipBuiltins` key — verified upstream)

## 4. Intake birth write (D3)

- [x] 4.1 RED-FIRST pin: a doubled `Create` returning `MutationConflict`
      yields no error AND no second coordinator-wake publish
- [x] 4.2 `intake/component.go`: replace the raw
      `graph.mutation.entity.create_with_triples` request with
      `MutationClient.Create` under the admission contract; conflict = logged,
      counted, wake-suppressed idempotent duplicate; all other classified
      kinds fail the delivery loudly as today

## 5. Config and port cutover (D4, D5)

- [x] 5.1 Rewrite the three Go `PortDefinition` sites (`internal/intake`,
      `internal/conversationchannel`, `internal/station`) to the typed
      `Portable` config envelope; add a requester output
      (`semstreams.graph.mutation` v1) to every semdev component that holds a
      graphown writer (issue-intake, conversation-channel, the six stations)
- [x] 5.2 `configs/semdev-bootstrap.json` + `configs/semdev-live-gemini.json`:
      every ports block → strict envelope; graph-ingest mutation input →
      canonical typed `nats-request` port copied from the framework's shipped
      `configs/agentic.json` shape; requester outputs on `rule`,
      `agentic-tools`, `agentic-loop`
- [x] 5.3 Services block: remove inner `name` fields (strict decode rejects
      unknown fields); TOOL stream `tool.>` → `tool.execute.>` +
      `tool.result.>`; bump top-level config `version`
- [x] 5.4 Sweep journey-local/test configs for the same three surfaces (port
      envelope, services shape, stream subjects)
- [ ] 5.5 Boot the assembled runtime against real NATS and iterate until
      static flow validation admits the flow; record every validation demand
      the docs did not predict as a design note

## 6. Effect metadata adoption (D6)

- [ ] 6.1 Declare a worst-effect class on every tool `RegisterTools`
      registers: `open_pr` = `external_effect`; the graph/workspace mutators
      (`create_change`, `validate_change`, `apply_patch`, `measure_task`,
      `check_floors`, `submit_review`, `verify_artifact`,
      `provision_sandbox`, `project_tasks`, `classify_intent`) = `mutating`;
      pure readers (semsource proxy reads) = `read_only`
- [ ] 6.2 RED-FIRST census: source-level test over semdev's registration
      table failing on absent or unrecognized effect values (mirrors the
      framework's own check, which excludes adopter tools)
- [ ] 6.3 Pin that effect metadata changed no gate: the advertised-tools
      admission and approval-gate behavior in existing pins is byte-identical
      before/after classification

## 7. Evidence ladder and closeout

- [ ] 7.1 Offline ladder green: `task check` (build + vet + unit + offline
      pins) across all packages
- [ ] 7.2 Full `task e2e -race` on fresh docker NATS (`task nats:reset`
      satisfies the fresh-storage adoption premise): all bridge-proof
      journeys green, zero skips — named gates:
      `TestBridgeProofWebhookIssueToApprovedRun`,
      `TestBridgeProofSelfTargetForgeCloneToPR`,
      `TestBridgeProofStationFailureParks`,
      `TestBridgeProofApprovalByPollNoWebhook`,
      `TestBridgeProofNLApprovalReleasesGate`, the retry/rejection/exhaustion
      journeys
- [ ] 7.3 Run the e2e gate 3× green consecutively (Reconcile's doubled wire
      ops shift timing; three runs is the flake bar prior bumps used)
- [ ] 7.4 Adversarial review: `semstreams-reviewer` + `go-reviewer` on the
      full migration diff (standing directive); fold ALL findings before the
      group commits are final
- [ ] 7.5 Evidence-ledger entry: the bump, the upstream artifact (tag
      `v1.0.0-beta.160` = candidate-proof SHA `8403a221`), the journey
      evidence, and the resolution of beta.159's parked posture/Resign
      questions (moot — mechanism removed)
- [ ] 7.6 Docs: real-LLM runbook gains the fresh-NATS-storage adoption note;
      CLAUDE.md pin moves to beta.160 with the wave summary
- [ ] 7.7 `openspec validate --all --strict` green; sync the
      harness-measurement delta; archive the change
