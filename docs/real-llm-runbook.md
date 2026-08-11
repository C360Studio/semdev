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
2. **Key present**: `cp .env.example .env` and fill in `GEMINI_API_KEY` —
   the Taskfile loads `.env` for every task (`dotenv`, the semspec-proven
   pattern; a shell `export` still overrides the file). `.env` is gitignored;
   the journey reads the key via the registry's `api_key_env` — it never
   appears in a config file or commit. A declared run (`SEMDEV_REAL_LLM=1`)
   with the key missing FAILS immediately by design; do not "fix" that by
   weakening the gate. NOTE: bare `go test` invocations do NOT load `.env` —
   launch through `task realllm:launch`, or export the key yourself.
3. **Docker up**, NATS compose reachable (the journey resets it itself).
4. **Abort criteria written down** (§3) before launch, not improvised after.

## 1. The model config (Gemini — the framework's first-class route)

The paid provider is Gemini (operator constraint: Anthropic API rates are
unaffordable for this project). The journey
(`test/e2e/realllm_journey_test.go`, `realLLMConfigPath`) patches the
bootstrap config to a single endpoint copying the framework's OWN
`configs/gemini-example.json` shape:

```json
"gemini": {
  "provider": "gemini",
  "url": "https://generativelanguage.googleapis.com/v1beta/openai",
  "model": "gemini-3.1-pro-preview",
  "api_key_env": "GEMINI_API_KEY",
  "max_tokens": 1048576,
  "supports_tools": true,
  "tool_format": "openai",
  "stream": false,
  "reasoning_effort": "medium",
  "wire_backend": "wire",
  "max_output_tokens": 8192,
  "request_timeout": "300s",
  "input_price_per_1m_tokens": 2.00,
  "output_price_per_1m_tokens": 12.00
}
```

Why this exact shape (all framework-verified, beta.153): Gemini rides
Google's OpenAI-compatible endpoint; `provider: "gemini"` engages the native
`GeminiAdapter` and `wire_backend: "wire"` the framework-owned wire client —
BOTH are REQUIRED for the Gemini 3.x preview per-tool_call
`thought_signature` contract (ADR-037 chunk 8; the framework's live test
drives exactly this endpoint+model with tools). Gemini 2.5-stable models can
instead use the plain `provider: "openai"` umbrella (see the example config's
`gemini-flash` entry — the cheaper fallback tier if run costs need to drop
further). The PREVIEW SLUG ROTATES — update the model id and prices together
when Google publishes the stable id. The price fields make the framework's
`agent.loop.cost-usd` stamps real — they are the ledger's cost record (G3:
harness-stamped, never model-reported).

(Alternative providers, verified this change: Anthropic has NO native adapter
in beta.153 — an Anthropic run must use its OpenAI-compat endpoint
`https://api.anthropic.com/v1` with `provider: "openai"`; the registry
accepts `provider: "anthropic"` but nothing implements it, and with no URL
go-openai dials api.openai.com. Recorded so nobody configures it that way.)

## 2. Smoke probe before the journey (one cheap turn)

Prove auth + endpoint + model id + tool calling with a single bounded request
before booting anything:

```sh
task realllm:probe
```

(the task wraps one curl against
`https://generativelanguage.googleapis.com/v1beta/openai/chat/completions`
with a single forced `ping` tool call — the exact wire + auth the runtime
will speak.)

Expected: an assistant message whose `tool_calls` names `ping`. Anything else
(401, model not found, no tool_calls) — stop; the journey would fail the same
way for real money. Also confirm on run day that the configured prices
(2.00/12.00 per 1M for gemini-3.1-pro-preview, ≤200K-token prompts) still
match the published pricing — the preview slug AND its prices rotate; the
cost stamps inherit them, and token counts are the recomputable ground truth
if they drift.

## 3. Abort criteria (write these into the launch note)

Abort = kill the `go test` process, then `task nats:reset` and remove any
`semdev-*` containers left by the sandbox stations.

- **Wedge**: any single station shows no NEW fact on the run/loop entities for
  longer than 2× its expected step time (decision ≈ 1 min expected, authoring
  ≈ 2–3 min, dev loop turn ≈ 2–4 min incl. in-container measure, review ≈ 2
  min, verify ≈ 3 min) AND the journey has not failed on its own window.
- **Cost runaway**: summed `agent.loop.cost-usd` across run-bound loops
  exceeds $5 (run 1's arc should land WELL under $1 at gemini-3.1-pro
  prices), or
  more attempt/loop entities appear than the budget admits (> 5 attempts =
  the clamp failed — abort AND file the pin).
- **Thrash**: the same station fails → retries more than the transient grace
  cap (2) with a new model turn each time.
