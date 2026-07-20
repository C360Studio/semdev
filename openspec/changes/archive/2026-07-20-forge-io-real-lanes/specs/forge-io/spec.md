# Forge IO — delta for forge-io-real-lanes

## MODIFIED Requirements

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
