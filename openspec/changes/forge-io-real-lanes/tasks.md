# Tasks: forge-io-real-lanes

## 1. Ground truth first (cheap, settles D2/D3)

- [ ] 1.1 Verify the CURRENT framework webhook input's payload shapes against the module cache: which fields a comment event and a labeled event carry (issue number? actor? specific label?) — settles the D3 approval signal (comment command vs label) and refreshes the documented upstream gaps
- [ ] 1.2 Verify what the mint rule's spawn context exposes for D2 (`$entity` substitution reach for the wake's TaskID/ref) with a ruleload-level pin — settles the `run.issue.ref` stamping route (mint-rule `add_triple` vs loop-task thread)

## 2. Intake component (the front door goes live)

- [ ] 2.1 Red-first component pins: an admitted issue event → exactly one wake published (byte-shape = `intake.CoordinatorTask` + BaseMessage `PublishToStream`, the journey-proven contract); a rejected event → zero publishes, zero tokens, admission metric; a malformed payload → logged skip, never a crash
- [ ] 2.2 Implement the registered intake component (durable consumer on the GITHUB stream's issue subject → Normalize → Decide → publish); framework-alignment note + registry entry (G1)
- [ ] 2.3 `run.issue.ref` stamping per the settled D2 route — writer moves to its rule-pack name (vocab table updated per the recorded `deferred_issue_ref` plan); red-first pin proves the minted run carries the ref
- [ ] 2.4 Bootstrap config surface (subjects, repo binding, token env name via dotenv); boot wiring + census
- [ ] 2.5 e2e: a journey that publishes a RAW webhook payload onto the GITHUB stream (no CoordinatorTask call in the test!) and asserts the arc reaches the authored change — the intake component drove the front of the arc

## 3. Human gates on the issue

- [ ] 3.1 Approval adapter per settled D3: authorized actor's signal → `run.change.approved` (Source `approval-adapter`) on the run resolved via `run.issue.ref`; red-first pins: authorized releases the gate, unauthorized is ignored (Event invariant), replay is idempotent
- [ ] 3.2 e2e: approval journey drops the journey's stand-in write — the gate releases from the simulated signal event end-to-end
- [ ] 3.3 Park-message posting (D4): consume the park lane's `user.response.*` publish → issue comment via the GitHub client; bounded retries, posting failure never blocks the park; pin: a parked run's message reaches the double as a comment request
- [ ] 3.4 If (and only if) 1.1 shows neither comment nor label binds actor+issue: file the upstream semstreams ask with the payload evidence and keep the stand-in — record the decision in the design as as-built

## 4. Real PR delivery (discharges reshape group 8)

- [ ] 4.1 `internal/forge/github` client additions: branch push (token auth), PR create, query-PR-by-head-branch, with unit pins against recorded request/response shapes
- [ ] 4.2 Protocol-faithful local forge double for e2e (records real request shapes; asserts push → query → create ordering)
- [ ] 4.3 `openpr` speaks the adapter: push `semdev/<run-suffix>`, query-by-head-branch first (forge-level idempotency — the reshape's M2 caveat), create on absence with the evidence-summary body; `delivery.pr.ref` = the real PR URL
- [ ] 4.4 DELETE the `local-delivery:` stub path; no-forge config = loud tool error (fail closed); update the delivery journeys to the double; red-first: the replay journey proves one PR across replays at BOTH guards
- [ ] 4.5 Reshape bookkeeping: mark simplify-m0-execution-rail group 8 tasks discharged-by-this-change (as-built note, no silent duplication)

## 5. Verification + review + evidence

- [ ] 5.1 Full offline ladder + `task e2e` green (existing 7 journeys' contracts unchanged; new journeys green), `-race`, uncached
- [ ] 5.2 Adversarial review (go-reviewer + semstreams-reviewer) — standing directive; apply findings
- [ ] 5.3 `openspec validate --strict` green
- [ ] 5.4 Operator-gated: ONE recorded real-forge delivery against a disposable repo (operator picks the target per reshape 8.3); evidence-ledger entry — the M0-completion claim's outstanding requirement (G7); docs + CLAUDE.md status synced