- The journey itself fails loud on a park (`run.awaiting.human`) and dumps
  the run's facts — let that happen; it is evidence, not a wedge.

## 4. Launch

```sh
task realllm:launch
```

(resets NATS, gates on `GEMINI_API_KEY`, runs the journey with the
LOAD-BEARING `-count=1 -timeout 80m`, and tees to `/tmp/realllm-run.log` for
the sidecar. Run it in one shell; the sidecar in another.)

The journey logs a `real-llm station:`/`real-llm milestone:` line as each
station lands, an `EVIDENCE` dump on any failure, and `LEDGER` lines (per-loop
tokens + cost and the sum) at the terminal.

## 5. The watch sidecar (active, not passive)

Run `task realllm:status` every 30–60s from a second shell (it bundles the
narration grep, an error-shape grep, and the authoritative run-entity
listing). Silence is not success — every filter below was sanity-checked
against a live mock journey BEFORE arming (a grep that matches nothing on a
healthy log is a broken filter, not a quiet system; `sidecar dry-run`,
first-real-llm-journey task 3.2). The raw shapes, for drill-down:

```sh
# (a) Station/milestone progress — the journey's own narration:
grep -E "real-llm (station|milestone|TERMINAL)|EVIDENCE|LEDGER|--- (PASS|FAIL|SKIP)" /tmp/realllm-run.log | tail -5

# (b) Authoritative graph state, independent of the log — via nats-box on the
#     compose network (the NATS image itself ships NO nats CLI; a docker exec
#     into it is a broken filter — dry-run-proven 2026-07-19):
docker run --rm --network semdev_default natsio/nats-box:latest \
  nats -s nats://nats:4222 kv ls ENTITY_STATES | grep chain.execution

docker run --rm --network semdev_default natsio/nats-box:latest \
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
- Command: task realllm:launch
- Model: gemini-3.1-pro-preview via generativelanguage.googleapis.com
  (provider gemini + wire backend, framework-native route), all roles
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

## 8. Live-target run (self-target, PULL-FIRST)

§1–7 drive the **fixture** arc (`SandboxSourceDir` → the committed
`go-health-class` tree). The live-target run instead develops a **real
repository** the run CLONES from its own coordinate (self-target
provisioning-and-launch-driver). This is the shape the M0-completion claim
requires (one recorded real-forge delivery — task 5.4 / 7.4). PULL-FIRST:
`semdev launch` is OUTBOUND, so the host needs NO inbound webhook reachability
and NO webhook secret.

**Prerequisite — SEED the target first.** An empty repo has no default branch to
clone and no issue to develop; `Materialize` fails closed with "no commits — seed
it first" (enforced in code, `runspace` group 2). The disposable target
(`semdev-test`) must carry: a buildable project, a declared
`.devcontainer/Dockerfile` (per the sandbox spec), and an authored issue.

**Config — select CLONE mode.** Add a `source.forge` block to the bootstrap
config the runtime boots (design D5; absent → the fixture default):

```jsonc
"source": {
  "forge": {
    "base_url": "https://github.com",
    "token_env": "GITHUB_TOKEN"   // empty ⇒ unauthenticated (public repos only)
  }
}
```

The config file carries only the forge source; the both-set fail-closed guard
(design D5) protects the internal `RunOptions` seam (exercised by the e2e tests),
so a config-file operator cannot trip it. `GITHUB_TOKEN` authenticates the clone,
the issue read, and the PR push; it rides the token ENV, never argv (D3).

**Run it** — two shells:

```sh
# shell 1: bring up the runtime (resets NATS, loads .env for GITHUB_TOKEN)
task serve

