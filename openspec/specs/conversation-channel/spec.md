# Conversation Channel Specification

## Purpose

Defines the channel-agnostic conversation seam: semdev posts human messages and
reads human signals through a `ConversationChannel` port whose thread handle is
opaque to the arc, over a normalized channel-neutral `Message`. GitHub issue/PR
comments is the v1 implementation; a second channel (Slack, Jira, …) composes
behind the same port without touching the arc. This capability owns the change-
approval gate operable from the thread and the park-message posting; it shares the
admission authorization gate with `forge-io` (issue intake) as one implementation.

## Requirements

### Requirement: A channel-agnostic conversation seam

The system SHALL post human messages, resolve a run's thread, and read a thread's
messages through a `ConversationChannel` port whose thread handle (`ThreadRef`) is
opaque to the arc: only the channel implementation resolves it. The port SHALL expose
`Post` (post a message to a thread), `ResolveThread` (resolve a work coordinate to its
thread), and `Read` (read a thread's messages after an opaque cursor, returning the
messages and the next cursor). The conversation component SHALL process a normalized,
channel-neutral `Message`; the arc SHALL depend only on the message facts the seam
produces, never on a channel's payload shape (e.g. no arc rule or product component
outside the channel implementation references `githubwebhook.CommentEvent` or a
channel-native coordinate). GitHub issue/PR comments is the v1 implementation; for
GitHub a thread handle is the work coordinate itself (`ResolveThread` is identity —
comments live on the work object) and `Read` lists the thread's comments.

Adding or replacing a conversation channel SHALL require no change to any arc rule
or product component; only the channel implementation is channel-aware.

#### Scenario: A message is posted to a resolved thread
- **WHEN** a component posts a message for a run
- **THEN** the seam resolves the run's work coordinate to a `ThreadRef` and posts the message through the configured channel implementation
- **AND** no product code outside the channel implementation references the channel's native coordinate

#### Scenario: The arc consumes normalized message facts, not channel payloads
- **WHEN** a rule reacts to a human message
- **THEN** it references only a normalized message fact
- **AND** it references no channel-specific payload field or predicate

#### Scenario: A second channel requires no arc change
- **WHEN** a second conversation-channel implementation is introduced behind the port
- **THEN** no arc rule and no product component is modified to accommodate it

#### Scenario: Reading a thread returns messages after the cursor
- **WHEN** the seam reads a thread with a cursor
- **THEN** it returns the neutral `Message`s posted after that cursor, each attributed to its author, plus the cursor to pass on the next read
- **AND** an empty cursor reads the whole thread (the restart / first-read case)

### Requirement: The message-fact family has a single writer

The normalized inbound message fact `human.opt.signal` SHALL be written by exactly
one writer (`conversation-adapter`, capability `conversation-channel`) — never by a
channel implementation directly. Channel implementations (GitHub v1, others later)
are internal to the conversation component, not fact writers. This preserves
one-writer-per-fact (G5) across any number of channels.

#### Scenario: One writer regardless of channel
- **WHEN** any channel implementation ingests a human message that becomes a `human.opt.signal` fact
- **THEN** the fact carries the single writer `conversation-adapter`
- **AND** no channel implementation writes the fact under its own source

### Requirement: Human communication rides the conversation channel

The system SHALL post messages to humans — a parked run's `run.awaiting.human`
notice and any `ask_human` question — to the run's thread through the port, and the
arc SHALL NOT contain a bespoke chat surface. The change-approval human gate SHALL be
operable from the thread by an **authorized actor** (a push-capable repository
collaborator or an allowlisted actor, per the shared admission gate) expressing an
intent — either the exact opt-in command (`<command> approve` / `<command> reject`) OR
NATURAL LANGUAGE the system classifies. An APPROVE intent SHALL land as the
single-valued gate decision (`run.change.decision` == `approve`) on the run through
the approval adapter — the same fact, source, and placement the resume rule consumes.
A REJECT intent SHALL land as `run.change.decision` == `reject`, on which a rule
fires the run's `awaiting_approval → cancelled` transition (the transition stays
rule-owned). The decision fact is single-valued and first-writer-wins (the
run-lifecycle capability owns its full contract). An intent from an actor who is not
authorized SHALL NOT be routed to the gate and SHALL leave the run gated; a message
carrying no directive SHALL leave the run gated. (The human-reply lane — a reply
re-entering as `human.opt.signal` to resume a parked run — is a reserved forward
contract, not delivered by this change; see the design.)

