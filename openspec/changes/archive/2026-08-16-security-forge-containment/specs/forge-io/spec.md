# Forge IO — security-forge-containment delta

## ADDED Requirements

### Requirement: Delivery credentials are contained and pushes are bounded

Forge credential material SHALL NOT appear in the argv of any spawned process
or in any captured/logged output on the delivery path; the push SHALL supply
its credential through the environment (the same containment discipline the
clone path already enforces). Git invocations on the delivery path SHALL be
non-interactive: an absent or rejected credential SHALL fail fast with a
classified error, never block on a prompt. Every push SHALL be bounded by a
deadline; a push that exceeds it SHALL be terminated and its failure routed
through the existing retry/park lanes. Delivery retries SHALL preserve all of
the above — a retry attempt SHALL NOT weaken containment or bounding.

#### Scenario: The push carries no credential on argv
- **WHEN** delivery pushes to a token-authenticated remote
- **THEN** the spawned git command's argument vector contains no credential
  material
- **AND** captured stderr/stdout recorded for the run contains no credential
  material

#### Scenario: A missing credential fails fast instead of hanging
- **WHEN** delivery pushes and no usable credential is available
- **THEN** the push fails promptly with a classified error
- **AND** no interactive prompt blocks the run

#### Scenario: A stalled push cannot hang the run
- **WHEN** the remote stops responding during a push
- **THEN** the push is terminated at its deadline
- **AND** the failure routes through the existing retry/park lanes rather than
  wedging the station

#### Scenario: Retries do not re-expose the credential
- **WHEN** the station retries a failed delivery
- **THEN** every retry attempt satisfies the same argv/log containment and
  deadline bounds as the first
