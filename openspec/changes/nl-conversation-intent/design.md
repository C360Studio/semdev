## Context

Phase 1 (`conversation-channel-seam`) carved the `Channel` port; `pull-first-transport`
added `Read` + the poller so the approval gate is reachable by polling. Both keep the
human signal an EXACT string: `handleMessage` (`internal/conversationchannel/approval.go:98`)
calls `hasApprovalCommand` (`:179`) — two whole tokens `/semdev approve` — and, if the
author passes `admission.Authorize`, stamps `run.change.approved`. Everything before the
command check (repo-bind) and after it (Authorize, resolve, write) is deterministic Go.

semdev already has the machinery to read INTENT with an LLM: the coordinator persona
(Sarah) reads an issue and returns one action from a CLOSED taxonomy
(`internal/taxonomy/taxonomy.go` — the Go source of truth; mirrored in
`configs/personas/fragments/coordinator/10-decision-contract.md`; the framework `decide`
tool stamps `coordinator.decision.next-action`; rules route on it). That is exactly the
shape this change applies to the approval gate — one persona-read intent from a closed
set, a rule routes on the resulting fact.

Two facts constrain the design:
- **The classification and the write live in different execution worlds.** LLM
  classification = a rule spawns a loop, the loop's tool stamps a fact, a rule routes.
  The approval write = deterministic Go inline in a component that spawns no loop. The
  bridge must be async (the LLM turn cannot sit inside the poll/webhook ack window) and
  must keep `Authorize` + the write deterministic.
- **A rule can only template the FIRING entity's own triples** (`dispatch-developer.json:5`).
  For a spawned classifier to see the human's message, the message must be a triple on
  the run entity the rule fires on — not left transient in the transport.

## Goals / Non-Goals

**Goals:**
- The change-approval gate reads NATURAL-LANGUAGE approve/reject intent from an
  authorized author's message, via the coordinator-decision house pattern (persona →
  closed taxonomy → routing fact → rule), never an inline model call in product Go.
- The exact `/semdev approve` (and a new `/semdev reject`) stay deterministic zero-token
  fast-paths. NL classification fires only on a non-command message from an authorized
  author on an `awaiting_approval` run.
- A rejected change CANCELS the run (the existing `awaiting_approval → cancelled` edge,
  rule-owned) instead of hanging at the gate.
- Fail-safe: `Authorize` decides WHO (deterministic); the approval/rejection FACT is
  harness-stamped (`approval-adapter`, G5); an inferred action is announced on the thread
  before it takes effect; the LLM classification is a ROUTING signal (G3), never a
  measurement; re-classification is idempotent (deduped by the message id).

**Non-Goals:**
- Answering an `ask_human` clarifying question (the reserved `human.opt.signal` reply
  lane — not built) and change-request / "tweak the plan" intent (Phase 3 draft-PR
  surface).
- Classifying whole-thread aggregate text — intent is always bound to ONE authorized
  author's message. Reading the fuller thread for richer context is a later enrichment
  (v1 classifies the single triggering message).
- Non-GitHub channels; a confirm-then-wait round-trip (v1 acts on a confident
  classification and announces it, it does not block on a second human turn).

## Decisions

### D1 — a closed conversation-intent taxonomy, Go source of truth
`internal/conversationintent` (sibling of `internal/taxonomy`) declares the CLOSED
intent set `{approve, reject, none}` with `Valid()` + `Names()`. `none` = no directive
(ordinary chatter); the run stays gated. The set is mirrored into a new `conversation`
persona fragment's decision contract and into the routing rules; a conformance census
fails on drift (mirroring `TestTaxonomyMatchesPersonaContract`). Keeping it a THREE-member
closed set (not free text) is what makes the classifier's output routable and auditable.

### D2 — a `classify_intent` tool, the `decide` analog (G3-clean)
`internal/tools/classifyintent` exposes `classify_intent`: the classifier loop calls it
with `intent` (from the taxonomy) + `message_id` + `author` (the cited message it read) +
a short `reason`. It stamps `conversation.intent` (the routing value), `conversation.intent.message-id`,
and `conversation.intent.reason` on the firing loop / run. **This is NOT a G3 violation:**
G3 bars an LLM-supplied MEASUREMENT outcome (a test pass/fail the harness must stamp).
An intent is a ROUTING classification — the coordinator's `decide` already takes a
`next_action` from a closed taxonomy and that is the sanctioned house pattern. The tool
takes NO outcome boolean and stamps NO measurement fact; it records what the persona
read, exactly as `decide` records the coordinator's chosen action. `tool_choice: required`
forces the call so a weak model cannot terminate text-only.

