## MODIFIED Requirements

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

## ADDED Requirements

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
