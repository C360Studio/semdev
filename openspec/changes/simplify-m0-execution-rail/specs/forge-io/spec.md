## MODIFIED Requirements

### Requirement: Pull request delivery carries evidence

WHEN a run reaches the `open_pr` action, the configured adapter SHALL create a
real pull request on the configured forge whose description carries the run's
evidence summary (what was verified, how, and where the full trajectory lives)
and SHALL record `delivery.pr.ref`. A local stand-in reference (e.g.
`local-delivery:<run>`) SHALL NOT satisfy this requirement. Delivery SHALL be
idempotent: a replayed or restarted delivery looks up the run's existing pull
request before creating, so no run double-opens. An M0-completion claim SHALL
require at least one recorded real-forge delivery in the evidence ledger;
protocol-faithful test doubles satisfy e2e journeys but not the completion
claim.

#### Scenario: open_pr creates an evidence-bearing PR
- **WHEN** the `open_pr` action fires for a verified run
- **THEN** the adapter creates a pull request whose body carries the evidence summary
- **AND** `delivery.pr.ref` is recorded for the run

#### Scenario: Replayed delivery does not double-open
- **WHEN** the `open_pr` action fires again for a run that already delivered
- **THEN** the adapter finds the existing pull request and records the same `delivery.pr.ref`
- **AND** no second pull request is created

#### Scenario: A local stub cannot claim delivery
- **WHEN** a delivery records a local stand-in reference instead of a forge pull request
- **THEN** the requirement is unmet and no M0-completion claim may cite the run
