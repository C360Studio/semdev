# Proposal: first-real-llm-journey

## Why

M0 is a mock-LLM bridge proof; M1's first rung needs a real-LLM run of the same fixture arc. Recon against the tree (c729386) found the launch surface does not exist and one content gap the mock papered over: nothing in production publishes the front-door wake with real model config, and the admitted issue's CONTENT never reaches any model — the wake prompt carries only the issue ref, `Intake.Event.AuthoredText` is used solely for admission-signal detection, and the authoring loop is `tool_choice=function` with `[create_change]` only (it cannot read facts). A real model would be asked to author a change for an issue it has never seen. Verified against semstreams beta.153: there is NO native Anthropic adapter (AdapterFor: gemini/openai/ollama/generic; Bearer auth; OpenAI wire only), so the honest config route is Anthropic's OpenAI-compatible endpoint. This change lands the minimal honest instrument for run 1 — content lane + config surface + env-gated journey + run discipline.

## What Changes

- `intake.CoordinatorTask` threads the admitted issue's actor-attributed authored content (`Intake.Event.AuthoredText`, already captured under the sender==author guard) into the front-door wake prompt, bounded in length. The issue ref stays present verbatim (mock journey markers key on it; RequestCount contracts unchanged).
- Coordinator persona decision-contract addendum (`configs/personas/fragments/coordinator/10-decision-contract.md`): a `create_change` routing reason MUST preserve the concrete ask — donor semteams pattern; the authoring loop starts fresh and only sees the reason (`$entity.triple.coordinator.decision.reason` is the sole content channel into rule 02's prompt).
- New env-gated real-LLM journey variant in `test/e2e`: gated on `SEMDEV_REAL_LLM=1`; a declared run with `GEMINI_API_KEY` unset FAILS loud (D4 posture: declared intent never silently skips). Boots the real runtime with `model_registry` patched to the framework's FIRST-CLASS Gemini route (operator constraint: Anthropic rates unaffordable) — provider `gemini` + `wire_backend: wire` over Google's OpenAI-compatible endpoint, model `gemini-3.1-pro-preview`, `api_key_env: GEMINI_API_KEY`, `tool_format: openai`, pricing 2.00/12.00 per 1M so `agent.loop.cost-usd` stamps real cost, `max_output_tokens`, `request_timeout` — the exact shape of the framework's own `configs/gemini-example.json` (the Anthropic-compat route stays documented as the verified alternative; `provider: "anthropic"` has NO implementing adapter in beta.153). Drives the SAME fixture arc with eventual/relaxed assertions: any change slug, bounded retry/rejection re-entry tolerated, generous windows, a park is a LOUD failure naming the parked state; scrapes loop token/cost facts for the ledger record.
- `docs/real-llm-runbook.md`: watch-sidecar procedure (active ENTITY_STATES polling, wallclock stall detection, abort criteria written BEFORE launch), the ledger-entry template (kind `real-llm`), and the mock-ladder-green-first law.

## Capabilities

### New Capabilities
- `real-llm-launch`: how a real-LLM run is configured, gated, driven, monitored, and recorded — the compat-endpoint model config with cost stamping, the fail-loud env gate, the ask-reaches-the-author content lane, run discipline (mock ladder first, sidecar, abort), and the ledger linkage.

### Modified Capabilities
- `forge-io`: the "Host-agnostic issue intake" requirement gains: the coordinator wake SHALL carry the admitted issue's actor-attributed authored content alongside the host-neutral ref (the intake adapter is the only component that holds the webhook body).

## Impact

- `internal/intake/coordinatortask.go` (+ unit tests): wake prompt gains bounded issue content; `CoordinatorTask` signature unchanged (content rides `Intake`).
- `configs/personas/fragments/coordinator/10-decision-contract.md`: reason-preservation addendum.
- `test/e2e/`: new `realllm_journey_test.go` (build tag `e2e`, env-gated); existing journeys pass an empty-content Intake — behavior unchanged.
- `docs/real-llm-runbook.md` (new), `docs/evidence-ledger.md` (template reference only; no entry until a run exists — G7).
- NO new vocabulary predicates (G9). NO rule-pack changes: the authoring station stays `tool_choice=function` single-turn; the donor-style multi-turn author + author persona fragment (rule 02's DEFERRED note) is a NAMED follow-up justified by run-1 evidence, partially mitigated here via the reason channel. NO operator CLI driver (the `experiment.Launch` path stays M1). Mock ladder green unchanged. Operator lane rides the Taskfile (house convention): `realllm:probe` / `realllm:launch` / `realllm:status`.
