## Context

Phase 1 (`conversation-channel-seam`) carved the `Channel` port; `pull-first-transport`
added `Read` + the poller so the approval gate is reachable by polling. Both keep the
human signal an EXACT string: `handleMessage` (`internal/conversationchannel/approval.go:98`)
calls `hasApprovalCommand` (`:179`) — two whole tokens `/semdev approve` — and, if the
author passes `admission.Authorize`, stamps `run.change.approved`. Everything before the
command check (repo-bind) and after it (Authorize, resolve, write) is deterministic Go.

semdev already reads INTENT with an LLM: the coordinator persona reads an issue and
returns one action from a CLOSED taxonomy (`internal/taxonomy/taxonomy.go` — the Go
source of truth; mirrored in `configs/personas/fragments/coordinator/10-decision-contract.md`;
the framework `decide` tool stamps `coordinator.decision.next-action` **on the loop
entity** — `decide.go:361`; rules route). This change applies that shape to the approval
gate: one persona-read intent from a closed set, a rule routes on the resulting fact.

Two facts constrain the design (both confirmed in the pre-impl review):
- **Classification and the gate write live in different execution worlds.** LLM
  classification = a rule spawns a loop, the loop's tool stamps a routing fact, a rule
  routes. The gate write = deterministic Go. The bridge must be async (the LLM turn
  cannot sit inside the poll/webhook ack window) and must keep `Authorize` + the write
  deterministic.
- **A rule can only template the FIRING entity's own triples** (`dispatch-developer.json:5`),
  and `handleMessage`/the dedup only ever see the RUN (via `ResolveRunByRef`). So the
  human message AND the intent classification must live as triples ON THE RUN — not on
  the classifier loop (the `decide` default) and not transient in the transport.

**The stakes, stated honestly (semstreams pre-impl).** `decide` routes the system's own
next step and every downstream consequence is itself floored. `classify_intent → approve`
releases the HUMAN floor. There is NO harness ground-truth for "what a human meant," so —
unlike `submit_review`, which floors its verdict on `measurement.CanApprove` — an
analogous floor is impossible. The safety case therefore rests entirely on: (1) a
deterministic WHO-gate, (2) message-grounding, (3) a conservative persona, (4) transparency,
and — the real backstop — (5) the downstream **PR merge is still a human gate**: a false
NL-approval wastes development tokens but CANNOT ship unreviewed code, because semdev opens
a PR it never auto-merges. Every one of these must actually hold; the design below wires
each.

## Goals / Non-Goals

**Goals:**
- The change-approval gate reads NATURAL-LANGUAGE approve/reject intent from an
  authorized author's message, via the coordinator-decision house pattern, never an
  inline model call in product Go.
- The exact `/semdev approve` (and a new `/semdev reject`) stay deterministic zero-token
  fast-paths. NL classification fires only on a non-command message from an authorized
  author on an `awaiting_approval` run.
- A rejected change CANCELS the run (the existing `awaiting_approval → cancelled` edge,
  rule-owned, PHASE-GUARDED).
- Fail-safe against manufacturing an approval: `Authorize` decides WHO (deterministic,
  re-checked at apply, on a HARNESS-bound author); the gate fact is harness-stamped
  (`approval-adapter`, G5); the classification is a ROUTING signal (G3); an inferred
  action is announced before it lands; re-classification is idempotent; the run cannot
  end with both approved AND rejected.

**Non-Goals:**
- Answering an `ask_human` question (the reserved `human.opt.signal` lane) and
  change-request / "tweak the plan" intent (Phase 3 draft-PR surface).
- Classifying whole-thread aggregate text — intent is bound to ONE authorized author's
  message. Reading the fuller thread is a later enrichment (v1 = the single triggering
  message; multi-turn intent fails toward staying gated, never toward a false approve).
