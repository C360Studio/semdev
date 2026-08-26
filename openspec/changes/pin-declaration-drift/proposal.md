# Pin the declaration the baseline proved

## Why

The cold clean-room verify runs under a contract the model can write. Proven
against real docker (issue #38, tripwire in
`internal/coldproof/coldproof_integration_test.go`): source byte-identical to
`TestProveArtifactRealTestsFailIsFail` — which correctly proves `fail` — passes
when the committed devcontainer declares `"testCommand": ["true"]`.

```
resolved TestCmd = [true]
verify outcome   = "pass"
```

`pathguard.SafeJoin` constrains where a patch may write, not which files, so
`Dockerfile` and `.devcontainer/devcontainer.json` are writable.
`verifyartifact.go:137-140` then re-resolves the manifest from the committed
clone — deliberately, so what is proven is what was committed — and nothing
compares it against the declaration `ProveBaseline` proved. The floors are
Go-AST only; `forbidden.Scan` reads those files but hunts raw-URL fetches, not a
weakened measurement contract.

G3 still holds literally — the harness stamps the outcome — but it stamps the
outcome of the command the model declared. G4 and G7 sit downstream of a value
the model chose.

## What Changes

- The provisioning baseline attests the declaration it proved as
  `sandbox.attestation.manifest`: a digest over the resolved run contract AND
  the bytes of the declaration sources (Dockerfile, devcontainer.json).
- The clean-room verify recomputes that digest from the committed clone. On a
  mismatch it stamps `verify.declaration.drift` and stamps **no verdict** — a
  run whose contract moved has not been measured, and must not read as either
  pass or fail.
- A park rule fires on that fact and parks toward the human, naming what
  changed. Operator decision, per D5 below; a divergence is never
  auto-accepted, and never silently re-contracted.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `sandbox`: the readiness attestation carries the declaration it proved.
- `clean-room-verify`: the terminal gate refuses to render a verdict when the
  declaration moved under it.
- `run-lifecycle`: declaration drift is a park toward the human.

## Impact

- **Facts:** `sandbox.attestation.manifest` (writer sandbox-provisioner, joins
  the existing attestation package), `verify.declaration.drift` (writer
  verify-tools).
- **Rules:** one new park rule in `configs/rules/run-lifecycle/`.
- **Code/tests:** `internal/harness` (the digest), `internal/tools/provisionsandbox`,
  `internal/tools/verifyartifact`, `internal/vocab`, plus the #38 tripwire
  flipped to a regression guard.
- **Issues:** closes #38; unblocks #29's warm-cache half.
