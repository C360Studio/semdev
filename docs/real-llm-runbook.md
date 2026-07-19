# Real-LLM run runbook (M1 easy tier)

The operator procedure for semdev's paid runs: the first real-LLM journey and
every one after it. Law, not guidance — a bug that reaches a paid run is a
process failure (CLAUDE.md), and a paid run without this discipline produces
evidence the ledger cannot accept (G7).

## 0. Preconditions (no exceptions)

1. **Mock ladder green FIRST, same session.** `task check` and an UNCACHED
   `task e2e` (a cached `ok` is not a run — use `go test -race -tags=e2e
   -count=1 ./test/e2e/...` after `task nats:reset` if in doubt). All seven
   bridge-proof journeys pass before the first paid token.
2. **Key present**: `export ANTHROPIC_API_KEY=...` in the launching shell. The
   journey reads it via the registry's `api_key_env` — it never appears in a
   config file. A declared run (`SEMDEV_REAL_LLM=1`) with the key missing
   FAILS immediately by design; do not "fix" that by weakening the gate.
3. **Docker up**, NATS compose reachable (the journey resets it itself).
4. **Abort criteria written down** (§3) before launch, not improvised after.

## 1. The model config (and why it looks like OpenAI)

The journey (`test/e2e/realllm_journey_test.go`, `realLLMConfigPath`) patches
the bootstrap config to a single endpoint:

```json
"anthropic": {
  "provider": "openai",
  "url": "https://api.anthropic.com/v1",
  "model": "claude-opus-4-8",
  "supports_tools": true,
  "tool_format": "openai",
  "api_key_env": "ANTHROPIC_API_KEY",
  "max_output_tokens": 8192,
  "request_timeout": "300s",
  "input_price_per_1m_tokens": 5.00,
  "output_price_per_1m_tokens": 25.00
}
```

`provider: "openai"` is deliberate and verified: semstreams (beta.153) has NO
native Anthropic adapter — its model-call path speaks the OpenAI
chat-completions wire with Bearer auth (`AdapterFor`: gemini/openai/ollama +
generic fallback). Anthropic's OpenAI-compatible endpoint at
`https://api.anthropic.com/v1` accepts exactly that. The registry VALIDATES
`provider: "anthropic"` but nothing implements it; with no URL, go-openai
would default to api.openai.com. Do not switch this to `provider:
"anthropic"` until a native adapter lands upstream. The price fields make the
framework's `agent.loop.cost-usd` stamps real — they are the ledger's cost
record (G3: harness-stamped, never model-reported).

## 2. Smoke probe before the journey (one cheap turn)

Prove auth + endpoint + model id + tool calling with a single bounded request
before booting anything:

```sh
curl -sS https://api.anthropic.com/v1/chat/completions \
  -H "Authorization: Bearer $ANTHROPIC_API_KEY" \
  -H "content-type: application/json" \
  -d '{
    "model": "claude-opus-4-8",
    "max_tokens": 64,
    "messages": [{"role": "user", "content": "Call the ping tool."}],
    "tools": [{"type": "function", "function": {"name": "ping", "description": "reply check", "parameters": {"type": "object", "properties": {}}}}],
    "tool_choice": "required"
  }' | head -c 2000; echo
```

Expected: an assistant message whose `tool_calls` names `ping`. Anything else
(401, model not found, no tool_calls) — stop; the journey would fail the same
way for real money. Also confirm on run day that the configured prices
(5.00/25.00 per 1M for claude-opus-4-8) still match the published pricing —
the cost stamps inherit them, and token counts are the recomputable ground
truth if they drift.

## 3. Abort criteria (write these into the launch note)

Abort = kill the `go test` process, then `task nats:reset` and remove any
`semdev-*` containers left by the sandbox stations.

- **Wedge**: any single station shows no NEW fact on the run/loop entities for
  longer than 2× its expected step time (decision ≈ 1 min expected, authoring
  ≈ 2–3 min, dev loop turn ≈ 2–4 min incl. in-container measure, review ≈ 2
  min, verify ≈ 3 min) AND the journey has not failed on its own window.
