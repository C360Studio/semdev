# Tasks — sealed-run-contract

## 1. The declared profile

- [ ] 1.1 Red-first pins: `.semdev/profile.yaml` parses (version + profile); an
      absent file, an unknown profile, and a malformed document each fail closed
      and are distinguishable from one another.
- [ ] 1.2 Implement the declaration reader in `internal/harness` (or alongside
      `internal/standards`, matching that package's shape).
- [ ] 1.3 Red-first pin: the declared profile disagreeing with `DetectProfile` —
      in BOTH directions, including detection finding nothing — is a mismatch.
- [ ] 1.4 Invert `runspace.Manifests.Resolve` to take the declared profile and
      check detection against it, instead of letting detection decide.

## 2. Carry the contract

- [ ] 2.1 Red-first pin: provisioning stamps `sandbox.contract.resolved` carrying
      the fully-resolved manifest.
- [ ] 2.2 Stamp it in `provisionsandbox` under the existing attestation writer (G5);
      declare the predicate in `internal/vocab`.
- [ ] 2.3 Red-first pin: `verify_artifact` proves under the CARRIED contract and
      does not re-resolve from the clone — assert a clone whose declaration differs
      is proven under the carried one, not the committed one.
- [ ] 2.4 Red-first pin: a gate finding NO carried contract fails closed and parks;
      it must never fall back to re-resolving.
- [ ] 2.5 Rewire `verify_artifact` to read the contract instead of resolving it.

## 3. Seal the declaration surface

- [ ] 3.1 Define the sealed set once, shared by both enforcement points.
- [ ] 3.2 Red-first pin: `apply_patch` refuses a write to each member of the set
      (`.semdev/`, `Dockerfile`, `.devcontainer/`) with an actionable error, and
      permits an ordinary source path.
- [ ] 3.3 Implement the `apply_patch` refusal.
- [ ] 3.4 Red-first pin: a sealed-set delta reaching the commit — the
      `measure_task`-writes path the tool boundary cannot see — is caught at the
      commit boundary, stamps a violation, and is not measured.
- [ ] 3.5 Implement the commit-boundary zero-delta assertion against
      `refs/semdev/base`.

## 4. Park (rule-owned, G2)

- [ ] 4.1 Park rules for: undeclared profile, declaration/reality mismatch, sealed-set
      violation. Follow `run-lifecycle/05-park-station-failure-run.json` —
      park-first ordering, fired-once marker, publish to `semdev.park-post.request`.
- [ ] 4.2 Pin via the rule-load and bootstrap conformance tests.

## 5. Migration (D6)

- [ ] 5.1 Seed `.semdev/profile.yaml` into both fixtures.
- [ ] 5.2 Verify the e2e journeys park correctly for a repo WITHOUT one, then seed
      and confirm green. The park is the correct failure — do not paper over it.
- [ ] 5.3 Document the requirement in `docs/target-repo-contract.md`; note that
      `semdev-test` needs seeding before its next live run.

## 6. Evidence

- [ ] 6.1 Flip the #38 characterization test to a regression guard: assert the
      weakened declaration does NOT take effect, and that the run parks — not
      merely that no pass appears (absent-pass is also what a crash looks like).
- [ ] 6.2 A journey proving the seal end to end on real docker.
- [ ] 6.3 `task check`, `task test:integration`, `task e2e` green;
      `openspec validate --all --strict`.
- [ ] 6.4 Adversarial review the diff and fold all findings (standing directive).
- [ ] 6.5 Evidence-ledger entry recording the closed hole and its proof.
