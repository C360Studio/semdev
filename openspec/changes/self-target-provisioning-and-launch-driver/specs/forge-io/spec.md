## ADDED Requirements

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