# shell 2: mint one run against the live issue (thin outbound client)
task launch REF=owner/semdev-test#<n> MODEL=<model_registry key>
# sidecar (repeat every 30–60s), same wallclock rule as §5:
task launch:status
```

`MODEL` is the running runtime's `model_registry` key — `mock` for a mock
runtime (a zero-token dry run of the launch seam), the gemini key for the paid
live run. The webhook door (if the host is reachable) remains an optional latency
accelerator; `semdev launch` needs neither it nor its secret.

`task serve` is a FRESH-STATE boot (its `nats:reset` dep wipes durable NATS):
restarting the daemon mid-run discards the in-flight run's state — the documented
restart-safety gap (design D2 review H3: `Provision` no-ops once `sandbox.ready`
is stamped, and no durable base fact exists yet). Do not restart `serve` during a
live run.

**Approval on a webhook-unreachable host — the POLL transport (pull-first-transport,
landed).** On a host no webhook can reach, the change-approval gate is now
releasable by POLLING the issue thread — no stand-in needed. Configure
conversation-channel in POLL mode and approve with a real `/semdev approve` comment
on the issue; the poller reads it on its interval and lands the exact
`run.change.decision` = `approve` fact (Source `approval-adapter`) the resume rule consumes. This
is the pull-first deployment shape:

```jsonc
"components": {
  "issue-intake":         { "config": { "http_port": 0 } },   // no webhook receiver (the default)
  "conversation-channel": { "config": {
    "poll": { "enabled": true, "interval": "15s" },            // the poll transport (default 15s, floor 5s)
    "token_env": "GITHUB_TOKEN",                               // required — the poller Reads the thread
    "repo": "owner/semdev-test", "allowlist": ["<you>"]        // MUST match issue-intake's (below)
  } }
}
```

Proven end-to-end by `TestBridgeProofApprovalByPollNoWebhook` (http_port 0 + poll on,
real docker, zero paid tokens). Notes:

- **Poll vs webhook is an XOR** (one inbound comment lane): poll mode SKIPS the
  webhook comment consumer. A webhook-reachable host instead leaves `http_port > 0`
  and `poll.enabled` off and approves with a real `/semdev approve` comment today
  (proven by `TestBridgeProofWebhookIssueToApprovedRun`). The webhook is the optional
  latency accelerator; poll is the portable default.
- **Boot coherence**: `http_port 0` AND `poll.enabled false` leaves the approval lane
  DEAD (a run parks at `awaiting_approval` forever) — boot LOUD-WARNS this combo. Set
  exactly one inbound path.
- **Paired admission-config invariant** (still holds — the carve split these knobs
  across two components): conversation-channel's `allowlist` + `repo` +
  `opt_in_command` MUST match issue-intake's, or an admitted actor's `/semdev approve`
  is rejected at the (separately-configured) approval gate and the run parks forever.
- Poll mode needs a forge token (to Read the thread); it still needs NO webhook secret.

A CLI stand-in write (the exact `run.change.decision` = `approve` fact) remains available for a
host that runs NEITHER transport, but is no longer the only non-webhook option.

**The NL-intent gate (nl-conversation-intent, landed).** The approval gate also reads
NATURAL LANGUAGE: an authorized collaborator writing "looks good, ship it" on the
thread classifies as an approval (one short model turn), posts a transparency note
naming the inferred decision and its author, and lands the same
`run.change.decision` fact through the same adapter. Operator facts:

- **Once the run is at the gate, the exact commands always work** and always win on
  cost: `/semdev approve` / `/semdev reject` are byte-identical fast-paths at ZERO
  model turns. (Pre-gate commands are definitively ignored — the phase guard — and
  pre-gate-open messages are watermark-discarded; see the next bullets.)
- **A rejection cancels a GATED run only** (`awaiting_approval → cancelled`,
  rule-owned, phase-guarded). **An NL approval is NOT reversible via NL**: once the
  run resumes, a later "wait, no" cancels nothing — the PR merge is the downstream
  human stop, by design.
- **NL classification is spend-bounded at 3 paid turns per run's gate** (counted at
  dispatch, faults included). On exhaustion semdev posts the exact-command escape
  hatch once and refuses further NL messages at zero cost; the exact commands keep
  working forever. A message written BEFORE the gate opened (a reused issue's old
  "ship it", a restart re-read) is definitively discarded — the gate-open watermark.
- **Marker-leak posture (known, accepted, PARTIALLY tripwired)**: a leaked
  serialization marker CLOSES THE NL LANE SILENTLY for the run — new messages stamp
  and die unclassified; the exact command always recovers. Detection depends on the
  leak shape. The FAILED-ACTION shapes (a failed publish after the marker stamp; a
  failed marker removal at release) bump `semstreams_rule_action_failures_total` —
  alert on the fully-qualified name; the bare suffix matches nothing in PromQL. The
  WEDGED-LOOP shape (a classifier that spawns successfully and never reaches a
  terminal) fails NO action and has NO metric today — its only symptom is human-side
  silence, which is exactly why the reconciliation exists. The marker-leak +
  exhaustion-residue reconciliation is a NAMED pre-production follow-up tracked in
  semdev#5 (the residue: after a spent budget plus an exact-command decision, the
  last refused message's `conversation.pending.*` facts stay on the run — harmless
  under the phase gates, forensically "in flight").

**What the offline journey does and does NOT cover** (honesty, ledger it): the
`TestBridgeProofSelfTargetForgeCloneToPR` mock journey proves the
clone→develop→diff→deliver MECHANICS against a local bare remote with zero paid
tokens — the delivered PR diff is the fix alone, history preserved. It does NOT
exercise the D3 no-argv token path (file:// transport never prompts for
credentials — only the `clone` unit pin and this live run do) nor a moved
server-side merge-base. Record the live run in the ledger per §6 (kind:
`live-forge`), including the delivered PR URL.
- A `(cached)` e2e result is not a green ladder. `-count=1`.
