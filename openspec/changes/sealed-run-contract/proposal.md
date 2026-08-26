# Seal the run contract and resolve it before the run

## Why

The clean room proves against a contract the work under test can rewrite. Proven
against real docker (#38): source byte-identical to
`TestProveArtifactRealTestsFailIsFail` — which correctly proves `fail` — renders
`verify outcome = "pass"` when the committed devcontainer declares
`"testCommand": ["true"]`.

The root cause is not the verify gate. It is that every gate re-derives the
contract from mutable state: `DetectProfile` runs at provision AND at verify with
no relationship between them, no human can declare a profile at all, and the
declaration lives inside the artifact being modified.

Full reasoning, the measured decision table, and the rejected alternatives are in
[DR-0003](../../../docs/decisions/0003-run-contract-is-sealed-and-pre-resolved.md).

## What Changes

- **Declared.** A repo carries `.semdev/profile.yaml` naming its profile.
  `DetectProfile` becomes a check against that declaration, not the source of
  truth. A repo that declares none parks toward the human.
- **Pre-resolved and carried.** The contract resolves once, before the dev loop,
  and is carried as run facts. No later gate re-derives it from the artifact.
- **Sealed.** `.semdev/**`, `Dockerfile`, `.devcontainer/**` may not change as a
  side effect of a run: `apply_patch` refuses a write there, and the commit
  boundary asserts zero delta over the set. Two points, because `measure_task`
  runs repo-declared commands with the checkout mounted and is not covered by the
  tool boundary.
- **Mismatch parks.** Declaration disagreeing with repo reality is a human
  decision, never an automatic reconciliation.

Out of scope, enabled by this: the pre-run image + dependency-cache build that
answers #29 by chronology instead of containment. It needs this change's
pre-resolution to exist first, and is a separate change.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `sandbox`: the profile is declared and checked, not inferred; the contract is
  resolved once and attested.
- `clean-room-verify`: the terminal gate proves under the carried contract.
- `run-lifecycle`: an undeclared profile, a declaration/reality mismatch, and a
  sealed-set delta each park toward the human.

## Impact

- **Facts:** the carried run contract and its identity (writer
  sandbox-provisioner, joining the existing attestation package); a sealed-set
  violation fact.
- **Rules:** park rules for undeclared profile, mismatch, and seal violation.
- **Code/tests:** `internal/harness` (declaration + digest), `internal/runspace`,
  `internal/tools/{provisionsandbox,applypatch,verifyartifact}`, `internal/vocab`,
  the fixtures, and the #38 characterization test flipped to a regression guard.
- **Operators:** every target repo now needs `.semdev/profile.yaml`. `semdev-test`
  and both fixtures must be seeded with one.
- **Issues:** closes #38; supersedes #39; unblocks #29.
