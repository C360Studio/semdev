# Semsource-AB Specification

## Purpose

The semsource A/B instrument: an operator-declared per-run condition
(`baseline` | `semsource`) in which the ONLY variable is the developer's
available read tools — semsource's knowledge-graph read surface exposed as
thin proxy tools behind a fail-closed per-signal readiness gate — producing
harness-measured, condition-labeled evidence a human compares in the ledger.
semdev computes no winner.

## Requirements

### Requirement: The experiment condition is operator-declared and stamped once as an evidence label

The A/B condition (`baseline` | `semsource`) SHALL be declared by the operator
in boot configuration and stamped on the run at mint as the single fact
`experiment.run.condition` (writer `experiment-intake`, one writer — G5). The
fact SHALL be an evidence label only: no rule DOCUMENT in any pack —
conditions, actions, prompts, or substitution tokens — SHALL reference an
`experiment.*` field, so no deterministic channel can behave differently on
the label — only on the tools actually present.

#### Scenario: A run carries its condition from mint
- **WHEN** the operator launches a run under a declared condition
- **THEN** `experiment.run.condition` is stamped on the run entity at mint with that value

#### Scenario: The condition never routes and never substitutes
- **WHEN** the rule packs are linted
- **THEN** no rule document (conditions, actions, prompts, substitution tokens) references an `experiment.*` field

### Requirement: Semsource tools are read-only proxies over its public surface

The `semsource` condition SHALL expose exactly the semsource product-surface
read tools (`code_context`, `code_impact`, `code_search`, `doc_context`) as
thin proxy executors over semsource's HTTP read surface. Each proxy SHALL be
read-only: it stamps no facts (no G5 writer), its schema takes query
parameters only (no outcome parameters — G3), and its result returns to the
loop as tool content. NO semsource fact SHALL enter semdev's graph. The
proxies SHALL be registered UNCONDITIONALLY (schema-only with no live client
absent a configured endpoint, failing loudly if executed) so the schema
census covers them; a live client SHALL exist only when boot declares the
`semsource` condition, and ADVERTISEMENT SHALL occur only via the
`semsource` condition's variant dispatch pack.

#### Scenario: Baseline loops never see semsource tools
- **WHEN** the runtime boots without a declared `semsource` condition
- **THEN** no spawn advertises a semsource proxy tool
- **AND** no live semsource client is constructed

#### Scenario: Executing an unconfigured proxy fails loudly
- **WHEN** a semsource proxy executes with no live client configured
- **THEN** it returns an explicit tool error, never an empty success

#### Scenario: Proxy schemas accept no outcome parameters
- **WHEN** the G3 schema conformance census runs over the registered tools
- **THEN** every semsource proxy schema is present in the census and contains only query parameters

### Requirement: The semsource condition differs from baseline only in the developer's tool set

The `semsource` condition SHALL select a variant of exactly the
developer-spawning dispatch rules whose `tools` arrays append the semsource
read tools. Each variant rule MUST be byte-identical to its baseline sibling
except its `id`/`name` suffix and the `tools` array, and the tools delta MUST
be exactly the semsource read tools appended to the baseline set (extending,
never replacing). Prompts, conditions, budgets, and actions SHALL be
identical across conditions.

#### Scenario: Variant parity is machine-checked
- **WHEN** the variant-parity conformance pin compares each variant rule to its baseline sibling
- **THEN** any difference beyond the id/name suffix and the appended tools array fails the pin

#### Scenario: Baseline and variant packs never load together
- **WHEN** the rule set is loaded
- **THEN** it never contains both a rule and its `_semsource` variant sibling

#### Scenario: The developer's allowlist stays strict in both conditions
- **WHEN** a developer loop is dispatched in the `semsource` condition
- **THEN** its advertised tools are the baseline set plus exactly the semsource read tools, enforced per-loop at execution

### Requirement: Condition integrity is proven at mint and fails closed

In the `semsource` condition the front door SHALL probe semsource's status
surface BEFORE stamping `experiment.run.condition` and minting the run, and
SHALL gate on the PER-SIGNAL readiness semsource documents for the advertised
tools — structural index readiness AND retrieval/embedding readiness — not an
aggregate phase alone (an aggregate-ready gateway with cold retrieval returns
weak 200-OK results: silent degradation). A failed probe SHALL fail the
launch loudly — no run is minted and no half-labeled evidence exists. A
mid-run semsource fault SHALL surface as a loud tool error in the trajectory,
never a silent fallback: the run's tool set never mutates mid-run.

#### Scenario: An unreachable semsource blocks the launch
- **WHEN** the operator launches a `semsource`-condition run and the readiness probe fails
- **THEN** no run is minted and the failure is surfaced to the operator

#### Scenario: A mid-run outage is loud, not a fallback
- **WHEN** a semsource proxy call fails during a running loop
- **THEN** the loop receives an explicit tool error recorded in the trajectory
- **AND** the baseline tool set is not substituted mid-run
