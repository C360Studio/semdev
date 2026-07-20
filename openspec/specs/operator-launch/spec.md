# operator-launch Specification

## Purpose
TBD - created by archiving change self-target-provisioning-and-launch-driver. Update Purpose after archive.
## Requirements
### Requirement: An operator launches a run through the sanctioned mint seam

The system SHALL provide an operator command that launches a run against a named
target by minting through the single sanctioned launch seam
(`probe → publish → bind → stamp`); no other production path SHALL stamp a run's
experiment condition. The command SHALL:

1. fetch the target issue's authored content from the forge and compose the
   front-door coordinator wake in the SAME shape the webhook intake produces
   (host-neutral issue ref carried verbatim AND the issue's real authored content),
   then publish it to the front-door subject; a bare ref with no content SHALL NOT be
   published (the arc never authors against an empty ask);
2. for a declared semsource condition, run the per-signal readiness probe FIRST and
   abort the launch — minting no run — if the probe fails, so no half-labeled
   evidence exists;
3. bind the run THIS launch minted by OBSERVATION of the rule-stamped `run.issue.ref`
   (an exact ref match; when the ref is ambiguous because another front door already
   woke it, bind the run minted after this launch's publish, never a pre-existing
   run), then stamp the operator-declared condition as the single evidence label; an
   unconfigured (baseline) launch stamps no condition.

The command is the durable production front door beside the webhook intake; the e2e
journey's hand-composed mint is a pinned test exemption, not a production publisher.

#### Scenario: Launching a target mints a run and reports it
- **WHEN** the operator launches against a target coordinate with no declared condition
- **THEN** the wake is published carrying the target issue's authored content, the
  coordinator-minted run is bound by observation, and the run's entity id is reported
- **AND** no experiment condition is stamped

#### Scenario: A launch whose issue content cannot be read is refused
- **WHEN** the target issue's authored content cannot be fetched from the forge
- **THEN** the launch fails loud and publishes no wake
- **AND** no run is minted against an empty ask

#### Scenario: An unready semsource condition aborts before minting
- **WHEN** the operator launches under the semsource condition and the readiness probe
  fails
- **THEN** the launch aborts with a loud error naming the unready signal
- **AND** no wake is published and no run is minted

#### Scenario: A declared condition is stamped on the bound run
- **WHEN** the operator launches under a declared condition and a run binds within the
  window
- **THEN** the condition is stamped once as the run's evidence label
- **AND** the label is read by the ledger and never routes or selects tools

### Requirement: The launch driver fires no lifecycle transition from Go

The launch command SHALL NOT create the run entity itself nor fire any lifecycle
transition (G2): the run is minted downstream by the coordinator rule's
`run_scope=new` spawn, and the command only publishes the wake and OBSERVES the
result. Binding SHALL be a read of the graph, never a write that advances the run.
When no run binds within the operator-set window, the command SHALL fail loud without
stamping any fact, never invent or advance a run.

#### Scenario: Binding is observation, not a lifecycle write
- **WHEN** the command binds the minted run
- **THEN** it resolves the run by reading its rule-stamped `run.issue.ref`
- **AND** it writes no fact that advances the run's lifecycle

#### Scenario: An unbound run fails loud without a partial label
- **WHEN** no run carries the launched coordinate within the bind window
- **THEN** the command fails loud (the mint did not complete)
- **AND** no condition or other fact is stamped for the coordinate