- **Reversing a LANDED NL-approval via the NL lane.** Once `run.change.approved` lands and
  the run resumes to `executing`, the NL lane does NOT cancel it (an `executing→cancelled`
  reject rule would let a stale/second-thoughts reject kill approved, token-burning work —
  architect H1). NL-approve is irreversible-once-landed; the transparency post is honest
  VISIBILITY, not an undo promise; the PR merge is the human's downstream stop.
- Non-GitHub channels; a confirm-then-wait round-trip.

## Decisions

### D1 — a closed conversation-intent taxonomy, Go source of truth
`internal/conversationintent` (sibling of `internal/taxonomy`) declares the CLOSED set
`{approve, reject, none}` with `Valid()` + `Names()`. `none` = no directive; the run
stays gated. The set is the source of truth for the `conversation` persona's decision
contract AND the routing rules; a conformance census fails on drift (the routing-rule arm
of the census lands with the rules in group 4 — noted so it is not asserted red early).

### D2 — a `classify_intent` tool: the model supplies JUDGMENT, the harness supplies IDENTITY
`internal/tools/classifyintent` exposes `classify_intent(intent, reason)` — the model
supplies ONLY the classification (`intent` from the taxonomy) and a short `reason`. It
takes NO `author` and NO `message_id` (architect H3 / semstreams HIGH-2): the message the
classifier was spawned to read is already on the run's `conversation.message.pending.*`
triples, so the HARNESS binds identity. The tool subject-overrides to the RUN (the
`create_change`/`dispatch-developer` pattern, `$entity.triple.agent.run.entity-id`) and
stamps on the RUN — NOT the classifier loop — `conversation.intent`, plus
`conversation.intent.message-id` and `conversation.intent.author` copied from the run's
pending triples (matched by the pending message id), plus `conversation.intent.reason`
(the model's echo, inert to the security path). Stamping on the run is load-bearing:
`handleMessage`'s dedup and the routing rule both read the run (architect H2).

**G3 (routing, not measurement) + G1 (why a new tool, not `decide`):** `classify_intent`
takes no outcome boolean and stamps no measurement fact — it is the `decide` shape (a
routing classification the harness records), NOT the `submit_review` shape (a
harness-floored verdict). It is a SEPARATE tool from `decide` because (a) `decide` carries
no message-id grounding (its args are action/reason/subtopics/retry_hint) and (b) `decide`
stamps under Source `coordinator-decide` on the coordinator's routing lane — reusing it
would give that predicate a second writer and collide with the coordinator's lane (G5).
The grounding fields + a distinct fact/writer (`conversation-classifier`) force a new tool.
`tool_choice: required` forces the call so a weak model cannot terminate text-only.

### D3 — a `conversation` classifier persona, inherit-scoped, single-message
A rule spawns an `inherit`-scoped loop with `role: conversation` (→
`configs/personas/fragments/conversation`) when a run at `awaiting_approval` gains a
pending authorized message. v1 classifies the SINGLE triggering message: its author +
body are templated onto the prompt from the run's `conversation.message.pending.{author,body}`
triples. The decision contract: read the message, output exactly one intent, and default
to `none` for anything short of an explicit directive — NEVER approve from silence, a
reaction, or ambiguous positivity. The persona reports only `intent` + `reason`; it never
names an author (the harness owns identity, D2).

### D4 — the hybrid fast-path (exact command short-circuits; NL bridges), phase-gated
`handleMessage` reads the run's PHASE (the resolver gains a phase getter — architect M7,
`ResolveRunByRef` today returns only `(runID, approved, err)`). It keeps the deterministic
exact-command check FIRST: whole-token `/semdev approve` → the existing approve write;
whole-token `/semdev reject` → the reject write (both zero model turns). Otherwise, ONLY a
message that is (a) not an exact command, (b) from an author who passes `Authorize`, (c) on
a run at `agent.run.phase == awaiting_approval`, and (d) whose id is NOT already in the
run's classified-ledger (D5), stamps pending and ACKs. Chatter, unauthorized authors, and
messages on non-gated runs spend ZERO model turns.

