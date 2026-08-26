# Tasks — pin-declaration-drift

## 1. The digest (shared, pure)

- [ ] 1.1 Red-first pins: the declaration digest is stable across re-resolution
      of an unchanged checkout, and CHANGES for each of — `testCommand`,
      `cacheHomeEnvs`, `tiers`, `secretRefs`, `resolveCommand`, `buildCommand`,
      and a rewritten `Dockerfile` body with an identical resolved contract.
- [ ] 1.2 Implement `harness.DeclarationDigest` over the resolved run contract
      plus the declaration source bytes (D2), shared so writer and reader
      cannot drift.

## 2. Attest at the baseline

- [ ] 2.1 Red-first pin: a ready provisioning stamps `sandbox.attestation.manifest`
      alongside the existing image/tier attestation.
- [ ] 2.2 Stamp it in `provisionsandbox`, in the existing attestation package
      under the same writer (G5). Declare the predicate in `internal/vocab`.

## 3. Detect at the verify

- [ ] 3.1 Red-first pin: a verify whose re-resolved digest differs from the
      attested one stamps `verify.declaration.drift` and stamps NO
      `verify.result` (D3) — assert both, since absent-pass is also what a
      crash looks like.
- [ ] 3.2 Red-first pin: an UNCHANGED declaration stamps no drift and verifies
      exactly as today (no behavior change on the honest path).
- [ ] 3.3 Implement the comparison in `verifyartifact`; declare the predicate.
- [ ] 3.4 Fail closed on a MISSING attestation — a verify with nothing to
      compare against must not silently accept. Pin it.

## 4. Park (rule-owned, G2)

- [ ] 4.1 Add the park rule to `configs/rules/run-lifecycle/`, following
      `05-park-station-failure-run.json`: park-first ordering, fired-once
      marker, publish to `semdev.park-post.request`.
- [ ] 4.2 Pin it via the rule-load and bootstrap conformance tests
      (`TestEveryRuleFileIsBootstrapped`).

## 5. Evidence

- [ ] 5.1 Flip the #38 tripwire to a regression guard (D6) — assert the drift
      fact is stamped, not merely that pass is absent.
- [ ] 5.2 A journey proving the drift park end to end on real docker.
- [ ] 5.3 `task check`, `task test:integration`, `task e2e` green;
      `openspec validate --all --strict`.
- [ ] 5.4 Adversarial review the diff and fold all findings (standing directive).
- [ ] 5.5 Evidence-ledger entry recording the closed hole and its proof.
