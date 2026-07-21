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
classifier was spawned to read is already on the run's `conversation.pending.*`
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
body are templated onto the prompt from the run's `conversation.pending.{author,body}`
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
`handleMessage` stamps `conversation.pending.{message-id,author,body}` on the run
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

**Delivered mechanism (group 4 — the marker is RELEASED-AND-RE-ARMED, not never-removed,
and two rules the pre-impl sketch did not name are load-bearing; grp4 review folded):**
- **`conversation/01-anchor-gated-run`.** A run entity NEVER carries the bare
  `agent.loop.run` triple before `dev-from-task/01` fires at executing+approved, and
  `publish_agent` inherit reads exactly that triple off the firing entity's snapshot — so
  the gate-time spawn needs its own anchor rule (the dev-from-task/01+02 two-rule split;
  a same-rule `add_triple` is invisible to a same-rule inherit). The early stamp is inert:
  every other reader of the run's anchor also requires `run.change.approved`, and
  dev-from-task/01 is idempotently superseded (its `length_eq 0` guard; identical value).
- **`conversation/04-classifier-terminal-release`.** The `dev.developer.dispatched` shape
  (marker never removed) would close the NL lane after ONE classification — but the
  conflict journey (6.4) and the none-then-approve human flow need the run's NEXT message
  to classify. So the marker LIFECYCLE is: spawn stamps it (before the publish) → the
  classifier loop terminal — clean OR faulted — clears the pending slot and then the
  marker, re-arming the spawn. Removal order `author → body → message-id → marker` is
  doubly load-bearing (each action = its own KV revision): id-before-marker prevents the
  re-spawn revision; author/body-before-id prevents a concurrent bridge write being torn
  into an id-present/author-absent slot that spawns a fault-only classifier. The durable
  dedup is the classified LEDGER (untouched by the release), never the marker.
- **Read-once binding (grp4 semstreams HIGH-1).** The pending slot is latest-wins, so a
  second authorized message can replace it DURING the model turn. `classify_intent`
  therefore reads the marker VALUE (the message id the loop was dispatched for) and
  FAULTS — stamping and deduping nothing — when the slot no longer matches, instead of
  binding the newcomer's identity to a judgment of the old text. Inside the flight window
  latest-wins becomes latest-LOSES: the newcomer is retired unclassified and UN-deduped,
  the faulted terminal drives the D9 fallback note (surfaced, not silent), and a re-nudge
  or the exact command recovers. Bounded to one model turn.
- **The engine's per-action firing cap (grp4 go H1).** Actions default to a cap of 3
  fires per rule+entity (RULE_STATE-persisted). The spawn and the two routes are the
  repo's first rules designed to re-fire indefinitely on ONE entity (the run), so all
  their actions carry explicit `max_iterations: 0` — under the default the run's 4th
  authorized message would be silently skipped (the dead-lane class). The spend stays
  bounded by the ledger dedup + the marker + the release + the spawn's gate-facts-absent
  guards (grp4 go M1: a gate-fact-bearing run spends no classifier turn).
- **Best-effort action execution (grp4 go M3 / semstreams MEDIUM-1, honest limit).** A
  failed action logs, bumps `actionFailuresTotal`, and the engine CONTINUES — so a failed
  pending-removal degrades to one bounded duplicate turn, and a failed MARKER-removal (or
  a publish that fails after the marker stamp, or a never-terminal classifier — go M4:
  `publish_agent` is not a station dispatch, no park fires) closes the NL lane silently
  for that run; the exact command recovers. The orderings narrow these windows; closing
  them wants an upstream atomic multi-remove / abort-on-first-failure ask (tasks 7.1).

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

