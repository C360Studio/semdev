# Proposal: station-failure-parks

## Why

Real-LLM run 1 (2026-07-19, evidence ledger) exposed this live: the projection station refused the model-authored task, and after its bounded retries the run neither advanced nor parked — `internal/station/station.go:337` logs ERROR and returns ("no auto-park at M0 — R8/group 8"). In an attended journey the test window fails loud; unattended (M2 dogfood), a terminal station failure is a silent stall — the exact class the park posture exists to prevent. R8's restart-recovery half stays blocked on the upstream `on_recovery` routing gap, but THIS half is expressible today with existing primitives: a harness-stamped dispatch-outcome fact plus a park rule. M2's first safety floor.

## What Changes

- The station harness (`internal/station`) stamps ONE new fact when a `Handle` exhausts its bounded retries: `station.dispatch.failed` on the dispatched entity (object = station name + sanitized error), upsert-idempotent via `ReplaceTriples`. The harness ran the retries, so the stamp is G3-honest harness reporting; the success path is UNCHANGED (still no success fact from the harness — fail-closed posture intact).
- A run-lifecycle park RULE fires on that fact and records `run.awaiting.human` naming the failed station — the transition stays rule-owned (G2), donor shape `dev-from-task/06d` (`add_triple` + substitution + `publish user.response`, one-shot marker, already-parked and already-delivered guards).
- A dispatch-entity census in the design settles the run-fired vs loop-fired station split (the fact lands where the dispatch landed; the park rule variants bind `$entity.id` vs `$entity.triple.agent.run.entity-id`), pinned by tests.
- A new mock e2e journey reproduces run 1's exact shape (authored task without its measuring test → projection refuses → park lands, no false green).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `run-lifecycle`: the "Park toward the human rather than reconcile in Go" requirement gains: a deterministic station's TERMINAL dispatch failure SHALL park the run — the station harness records its dispatch-outcome fact (new predicate `station.dispatch.failed`, single writer `station-harness`, G5/G9) and a rule records `run.awaiting.human` from it.

## Impact

- `internal/vocab/vocab.go`: one new predicate `station.dispatch.failed` (writer `station-harness`, cap run-lifecycle).
- `internal/station/station.go` (+ unit tests): the retries-exhausted branch stamps the fact; needs an `OwnedFactWriter` seam threaded into the station config (DI, like the stations' existing writers).
- `configs/rules/run-lifecycle/`: the park rule(s) — count settled by the design's dispatch-entity census; bootstrap registration + ruleload census.
- `test/e2e/journey_test.go` (or sibling): the red-first station-failure park journey; existing 7 journeys' RequestCount contracts unchanged.
- NON-goals: restart-recovery (upstream-blocked; tripwire stays), station retry/backoff changes, any lifecycle write from Go.