### D5 — the bridge fact + dedup (an append-set ledger + a self-extinguishing spawn marker)
`handleMessage` stamps `conversation.message.pending.{message-id,author,body}` on the run
(the body/author a rule templates to the classifier). Dedup is by the channel-native
message id against an APPEND-SET ledger `conversation.intent.classified` on the run
(multi-valued, the `task.attempt.instance` shape — NOT single-valued latest-wins, which
would forget all but the most recent id and re-classify a redelivered earlier message,
semstreams MEDIUM-4): `handleMessage` stamps pending for a message id only if it is not in
the ledger; the classifier adds the id to the ledger when it records the intent. The spawn
rule uses a self-extinguishing marker `conversation.classifier.dispatched = <message-id>`
stamped before the publish and guarded on (the `dev.developer.dispatched` pattern —
`dispatch-developer.json:12` — because `publish_agent` is not idempotent and the run is a
long-lived, replay-exposed entity; RULE_STATE edge-triggering alone would duplicate-spawn
on replay, architect M5 / semstreams MEDIUM-4). Latest-authorized-message-wins the pending
SLOT; the ACCEPTED-AND-NAMED asymmetry (semstreams MEDIUM-5): a rejection dropped in favor
of a later approval is worse than the reverse — mitigated by the gate-still-open guard
(D6), the conservative-none default, and the PR-merge backstop, and named in Risks. Making
`reject` strictly sticky is deferred (OQ2).