**Both gate facts can co-exist — the resume AND cancel rules MUST partition the cell
space (grp3-review M2, go+semstreams).** The exact-command fast-paths (`releaseGate`)
BYPASS the routing rules' both-facts-absent guard (D6) and stamp a gate fact directly: an
authorized `/semdev reject` then `/semdev approve` on a still-gated run leaves it carrying
`run.change.rejected == true` AND `run.change.approved == true`. The Go fast-path refuses
only reject-on-ALREADY-approved (H1, via the resolver's `approved` getter); it deliberately
does NOT grow a second Go-side gate read to enforce full mutual exclusion, because the
lifecycle partition is the RULES' job (G2) and must be evaluated over ONE atomic mirror
snapshot (the routing-upgrades cell-space lesson — the engine writes each action as its own
KV revision, so a Go guard racing the rules is weaker, not stronger). Therefore BOTH
lifecycle rules must be mutually exclusive: the RESUME rule (`run-lifecycle/02`) gains
`run.change.rejected length_eq 0`, and the CANCEL rule carries `run.change.approved
length_eq 0` — a run holding both facts transitions to NEITHER, never both. That cell is
an UNSURFACED STALL, not a park: nothing stamps `run.awaiting.human`, nothing posts, and
no operator surface flags it. Parking it is NOT available as a fix — a park at this gate
is unrecoverable (grp5-review B1) — so it is named in Risks rather than papered over. Pinned by the `TestConflictingIntentsResolveToOneTerminal` journey (6.4).

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

### D9 — cost + fail-safety; a classifier that produces NO READING is surfaced, not silently stalled
One model turn per DISTINCT authorized non-command message on a gated run (deduped by the
ledger D5, gated on `awaiting_approval`, never for chatter/unauthorized/redelivery). The
classifier loop is async (off the transport ack window), so a slow/failed classification
never blocks the poller. **A classifier that produces no reading ≠ a confident `none`
(semstreams HIGH-3):** a loop that errors, truncates, exhausts its cap, or DELIBERATELY
REFUSES (the D2 read-once binding check) stamps no `conversation.intent` and is a
HUMAN-FACING dead-end if silent — the human wrote "ship it" and saw nothing. A rule
(`conversation/05`) posts a fallback note ("I couldn't read that as approve or reject —
reply `/semdev approve` or `/semdev reject`."). A confident `none` stays silent (ordinary
chatter). No new paid-token path on the exact-command flow.

**THE DISCRIMINATOR IS THE ABSENCE OF A RECORDED CLASSIFICATION, NOT THE LOOP OUTCOME —
corrected in group 6 after the journeys proved the original premise false.** This design
originally specified "a rule on the FAULTED classifier terminal (`agent.loop.outcome`
faulted)". That rule can never fire for the case it exists for: a tool returning a
`ToolResult` error does NOT fail its loop — the error goes back to the model and the loop
terminates NORMALLY. Observed end-to-end: the refusing classifier ended `outcome=success`
with `iterations=1`, the terminal-release rule wiped the pending slot, and the human was
told nothing. So `classify_intent` now stamps `conversation.classifier.recorded` on ITS OWN
LOOP (the `submit_review` route-mirror shape) when a classification lands, and the note
fires on that fact's ABSENCE at a non-`cancelled` terminal. A loop-local witness is the only
thing available: rule conditions read only the FIRING entity, and the run's
`conversation.intent.*` is unreachable from a loop-fired rule. The mirror is derived BEFORE
the run write so a mis-wired platform faults CLOSED (nothing routes) rather than open.

### D10 — vocabulary + writers (register-before-write; censused single writer)
New canonical predicates (3-seg lower-kebab, `internal/vocab.Register`), registered in
group 1 BEFORE any writer (beta.150 fails closed at the graph-write boundary on an
unregistered predicate — architect M8): `conversation.pending.{message-id,author,body}`
(writer `conversation-adapter`), `conversation.intent.{value,message-id,author,reason}` +
the `conversation.intent.classified` ledger (writer `conversation-classifier`),
`conversation.classifier.dispatched` (the spawn rule's self-extinguishing marker — a
rule `add_triple`, hence a graph write that MUST be registered; writer
`conversation-spawn-rule`), `conversation.classifier.recorded` (group 6 — the LOOP-scoped
witness that a classification landed, writer `conversation-classifier`; the fault note keys
on its ABSENCE, see D9), and `run.change.rejected` (writer `approval-adapter`). `run.change.approved`
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

**Delivered mechanism (group 5 — three decisions the pre-impl sketch left open).**

