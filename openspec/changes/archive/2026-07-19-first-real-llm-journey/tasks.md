# Tasks: first-real-llm-journey

## 1. Content lane (intake wake + persona)

- [x] 1.1 Red-first pin in `internal/intake`: `CoordinatorTask` with a non-empty `Event.AuthoredText` embeds the bounded content in the prompt AND keeps the ref verbatim; empty content reproduces today's prompt; oversized content truncates at the rune budget with the `[content truncated]` marker (table-driven; fails before the implementation lands)
- [x] 1.2 Implement the wake-content threading in `intake.CoordinatorTask`/`coordinatorPrompt` (signature unchanged; content rides `Intake.Event.AuthoredText`)
- [x] 1.3 Persona addendum in `configs/personas/fragments/coordinator/10-decision-contract.md`: a `create_change` reason MUST preserve the concrete ask (what to change, where, the acceptance signal) — the author only sees the reason; keep the fragment's existing voice
- [x] 1.4 Offline ladder green: `task check` + the intake unit tests prove the lane without docker

## 2. Real-LLM journey variant (test/e2e)

- [x] 2.1 `test/e2e/realllm_journey_test.go` (build tag `e2e`): the `SEMDEV_REAL_LLM=1` gate (unset → `t.Skip` before any boot); gate set + `ANTHROPIC_API_KEY` unset/empty → immediate `t.Fatal` naming the variable (D4 posture — proves the fail-loud scenario by inspection + a unit-style gate test if expressible offline)
- [x] 2.2 `realLLMConfigPath` helper: copy the bootstrap config, rewrite rules_files absolute (reuse the journey pattern), REPLACE `model_registry` with the single `anthropic` endpoint (provider `openai`, url `https://api.anthropic.com/v1`, model `claude-opus-4-8`, `api_key_env: ANTHROPIC_API_KEY`, `tool_format: openai`, `supports_tools: true`, prices 5.00/25.00 per 1M, `max_output_tokens`, `request_timeout`) and point all three capabilities + defaults at it (the dead mock endpoint is REMOVED so any stale preference fails loud at registry validation)
- [x] 2.3 `startRealLLMRuntime`: resetNATS → boot.NewRuntime (PersonasDir + SandboxSourceDir as the mock journeys) → requireAgenticHealthy — no mockllm anywhere in the path
- [x] 2.4 The fixture issue body (G8-realistic, describes the inclusive-boundary bug as an issue author would, no prescribed diff) + `publishRealCoordinatorWake` passing it via `Intake.Event.AuthoredText` with model "anthropic"
- [x] 2.5 Outcome assertions with model-latency windows (design D5): decision → run minted → change authored (ANY non-empty slug + decodable document) → awaiting_approval → stood-in approval → task.spec + sandbox ready → measurement green within budget (1..5 attempts, retries tolerated) → review eventually approved (transient changes_requested tolerated) → verify pass → `delivery.pr.ref`; every window-expiry failure dumps the run's triples + newest loop terminals (evidence-bearing park diagnosis)
- [x] 2.6 Cost scrape (design D6): after the terminal assertion (or in the failure dump), log per-loop token/cost facts (predicate names read from `agvocab`) + their sum — the ledger's cost record source
- [x] 2.7 Prove the mock ladder is UNTOUCHED: full `task e2e` green (all 7 journeys, `-race`, uncached) with the new file compiled in and the gate unset (the skip path)

## 3. Run discipline (runbook + ledger surface)

- [x] 3.1 `docs/real-llm-runbook.md`: mock-ladder-first law, the config surface + why compat (design D1), the one-turn curl smoke probe, the launch command, the ACTIVE sidecar procedure (poll cadence, authoritative-state sources, filter sanity-check against real mock output, wallclock stall rule), pre-written abort criteria + cleanup, and the ledger-entry template (kind `real-llm`: status, cost record, sidecar record, hazards)
- [x] 3.2 Sidecar dry-run against a MOCK journey: run one mock journey with `-v` in the background and exercise the runbook's polling (output-file grep shapes + `nats kv ls/get` ENTITY_STATES) — prove the monitors observe phases/attempts BEFORE money is on the line; record the proven commands back into the runbook
- [x] 3.3 `docs/evidence-ledger.md`: reference the runbook as the M1 rung's procedure (NO entry authored — G7: entries only for runs that happened)

## 4. Verification + review

- [x] 4.1 Full offline ladder + `task e2e` green at the change's head (the evidence: unit pins from 1.1/2.1 + the 7 mock journeys)
- [x] 4.2 Adversarial review (go-reviewer + semstreams-reviewer) of the diff — standing directive; apply findings
- [x] 4.3 `openspec validate --strict` green for this change

## 5. The paid run itself (operator-gated — blocked on GEMINI_API_KEY in the launch shell)

- [x] 5.1 Operator puts `GEMINI_API_KEY` in the gitignored `.env` (`cp .env.example .env`; the Taskfile dotenv-loads it — plumbing proven with a dummy key reaching curl); `task realllm:probe` passes (one forced ping tool call against the real endpoint)
- [x] 5.2 First paid run: `task realllm:launch` (wraps the LOAD-BEARING `-count=1 -timeout 80m` — go test's default 10m would panic-kill the paid run mid-arc — plus the NATS reset and the log tee) with `task realllm:status` armed in a second shell and abort criteria in hand
- [x] 5.3 Ledger entry (kind `real-llm`) with cost + sidecar records; M1 rung claimed ONLY on that named evidence (G7)

## 6. Provider re-target: Gemini (operator constraint, post-review)

- [x] 6.1 Swap the journey to the framework's first-class Gemini route (provider `gemini` + `wire_backend: wire`, `GEMINI_API_KEY`, `gemini-3.1-pro-preview`, prices 2.00/12.00 per 1M) copying `configs/gemini-example.json` from the beta.153 module — the Anthropic-compat route stays documented as the verified alternative
- [x] 6.2 Operator lane in the Taskfile (house convention): `realllm:probe` / `realllm:launch` / `realllm:status`; runbook §2/§4/§5 reference the tasks
- [x] 6.3 Gates re-proven after the swap: vet + gate-path tests (skip / malformed / keyless) + `task check`
- [x] 6.4 Secrets lane: gitignored `.env` dotenv-loaded by the Taskfile (semspec-proven pattern; `.env.example` committed; `.gitignore` already covered `.env`/`!.env.example`; injection proven end-to-end with a dummy key — precondition passed and the key reached curl, zero cost)
