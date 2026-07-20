## ADDED Requirements

### Requirement: A channel-agnostic conversation seam

The system SHALL post and read human messages through a `ConversationChannel`
port whose thread handle (`ThreadRef`) is opaque to the arc: only the channel
implementation resolves it. The port SHALL expose exactly three verbs — post a
message to a thread, read the messages after a cursor, and resolve a work
coordinate to its thread. The arc SHALL depend only on the normalized message
facts the seam produces, never on a channel's payload shape. GitHub issue/PR
comments is the v1 implementation; for GitHub a thread handle is the work
coordinate itself (`ResolveThread` is identity — comments live on the work object).

Adding or replacing a conversation channel SHALL require no change to any arc
rule or product component; only the channel implementation is channel-aware.

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

### Requirement: The message-fact family has a single writer

The normalized inbound message fact `human.opt.signal` SHALL be written by the
`conversation-channel` component and no other writer; channel implementations
(GitHub v1, others later) are internal to that component, not fact writers. This
preserves one-writer-per-fact (G5) across any number of channels. The poll
cursor `conversation.thread.cursor` SHALL likewise be written only by the
`conversation-channel` component.

#### Scenario: One writer regardless of channel
- **WHEN** any channel implementation ingests a human message
- **THEN** the resulting `human.opt.signal` fact carries the single writer `conversation-channel`
- **AND** no channel implementation writes the fact under its own source

### Requirement: Human communication rides the conversation channel

Questions to humans SHALL be posted to the run's thread through the port, and a
human reply SHALL re-enter as a normalized `human.opt.signal` fact keyed to the
run. The arc SHALL NOT contain a bespoke chat surface. The change-approval human
gate SHALL be operable from the thread: an authorized actor's approval signal
SHALL land as `run.change.approved` on the run through the approval adapter — the
same fact, source, and placement the resume rule already consumes. A parked run's
`run.awaiting.human` message SHALL reach the human through the port. A message
steering a run SHALL be authorized by the same admission gate as intake: a signal
from an actor other than the run's authorized requester SHALL NOT be routed to the
run's human gate.

#### Scenario: ask_human posts a message and resumes on reply
- **WHEN** the `ask_human` action fires for a parked run
- **THEN** the seam posts the question to the run's thread through the port
- **AND** a human reply re-enters as a `human.opt.signal` fact that lets a rule resume the run

#### Scenario: An authorized approval on the thread releases the gate
- **WHEN** an authorized actor issues the approval signal on the run's thread
- **THEN** the approval adapter records `run.change.approved` on the run
- **AND** the existing resume rule advances the run with no journey stand-in write

#### Scenario: An unauthorized signal is ignored
- **WHEN** a signal arrives from an actor who is not the run's authorized requester
- **THEN** no `run.change.approved` lands, the message is not routed to the human gate, and the run stays gated

#### Scenario: A parked run surfaces its message on the thread
- **WHEN** a run records `run.awaiting.human`
- **THEN** the seam posts the park message to the run's thread naming what the human must decide

### Requirement: Messages are read pull-first

Reading a thread SHALL default to polling the channel on an interval and returning
only the messages after a per-thread cursor (`conversation.thread.cursor`),
deduplicated by message id, so a re-poll re-acts on nothing already seen. A webhook
receiver MAY be configured as a latency accelerator, but the poll transport and the
webhook receiver SHALL be mutually exclusive (config-selected, never both at once),
so no cross-transport double-delivery can occur. semdev SHALL NOT require inbound
webhook reachability to operate.

#### Scenario: A signal delivered by poll drives the gate with no webhook
- **WHEN** an authorized approval message is posted to the thread and the runtime is configured poll-only (no webhook receiver)
- **THEN** the poller reads the new message after the thread cursor and the approval adapter releases the change gate

#### Scenario: A re-poll does not re-act on a seen message
- **WHEN** the poller reads a thread whose new messages it has already processed
- **THEN** the cursor advances past them and no fact is re-written (dedup by message id)

#### Scenario: Poll and webhook are mutually exclusive
- **WHEN** the runtime is configured with both a poll transport and a webhook receiver for the same channel
- **THEN** boot fails closed rather than run two producers of the same messages
