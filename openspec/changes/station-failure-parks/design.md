# Design: station-failure-parks

## Context

`internal/station/station.go` is the shared harness for the six deterministic
stations (validation, projection, provision, floors, verify, delivery). Its
dispatch consumer runs `Handle` under bounded retries; on exhaustion it logs
ERROR and returns (`station.go:335-340`) — the triggering rule already fired
(edge-triggered engine), no fact landed, nothing re-fires: the run stalls with
no park. Real-LLM run 1 hit this at projection (evidence ledger 2026-07-19).
The reshape's R8 named this gap; its restart-recovery half is blocked on the
upstream `on_recovery` routing gap (tripwire in place), but the live-process
half needs no new engine feature: a fact append triggers a rule like any
other.

Donor shape for the park action: `dev-from-task/06d-route-escalate.json`
`on_enter` — a one-shot marker `add_triple`, the `run.awaiting.human`
`add_triple` with `$entity` substitution, and a `publish user.response.*`.

## Goals / Non-Goals

**Goals:**
- A terminal station failure parks the run toward the human, unattended-safe.
- The failure fact is harness-stamped (G3), single-writer (G5), one new
  predicate only (G9); the transition stays rule-owned (G2).
- Run 1's exact failure shape becomes a red-first mock journey.

**Non-Goals:**
- Restart recovery (upstream-blocked; the documented gap and its tripwire
  stand).
- Changing station retry counts, backoff, or the success path (no success
  fact from the harness — ever).
- Auto-resume or any reconciliation beyond the park.

## Decisions

**D1 — One predicate, stamped by the harness on the DISPATCHED entity.**
`station.dispatch.failed`, writer `station-harness` (the `internal/station`
consumer — the component that ran the retries; vocab-registered under
run-lifecycle). Object: `"<station-name>: <sanitized error>"`, error bounded
(~512 runes, the semsource truncate posture) so a pathological error string
cannot bloat the graph. Upsert via `ReplaceTriples` replace-by-predicate: a
crash-loop of repeated dispatches converges to one triple, never an append
pile. The writer seam is DI: the station `Config` gains an `OwnedFactWriter`
(exactly how station handlers already write their own facts); a nil writer =
today's log-only behavior (boot always wires it — the nil tolerance is for
unit tests of unrelated station behavior, and a boot-wiring census pins that
every registered station gets it).

**D2 — Park rules in the run-lifecycle pack, split by dispatch entity.** The
fact lands where the dispatch landed, so the rule must reach the run from
there. Expected split from the rule packs (the CENSUS TASK pins this from the
actual publish actions, not this table): run-dispatched stations — validation,
projection, provision, delivery — carry the run as `$entity.id`; loop-
dispatched stations — floors (L_n), verify (Q_n) — carry it as
`$entity.triple.agent.run.entity-id` (the 06d donor binding). Two rules, same
conditions otherwise:

- fire: `station.dispatch.failed` present (`length_gte 1` on the entity);
- guards: the resolved run's `run.awaiting.human` absent... — NOTE: rule
  conditions read the FIRING entity only (routing-upgrades crux fact), so the
  already-parked/already-delivered guards can only be expressed on facts the
  firing entity carries. For the run-fired rule that IS the run — guards are
  direct (`run.awaiting.human length_eq 0`, `delivery.pr.ref length_eq 0`).
  For the loop-fired rule the loop entity carries neither — the guard there is
  the one-shot marker alone, and the park `add_triple` on the run is
  IDEMPOTENT BY VALUE (`run.awaiting.human` is replace-by-predicate at the
  graph layer? — NO: `add_triple` appends. The census task must verify the
  engine's add_triple semantics on a duplicate predicate; if it appends, the
  loop-fired park landing on an already-parked run adds a second
  `run.awaiting.human` triple, which is BENIGN for every consumer (presence
  checks and message display) but must be stated. The design accepts a
  possible duplicate park message over a racy cross-entity guard — the
  routing-upgrades lesson: a rule excluding on another entity's stamp is racy,
  always.)
- one-shot: `station.park.routed` marker triple on the firing entity
  (`length_eq 0` condition + `add_triple` in on_enter, 06d pattern).

**D3 — The park message names the station.** `run.awaiting.human` object:
`"station <name> failed after retries: $entity.triple.station.dispatch.failed"`
(substitution threads the stamped object — exact phrasing settled at
implementation against what substitution supports; the fact's own object is
the source of truth, the message is display).

**D4 — The red-first journey reproduces run 1.** Mock arc: front-of-arc
fixtures but `create_change` args author task 0 with `target_files` lacking
any `*_test.go` → validation passes (the CLI oracle does not enforce the
includes-test contract — run 1 proved it) → approval → projection REFUSES
(the real `project_tasks` contract) → retries exhaust → fact → park. Asserts:
`run.awaiting.human` present naming projection; `station.dispatch.failed`
present on the run; NO `task.spec.test-command`, NO `verify.cleanroom.result`,
NO `delivery.pr.ref` (the exhaustion journey's no-false-green pattern).
RequestCounts of the existing 7 journeys untouched (new journey, own mock).

## Risks / Trade-offs

- **[Loop-fired park can duplicate `run.awaiting.human` on a corner race]** —
  accepted (D2): duplicate park facts are benign to every consumer; a
  cross-entity guard would be racy by the engine's per-action revision
  semantics. Stated in the spec scenario as "at most once per failure" (the
  one-shot marker), not "at most one park triple per run".
- **[A transient infra fault (NATS blip) parks a run that a human must
  resume]** — accepted and intended: the station already retried with
  backoff; after exhaustion, silence is the only alternative and silence is
  worse (run 1). The park message carries the error for the human's
  retry/resume decision.
- **[New DI seam touches all six station registrations]** — mechanical;
  the boot-wiring census pin keeps a seventh future station from silently
  booting writer-less.

## Migration Plan

Additive: new predicate, new rules, new journey; the harness change is one
branch in the existing failure path. Rollback = revert. No persisted-state
migration (the fact simply never landed before).

## Open Questions

- Engine `add_triple` duplicate-predicate semantics (append vs replace) —
  settled by a pin during implementation (D2 notes both outcomes are
  acceptable; the pin documents which).
