# Forge IO — delta for first-real-llm-journey

## MODIFIED Requirements

### Requirement: Host-agnostic issue intake

The system SHALL accept an issue from a configured code-host adapter and
normalize it into an issue fact that the arc consumes. The arc SHALL depend only
on the normalized fact, never on a specific host's API shape. GitHub is the v1
adapter.

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
