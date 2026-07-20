## MODIFIED Requirements

### Requirement: Intake is gated to authorized, opted-in actors

The system SHALL create a run, spend budget, or steer an existing run only in
response to an event whose code-host actor (`intake.actor.login`) is authorized — a
**push-capable** repository collaborator (write, maintain, or admin permission) or
a member of an explicit allowlist — and whose work is explicitly opted in by a
`semdev` label or `/semdev` command applied by that same authorized actor. A
read- or triage-only viewer is not authorized to spend budget; the allowlist is
the explicit escape hatch for any actor the operator trusts regardless of repo
permission. Admission SHALL be a deterministic, zero-token check recorded
as `intake.actor.admitted` that runs before any run is created and before any paid
token is spent; a rejected event creates no run and, by default, receives no
reply. The SAME authorization gate governs steering an existing run's human gate,
enforced at the conversation-channel seam (see the `conversation-channel`
capability) — this capability owns issue/PR intake and budget admission.

#### Scenario: Authorized, opted-in issue is admitted
- **WHEN** an issue is labeled `semdev` (or carries a `/semdev` command) by a push-capable repository collaborator or allowlisted actor
- **THEN** `intake.actor.admitted` is recorded and a run is created

#### Scenario: Unauthorized actor is rejected at zero token cost
- **WHEN** an issue, comment, or pull-request event arrives from an actor who is neither a collaborator nor on the allowlist
- **THEN** the event is rejected deterministically before any run is created
- **AND** no paid token is spent and, by default, no reply is posted

#### Scenario: Authorized but not opted in does not start a run
- **WHEN** an authorized actor opens an issue without the `semdev` label or `/semdev` command
- **THEN** no run is created

## REMOVED Requirements

### Requirement: Human communication rides the seam

**Reason**: The conversation half of the arc is decoupled into its own
`conversation-channel` capability behind the `ConversationChannel` port, so a
non-GitHub channel can carry the chat without touching the arc. `forge-io` narrows
to code-host concerns only (issue intake, PR delivery, source clone, issue content).

**Migration**: The delivered behavior — the change-approval gate is operable from
the thread and lands `run.change.approved` through the approval adapter, and a
parked run's `run.awaiting.human` message reaches the human — is now specified by
the `conversation-channel` capability's "Human communication rides the conversation
channel" requirement, through the port's `Post` + `ResolveThread` verbs. (The
human-reply lane — `ask_human` posting a question and a reply re-entering as
`human.opt.signal` to resume a parked run — is a RESERVED forward contract in the
new capability, not delivered by this carve.) The facts (`human.opt.signal`,
`run.change.approved`) and the resume rule are unchanged; `human.opt.signal`'s
writer moves from `comment-adapter` to the channel-neutral `conversation-adapter`
(capability `conversation-channel`).
