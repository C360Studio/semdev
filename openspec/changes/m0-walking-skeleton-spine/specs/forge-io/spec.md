## ADDED Requirements

### Requirement: Host-agnostic issue intake

The system SHALL accept an issue from a configured code-host adapter and
normalize it into an issue fact that the arc consumes. The arc SHALL depend only
on the normalized fact, never on a specific host's API shape. GitHub is the v1
adapter.

#### Scenario: GitHub adapter intake creates a run
- **WHEN** an issue is presented by the GitHub v1 adapter
- **THEN** the adapter normalizes it and the arc creates a run carrying `run.issue_ref`

#### Scenario: The arc consumes normalized facts, not host payloads
- **WHEN** an arc rule reacts to an intaken issue
- **THEN** it references only the normalized issue fact
- **AND** it references no host-specific predicate or payload field

### Requirement: Host adapter is swappable behind the seam

The comms/host boundary SHALL be a channel-agnostic seam. Adding or replacing a
code-host adapter SHALL require no change to any arc rule or product component;
only the adapter is host-aware.

#### Scenario: A second adapter requires no arc change
- **WHEN** a second code-host adapter is introduced behind the seam
- **THEN** no arc rule and no product component is modified to accommodate it

### Requirement: Pull request delivery carries evidence

WHEN a run reaches the `open_pr` action, the configured adapter SHALL create a
pull request whose description carries the run's evidence summary (what was
verified, how, and where the full trajectory lives) and SHALL record `pr.ref`.

#### Scenario: open_pr creates an evidence-bearing PR
- **WHEN** the `open_pr` action fires for a verified run
- **THEN** the adapter creates a pull request whose body carries the evidence summary
- **AND** `pr.ref` is recorded for the run

### Requirement: Human communication rides the seam

Questions to humans SHALL be posted as issue/PR comments through the adapter, and
a human reply SHALL re-enter as a normalized `human.signal` fact keyed to the
run. The arc SHALL NOT contain a bespoke chat surface.

#### Scenario: ask_human posts a comment and resumes on reply
- **WHEN** the `ask_human` action fires for a parked run
- **THEN** the adapter posts the question as an issue/PR comment
- **AND** a human reply re-enters as a `human.signal` fact that lets a rule resume the run

### Requirement: Intake is gated to authorized, opted-in actors

The system SHALL create a run, spend budget, or steer an existing run only in
response to an event whose code-host actor (`intake.actor`) is authorized — a
**push-capable** repository collaborator (write, maintain, or admin permission) or
a member of an explicit allowlist — and whose work is explicitly opted in by a
`semdev` label or `/semdev` command applied by that same authorized actor. A
read- or triage-only viewer is not authorized to spend budget; the allowlist is
the explicit escape hatch for any actor the operator trusts regardless of repo
permission. Admission SHALL be a deterministic, zero-token check recorded
as `intake.admitted` that runs before any run is created and before any paid
token is spent; a rejected event creates no run and, by default, receives no
reply.

#### Scenario: Authorized, opted-in issue is admitted
- **WHEN** an issue is labeled `semdev` (or carries a `/semdev` command) by a push-capable repository collaborator or allowlisted actor
- **THEN** `intake.admitted` is recorded and a run is created

#### Scenario: Unauthorized actor is rejected at zero token cost
- **WHEN** an issue, comment, or pull-request event arrives from an actor who is neither a collaborator nor on the allowlist
- **THEN** the event is rejected deterministically before any run is created
- **AND** no paid token is spent and, by default, no reply is posted

#### Scenario: Authorized but not opted in does not start a run
- **WHEN** an authorized actor opens an issue without the `semdev` label or `/semdev` command
- **THEN** no run is created

#### Scenario: Only the authorizing requester steers a run's human gate
- **WHEN** a comment answering a run's `ask_human` question arrives from an actor other than that run's authorized requester
- **THEN** it is not routed to the run's human-response gate