### D6 — the deterministic apply consumer (gate-still-open guard, harness-bound author, one writer)
A rule fires on `conversation.intent == approve` (or `reject`) on a run at
`awaiting_approval` — AND with both `run.change.approved` and `run.change.rejected` ABSENT
(`length_eq 0`) — and PUBLISHES to the conversation-channel component's apply consumer
(subject `component.conversation-apply.dispatch`; a declared jetstream input port — the R6
station shape, mirroring `validate-authored-change → component.validation-station.dispatch`).
The consumer, in Go: (1) re-checks the gate is still OPEN (neither gate fact present — the
`alreadyApproved` guard extended to both facts, so a second racing classifier is a no-op —
architect H4a); (2) reads the cited author from `conversation.intent.author` (HARNESS-bound
per D2, matched to the classified message — NOT the overwrite-prone pending slot, architect
H4b / semstreams HIGH-2) and RE-RUNS `admission.Authorize` on it (the classifier's judgment
is never trusted for authorization); (3) POSTS a transparency comment via `Channel.Post`
("Proceeding based on @author's approval." / "Cancelling this run based on @author's
rejection.") — honest VISIBILITY, no undo promise; (4) stamps `run.change.approved`
(approve) or `run.change.rejected` (reject). **Ordering + at-least-once (semstreams
MEDIUM-6):** Post BEFORE stamp; a `Post` failure returns TRANSIENT and BLOCKS the stamp
(guard 4 must precede the effect), so redelivery re-Posts (bounded duplicate comments,
the precedented park-post at-least-once posture) until the stamp lands. The stamp
(idempotent `ReplaceTriples`) is what the resume/cancel rules key on, not the post. Both
gate facts stay written by `approval-adapter` (G5): the fast-path and the apply consumer
call ONE shared writer method, censused (D10).

### D7 — the reject lane cancels a GATED run only (rule-owned, phase-guarded)
`run.change.rejected` (writer `approval-adapter`) triggers a run-lifecycle rule that fires
`awaiting_approval → cancelled`. The rule is PHASE-GUARDED `agent.run.phase == awaiting_approval`
(mirroring the resume rule, `run-lifecycle/02:9`) — LOAD-BEARING (architect H1): the
state machine's legal `executing → cancelled` edge means an unguarded reject rule would
kill an already-approved, executing run. No Go fires the transition (G2). A cancelled run
leaves `awaiting_approval`, so the poller stops enumerating it and no further
classification fires. The authored change is abandoned; a re-triggered issue starts a fresh
run (a rework loop that keeps the change is a Phase 3 concern, OQ2).

### D8 — the false-approval safety posture (four pre-landing guards + the downstream backstop)
An LLM must never manufacture an approval a human did not give. Guards, none the LLM's
word alone:
1. **Authorization is deterministic, re-checked, and on a HARNESS-bound author.**
   `Authorize` runs in Go before pending is stamped (D4) AND on `conversation.intent.author`
   at apply (D6) — the harness-bound identity, never the model's arg (D2). The classifier
   cannot approve on behalf of, or attribute approval to, anyone it names.
2. **Grounded to one real message.** The intent carries the classified message's id +
   (harness-bound) author; the apply acts on THAT, never aggregate sentiment.
3. **Conservative persona.** `none` is the default for anything short of an explicit
   directive (D3).
4. **Transparency before effect.** semdev POSTS what it inferred before the stamp lands
   (D6). This is VISIBILITY — the human SEES a misread on the thread. It is NOT reversible
   via NL (D7 Non-Goal); to that end the wording makes no undo promise.
5. **The downstream backstop (the real floor).** A false NL-approval resumes development
   but the run still faces measure → floors → clean-room verify → a PR that semdev OPENS
   and NEVER auto-merges. So a misread wastes tokens; it cannot ship unreviewed code — the
   PR merge is a human gate downstream of this one. This is why irreversible-once-landed
   (D7) is acceptable for v1. The exact command (D4) remains the unambiguous channel.

### D9 — cost + fail-safety; a classifier FAULT is surfaced, not silently stalled
One model turn per DISTINCT authorized non-command message on a gated run (deduped by the
ledger D5, gated on `awaiting_approval`, never for chatter/unauthorized/redelivery). The
classifier loop is async (off the transport ack window), so a slow/failed classification
never blocks the poller. **A classifier FAULT ≠ a confident `none` (semstreams HIGH-3):** a
loop that errors or truncates (no `conversation.intent` stamped, `agent.loop.outcome`
faulted) is a HUMAN-FACING dead-end if silent (the human wrote "ship it" and saw nothing).
A rule on the faulted classifier terminal POSTS a fallback note ("I couldn't read that as
approve or reject — reply `/semdev approve` or `/semdev reject`."). A confident `none` stays
silent (ordinary chatter). No new paid-token path on the exact-command flow.

### D10 — vocabulary + writers (register-before-write; censused single writer)
New canonical predicates (3-seg lower-kebab, `internal/vocab.Register`), registered in
group 1 BEFORE any writer (beta.150 fails closed at the graph-write boundary on an
unregistered predicate — architect M8): `conversation.message.pending` (writer
`conversation-adapter`), `conversation.intent` + `.classified` ledger (writer
`conversation-classifier`), `run.change.rejected` (writer `approval-adapter`). `run.change.approved`
and `run.change.rejected` are written from TWO code sites (the fast-path inline + the apply
consumer) under the ONE Source `approval-adapter` — the sanctioned "one logical writer,
multiple realizing sites" precedent (`g5_writers_test.go`, the route-mirror) — which
REQUIRES both sites call one shared writer method AND a sanctioned-writer census pin so the
Source cannot drift (D11). `conversation-adapter` becomes a LIVE Source for the first time
(no live emitter today); the Source split from `conversation-classifier` (same struct family)
is censused. The classifier READS `agent.run.phase`/`run.issue.ref`; no Go fires a
transition (G2).

### D11 — the shared-writer census (G5, semstreams confirmed-clean requirement)
A conformance pin (mirroring `TestOnlySanctionedParkWriters`) asserts the ONLY sources of
`run.change.approved` / `run.change.rejected` are `approval-adapter`, and that both the
fast-path and the apply consumer route through one shared writer method — so the two code
sites cannot drift their Source, preserving one-logical-writer (G5).

## Risks / Trade-offs

- **[A false NL approval resumes a run the human didn't approve]** → guards D8.1–4
  pre-landing + the D8.5 PR-merge backstop (it wastes tokens, it cannot ship unreviewed
  code). The NL-approve/reject/conservative-none journeys pin all three outcomes.
- **[The classification is net-new nondeterminism on a safety gate with no floor]** →
  confirmed unavoidable (approval has no harness ground-truth); mitigated by keeping every
  consequential effect deterministic/rule-owned and the classification inside a closed set
  (`tool_choice: required` + the decide-allowlist metadata).
- **[Concurrent classifiers stamp both approved AND rejected]** → the gate-still-open guard
  (D6.1) makes the first apply terminal and the second a no-op; the routing rules gate on
  both-facts-absent. A CONFLICT journey pins "exactly one terminal, never both."
- **[Safety-asymmetric drop: a rejection lost to a later approval]** (semstreams MEDIUM-5)
  → named; mitigated by the gate-still-open guard, conservative-none, and the PR-merge
  backstop. Strict reject-stickiness deferred (OQ2).
- **[Re-classification storm on restart / redelivery]** → the append-set ledger (D5) +
  the self-extinguishing spawn marker; a re-read/redelivery re-stamps nothing.
- **[A classifier fault silently strands the human]** → D9's faulted-terminal fallback note.
- **[Transparency Post failure]** → transient-return blocks the stamp (D6); bounded
  duplicate posts (park-post at-least-once precedent).
- **[Comment body on the graph]** → a bounded string on the run, needed for rule
  templating; transient run state, superseded per gate.

## Migration Plan

1. Vocab registration of ALL new predicates (incl. `run.change.rejected`) + the intent
   taxonomy (`internal/conversationintent`) + the `conversation` persona fragment tree +
   the taxonomy↔persona census (the routing-rule arm lands in step 4).
2. `classify_intent` (intent+reason only; subject-override to run; harness-bound
   author/message-id) + schema + registration; the G3 (no outcome field) + G1 (why-not-decide)
   pins.
3. `handleMessage`: the resolver phase getter; retain the exact-command fast-path; add
   `/semdev reject`; the authorized-non-command → pending bridge with the append-set ledger
   dedup; the spawn marker. Red-first: the exact-command approval stays byte-identical.
4. The spawn rule (marker-guarded, phase-guarded, body/author templated) + the two routing
   rules (gate on both-facts-absent) + the routing-rule arm of the taxonomy census.
5. The apply consumer (gate-still-open guard + harness-bound Authorize + Post-then-stamp +
   transient-on-Post-failure) + the phase-guarded reject→cancel rule + the faulted-classifier
   fallback-note rule + the D11 shared-writer census.
6. The NL journeys — approve, reject, conservative-none, the CONFLICT terminal, and the
   fault fallback — plus a real-LLM classification probe (decide the model tier, OQ4). The
   exact-command journeys stay green.
7. Spec + docs + full ladder + e2e -race + the censuses (G5/G1/taxonomy) + archive.
Rollback: additive — the exact-command path is untouched; reverting drops the NL bridge,
the classifier, and the reject lane.

## Open Questions

- **OQ1 — single message vs. read the fuller thread?** RESOLVED v1: the single triggering
  message (D3) — self-contained for approve/reject; multi-turn intent fails SAFE (stays
  gated). Thread-context enrichment is a later change.
- **OQ2 — reject = cancel vs. rework; strict reject-stickiness?** RESOLVED v1: cancel a
  GATED run (D7); NL-approve irreversible-once-landed (D7 Non-Goal, backstopped by the PR
  merge, D8.5). A rework loop and strict reject-stickiness are deferred (Phase 3).
- **OQ3 — does the exact-command fast-path post a transparency comment?** RESOLVED: NO —
  the command is explicit; the post is scoped to the INFERRED (NL) path (D6).
- **OQ4 — classifier model tier.** OPEN: a cheap single-turn read MAY run a smaller tier;
  decide during implementation against the real-LLM probe (step 6).
