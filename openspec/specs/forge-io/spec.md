# Forge IO Specification

## Purpose

Defines the channel-agnostic code-host seam: issues enter as normalized facts
gated to authorized, opted-in actors; delivery opens a real, evidence-bearing
pull request idempotently; and human communication rides issue/PR comments
through a swappable adapter, with GitHub as the v1 implementation.
## Requirements
### Requirement: Host-agnostic issue intake

The system SHALL accept an issue from a configured code-host adapter and
normalize it into an issue fact that the arc consumes. The arc SHALL depend only
on the normalized fact, never on a specific host's API shape. GitHub is the v1
adapter.

The intake lane SHALL run as a registered runtime component: it consumes the
webhook input's issue events from the durable stream, normalizes them, applies
the admission gate (authorized, opted-in actors — zero model tokens for
rejects), and on admission publishes the coordinator wake through the same
front-door contract the journeys prove. The admitted issue's host-neutral ref
SHALL land on the run as `run.issue.ref` by its declared single writer once
the run exists; no product Go fires the run-creating transition itself.

The coordinator wake built from an admitted issue SHALL carry the issue's
actor-attributed authored content (the text the admission invariant binds to the
event's actor) alongside the host-neutral issue ref, bounded in length. The
issue ref SHALL remain present verbatim in the wake prompt. The intake adapter
is the only component that holds the host payload, so it is the only place the
content can enter the arc; an issue admitted with no attributable authored text
produces a wake that names the ref alone, exactly as before.

#### Scenario: GitHub adapter intake creates a run
- **WHEN** an issue is presented by the GitHub v1 adapter
- **THEN** the adapter normalizes it and the arc creates a run carrying `run.issue.ref`

#### Scenario: The arc consumes normalized facts, not host payloads
- **WHEN** an arc rule reacts to an intaken issue
- **THEN** it references only the normalized issue fact
- **AND** it references no host-specific predicate or payload field

#### Scenario: The wake carries the admitted issue's authored content
- **WHEN** an admitted issue with actor-attributed authored text is turned into a coordinator wake
- **THEN** the wake prompt contains that authored text, bounded in length
- **AND** the wake prompt still contains the host-neutral issue ref verbatim

#### Scenario: An issue without attributable content wakes on the ref alone
- **WHEN** an admitted event carries no actor-attributed authored text
- **THEN** the wake prompt names the issue ref and carries no fabricated content

#### Scenario: A live webhook event drives the arc without a test harness
- **WHEN** the running intake component receives an admitted issue event on the webhook lane
- **THEN** it publishes the coordinator wake with no journey or test code in the path
- **AND** a rejected event publishes no wake and spends zero model tokens

#### Scenario: The intake component is registered, not bespoke
- **WHEN** the runtime boots from the bootstrap config
- **THEN** the intake component is a registered component with a registry entry
- **AND** its framework-alignment note records why a component (not a rule alone) is required

### Requirement: Host adapter is swappable behind the seam

The comms/host boundary SHALL be a channel-agnostic seam. Adding or replacing a
code-host adapter SHALL require no change to any arc rule or product component;
only the adapter is host-aware.

#### Scenario: A second adapter requires no arc change
- **WHEN** a second code-host adapter is introduced behind the seam
- **THEN** no arc rule and no product component is modified to accommodate it

### Requirement: Pull request delivery carries evidence

WHEN a run reaches the `open_pr` action, the configured adapter SHALL create a
real pull request on the configured forge whose description carries the run's
evidence summary (what was verified, how, and where the full trajectory lives)
and SHALL record `delivery.pr.ref`. A local stand-in reference (e.g.
`local-delivery:<run>`) SHALL NOT satisfy this requirement, and the stand-in
code path SHALL NOT exist once this requirement is implemented. Delivery SHALL
be idempotent at BOTH layers: the graph-side read-before-create replay guard,
AND a forge-level guard (querying the forge for an existing pull request by
the run's head branch) so a concurrent double-fire cannot double-open. An
M0-completion claim SHALL require at least one recorded real-forge delivery in
the evidence ledger; protocol-faithful test doubles satisfy e2e journeys but
not the completion claim.

#### Scenario: open_pr creates an evidence-bearing PR
- **WHEN** the `open_pr` action fires for a verified run
- **THEN** the adapter creates a pull request whose body carries the evidence summary
- **AND** `delivery.pr.ref` is recorded for the run

#### Scenario: Replayed delivery does not double-open
- **WHEN** the `open_pr` action fires again for a run that already delivered
- **THEN** the adapter finds the existing pull request and records the same `delivery.pr.ref`
- **AND** no second pull request is created

#### Scenario: Concurrent delivery cannot double-open at the forge
- **WHEN** two deliveries race past the graph-side guard for the same run
- **THEN** the forge-level head-branch query resolves them to one pull request

#### Scenario: A local stub cannot claim delivery
- **WHEN** a delivery records a local stand-in reference instead of a forge pull request
- **THEN** the requirement is unmet and no M0-completion claim may cite the run

#### Scenario: Journeys speak the real protocol against a local double
- **WHEN** an e2e journey drives delivery
- **THEN** it exercises the real adapter request shapes against a protocol-faithful local forge double
- **AND** no journey depends on a live forge

### Requirement: Human communication rides the seam

Questions to humans SHALL be posted as issue/PR comments through the adapter, and
a human reply SHALL re-enter as a normalized `human.opt.signal` fact keyed to the
run. The arc SHALL NOT contain a bespoke chat surface.

The change-approval human gate SHALL be operable from the issue: an authorized
actor's approval signal (the v1 signal is settled in the design; it binds to
ITS actor per the admission Event invariant) SHALL land as `run.change.approved`
on the run through the approval adapter — the same fact, source, and placement
the resume rule already consumes. A parked run's `run.awaiting.human` message
SHALL reach the human as an issue comment through the adapter.

#### Scenario: ask_human posts a comment and resumes on reply
- **WHEN** the `ask_human` action fires for a parked run
- **THEN** the adapter posts the question as an issue/PR comment
- **AND** a human reply re-enters as a `human.opt.signal` fact that lets a rule resume the run

#### Scenario: An authorized approval on the issue releases the gate
- **WHEN** an authorized actor issues the approval signal on the admitted issue
- **THEN** the approval adapter records `run.change.approved` on the run
- **AND** the existing resume rule advances the run with no journey stand-in write

#### Scenario: An unauthorized approval signal is ignored
- **WHEN** an actor without authorization issues the approval signal
- **THEN** no `run.change.approved` lands and the run stays gated

#### Scenario: A parked run surfaces its message on the issue
- **WHEN** a run records `run.awaiting.human`
- **THEN** the adapter posts the park message as an issue comment naming what the human must decide

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
reply.

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

#### Scenario: Only the authorizing requester steers a run's human gate
- **WHEN** a comment answering a run's `ask_human` question arrives from an actor other than that run's authorized requester
- **THEN** it is not routed to the run's human-response gate

### Requirement: The forge provides the run's source at its coordinate

The code-host seam SHALL provide a read-only CLONE lane: given a run's host-neutral
coordinate (`owner/repo#N`), the configured forge adapter SHALL make that
repository's source available at its default branch, scoped to the configured forge
base URL and authenticated with the configured token. GitHub is the v1 adapter, and
the lane SHALL depend only on the host-neutral coordinate and the adapter seam, never
on a host-specific payload shape (the same swappability the intake and delivery lanes
hold).

The lane SHALL fail closed: an unknown or unreachable repository, an authentication
fault, or a clone failure SHALL surface as an error toward the operator (the run
parks), never a partial or guessed source.

The configured token SHALL NOT appear in any process argument the clone shells to
(no credential in a command line visible to the host's process listing); it SHALL be
supplied only through the subprocess environment.

#### Scenario: A coordinate resolves to a cloned source
- **WHEN** the clone lane is asked for the source of a run whose coordinate names an
  accessible repository
- **THEN** the adapter provides that repository's source at its default branch
- **AND** the source feeds the run's provisioning pipeline

#### Scenario: An unreachable or unauthorized repository fails closed
- **WHEN** the clone lane targets a repository that does not exist, is unreachable, or
  rejects the configured token
- **THEN** the lane returns an error toward the operator and the run parks
- **AND** no partial or guessed source is materialized

#### Scenario: The token never rides a process argument
- **WHEN** the clone lane authenticates to a private forge
- **THEN** the token is passed only through the subprocess environment
- **AND** it appears in no argument of any command the lane executes

### Requirement: The forge provides an issue's authored content on demand

The code-host seam SHALL provide a read-only ISSUE lane: given a run's host-neutral
coordinate (`owner/repo#N`), the configured forge adapter SHALL return that issue's
actor-attributed authored content (title and body). This is the content channel for a
front door that has no webhook payload to draw from (the operator launch driver): the
wake it composes SHALL carry the issue's real authored content, exactly as the webhook
front door carries the payload's content, so the arc never authors against an empty
ask. The lane SHALL depend only on the host-neutral coordinate and the adapter seam.

#### Scenario: The launch front door reads real issue content
- **WHEN** the operator launch driver builds a wake for a coordinate
- **THEN** it fetches that issue's authored content through the forge issue lane
- **AND** the wake carries that content, not an empty ask

#### Scenario: An unreadable issue fails closed
- **WHEN** the issue lane targets an issue that does not exist or is unreachable
- **THEN** the lane returns an error toward the operator and no run is launched
- **AND** no wake carrying fabricated or empty content is published

