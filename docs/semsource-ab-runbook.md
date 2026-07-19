# semsource A/B — operator runbook

How to run the semsource-condition arm (integrate-semsource-ab-harness). The
baseline arm needs nothing: an unconfigured `experiment` section (the shipped
default) runs the pre-change arc byte-for-byte.

## 1. Deploy semsource locally

From a semsource checkout (`~/Code/c360/semsource`), the UI-free core is
enough:

```bash
docker compose up -d
```

semsource's ServiceManager serves the read surface on `:8080` (status:
`GET /source-manifest/status`).

## 2. Index the target repo

Add the repo the A/B's runs will work on as a source (for the mock-ladder
plumbing journey that is the semdev fixture repo; for the first real A/B it is
the run's target repo):

```bash
# per semsource's own docs — add_source of the target checkout, e.g.
semsource add_source /path/to/target-repo
```

Then poll `GET /source-manifest/status` until BOTH per-signal gates are ready
— `index.ready` (structural: code_context/code_impact/doc_context) AND
`embedding.ready` (semantic: code_search). The aggregate `phase: "ready"`
alone is NOT sufficient (cold embeddings return weak 200-OK code_search
results — the exact degradation the per-signal launch gate exists to block).
First-pass embedding is CPU-heavy; raise `SEMEMBED_CPUS` on a real machine.

## 3. Declare the condition

In `configs/semdev-bootstrap.json`:

```json
"experiment": {
  "condition": "semsource",
  "semsource_endpoint": "http://localhost:8080"
}
```

Boot then (a) substitutes the `-semsource` variant dispatch pack (the three
developer-spawning rules gain exactly the four read proxies — parity-pinned),
(b) constructs the live proxy client, and (c) the launch path gates run
minting on the per-signal readiness probe — a cold semsource FAILS the launch
loudly; no run is minted.

NOTE (semsource port vs semdev): semdev's own service-manager registry runs on
18080 precisely so semsource can keep its native 8080 on the same machine.

## 4. Prove the plumbing (mock ladder, zero paid tokens)

```bash
task nats:reset
SEMDEV_SEMSOURCE_E2E_ENDPOINT=http://localhost:8080 \
  go test -race -tags=e2e -count=1 -run TestBridgeProofSemsourceConditionPlumbing ./test/e2e/...
```

Green = probe passed per-signal, variant pack loaded, condition stamped,
code_search round-tripped inside the developer loop, arc delivered. This is a
BRIDGE PROOF of the machinery — retrieval VALUE is unmeasurable against a
mock LLM.

## 5. The first real A/B (M1)

Serial runs per the v1 non-claims: pick the issue set, run each issue once per
condition (baseline first), and record each run's condition-labeled entry in
`docs/evidence-ledger.md` per its "Experiment conditions" rules — degraded
semsource runs are stated and ineligible as condition evidence; no aggregate
verdict is ever recorded. The comparison is a human reading the
condition-labeled entries and trajectories.