### D3 — a `conversation` classifier persona, inherit-scoped, single-message
A rule spawns an `inherit`-scoped loop with `role: conversation` (→ the
`configs/personas/fragments/conversation` fragment tree) when a run at
`awaiting_approval` gains a pending authorized message. **v1 classifies the SINGLE
triggering message**, not the whole thread: the message's author + body are templated
onto the loop's prompt from the run's `conversation.message.pending.*` triples (resolving
the "rules template only the firing entity's triples" limit without a tool round-trip).
The persona's contract: read the human's message, output exactly one intent from the
closed set with the message cited, and NEVER infer approval from silence, a reaction, or
ambiguous positivity — an unclear message is `none`. Reading the fuller thread for
context is deferred (it needs `github_list_comments` in a scoped `tools` list; v1's
single, self-contained approval/rejection utterance does not require it).

### D4 — the hybrid fast-path (exact command short-circuits; NL bridges)
`handleMessage` keeps the deterministic exact-command check FIRST: a whole-token
`/semdev approve` → the existing approve write (no model turn); a new whole-token
`/semdev reject` → the reject write. Only a message that is NOT an exact command, from an
author who passes `Authorize`, on a run at `awaiting_approval`, stamps
`conversation.message.pending` and returns (ACK). Ordinary chatter from anyone, and any
message on a non-gated run, is ignored deterministically with zero model turns — the
model is spent ONLY on a plausible NL directive from someone allowed to give one.

### D5 — the bridge fact + dedup by message id
`handleMessage` stamps `conversation.message.pending.{message-id,author,body}` on the RUN
(so a rule can template the body/author). Dedup is by the channel-native message id (the
poll transport's `Message.ID` = the real comment id; the webhook path's delivery-guid
fallback): `handleMessage` stamps pending for a message id ONLY if the run has not already
recorded a `conversation.intent.message-id` == that id (already classified) — so the
poller's re-read after a restart, and webhook redelivery, re-stamp nothing and re-spawn no
classifier. Latest-authorized-message-wins: a newer pending message overwrites the pending
slot (the approval gate expects ONE decision; rapid multiples are rare and the human can
re-post — noted as an accepted v1 limit). The spawn rule is edge-triggered (RULE_STATE
fire-once per pending value) so one classifier loop spawns per distinct pending message.

