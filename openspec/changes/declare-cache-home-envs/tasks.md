# Tasks — declare-cache-home-envs

## 1. Pins, red first

- [ ] 1.1 Red-first pin: a non-Go profile with a complete `customizations.semdev`
      block (resolve/build/test + `cacheHomeEnvs`) resolves to a manifest that
      `ProveBaseline` accepts. Fails today at `baseline.go:60` with no operator
      remedy; record the pre-fix failure.
- [ ] 1.2 Red-first pin: `ResolveManifest` fails closed when neither convention
      nor customizations names a cache home, and the error names that field.
- [ ] 1.3 Pin the Go path unchanged: convention-only and partially-customized Go
      manifests keep `GOMODCACHE`/`GOCACHE`, and a declared `cacheHomeEnvs`
      overrides the convention.

## 2. The declaration surface

- [ ] 2.1 Add `CacheHomeEnvs` to `Customizations` (`cacheHomeEnvs`) and overlay
      it in `ResolveManifest`, symmetric with the other five fields.
- [ ] 2.2 Add the fail-closed cache-home guard to `ResolveManifest` alongside the
      image and test-command guards.
- [ ] 2.3 Make both `coldproof` incompleteness errors name the specific missing
      fields and point at the declaration surface; keep them as defense in depth.

## 3. Truth and evidence

- [ ] 3.1 Update the operator-facing docs for the `customizations.semdev` block
      to list the new field.
- [ ] 3.2 `task check` green; `openspec validate --all --strict` green.
- [ ] 3.3 Adversarial review the diff and fold all findings (standing directive).
