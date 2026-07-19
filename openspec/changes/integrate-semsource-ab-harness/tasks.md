## 0. PREREQUISITE — baseline `query_entity` allowlist fix (design fact 5; lands BEFORE any variant)

- [ ] 0.1 Red-first conformance pin: every tool a spawn PROMPT names is in that spawn's `tools` allowlist (prompt⊆advertised) — MUST FAIL on today's pack (all four spawn prompts instruct `query_entity`; none advertises it; post-#551 a real-LLM attempt opens with a rejected `not_advertised` call)
- [ ] 0.2 Add `query_entity` to the four spawn allowlists (`04-dispatch-developer`, `06c-route-retry`, `07b-review-retry`, `06a-route-advance`) → pin green; rule descriptions/metadata updated (G10)
- [ ] 0.3 Offline ladder + all four docker journeys stay green (mock never calls `query_entity` — this is a no-regression check, labeled bridge proof)

## 1. Vocabulary + condition stamp (G5/G9)

- [ ] 1.1 Declare `experiment.run.condition` in `internal/vocab` (writer `experiment-intake`, capability `semsource-ab`, introduced-by this change) — canonical 3-seg under the `vocab.Register` panic-guard
- [ ] 1.2 Front-door mint path: stamp `experiment.run.condition` on the run at mint from the boot-config condition (single writer `experiment-intake`); no condition configured → no stamp (baseline default, zero new facts)
- [ ] 1.3 Unit pin: minted run carries the configured condition; unconfigured boot stamps nothing
- [ ] 1.4 Conformance pin (D3, WHOLE-DOCUMENT): no rule document in any pack — conditions, actions, prompts, substitution tokens — references an `experiment.*` field (a conditions-only lint would miss a prompt substitution)

## 2. Semsource proxy tools (D1 — always registered, conditionally advertised)

- [ ] 2.1 Shared semsource HTTP client (endpoint from boot config; the `internal/forge/github` client shape is the precedent) + readiness helper reading the status surface's PER-SIGNAL readiness (`index.ready` AND `embedding.ready` — NOT the aggregate `phase` alone, D4)
- [ ] 2.2 Four read-only proxy executors named exactly `code_context`, `code_impact`, `code_search`, `doc_context`; schemas take query parameters ONLY (no outcome fields — G3); results return as tool content; NO fact writes (no G5 writer)
- [ ] 2.3 Register all four UNCONDITIONALLY in boot — schema-only nil client when no endpoint is configured, loud errResult if executed (the TRUE `github_list_comments` precedent: the G3 census boots with fixed empty deps and must see every schema); live client only when boot declares the `semsource` condition. Registry entries + framework-alignment note in `docs/alignment-notes.md` (G1: no framework MCP client — verified against the beta.150 module cache)
- [ ] 2.4 Unit pins: proxy returns content on 200; upstream fault OR nil-client execution → loud errResult (never empty-success); G3 schema census covers the four (now genuinely — they are always registered)
- [ ] 2.5 Pin: baseline boot ADVERTISES zero semsource tools (no spawn's tools list contains one) and constructs zero live semsource clients

## 3. Variant dispatch pack + parity pin (D2)

- [ ] 3.1 Variant copies of `04-dispatch-developer`, `06c-route-retry`, `07b-review-retry` with the four semsource tools APPENDED to each `tools` array (ids/names suffixed `_semsource`, the pack's underscore id style); boot-config key selects baseline or variant pack
- [ ] 3.2 PARITY PIN (conformance, the load-bearing one): each variant rule is byte-identical to its baseline sibling except the id/name suffix and the appended tools — any prompt/condition/budget/action drift fails; the tools delta is exactly the four semsource tools
- [ ] 3.3 MUTUAL-EXCLUSION PIN: the loaded rule set never contains BOTH a rule and its `_semsource` sibling (a double-load double-fires the developer spawn — the shared dispatched-marker guard is a race across two rules, and `publish_agent` is not idempotent)
- [ ] 3.4 Offline rule-load gate (`test/ruleload/`) green with the variant pack loaded
- [ ] 3.5 Reviewer isolation pin: Quinn's dispatch (`06a`) is NOT varianted — reviewer allowlist identical across conditions (dev-from-task delta scenario)

## 4. Fail-closed condition integrity (D4)

- [ ] 4.1 `semsource`-condition launch: PER-SIGNAL readiness probe BEFORE stamping/minting; probe fail → loud launch failure, NO run minted (pin: no run entity, no `experiment.run.condition` fact after a failed probe)
- [ ] 4.2 Pin: a mid-run proxy fault produces an explicit tool errResult in the loop (trajectory-visible), and the loop's advertised tool set is unchanged (no baseline substitution)

## 5. Evidence (G7)

- [ ] 5.1 `docs/evidence-ledger.md`: condition column/prose per the evidence-ledger delta (condition-labeled entries; degraded `semsource` runs stated and ineligible as condition evidence; no aggregate verdict ever recorded)
- [ ] 5.2 Mock-ladder journey (docker): baseline condition — arc unchanged but for the group-0 prerequisite, all four bridge-proof journeys green, zero semsource advertisement (assert via the 2.5 pin surface)
- [ ] 5.3 Mock-ladder journey (docker, gated on a local semsource compose): `semsource`-condition plumbing — per-signal probe passes, condition stamped, developer loop advertises baseline+4, a proxy call round-trips; labeled BRIDGE PROOF (retrieval value is unmeasurable against a mock LLM)
- [ ] 5.4 Operator runbook note (docs): local semsource deployment + `add_source` of the fixture repo for the first real A/B; recorded with that run's ledger entry (M1)

## 6. Ship

- [ ] 6.1 Adversarial review (`semstreams-reviewer` + `go-reviewer` on the Go) — both pass
- [ ] 6.2 Docs (G10): `docs/architecture.md` vocab table gains `experiment.run.condition`; tools table gains the four proxies with their always-registered/conditionally-advertised gate
- [ ] 6.3 `openspec validate --strict` green; conventional commit; push
