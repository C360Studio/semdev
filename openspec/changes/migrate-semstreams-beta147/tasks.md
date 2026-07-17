# Tasks — migrate to semstreams beta.147

## 1. Bump + package boundary + tripwire (foundation)

- [x] 1.1 Bump go.mod `v1.0.0-beta.146` → `v1.0.0-beta.147`; `go build ./...` finds the breaks
- [x] 1.2 Re-home the removed `input/github-webhook` → semdev-owned `internal/forge/githubwebhook` (issue-cluster types); update the 2 real importers (D8)
- [x] 1.3 Flip `TestTripwireOnRecoveryRoutingGate` to a REGRESSION GUARD (asserts the beta.147 `hasStatefulRuleActions` gate still admits OnRecovery — #530 landed); build + full unit suite green
- [x] 1.4 Commit the foundation slice (build + unit green; e2e still red pending the vocab work)

## 2. Canonical vocabulary + registry (D2/D5)

- [ ] 2.1 Rewrite `internal/vocab.Predicates` to the canonical D2 names (drop the index/key, kebab, pad to 3 segments; writers unchanged)
- [ ] 2.2 Add `internal/vocab.Register()` (init) calling `vocabulary.RegisterPredicate` for every canonical semdev predicate with its writer/capability metadata; boot imports it before rule load
- [ ] 2.3 Blank-import the framework vocab package(s) declaring the `agent.*`/`coordinator.*` predicates semdev reads (D5); resolve any framework-side renames boot-driven
- [ ] 2.4 Add per-predicate name constants (or a lookup) so Go writers reference the canonical names in one place (no scattered string literals)

## 3. Go writers/readers to canonical (D2/D3/D4)

- [ ] 3.1 `openpr`, `applypatch`, `measuretask`, `checkfloors`, `submitreview`, `projecttasks`, `validatechange`, `provisionsandbox` → canonical predicate constants
- [ ] 3.2 Blob the OpenSpec change projection (D3): `createchange` serializes `openspec.Change` to `openspec.change.document` (+ `openspec.change.slug`); `changefacts.Hydrate` deserializes the scalar; `hydratechange`/write-to-workspace/validate read it
- [ ] 3.3 Flatten floor findings (D4): `checkfloors` writes `floor.finding.rejected` (aggregate) + `floor.finding.detail` (scalar); readers updated
- [ ] 3.4 `go build ./...` + `go vet ./...` green

## 4. Rules + entity-ID config (D6/D7)

- [ ] 4.1 Rewrite all 27 rule packs: condition fields → declared canonical or explicit `$state.*`/`$message.*` (D6); `add_triple` action predicates → canonical declared literals (D2)
- [ ] 4.2 `entity_watch_patterns` → `entity_watch_buckets` in `configs/semdev-bootstrap.json`; add per-rule `entity.pattern` in the six-position language (D7)
- [ ] 4.3 Audit all semdev-constructed entity IDs against the six-position contract (D7); fix any non-6-part IDs

## 5. Unit + conformance green

- [ ] 5.1 Update conformance pins/fixtures to the canonical vocabulary (the G5/G9 census reads `internal/vocab`; the route-family and no-Go-routing-tokens pins reference predicate names)
- [ ] 5.2 Update unit-test fixtures/expectations to canonical names; full `go test ./...` (mock ladder) green

## 6. Boot-driven e2e green (the real proof)

- [ ] 6.1 Boot semdev on beta.147; work the config-validation error list to zero (each error names the offending rule/field; fix per D2/D6)
- [ ] 6.2 All four journeys (happy + retry + rejection + exhaustion) green under real docker with per-test `resetNATS`
- [ ] 6.3 Confirm no `structural_predicate_invalid` / entity-contract rejections in the runtime logs (the graph accepts every semdev write)

## 7. Docs + archive (G10)

- [ ] 7.1 Re-project the change specs (predicate renames across forge-io/run-lifecycle/dev-from-task/harness-measurement/openspec-io/clean-room-verify/sandbox)
- [ ] 7.2 `openspec validate --strict`; update CLAUDE.md status (pinned beta.147); note the deferred items (restart-recovery park, M1 keying, brownfield scalar)
- [ ] 7.3 Adversarial review (semstreams-reviewer + go-reviewer) over the migration; archive the change