**(1) The apply dispatch rides a JETSTREAM stream, and D6's wording was right.** At
implementation time the repo appeared to say otherwise: all six `component.*.dispatch`
consumers (`internal/station`) ride CORE NATS, and no declared stream covered
`component.>`. That looked like precedent contradicting the design — but the framework's
own primitive heuristic (`docs/concepts/03-streams-vs-kv-watches.md`) settles it the other
way, and unanimously: this dispatch is a REQUEST (not a fact) with real external side
effects (a forge comment POST, a permission API call), handled by exactly one consumer,
whose replay would re-post to a human's thread. Fact→KV, request→stream; core NATS is the
request/REPLY lane (`graph.query.*`), not a coordination primitive. The station base's own
package doc books its transport as named M0 debt ("the crash-in-the-publish→handle-window
durability the old `publish_agent` inherited from the AGENT JetStream stream is NOT
preserved here … is design R8"). This lane declines to inherit that debt, because a
dispatch dropped in that window means an authorized human's approval is silently swallowed
and the run sits gated forever — the exact silent dead-end D9/HIGH-3 exists to prevent. A
NARROW `CONVERSATION` stream over `component.conversation-apply.>` makes only this lane
restart-safe; the other six keep their R8 debt explicit rather than being silently
persisted.

**Retry and durability are kept SEPARATE.** JetStream buys RESTART safety only; the RETRY
posture is bounded in-process attempts, as in the station base.

**But the TERMINAL path deliberately breaks with every other station, and this is the
most important decision in the group (grp5-review B1, caught in review — the
implementation had it wrong).** Every other station routes a retries-exhausted dispatch
to `station.dispatch.failed`, which `run-lifecycle/05` converts into `run.awaiting.human`.
Doing that HERE is unrecoverable: this run sits AT the change-approval gate, and BOTH
release rules require `run.awaiting.human` ABSENT (`run-lifecycle/02` resume, `/07` cancel)
— and NOTHING in the repo ever removes that predicate (eight park rules add it, zero
remove it; a resume-from-park rule is still unbuilt). The run could then never be approved
or cancelled, and even the human's deterministic escape hatch would die, because
`/semdev approve` would stamp a fact no rule would ever consume. The trigger bar is low —
any forge 5xx outlasting the retry budget — and the loss is the whole authored change.

This is the SAME hazard D9's fault note was written to avoid, applied to a more likely
trigger; the apply lane had walked straight into it. So on exhaustion this lane POSTS A
NOTE and LEAVES THE GATE OPEN: the human is told the automatic path failed and pointed at
the exact commands, which still work because nothing was stamped. Fail toward the human
WITHOUT taking away their controls. The group-4 dispatch-entity census keeps its
`conversation-apply: run` entry (the firing entity IS the run) with the opt-out recorded,
and `TestApplyLaneIsTheDeliberateNonParkingStation` enforces it — a future "be consistent
with the other six stations" refactor is exactly the change that must fail loudly.

Because exhaustion is non-destructive, the retry budget is also LARGER than the station
base's sub-second one: each attempt crosses an external HTTP dependency, so a routine
forge blip must not become a terminal outcome. The JetStream `MaxDeliver` is a crash-loop backstop,
NOT the retry budget. The rejected alternative — deriving "retries exhausted" from
`msg.Metadata().NumDelivered` — was gymnastics: it duplicates `MaxDeliver` into the
handler, requires threading the raw message through a byte-oriented dispatch path shared
with two other lanes, and loses the park entirely if the process dies on the final
delivery. The consumer is SERIAL (`max_ack_pending: 1`), which removes interleaving of two
concurrent APPLY dispatches. It does NOT make the D7 partition redundant
(grp5-review M4/M5): the serialization is per-consumer and global — one durable
consumer, one exact filter subject — so it is orthogonal to the exact-command
fast-path, which runs on a different consumer (or the poller goroutine) and can
stamp a gate fact concurrently with an in-flight apply. The rule partition remains
the only thing preventing a run from both resuming and cancelling. The global
serialization also means one slow forge call is head-of-line blocking for every
other awaiting-approval run — acceptable at M2 scale, named rather than implied.

**(1b) A channel-less deployment REFUSES to apply (grp5-review H2).** The park lane
degrades to graph-only without a forge token, because its post is a courtesy on top of an
already-durable fact. Here the transparency post IS the entire visibility case for a
decision with no harness floor (D8 guard 4), so the same degrade would release a gate no
human was ever told about — reachable, since webhook mode needs no token and the
classifier needs none either. The lane therefore fails closed with a loud warning; the
exact-command path is unaffected.

**(2) The fault note gets its OWN lane, NOT the park lane.** The park rules stamp
`run.awaiting.human`, and `run-lifecycle/02` reads that predicate as a RESUME BLOCKER.
Routing a classifier fault through the park would therefore WEDGE the run — a later
successful approval could never resume it, converting a recoverable one-message miss into a
permanently stuck run. `conversation/05` publishes `user.note.<instance>` instead, riding
the EXISTING `USER` stream (subjects `user.>`, so no new stream) and stamping NOTHING: a
note is a message to a human, not a lifecycle event (G9 — no new vocabulary for a post).
The rule fires on the classifier LOOP, where conditions can only read the firing entity's
own facts (the run's intent facts are unreachable), so the discriminator is
`agent.loop.outcome == "failed"` (`agentic.OutcomeFailed`). A `success` terminal means
classify_intent ran — including the deliberate conservative `none`, which stays SILENT
because ordinary chatter must not draw a reply — and `cancelled` means the run is going
away.

**(3) The cancel rule carries the D15 park exclusion.** Like every active
`lifecycle_transition` rule it requires `run.awaiting.human length_eq 0`
(`TestLifecycleTransitionRulesExcludeParkedRuns` enforces this repo-wide). The guard is
inert on the normal path — the change-approval gate is PHASE-based and stamps no park
marker — and bites only when an `ask_human` park and a rejection coincide, where holding
still is the correct conservative outcome.

## Risks / Trade-offs

**Config-KV hazard, found in review and worth recording (grp5-review, BLOCKING).** Both shipped
configs share ONE KV entry — the bucket (`semstreams_config`) and key (`version`) are hardcoded
globals, not platform-derived — AND declare the identical platform identity
(`c360/semdev-bootstrap/development`), so the framework's gh#459 cross-app detach guard cannot
fire and the VERSION ALONE decides which file wins. `PushToKV` writes `model_registry`.
`semdev-live-gemini.json` had carried a decorated version (`0.28.0-live-gemini`) for its whole
life, which `CompareVersions` cannot parse — so it always took the sync-from-KV branch and could
never push. That was an accident that happened to fail SAFE (a live run silently degrading to
mock). Making the version parseable removed that accidental protection and let the live config
win, so a subsequent mock-config boot would adopt gemini endpoints and the free ladder would
spend real tokens behind one WARN line. Fixed by keeping the MOCK config's version STRICTLY
GREATER (`0.30.0` vs `0.29.1`), so every stale-KV race resolves toward the free config; the live
lane loads via its mandatory `task nats:reset`. Both invariants — plain semver AND the ordering —
are pinned by `TestShippedConfigVersionsAreParseableSemver`, verified red against the inversion.
Giving the live config a distinct `platform.id` is the framework's sanctioned isolation and the
stronger fix. **The reason first given for deferring it here was wrong and is corrected:** entity
IDs derive from `platform.instance_id` (`internal/boot/runtime.go:236-241`), which BOTH configs set
to `semdev-001`; `Platform.ID` is read in exactly one place in the repo and only as a fallback that
is dead while `instance_id` is set, and the sidecar queries grep `chain.execution`/`ENTITY_STATES`,
which contain no identity segment. So the split would change no entity ID and no sidecar query — it
is a one-field edit, not a migration.

It is still deferred, for a DIFFERENT and accurate reason: tripping the gh#459 guard makes the live
config run DETACHED (no sync, push, watch, or later runtime write against the shared bucket), which
changes the boot semantics of the PAID lane — and that cannot be exercised without spending a real
run. Changing how the paid lane loads its config belongs in a change that can verify it, not as a
tail-end edit to this one. What the split would additionally close is a FALSE-PROVENANCE residual
the ordering fix does not: a live boot against a stale mock KV silently runs on the mock endpoint
with `defaults.model: mock`, which is safe on spend but could record a run in the evidence ledger as
real-Gemini when it was mock (G7). Until the split lands, that risk is carried by the mandatory
`task nats:reset` on every config-loading lane (`serve`, `e2e`, `test:integration`,
`realllm:launch`), whose `nats:down -v` removes the volume and so genuinely wipes the config KV.


**Known gap: the both-gate-facts cell is an unsurfaced stall (grp5-review, semstreams M3).**
The D7 partition guarantees a run carrying BOTH gate facts fires NEITHER lifecycle rule —
strictly safer than a double transition, but it is NOT a park: nothing stamps
`run.awaiting.human`, nothing posts, and no operator surface flags it. The run sits at
`awaiting_approval` indefinitely with no human-visible signal, and if the NL lane got there
first the human has already seen a transparency comment for a decision that never took
effect. Parking the cell is NOT available as a fix — a park at this gate is the B1 wedge.
Reaching it needs an exact `/semdev reject` and `/semdev approve` to interleave inside one
mirror evaluation, so it is narrow; it is named here rather than left implied, and a
conflict-note lane (publishing `user.note.*`, which cannot wedge) is the natural follow-up.

**Known gap: two definitive zero-write exits are only partly surfaced (grp5-review M5/L9).**
An intent with no harness-bound author now posts a notice, but an unparseable
`run.issue.ref` cannot — there is no thread to post to — so it is a loud log only. Both
shapes are near-unreachable by construction (the harness binds the author; the ref is
validated upstream), which is the argument for treating them as definitive rather than
redelivering forever.

**Known gap: the D11 census is package-scoped.** It proves the gate writer is not
duplicated within `internal/conversationchannel` (covering composite-literal, positional,
assignment, raw-literal, and sanctioned-Source shapes — all verified to fail red against
planted evasions), but a gate write introduced in another package would be invisible to it,
and `TestSingleWriterPerPredicate` censuses the vocab table rather than code. Bounded by
both writing lanes living in this package by construction.


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
