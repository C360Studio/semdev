## MODIFIED Requirements

### Requirement: Human communication rides the conversation channel

The system SHALL post messages to humans — a parked run's `run.awaiting.human`
notice and any `ask_human` question — to the run's thread through the port, and the
arc SHALL NOT contain a bespoke chat surface. The change-approval human gate SHALL be
operable from the thread by an **authorized actor** (a push-capable repository
collaborator or an allowlisted actor, per the shared admission gate) expressing an
intent — either the exact opt-in command (`<command> approve` / `<command> reject`) OR
NATURAL LANGUAGE the system classifies. An APPROVE intent SHALL land as
`run.change.approved` on the run through the approval adapter — the same fact, source,
and placement the resume rule already consumes. A REJECT intent SHALL land as
`run.change.rejected`, on which a rule fires the run's `awaiting_approval → cancelled`
transition (the transition stays rule-owned). An intent from an actor who is not
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
- **THEN** the approval adapter records `run.change.approved` deterministically, spending no model turn
- **AND** the existing resume rule advances the run with no journey stand-in write

#### Scenario: A natural-language approval releases the gate
- **WHEN** an authorized actor posts a non-command message the classifier reads as approval on a run awaiting approval
- **THEN** the system posts a transparency note naming the inferred approval and its author, then records `run.change.approved` through the approval adapter
- **AND** the existing resume rule advances the run

#### Scenario: A natural-language rejection cancels the run
- **WHEN** an authorized actor posts a message the classifier reads as rejection on a run awaiting approval
- **THEN** the system posts a transparency note naming the inferred rejection, records `run.change.rejected`, and a rule fires the `awaiting_approval → cancelled` transition
- **AND** no further classification fires for that run (it has left the gate)

#### Scenario: An unauthorized signal is ignored
- **WHEN** a signal arrives from an actor who is neither a push-capable collaborator nor allowlisted
- **THEN** no `run.change.approved` or `run.change.rejected` lands, no classification is triggered, and the run stays gated

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