The exact opt-in command SHALL remain a DETERMINISTIC fast-path: a whole-token command
match SHALL release (or cancel) the gate with no model turn, exactly as before. Only a
non-command message from an authorized actor on a run awaiting approval SHALL trigger
natural-language classification.

#### Scenario: A parked run surfaces its message on the thread
- **WHEN** a run records `run.awaiting.human`
- **THEN** the seam posts the park message to the run's thread through the port, naming what the human must decide

#### Scenario: An exact approval command releases the gate with no model turn
- **WHEN** an authorized actor posts the whole-token approval command on the run's thread
- **THEN** the approval adapter records `run.change.decision` == `approve` deterministically, spending no model turn
- **AND** the existing resume rule advances the run with no journey stand-in write

#### Scenario: A natural-language approval releases the gate
- **WHEN** an authorized actor posts a non-command message the classifier reads as approval on a run awaiting approval
- **THEN** the system posts a transparency note naming the inferred approval and its author, then records `run.change.decision` == `approve` through the approval adapter
- **AND** the existing resume rule advances the run

#### Scenario: A natural-language rejection cancels the run
- **WHEN** an authorized actor posts a message the classifier reads as rejection on a run awaiting approval
- **THEN** the system posts a transparency note naming the inferred rejection, records `run.change.decision` == `reject`, and a rule fires the `awaiting_approval → cancelled` transition
- **AND** no further classification fires for that run (it has left the gate)

#### Scenario: An unauthorized signal is ignored
- **WHEN** a signal arrives from an actor who is neither a push-capable collaborator nor allowlisted
- **THEN** no gate decision lands, no classification is triggered, and the run stays gated

#### Scenario: A non-directive message leaves the run gated
- **WHEN** an authorized actor posts a message the classifier reads as carrying no approve/reject directive (ordinary chatter, ambiguous positivity)
- **THEN** no gate fact lands and the run stays awaiting approval

### Requirement: The approval gate is operable with no inbound webhook

The change-approval gate SHALL be releasable from the conversation thread on a
deployment that receives NO inbound webhook. The system SHALL poll each run awaiting
approval on a configured interval, reading its thread through the port's `Read` verb,
and SHALL release the gate for an authorized approval signal exactly as the webhook
path does — landing `run.change.decision` == `approve` on the run through the
approval adapter (the same fact, source, and placement the resume rule consumes). The webhook transport
remains an OPTIONAL latency accelerator, never a requirement.

A deployment SHALL run EITHER the webhook-fed comment consumer OR the poller for the
inbound comment lane, never both, so a comment is never processed twice. The read
cursor SHALL be in-memory poll state, NOT a domain-graph fact; correctness SHALL rest
on the approval's idempotency (re-reading an already-applied approval is a no-op), so
a restart that rebuilds the cursor by re-reading is safe. The poller SHALL fire no
lifecycle transition (it observes the run's phase and feeds an approval message; the
resume rule owns the transition) and SHALL introduce no new fact writer.

Poll-path authorization SHALL use the message author as the principal: a polled
message carries one identity (its author), and the same admission gate (allowlisted or
push-capable collaborator) governs it. An approval signal from an unauthorized author
SHALL NOT release the gate.

The poll transport reads a comment's CURRENT body, so it MAY honor an approval that
was edited into a comment (attributed by the host to that comment's author, which the
admission gate governs); the webhook transport acts only on comment creation. This
edit divergence is accepted and documented, not a byte-identical guarantee across
transports.

The deployment SHALL make the active inbound mode observable: enabling the poller
without an inbound webhook, or configuring neither, SHALL be surfaced at boot (a loud
mode log, and an assembly-time coherence check) rather than presenting as a healthy
component that silently never releases the gate.

