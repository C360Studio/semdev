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
arc SHALL NOT contain a bespoke chat surface. The change-approval human gate SHALL be operable from
the thread: an approval signal from an **authorized actor** (a push-capable
repository collaborator or an allowlisted actor, per the shared admission gate) SHALL
land as `run.change.approved` on the run through the approval adapter — the same fact,
source, and placement the resume rule already consumes. A signal from an actor who is
not authorized SHALL NOT be routed to the run's human gate and SHALL leave the run
gated. (The human-reply lane — a reply re-entering as `human.opt.signal` to resume a
parked run — is a reserved forward contract, not delivered by this change; see the
design. This requirement covers the delivered lanes: posting and the approval gate.)

#### Scenario: A parked run surfaces its message on the thread
- **WHEN** a run records `run.awaiting.human`
- **THEN** the seam posts the park message to the run's thread through the port, naming what the human must decide

#### Scenario: An authorized approval on the thread releases the gate
- **WHEN** an authorized actor issues the approval signal on the run's thread
- **THEN** the approval adapter records `run.change.approved` on the run
- **AND** the existing resume rule advances the run with no journey stand-in write

#### Scenario: An unauthorized signal is ignored
- **WHEN** an approval signal arrives from an actor who is neither a push-capable collaborator nor allowlisted
- **THEN** no `run.change.approved` lands and the run stays gated

### Requirement: The approval gate is operable with no inbound webhook

The change-approval gate SHALL be releasable from the conversation thread on a
deployment that receives NO inbound webhook. The system SHALL poll each run awaiting
approval on a configured interval, reading its thread through the port's `Read` verb,
and SHALL release the gate for an authorized approval signal exactly as the webhook
path does — landing `run.change.approved` on the run through the approval adapter (the
same fact, source, and placement the resume rule consumes). The webhook transport
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
- **THEN** the poller reads it on the next interval and the approval adapter records `run.change.approved` on the run
- **AND** the resume rule advances the run with no webhook and no stand-in write

#### Scenario: An unauthorized polled approval is ignored
- **WHEN** a polled approval signal's author is neither a push-capable collaborator nor allowlisted
- **THEN** no `run.change.approved` lands and the run stays gated

#### Scenario: Re-reading an already-approved thread is a no-op
- **WHEN** the poller re-reads a thread whose approval already landed (e.g. after a restart rebuilt the cursor from the top)
- **THEN** no second `run.change.approved` write occurs and the run is unaffected

#### Scenario: Poll and webhook do not both drive the comment lane
- **WHEN** a deployment enables the poller
- **THEN** the webhook-fed comment consumer does not also run
- **AND** a single approval comment is processed exactly once
