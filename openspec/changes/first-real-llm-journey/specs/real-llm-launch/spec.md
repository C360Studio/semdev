# Real-LLM Launch — new capability for first-real-llm-journey

## ADDED Requirements

### Requirement: Real-model configuration is honest about the wire it speaks

A real-LLM run SHALL configure the model registry against an endpoint the
pinned framework actually implements. On semstreams beta.153 the model-call
path speaks the OpenAI chat-completions wire only (no native Anthropic
adapter), so an Anthropic run SHALL use Anthropic's OpenAI-compatible endpoint
with `provider: "openai"`, `tool_format: "openai"`, and the API key resolved at
runtime from a named environment variable (`api_key_env`) — never a key in a
config file. The endpoint config SHALL carry the model's real per-1M-token
prices so the framework stamps true `agent.loop.cost-usd` facts (the ledger's
cost record derives from them, G3/G7: costs are harness-stamped, never
model-reported), and SHALL set an output-token ceiling and a request timeout.

#### Scenario: The registry resolves the key from the environment
- **WHEN** the real-LLM config is loaded with `api_key_env` naming a variable
- **THEN** the key is read from that variable at resolve time
- **AND** no API key appears in any config file or test source

#### Scenario: Cost facts are stamped from configured real prices
- **WHEN** a real-model loop completes under an endpoint carrying real per-1M prices
- **THEN** the loop's cost fact reflects those prices and the measured token counts

### Requirement: A declared real-LLM run fails loud, never silently skips

The real-LLM journey SHALL run only when explicitly declared via its
environment gate. When the gate is set but a launch precondition is missing
(the API key variable unset or empty), the journey SHALL FAIL naming the
missing precondition — declared intent never degrades into a skip. When the
gate is not set, the journey SHALL skip without contacting any endpoint.

#### Scenario: Gate set, key missing — loud failure
- **WHEN** the env gate declares a real-LLM run and the API key variable is unset
- **THEN** the journey fails immediately, naming the missing key variable
- **AND** no runtime boots and no request leaves the machine

#### Scenario: Gate unset — zero-cost skip
- **WHEN** the env gate is not set
- **THEN** the journey skips without booting the runtime or spending any token

### Requirement: The admitted ask reaches the authoring model

The arc SHALL convey the admitted issue's content to the model that authors
the change. The lane is: the wake prompt carries the actor-attributed authored
content (forge-io), and the coordinator's persona decision contract requires a
`create_change` routing reason to preserve the concrete ask — the authoring
loop starts fresh and its prompt threads only the decision reason, so the
reason is the sole content channel into authoring. No new vocabulary predicate
is introduced for this lane (G9).

#### Scenario: The persona contract binds the reason to the ask
- **WHEN** a coordinator routes to `create_change`
- **THEN** its persona decision contract instructs that the reason carry the concrete ask, not a generic classification

#### Scenario: The authoring prompt threads the preserved reason
- **WHEN** the authoring loop spawns on a `create_change` decision
- **THEN** its prompt contains the routing coordinator's decision reason verbatim via substitution

### Requirement: The real-LLM journey proves the arc on outcomes, not scripts

The real-LLM journey SHALL drive the same fixture arc as the bridge proof
through the real runtime with no mock harness, asserting station OUTCOMES with
model-latency-tolerant windows: a change is authored (any slug) and validates,
the human gate releases on the stood-in approval, the sandbox cold-proof and
projection complete, the developer's fix measures green in-container within the
authored attempt budget (bounded retry and review re-entry tolerated), the
clean-room verify passes, and delivery records `delivery.pr.ref`. A run that
parks SHALL fail the journey loudly, naming the parked state and the facts
that led there. The journey SHALL NOT assert mock-specific contracts (exact
request counts, scripted slugs, scripted diffs) and SHALL NOT backdoor-write
any completion fact (evidence-ledger law).

#### Scenario: A converging real run is the M1 rung's evidence
- **WHEN** the gated journey runs against the real endpoint and the arc converges to delivery
- **THEN** every station outcome above was observed in order
- **AND** the run's token and cost facts are read back for the ledger record

#### Scenario: A parked real run fails loud with evidence
- **WHEN** the arc parks toward the human instead of delivering
- **THEN** the journey fails naming the parked state and its route facts
- **AND** the failure output carries enough graph evidence to diagnose without re-running

### Requirement: Run discipline governs every paid run

A real-LLM run SHALL be preceded by a green mock ladder in the same session,
SHALL be actively monitored (authoritative-state polling on a 30–60s cadence
with wallclock stall comparison — silence is not success), SHALL have abort
criteria written down before launch and applied fast when a wedge is provable,
and SHALL produce an evidence-ledger entry of kind `real-llm` carrying the
cost record and the monitoring record before any milestone claim cites it
(G7). The runbook documents the procedure; the ledger template names the
required fields.

#### Scenario: No paid token before the mock ladder is green
- **WHEN** an operator prepares a real-LLM run
- **THEN** the full mock ladder (unit + e2e journeys) has passed in the same session first

#### Scenario: The run is recorded before it is claimed
- **WHEN** a real-LLM run completes (converged or aborted)
- **THEN** a ledger entry of kind `real-llm` records its status, cost, and monitoring evidence
- **AND** no M1 claim cites the run without that entry
