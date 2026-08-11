## MODIFIED Requirements

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

## ADDED Requirements

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
