# Design: first-real-llm-journey

## Context

M0's arc is proven end-to-end under the mock (7 journeys green, `-race`,
uncached, this session). The user directive is the first real-LLM run (M1 easy
tier: the `go-health-class` fixture). Recon established, against the tree at
`c729386` and the semstreams beta.153 module cache:

- **No operator launch path**: the e2e journey is the only front-door
  publisher (sole caller of `intake.CoordinatorTask` + `intake.FrontDoorSubject`).
- **No issue-content lane**: the wake prompt carries only the ref
  (`coordinatortask.go`); `Intake.Event.AuthoredText` (actor-attributed under
  the sender==author guard, `normalize.go:86-89`) is consumed only by
  admission-signal detection; `run.issue.ref` is declared in vocab but
  deliberately unstamped (deferred to the M1 intake component,
  `01-issue-intake-mint-run.json` metadata). The authoring loop
  (`02-create-change-spawn.json`) is `tool_choice=function` with
  `[create_change]` only — it cannot read facts; its prompt threads exactly
  one dynamic value: `$entity.triple.coordinator.decision.reason`.
- **No native Anthropic adapter in the framework**: `AdapterFor` knows
  gemini/openai/ollama + a generic fallback; auth is `Authorization: Bearer`;
  both the go-openai client and the ADR-037 wire client speak OpenAI
  chat-completions. The registry VALIDATES `provider: "anthropic"` but nothing
  implements it — worse, with URL unset, go-openai would default to
  api.openai.com. (This corrects the prior session's memory.)
- The donor (semteams `create-change/01`, `04`) solves the content hand-off
  with the DECISION REASON as the sanctioned channel: "Your reason MUST carry
  … the original ask … — the new author starts fresh and only sees your
  reason."
- Rule 02 carries a DEFERRED note: the authoring loop inherits Sarah's
  ROUTING persona, which fights the forced `create_change` gate under a real
  model; the prescribed fix (author persona fragment / sub-role, donor-style
  multi-turn authoring) has real blast radius (role bindings, conformance
  pins, mock cursor semantics).

## Goals / Non-Goals

**Goals:**
- The minimal HONEST instrument for run 1: real model, same fixture arc,
  outcome assertions, real cost stamps, loud failures.
- The issue content reaches the authoring model via production-shaped lanes
  only (wake prompt + persona reason contract), with zero new vocabulary.
- Run discipline codified (runbook + ledger template) before any paid token.

**Non-Goals:**
- The donor-style multi-turn authoring station (role/tooling change) — a
  NAMED follow-up; run-1 evidence decides whether it is needed.
- The operator CLI driver minting via `experiment.Launch` — required before
  any REAL A/B condition run, not for run 1.
- Stamping `run.issue.ref`/issue-content facts — stays with the M1 intake
  component per the rule metadata.
- Any change to mock journeys' scripted contracts (RequestCounts, markers).

## Decisions

**D1 (RE-TARGETED to Gemini — operator constraint: Anthropic API rates are
unaffordable; Gemini is the paid provider in use).** Gemini is the
framework's FIRST-CLASS route: beta.153 ships a native `GeminiAdapter`
(provider `"gemini"`) over Google's OpenAI-compatible endpoint, and the
module's own `configs/gemini-example.json` + live Gemini test define the
authoritative shape this change copies verbatim: `provider: "gemini"` AND
`wire_backend: "wire"` (BOTH required for the Gemini 3.x preview
per-tool_call `thought_signature` contract, ADR-037 chunk 8),
`url: https://generativelanguage.googleapis.com/v1beta/openai`,
`api_key_env: GEMINI_API_KEY`, `tool_format: "openai"`, `stream: false`,
`reasoning_effort: "medium"`. Gemini 2.5-stable tiers can ride the plain
`provider: "openai"` umbrella instead (the example's `gemini-flash` entry —
the cheaper fallback if run costs must drop further). Original D1 finding
kept for the record: Anthropic has NO native adapter — an Anthropic run must
use its OpenAI-compat endpoint (`provider: "openai"`, url
`https://api.anthropic.com/v1`); `provider: "anthropic"` validates but
nothing implements it, and with no URL go-openai dials api.openai.com.

**D2 — Model `gemini-3.1-pro-preview`, all three roles, real prices
2.00/12.00 per 1M (≤200K-token prompts).** The framework example + live-test
default, so run 1 rides the exact endpoint+model the framework itself tests;
the preview slug and prices rotate together — confirm on run day. One model
for coordinator/developer/reviewer keeps run 1's variables minimal; the arc
is bounded (attempt budget clamped [1,5], iteration cap 8, transient grace
2), so worst-case spend is small (well under $1 expected). Per-role
cheaper-tier models (2.5/3 Flash) are a tuning follow-up, not a launch
precondition.

**D3 — Content lane = wake prompt + persona reason contract (donor pattern),
no new facts, no rule edits.** `CoordinatorTask` gains the bounded authored
text in the prompt body BELOW the ref line (the ref stays verbatim — every
mock fixture keys on it as a substring, so mock journeys are unaffected; the
added content is additive text the mock never reads). The coordinator persona
decision contract gains: when routing to `create_change`, the reason must
carry the concrete ask (what to change, where, and the acceptance signal), not
a classification. Rule 02's prompt already threads
`$entity.triple.coordinator.decision.reason`. Alternatives considered: (a)
stamping issue facts on the run + giving the author `query_entity` — needs a
`tool_choice` mode change (function→required), a scoped-tools change, a
persona fix (the DEFERRED note), and either a race-prone test-side stamp or
the M1 intake component; (b) threading content through rule prompts — engine
substitution is firing-entity-only, and no loop entity carries the content.
Both are the follow-up's shape, not run 1's.

**D4 — Bound the wake content.** `AuthoredText` is webhook-supplied and
unbounded; the wake prompt truncates it at a fixed rune budget (~4 KiB) with
an explicit `[content truncated]` marker so a pathological issue body cannot
blow the coordinator context. The journey's fixture issue body is small and
realistic (G8): it describes the health classifier's inclusive-boundary bug
in the terms an issue author would use, without prescribing the diff.

**D5 — The journey asserts outcomes, tolerates bounded non-determinism, and
fails loud on park.** Custom eventual-poll helpers (reusing `scanEntities` /
`tripleString`) with model-latency windows (implemented values): decision 4 min; change
authored 8 min; validated→awaiting_approval 3 min; dev re-wake decision 4
min; converge-to-delivery (dev loop + measure + floors + review + verify +
delivery, retries and re-entry included) 25 min — under a 75-minute journey
backstop that exceeds the window sum and stays below the launch command's
`go test -timeout 80m`, with every dump on a fresh evidence context so the
post-mortem survives backstop expiry. "Authored" asserts a non-empty
`openspec.change.slug` + a decodable document — no slug/intent equality.
Review tolerates transient `changes_requested` (re-entry is a legal arc).
Attempt count is asserted `1 ≤ n ≤ 5` (the projector clamp), never an exact
count. On any window expiring, the helper dumps the run entity's triples and
the newest loop entities' terminal facts before failing — the park diagnosis
must not require a re-run. The gate: `SEMDEV_REAL_LLM=1` runs, unset skips;
gate set + `ANTHROPIC_API_KEY` unset/empty = immediate `t.Fatal` naming the
variable (D4 posture of the semsource probe, applied to the launch gate).

**D6 — Cost scrape for the ledger.** After delivery (or on failure), the
journey scans loop entities bound to the run and logs per-loop
`agent.loop.tokens-in/-out` and `agent.loop.cost-usd` (whatever of those the
framework stamped) plus their sum — the ledger entry's cost record is copied
from this output, keeping G3 (harness-stamped costs only). The exact predicate
names are read from `agvocab` at implementation time, not hardcoded from
memory.

**D7 — The runbook is the sidecar.** `docs/real-llm-runbook.md` documents:
mock-ladder-green-first (same session), the config surface, launch command,
the ACTIVE monitoring procedure (poll the journey's verbose output file +
`nats kv` ENTITY_STATES on a 30–60s cadence; wallclock-compare the newest
fact timestamps; >2× expected step time with no forward progress = wedged),
the pre-written abort criteria (kill the test process, `task nats:reset`,
docker cleanup), and the ledger template (kind `real-llm`, status, cost
record, sidecar record, hazards observed). Filters are sanity-checked against
real mock-journey output before arming (the CLAUDE.md monitor law).

## Risks / Trade-offs

- **[Reason channel is model-dependent]** A weak reason starves the author →
  a low-quality change → validation fails or floors reject → bounded retries
  → park. Mitigation: the persona addendum is explicit about WHAT a
  create_change reason must carry; the park is a loud, evidence-bearing
  failure; the named follow-up (multi-turn author) is the structural fix if
  run 1 shows this failure mode.
- **[Authoring persona still fights the tool gate]** (rule 02's DEFERRED
  note, accepted for run 1). `tool_choice=function` structurally bounds the
  damage: the model can only call `create_change` or produce text (a
  no-tool-call turn under `function` terminates the loop → no change authored
  → the authored-window fails loud). Cost of the failure mode ≈ one model
  turn.
- **[Compat-endpoint drift]** Anthropic's OpenAI-compat layer is a
  compatibility surface; exotic fields could behave differently than
  first-party. Mitigation: semdev sends a plain tools+tool_choice
  chat-completions shape; the runbook's first step is a one-turn smoke probe
  (curl) against the endpoint with the configured model before the journey.
- **[Windows too tight/too loose]** First-run latency is unknown.
  Mitigation: windows err generous; the sidecar (not the test timeout) is the
  abort authority — a provably wedged run is killed by the operator, never
  left to grind.
- **[Fixture repo has no real issue]** The journey supplies the issue body
  inline (the intake adapter's stand-in, like `approveChange`). Trade-off
  accepted: run 1 measures the arc, not GitHub ingestion.

## Migration Plan

Additive only. The mock journeys pass a zero-content `Intake` — prompt
byte-shape for them changes only if `AuthoredText` is non-empty, which it is
not in existing tests. Rollback = revert; no persisted state, no vocab, no
rule changes.

## Open Questions

- None blocking. The paid-run execution itself waits on `ANTHROPIC_API_KEY`
  in the operator's environment (input only the operator can provide).
