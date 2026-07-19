# Tasks: station-failure-parks

## 1. Vocabulary + harness stamp

- [ ] 1.1 Vocab: register `station.dispatch.failed` (writer `station-harness`, cap run-lifecycle, IntroducedBy this change); vocab census green
- [ ] 1.2 Red-first unit pins in `internal/station`: (a) retries-exhausted → the fact lands on the dispatched entity, object names the station + bounded sanitized error (~512 runes), upsert-idempotent (second failure replaces, never appends); (b) SUCCESS path stamps nothing (no failure fact, no success fact); (c) nil-writer tolerance = today's log-only behavior
- [ ] 1.3 Implement the stamp in `internal/station/station.go`'s retries-exhausted branch via a new `OwnedFactWriter` DI seam on the station `Config`
- [ ] 1.4 Boot wiring: every registered station gets the writer; census pin so a future station cannot boot writer-less (`internal/boot` conformance test)

## 2. Park rules (run-lifecycle pack)

- [ ] 2.1 Dispatch-entity census pin: a test over the RULE PACKS' publish actions enumerating each station's dispatched entity (run vs loop) — the D2 table verified from source, not assumed
- [ ] 2.2 Settle the engine's `add_triple` duplicate-predicate semantics with a pin (append vs replace — D2 accepts either, the pin documents which)
- [ ] 2.3 Author the run-fired park rule (validation/projection/provision/delivery shape): fires on `station.dispatch.failed`, guards `run.awaiting.human length_eq 0` + `delivery.pr.ref length_eq 0` + one-shot `station.park.routed` marker; on_enter = marker + `run.awaiting.human` naming the station (06d donor shape) + `publish user.response.*`
- [ ] 2.4 Author the loop-fired park rule (floors/verify shape): binds the run via `$entity.triple.agent.run.entity-id`, one-shot marker guard (cross-entity guards deliberately omitted — D2 rationale in-rule description)
- [ ] 2.5 Bootstrap registration + `test/ruleload` census green (`TestEveryRuleFileIsBootstrapped` covers the new files)

## 3. The red-first journey (run 1's shape, zero paid tokens)

- [ ] 3.1 Mock journey `TestBridgeProofStationFailureParks`: create_change fixture authors task 0 WITHOUT its `*_test.go` in target_files → validation passes → approval → projection REFUSES → retries exhaust → assert `station.dispatch.failed` on the run AND `run.awaiting.human` naming projection AND no `task.spec.test-command` / `verify.cleanroom.result` / `delivery.pr.ref` (no false green) — red first (fails before groups 1-2 land), green after
- [ ] 3.2 Full `task e2e` green: the new journey + the existing 7 with UNCHANGED RequestCount contracts, `-race`, uncached

## 4. Verification + review + docs

- [ ] 4.1 Full offline ladder + `task e2e` green at head
- [ ] 4.2 Adversarial review (go-reviewer + semstreams-reviewer) — standing directive; apply findings
- [ ] 4.3 `openspec validate --strict` green
- [ ] 4.4 Docs: `internal/station/station.go` package doc + the `station.go:337` ERROR message updated (the R8 "no auto-park" caveat now names the park); CLAUDE.md status line; the evidence ledger's run-1 entry gains a "this class now parks" addendum ONLY after the journey is green (G7)