- **Cost runaway**: summed `agent.loop.cost-usd` across run-bound loops
  exceeds $10 (run 1's arc should land well under $5 at opus-4-8 prices), or
  more attempt/loop entities appear than the budget admits (> 5 attempts =
  the clamp failed — abort AND file the pin).
- **Thrash**: the same station fails → retries more than the transient grace
  cap (2) with a new model turn each time.
- The journey itself fails loud on a park (`run.awaiting.human`) and dumps
  the run's facts — let that happen; it is evidence, not a wedge.

## 4. Launch

```sh
task nats:reset
SEMDEV_REAL_LLM=1 go test -tags=e2e -count=1 -timeout 80m \
  -run 'TestRealLLMJourneyIssueToPR$' -v ./test/e2e/ \
  > /tmp/realllm-run.log 2>&1 &
```

The journey logs a `real-llm station:`/`real-llm milestone:` line as each
station lands, an `EVIDENCE` dump on any failure, and `LEDGER` lines (per-loop
tokens + cost and the sum) at the terminal.

## 5. The watch sidecar (active, not passive)

Poll every 30–60s from a second shell. Silence is not success — sanity-check
every filter against the mock dry-run output BEFORE arming it (a grep that
matches nothing on a healthy log is a broken filter, not a quiet system).
These shapes were proven against a live mock journey (`sidecar dry-run`,
first-real-llm-journey task 3.2):

```sh
# (a) Station/milestone progress — the journey's own narration:
grep -E "real-llm (station|milestone|TERMINAL)|EVIDENCE|LEDGER|--- (PASS|FAIL|SKIP)" /tmp/realllm-run.log | tail -5

# (b) Authoritative graph state, independent of the log — via nats-box on the
#     compose network (the NATS image itself ships NO nats CLI; a docker exec
#     into it is a broken filter — dry-run-proven 2026-07-19):
docker run --rm --network compose_default natsio/nats-box:latest \
  nats -s nats://nats:4222 kv ls ENTITY_STATES | grep chain.execution

docker run --rm --network compose_default natsio/nats-box:latest \
  nats -s nats://nats:4222 kv get ENTITY_STATES <run-entity-key> --raw \
  | python3 -c "import json,sys; e=json.load(sys.stdin); ts=sorted((t.get('timestamp',''),t.get('predicate'),str(t.get('object'))[:60]) for t in e.get('triples',[])); [print(*x) for x in ts[-8:]]"

# (c) Wallclock check: compare (b)'s newest triple timestamps to now; anything
#     "in flight" with no new fact for >2× the station's expected step time is
#     wedged — apply §3, do not wait for the 75m backstop.
```

If the nats-box image cannot be pulled (offline), fall back to the log (a)
plus the test's own 2s-cadence polling — but then WIDEN (a) to include error
shapes: `grep -iE "error|failed|panic" /tmp/realllm-run.log | tail -5`.

## 6. After the run — the ledger entry (before any claim)

Copy the journey's `LEDGER` lines into `docs/evidence-ledger.md` as a new
entry:

```markdown
### <date> — first real-LLM journey (M1 easy tier)   [kind: real-llm]

- Status: converged | parked(<message>) | aborted(<criterion>)
- Command: SEMDEV_REAL_LLM=1 go test -tags=e2e -run TestRealLLMJourneyIssueToPR ...
- Model: claude-opus-4-8 via api.anthropic.com/v1 (OpenAI-compat), all roles
- Cost record (harness-stamped): <the LEDGER lines — per-loop tokens-in/out,
  cost-usd, and the sum>
- Attempts: <n> of budget <B>; transient retries: <n>
- Sidecar record: cadence, what was polled, anomalies observed, abort? why/not
- Hazards observed: <e.g. #508 teardown deadlock (benign), 8080-class flakes>
```

G7: the M1 rung is claimed ONLY on a recorded entry. A parked or aborted run
gets an entry too — honest failures are evidence.

## 7. Known hazards (scar tissue)

- Teardown may log the known semstreams #508 ComponentManager deadlock —
  bounded and benign.
- The 8080 infra flake is fixed (service-manager pinned to 18080). If a
  front-of-arc wedge recurs WITHOUT an "address already in use" error, the
  consumer-not-active storm was independent — reopen
  adopt-reason-aware-escalate task 7.4b before trusting further paid runs.
- A `(cached)` e2e result is not a green ladder. `-count=1`.
