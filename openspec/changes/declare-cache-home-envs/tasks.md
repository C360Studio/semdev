# Tasks — declare-cache-home-envs

## 1. Pins, red first

- [x] 1.1 Red-first pin: a non-Go profile with a complete `customizations.semdev`
      block (resolve/build/test + `cacheHomeEnvs`) resolves to a manifest that
      `ProveBaseline` accepts. Fails today at `baseline.go:60` with no operator
      remedy; record the pre-fix failure.
- [x] 1.2 Red-first pin: `ResolveManifest` fails closed when neither convention
      nor customizations names a cache home, and the error names that field.
- [x] 1.3 Pin the Go path unchanged: convention-only and partially-customized Go
      manifests keep `GOMODCACHE`/`GOCACHE`, and a declared `cacheHomeEnvs`
      overrides the convention.

## 2. The declaration surface

- [x] 2.1 Add `CacheHomeEnvs` to `Customizations` (`cacheHomeEnvs`) and overlay
      it in `ResolveManifest`, symmetric with the other five fields.
- [x] 2.2 Add the fail-closed cache-home guard to `ResolveManifest` alongside the
      image and test-command guards.
- [x] 2.3 Make both `coldproof` incompleteness errors name the specific missing
      fields and point at the declaration surface; keep them as defense in depth.
- [x] 2.4 (from review) Report ALL missing run fields at the first boundary via a
      shared `harness.MissingRunFields`, so the operator is not dripped one field
      per round-trip — the very shape #28 is about. Both provers call the same
      function, which removes the transposable `(proveField, proveCmd)` pair.
- [x] 2.5 (from review, BLOCKING) Validate cache-home env names at the declaration
      boundary and collapse duplicates. `["  "]` cleared every `len() == 0` guard
      and produced a PASSING isolation verdict with the real cache home still warm;
      `[""]` and duplicates misclassified a declaration fault as an infra Retry.
- [x] 2.6 (from review) Name the freshened env vars in the cold-proof evidence, not
      just the anonymous volume IDs, so a wrong declaration is legible (G7).

## 3. Truth and evidence

- [x] 3.1 Write the operator-facing contract doc for the `customizations.semdev`
      block (`docs/target-repo-contract.md`) — review found the change was
      shipping a new operator knob documented nowhere an operator reads. Fix the
      `alignment-notes`/package-doc claims about what the block declares (G10).
- [x] 3.4 Real-docker check: coldproof (both G4 fabrication proofs), cleanroom,
      provisionsandbox, conformance.
- [x] 3.2 `task check` green; `openspec validate --all --strict` green.
- [x] 3.3 Adversarial review the diff and fold all findings (standing directive).
