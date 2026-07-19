## 0. PREREQUISITE — baseline `query_entity` allowlist fix (design fact 5; lands BEFORE any variant)

- [x] 0.1 Red-first conformance pin `TestSpawnPromptToolsAreAdvertised` (`test/conformance/rules_test.go`): every tool a spawn PROMPT instructs is in that spawn's `tools` allowlist (prompt⊆advertised) — verified RED on the unfixed pack (04/06a/06c/07b all instruct `query_entity`, none advertised). Matcher strips double-quoted literals first so the coordinator's `decide(action="ask_human")` TAXONOMY value is not misread as a tool call (no false positive); registered in the regression manifest
- [x] 0.2 Added `query_entity` to the four spawn allowlists (`04-dispatch-developer`, `06c-route-retry`, `07b-review-retry`, `06a-route-advance`) → pin green; the stale `TestDispatchDeveloperIsMultiTurnAndSelfExtinguishing` allowlist message updated (G10)
- [x] 0.3 Offline ladder + all 5 docker journeys stay green WITH `-race` (mock never calls `query_entity` — a no-regression bridge-proof check; PULLED FORWARD as a standalone commit ahead of the rest of this change, per the pre-real-LLM directive)

## 1. Vocabulary + condition stamp (G5/G9)

- [x] 1.1 Declare `experiment.run.condition` in `internal/vocab` (writer `experiment-intake`, capability `semsource-ab`, introduced-by this change) — canonical 3-seg under the `vocab.Register` panic-guard
- [x] 1.2 Front-door mint path (`internal/experiment`: `Launch` composes probe→publish→bind→stamp fail-closed; `StampCondition` writes via the OwnedFactWriter transport, writer `experiment-intake`): stamp `experiment.run.condition` on the run at mint from the boot-config condition (single writer `experiment-intake`); no condition configured → no stamp (baseline default, zero new facts)
- [x] 1.3 Unit pin (`TestLaunchStampsDeclaredCondition`): minted run carries the configured condition; unconfigured boot stamps nothing
- [x] 1.4 Conformance pin `TestNoRuleDocumentReferencesExperimentFields` (D3, WHOLE-DOCUMENT): no rule document in any pack — conditions, actions, prompts, substitution tokens — references an `experiment.*` field (a conditions-only lint would miss a prompt substitution)

## 2. Semsource proxy tools (D1 — always registered, conditionally advertised)