#### Scenario: A polled approval releases the gate with no webhook
- **WHEN** a run is awaiting approval on a deployment with no webhook receiver and polling enabled
- **AND** an authorized author posts the approval signal on the run's thread
- **THEN** the poller reads it on the next interval and the approval adapter records `run.change.decision` == `approve` on the run
- **AND** the resume rule advances the run with no webhook and no stand-in write

#### Scenario: An unauthorized polled approval is ignored
- **WHEN** a polled approval signal's author is neither a push-capable collaborator nor allowlisted
- **THEN** no gate decision lands and the run stays gated

#### Scenario: Re-reading an already-approved thread is a no-op
- **WHEN** the poller re-reads a thread whose approval already landed (e.g. after a restart rebuilt the cursor from the top)
- **THEN** no second decision write occurs and the run is unaffected

#### Scenario: Poll and webhook do not both drive the comment lane
- **WHEN** a deployment enables the poller
- **THEN** the webhook-fed comment consumer does not also run
- **AND** a single approval comment is processed exactly once

### Requirement: Natural-language intent is a routing classification, not a measurement

The system SHALL classify a human's natural-language message into a CLOSED intent
taxonomy (`approve`, `reject`, `none`) using a persona that reads the message, mirroring
the coordinator decision taxonomy. The taxonomy SHALL have a single Go source of truth
pinned to the persona's decision contract and to the routing rules, with a conformance
check that fails on drift. The classification SHALL be a ROUTING signal recorded by the
classifier (its own fact and writer), NEVER an LLM-supplied measurement outcome (G3): it
records what the persona read, exactly as the coordinator's `decide` records a chosen
action, and it carries no measurement fact and no outcome boolean.

Classification SHALL be bound to ONE authorized author's specific message, never to
aggregate thread sentiment: the classifier SHALL cite the message it read (its
channel-native id and author), and the deterministic apply SHALL re-run the
authorization gate on that cited author before recording any gate fact. The persona
SHALL default to `none` for anything short of an explicit directive (no approval from
silence, a reaction, or ambiguous positivity).

Classification SHALL be idempotent per message: a message already classified SHALL NOT
be re-classified (deduped by its channel-native id), so a poll re-read after a restart or
a webhook redelivery triggers no repeat model turn. Classification SHALL run only for a
run awaiting approval and SHALL fire no lifecycle transition (it observes the run's phase
and records an intent; a rule owns the transition).

#### Scenario: Classification records a routing fact, not a measurement
- **WHEN** the classifier reads an authorized author's message
- **THEN** it records one intent from the closed taxonomy with the cited message id and author
- **AND** it records no measurement fact and takes no outcome parameter

#### Scenario: The authorization gate is re-checked at apply, not trusted from the classifier
- **WHEN** an intent is routed to the deterministic apply
- **THEN** the apply re-runs the admission authorization gate on the cited author before recording any gate fact
- **AND** an unauthorized cited author yields no gate fact

#### Scenario: An already-classified message is not re-classified
- **WHEN** the transport re-reads a message whose intent was already recorded (e.g. after a restart or a redelivery)
- **THEN** no second classification is triggered and no repeat model turn is spent

#### Scenario: Classification is scoped to the approval gate
- **WHEN** a message arrives on a run that is not awaiting approval
- **THEN** no classification is triggered

#### Scenario: A classifier that produces no reading tells the human

- **WHEN** a classifier loop for an authorized message reaches a terminal without
  recording a classification — it errored, truncated, exhausted its iteration cap, or
  DELIBERATELY REFUSED because the pending slot moved off the message it was dispatched
  for — and the run has no gate decision
- **THEN** semdev SHALL post a fallback note on the thread telling the human it could not
  read the message as approve or reject, and naming the exact commands
- **AND** the gate SHALL be left untouched, so the human's deterministic controls stay live
- **AND** a classification that DID land SHALL post no such note