### D6 — the deterministic apply lane (one writer, G5) + transparency
A rule fires on `conversation.intent == approve` (or `reject`) on a run at
`awaiting_approval` and PUBLISHES to the conversation-channel component's apply consumer
(the R6 station shape: rule publish → component does deterministic work → stamps the
fact → a rule transitions). The consumer, in Go: (1) re-runs `admission.Authorize` on the
cited author (the gate is NEVER trusted from the classifier — the classifier proposes,
the harness re-verifies), (2) POSTS a transparency comment via `Channel.Post`
("Proceeding based on @author's approval — say so if that's wrong" / "Cancelling this run
based on @author's rejection"), (3) stamps `run.change.approved` (approve) or
`run.change.rejected` (reject). `run.change.approved` stays written by `approval-adapter`
(G5) — the SAME source as the fast-path; both the exact-command path and the NL path
converge on this one writer method, so there is exactly one code writer per fact.

### D7 — the reject lane cancels the run (rule-owned transition, G2)
`run.change.rejected` is a new fact; a run-lifecycle rule fires the existing
`awaiting_approval → cancelled` transition on it (agentrun's state machine already has
that edge). No Go fires the transition (G2) — the adapter stamps the fact, the rule owns
the phase move. A cancelled run stops the poller enumerating it (it leaves
`awaiting_approval`), so no further classification fires. The authored change is
abandoned; a re-triggered issue starts a fresh run (accepted — a rejection is terminal
for THIS attempt; keeping the work for a rework loop is a Phase 3 concern).

### D8 — the false-approval safety posture (the load-bearing invariant)
An LLM must never manufacture an approval a human did not give. Four independent guards,
none of which is the LLM's word alone:
1. **Authorization is deterministic and re-checked.** `Authorize(cited author)` runs in
   Go BEFORE classification is even triggered (D4) AND again in the apply consumer (D6).
   The classifier cannot approve on behalf of an unauthorized author.
2. **The classification is grounded.** The tool records the specific `message-id` +
   `author` the intent was read from; the apply consumer acts on THAT author. The intent
   is bound to one real, attributable message, never aggregate thread sentiment.
3. **The persona is conservative.** The decision contract makes `none` the default for
   anything short of an explicit directive (no approval from silence, emoji, or "nice").
4. **Transparency before effect.** semdev POSTS what it inferred before the transition
   lands (D6), so a misread is visible on the thread and catchable by the human.
The classification is a routing signal (like `decide`), not a measurement (G3); the
consequential facts are harness-stamped (G5). The exact-command fast-path (D4) is always
available as a deterministic, unambiguous channel.

### D9 — cost + fail-safety
One model turn per DISTINCT authorized non-command message on a gated run — deduped by
message id (D5), gated on `awaiting_approval` (no classification once resolved), and never
fired for chatter or unauthorized authors (D4). The classifier loop is async (spawned by a
rule, off the transport's ack window), so a slow/failed classification never blocks the
poller or redelivery; a classification that never completes leaves the run gated (the
human can re-post or use the exact command). No new paid-token path on the happy
exact-command flow.

### D10 — vocabulary + writers
New canonical predicates (3-seg lower-kebab, `internal/vocab.Register`), each one writer
(G5): `conversation.message.pending` (writer `conversation-adapter` — the transport that
observed the message), `conversation.intent` (writer `conversation-classifier` — the
loop's tool), `run.change.rejected` (writer `approval-adapter` — the same authority that
writes `run.change.approved`). `conversation.intent` is a routing fact the classifier
stamps and a rule reads; `agent.run.phase` and `run.issue.ref` are framework/rule facts
the classifier merely READS. No lifecycle transition is written from Go (G2).

## Risks / Trade-offs

- **[A false NL approval resumes a run the human didn't approve]** → the four guards of
  D8: deterministic double-Authorize, message-grounding, a conservative `none`-default
  persona, and a transparency post before the transition. The exact command remains the
  unambiguous path. The NL-reject and NL-approve journeys pin both directions; a
  "conservative none" pin asserts ambiguous positivity does NOT approve.
- **[The LLM classification is net-new nondeterminism on a safety gate]** → it is a
  ROUTING signal only; every consequential effect (authorization, the fact, the
  transition) stays deterministic/rule-owned. The classification cannot escape the
  taxonomy (closed set + `tool_choice: required` + the decide-allowlist metadata pattern).
- **[Re-classification storm on poll re-read / webhook redelivery]** → dedup by message
  id (D5) + edge-triggered spawn; a re-read re-stamps nothing and re-spawns nothing.
- **[Comment text on the graph]** → a bounded string on the run entity (the message body),
  needed so the rule can template it to the classifier (the firing-entity-triples limit).
  Acceptable; it is transient run state, cleared/superseded per gate.
- **[Latest-message-wins loses a rapid earlier message]** → the approval gate expects one
  decision; documented v1 limit, the human can re-post.

## Migration Plan

1. The intent taxonomy (`internal/conversationintent`) + a `conversation` persona
   fragment tree + the conformance census (taxonomy↔persona↔rules).
2. The `classify_intent` tool (the `decide` analog) + its schema + unit pins (G3-clean:
   no outcome field; stamps a routing fact).
3. `handleMessage`: retain the exact-command fast-path, add `/semdev reject`, add the
   non-command → `conversation.message.pending` bridge with message-id dedup. Red-first:
   the webhook/poll exact-command approval stays byte-identical.
4. The spawn rule (`conversation.message.pending` @ awaiting_approval → inherit classifier
   loop) + the intent-routing rules (approve/reject → the adapter apply publish).
5. The adapter apply consumer (re-Authorize + transparency Post + stamp
   `run.change.approved` / `run.change.rejected`) + the run-lifecycle reject→cancel rule.
6. The NL-approve + NL-reject bridge-proof e2e journeys (mock classifier fixtures, real
   docker), and a real-LLM classification probe. The exact-command journeys stay green.
Rollback: additive — the exact-command path is untouched; reverting drops the NL bridge,
the classifier, and the reject lane.

## Open Questions

- **OQ1 — classify the single message vs. read the fuller thread?** RESOLVED for v1:
  the single triggering message (D3) — simpler, safer, self-contained for approve/reject.
  Thread-context enrichment is a later change if a real run shows single-message intent is
  too thin.
- **OQ2 — reject = cancel vs. park-for-rework?** RESOLVED for v1: cancel (D7) — the
  existing state-machine edge, honest terminal for the attempt. A rework loop (keep the
  authored change, re-develop against the feedback) is a Phase 3 draft-PR-surface concern.
- **OQ3 — does the fast-path exact command also post a transparency comment?** RESOLVED:
  NO — the command is explicit, there is no inference to announce; the transparency post
  is scoped to the INFERRED (NL) path only (D6).
- **OQ4 — the classifier model tier.** OPEN: the front-of-arc coordinator runs the
  configured model; the classifier is a cheap single-turn read — it MAY run a smaller
  model tier. Decide during implementation against the real-LLM probe (cost vs. accuracy).