- [x] 2.1 Shared semsource HTTP client (`internal/forge/semsource` — routes verified against semsource's own workbench capability table) + per-signal readiness (`experiment.CheckReadiness`: `index.ready` AND `embedding.ready`, D4) (endpoint from boot config; the `internal/forge/github` client shape is the precedent) + readiness helper reading the status surface's PER-SIGNAL readiness (`index.ready` AND `embedding.ready` — NOT the aggregate `phase` alone, D4)
- [x] 2.2 Four read-only proxy executors (`internal/tools/semsourceproxy` — one executor, four schemas, single `query` parameter, results as content, no StopLoop (multi-turn), no fact writes) named exactly `code_context`, `code_impact`, `code_search`, `doc_context`; schemas take query parameters ONLY (no outcome fields — G3); results return as tool content; NO fact writes (no G5 writer)
- [x] 2.3 Register all four UNCONDITIONALLY in boot — schema-only nil client when no endpoint is configured, loud errResult if executed (the TRUE `github_list_comments` precedent: the G3 census boots with fixed empty deps and must see every schema); live client only when boot declares the `semsource` condition. Registry entries + framework-alignment note in `docs/alignment-notes.md` (G1: no framework MCP client — verified against the beta.150 module cache)
- [x] 2.4 Unit pins: proxy returns content on 200; upstream fault OR nil-client execution → loud errResult (never empty-success); G3 schema census covers the four (now genuinely — they are always registered)
- [x] 2.5 Pin (`TestBaselineAdvertisesZeroSemsourceTools` + boot's condition-gated client): baseline boot ADVERTISES zero semsource tools (no spawn's tools list contains one) and constructs zero live semsource clients

## 3. Variant dispatch pack + parity pin (D2)

- [x] 3.1 Variant copies of `04-dispatch-developer`, `06c-route-retry`, `07b-review-retry` with the four semsource tools APPENDED to each `tools` array (ids/names suffixed `_semsource`, the pack's underscore id style); boot-config key selects baseline or variant pack
- [x] 3.2 PARITY PIN `TestVariantParity` (red-verified against a deliberate prompt drift): each variant rule is byte-identical to its baseline sibling except the id/name suffix and the appended tools — any prompt/condition/budget/action drift fails; the tools delta is exactly the four semsource tools
- [x] 3.3 MUTUAL-EXCLUSION PIN `TestVariantMutualExclusion` (+ boot loads variants by SUBSTITUTION — `applyExperimentVariantPack`, unit-pinned incl. against the real repo bootstrap): the loaded rule set never contains BOTH a rule and its `_semsource` sibling (a double-load double-fires the developer spawn — the shared dispatched-marker guard is a race across two rules, and `publish_agent` is not idempotent)
- [x] 3.4 Offline rule-load gate (`test/ruleload/`) green with the variant pack loaded
- [x] 3.5 Reviewer isolation pin `TestReviewerDispatchIsNotVarianted` (06a not varianted + ONLY 04/06c/07b may have variants): Quinn's dispatch (`06a`) is NOT varianted — reviewer allowlist identical across conditions (dev-from-task delta scenario)

## 4. Fail-closed condition integrity (D4)

- [x] 4.1 `semsource`-condition launch (`TestLaunchSemsourceProbeFailsClosed`: probe fail → nothing published, no run bound, no stamp; nil probe = wiring fault): PER-SIGNAL readiness probe BEFORE stamping/minting; probe fail → loud launch failure, NO run minted (pin: no run entity, no `experiment.run.condition` fact after a failed probe)
- [x] 4.2 Pin (`TestProxyUpstreamFaultIsLoud` + `TestProxyNilClientFailsLoudly`): a mid-run proxy fault produces an explicit tool errResult in the loop (trajectory-visible), and the loop's advertised tool set is unchanged (no baseline substitution)

## 5. Evidence (G7)

- [x] 5.1 `docs/evidence-ledger.md`: condition column/prose per the evidence-ledger delta (condition-labeled entries; degraded `semsource` runs stated and ineligible as condition evidence; no aggregate verdict ever recorded)
- [x] 5.2 Mock-ladder journeys (docker): baseline condition — the FULL suite (all seven journeys) green `-race` under the unconfigured default (no experiment section → no swap, no live client); zero semsource advertisement pinned by `TestBaselineAdvertisesZeroSemsourceTools`
- [x] 5.3 Mock-ladder journey `TestBridgeProofSemsourceConditionPlumbing` (docker, gated on `SEMDEV_SEMSOURCE_E2E_ENDPOINT`): RAN GREEN 2026-07-19 against the LIVE local semsource compose (endpoint http://localhost:8080, both signals ready; env-gated so CI never re-runs it — re-run per the runbook to re-verify; the local stack was taken down after the green run — a post-review re-run attempt correctly FAILED the D4 probe loudly with connection-refused, a live demonstration of the fail-closed gate) (both signals ready) — per-signal probe passed, boot swapped the variant pack + live client, condition stamped, and the in-loop `code_search` TRAJECTORY STEP carries tool-status=success (the positive round-trip proof — delivery alone would not distinguish an absorbed errResult); labeled BRIDGE PROOF (retrieval value is unmeasurable against a mock LLM)
- [x] 5.4 Operator runbook (`docs/semsource-ab-runbook.md`): local semsource deployment + add_source + the per-signal wait + the plumbing-journey command; the first-real-A/B ledger recording rules (docs): local semsource deployment + `add_source` of the fixture repo for the first real A/B; recorded with that run's ledger entry (M1)

## 6. Ship

- [x] 6.1 Adversarial review (`semstreams-reviewer` + `go-reviewer`) — both APPROVE, zero blocking/high findings; all MEDIUM+LOW findings applied: g5 writer row for experiment-intake, hand-listed-variant rejection in EVERY condition (+ pin), per-swap + condition boot logging (partial-pack diagnosability), nil-client-outranks-args ordering asserted, oversized-body loud error, ledger "unmintable by construction" softened to the sanctioned-launch-path claim, `Launch` annotated as THE sanctioned M1 launch seam
- [x] 6.2 Docs (G10): `docs/architecture.md` vocab + tools tables, `docs/alignment-notes.md#semsource-read-proxy-tools`, `docs/semsource-ab-runbook.md`, the ledger condition rules
- [ ] 6.3 `openspec validate --strict` green; conventional commit; push

## 7. Follow-ups (recorded, NOT this change)

- [ ] 7.1 The M1 real-run driver (intake adapter / operator CLI — does not exist yet) MUST mint condition runs through `experiment.Launch` (the sanctioned fail-closed seam, annotated in its doc comment). Today `Launch` is unit-pinned only — the e2e plumbing journey hand-composes the same probe→publish→bind→stamp order against the shared front-of-arc helpers (a deliberate, annotated exemption, not a precedent)
- [ ] 7.2 `code_changes` (semsource's fifth read tool) stays OUT of the set — add only with a reason recorded in the ledger design (design open question)
