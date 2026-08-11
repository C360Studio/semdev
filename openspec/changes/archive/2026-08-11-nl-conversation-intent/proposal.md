## Why

The change-approval gate today fires on an EXACT string — `hasApprovalCommand`
matches the two whole tokens `/semdev approve` (`approval.go:102`). But the
conversational-forge north star is that semdev's human interface IS the comment
THREAD, read as natural language: "commands would be nice but outside of CLI it's
going to feel too synthetic — everyone expects NL now." A human who writes "looks
good, ship it" or "no, hold off — this is wrong" should be understood, not ignored
because they didn't type the magic string.

This change makes the human gate read NATURAL-LANGUAGE intent — **approve** or
**reject** — from an authorized author's message, using the SAME house pattern the
coordinator already uses to classify an issue into a closed action taxonomy (a
persona reads context → returns one intent from a closed set → a rule routes on the
resulting fact). The exact `/semdev approve` command stays as a deterministic,
zero-token fast-path (a symmetric `/semdev reject` is added), so the common case is
still free and there is always a deterministic escape hatch. It is the intent layer
only — the deployment transport (poll/webhook) and the code-host lanes are unchanged.

**The one safety invariant**: an LLM must NEVER manufacture an approval a human did
not give. The classification is a ROUTING signal (like the coordinator's `decide`),
never a measurement (G3); the deterministic `admission.Authorize` gate still decides
WHO may approve, the approval/rejection FACT is still stamped by the harness
(`approval-adapter`, G5), and semdev POSTS what it inferred before acting so a
misread is visible and catchable.

## What Changes

- **A closed conversation-intent taxonomy** (`approve` | `reject` | `none`) — a Go
  source-of-truth (mirroring `internal/taxonomy/taxonomy.go`) pinned to a new
  `conversation` persona fragment and to the routing rules, with a conformance
  census that fails on drift. `none` = no directive (ordinary chatter); the run
  stays gated.
- **A `classify_intent` tool** — semdev's analog of the framework `decide` tool: the
  classifier loop calls it with the intent it read plus the cited message's author,
  and it stamps a `conversation.intent` ROUTING fact. Intent-in-a-tool-schema is
  house-approved (the coordinator's `decide` takes `next_action`); it is NOT a G3
  measurement outcome.
- **A `conversation` classifier persona + an inherit-scoped classifier loop**: a rule
  spawns it on a run already `awaiting_approval` when an authorized human posts a
  non-command message. The loop reads the message (templated onto its prompt from the
  run's graph triple) and, when helpful, the fuller thread, and classifies the
  latest authorized message's intent.
- **The hybrid fast-path**: `handleMessage` keeps the exact-command check — a whole-
  token `/semdev approve` / `/semdev reject` short-circuits deterministically (no
  model turn, as today). Only a NON-command message from an AUTHORIZED author on an
  `awaiting_approval` run triggers NL classification (stamps
  `conversation.pending`, deduped by the channel-native message id).
- **The deterministic apply + transparency**: a rule routes `conversation.intent` to
  the conversation-channel adapter, which re-runs `Authorize` on the cited author,
  POSTS a short transparency note ("Proceeding based on @author's approval — say so
  if that's wrong"), and stamps `run.change.approved` (still `approval-adapter`, G5)
  for approve or `run.change.rejected` for reject.
- **A reject lane**: `run.change.rejected` + a run-lifecycle rule fires the existing
  `awaiting_approval → cancelled` transition (rule-owned, G2), so a rejected change
  stops consuming attention instead of hanging at the gate forever.
- **Regression guards**: the exact-command approval (webhook + poll journeys) stays
  byte-identical; a new NL-approve journey and a new NL-reject journey are added.

- **NOT in scope**: answering an `ask_human` clarifying question (needs the reserved
  `human.opt.signal` reply lane — not built); change-request / "tweak the plan" intent
  (needs the Phase 3 draft-PR review surface); non-GitHub channels; classifying
  whole-thread text divorced from a single authored message (intent is always bound
  to one authorized author's message, never an aggregate).

## Capabilities

### Modified Capabilities

- `conversation-channel` — the change-approval human gate reads NATURAL-LANGUAGE
  intent (approve/reject) from an authorized author, not only the exact `/semdev
  approve` string; the exact command remains a deterministic fast-path. Adds a reject
  intent that cancels the run and a transparency post on inferred actions. The
  classification is a routing signal (a persona read), never a measurement (G3); the
  authorization gate and the fact write stay deterministic (`approval-adapter`, G5).

## Impact

- **Code**: a `conversation` intent taxonomy (`internal/taxonomy` or a sibling) + a
  `conversation` persona fragment tree; a `classify_intent` tool
  (`internal/tools/classifyintent`); `handleMessage` gains the non-command →
  `conversation.pending` bridge (dedup by message id) alongside the retained
  exact-command fast-path; the adapter gains a deterministic apply lane (re-Authorize
  + transparency Post + stamp) fed by an intent-routing rule.
- **Graph / Vocab**: new READ + WRITE predicates — `conversation.pending`
  (the authorized human message awaiting classification), `conversation.intent` (the
  classifier's routing fact), `run.change.rejected` (the reject fact). Each gets a
  canonical vocab registration + a single writer (G5). The classifier READS
  `agent.run.phase` / `run.issue.ref` and the pending-message triple; it fires no
  lifecycle transition (G2).
- **Rules**: spawn the classifier on `conversation.pending` (awaiting_approval);
  route `conversation.intent == approve|reject` to the adapter apply lane; fire
  `awaiting_approval → cancelled` on `run.change.rejected` (run-lifecycle).
- **Regression guard**: the exact-command approval path (poll + webhook journeys) and
  the reject-less flow stay green; the NL approve + NL reject journeys are added.
- **Foundation unchanged**: the `Channel` port (Post/ResolveThread/Read), the poll
  transport, `admission.Authorize`, the resume rule, `semdev launch`.