The discriminator is the ABSENCE of a recorded classification on the classifier loop, NOT
the loop's outcome. A tool returning an error does not fail its loop, so a classifier that
refuses to classify still terminates successfully — an outcome-keyed rule never fires, which
is how this lane shipped silent and was caught only by an end-to-end journey.

### Requirement: A message can only decide the gate it was written for

BOTH inbound decision paths — the exact command AND natural-language classification —
SHALL honor a per-run gate-opening watermark: a message whose channel timestamp does
not postdate the watermark SHALL be definitively discarded (acknowledged, counted,
logged loudly), never classified and never applied. The watermark is the run's
gate-opening time LESS a bounded cross-clock skew allowance (the code-host stamps
poll-path timestamps on its own clock), applied uniformly to both transports; it
SHALL be read from a fact the platform contract promises is populated (the run's
last-transition audit fact). A gated run with NO establishable watermark, or a
message with NO usable timestamp, SHALL fail CLOSED — the message is refused loudly,
never honored.

Thread-to-run resolution SHALL be deterministic to the ACTIVE gated run (a run
awaiting approval preferred, newest gate-open next, a stable tiebreak last), never
first-match-in-page-order — two runs sharing one thread is reachable, and without the
watermark plus deterministic resolution, a historical "ship it" approves a proposal
the human never saw.

Named accepted consequence: PRE-APPROVAL — a decision typed before the proposal
exists — does not release the gate.

#### Scenario: A historical approval cannot decide a new proposal
- **WHEN** a second run mints against an issue whose thread carries an approval (natural-language or exact command) written for a PRIOR proposal
- **THEN** the old message is definitively discarded and the new proposal stays gated awaiting a fresh decision

#### Scenario: A restart re-read decides nothing
- **WHEN** the poll transport re-reads a thread from cursor zero after a restart
- **THEN** messages that do not postdate the watermark decide nothing and spend no model turn

#### Scenario: An unestablishable watermark fails closed
- **WHEN** a run at the gate has no usable gate-opening timestamp, or an inbound message carries no usable timestamp
- **THEN** the message is refused loudly and no gate decision lands

#### Scenario: A fresh message still decides normally
- **WHEN** an authorized message arrives after the run's gate opened
- **THEN** it is classified or applied exactly as the gate contract specifies

### Requirement: Natural-language classification is spend-bounded per run

The system SHALL bound the paid classifier turns spent on ONE run's gate by a fixed
per-run budget, counted at DISPATCH — so a faulted, refused, truncated, or
cap-exhausted classification consumes budget exactly like a successful one — and the
budget ledger SHALL survive classification cycles as the run's durable spend record.
Serialization (one classifier in flight at a time) is not the bound; without the
budget, N distinct authorized messages on one gated run are N paid model turns with
no ceiling, reachable by ordinary conversation on a live thread.

On exhaustion, a further authorized non-command message SHALL be refused WITHOUT a
model turn, and the refusal SHALL be ANNOUNCED on the thread once per run, naming the
exact commands — a budget-refused message and an ignored one are otherwise
indistinguishable from the human's side. Once-per-run is the nominal contract: under
the named best-effort action-failure shapes it degrades to BOUNDED repetition (the
rule's own metadata states the bound), never to silence. The exact command path SHALL
remain fully operable at zero model turns after exhaustion.

#### Scenario: The budget caps the paid turns
- **WHEN** more distinct authorized non-command messages arrive on one gated run than the budget allows
- **THEN** classifier dispatches stop exactly at the budget and every later message spends no model turn

#### Scenario: Exhaustion is announced once
- **WHEN** the first over-budget authorized message is refused
- **THEN** a note posts on the thread naming the exact commands, once for the run's lifetime in nominal operation

#### Scenario: The exact command outlives the budget
- **WHEN** the budget is exhausted and an authorized actor issues the exact approval command
- **THEN** the gate decides normally, spending no model turn

#### Scenario: A fault on a spent budget does not invite a hopeless retry
- **WHEN** a classifier produces no reading and the run's budget is already spent
- **THEN** the fallback note names the exact commands and does not invite re-wording, because a re-worded message can no longer be classified
