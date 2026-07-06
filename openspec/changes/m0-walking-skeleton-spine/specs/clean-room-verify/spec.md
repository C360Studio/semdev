## ADDED Requirements

### Requirement: Clean-room verification is the terminal gate

No run SHALL reach `open_pr` until clean-room verification has recorded a passing
result (`verify.result`). Verification SHALL run in a fresh isolated environment
with a distinct build-cache home per run, resolve and build dependencies from the
artifact's own declarations, and run the artifact's own tests.

#### Scenario: PR blocked until verify passes
- **WHEN** `verify.result` is absent or failing for a run
- **THEN** the `open_pr` action does not fire

#### Scenario: Verification runs in fresh isolation
- **WHEN** clean-room verification runs for a run
- **THEN** it uses a distinct build-cache home isolated from any warm environment
- **AND** it resolves and builds from the artifact's own declarations

### Requirement: Cache-masked fabrication is rejected

Clean-room verification SHALL reject an artifact that would pass only because a
warm cache masked a missing or fabricated dependency or result.

#### Scenario: Cache-masked fabrication fixture fails verification
- **WHEN** a cache-masked fabrication fixture is verified in fresh isolation
- **THEN** `verify.result` records a failure and the run does not open a PR

### Requirement: Fail closed, retry transport errors

Verification SHALL fail closed on a genuine artifact failure. WHEN verification
cannot complete due to a transport or infrastructure error, it SHALL retry and
SHALL NOT record a terminal rejection; only a genuine artifact failure records a
terminal fail.

#### Scenario: Transient infrastructure error retries, does not reject
- **WHEN** verification cannot complete because of a transport/infrastructure error
- **THEN** it retries and records no terminal `verify.result` rejection

### Requirement: Verification result is harness-stamped with a single writer

`verify.result` SHALL be stamped by the verification harness, never supplied by a
model, and SHALL have exactly one writer in the writers table.

#### Scenario: verify.result maps to the verify harness only
- **WHEN** the writers-census conformance test runs
- **THEN** `verify.result` maps to exactly one writer — the verification harness
