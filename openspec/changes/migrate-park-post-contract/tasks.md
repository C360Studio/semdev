# Tasks — migrate-park-post-contract

## 1. Contract pins, red first

- [x] 1.1 Add a two-config conformance pin for the USER stream capture, exact
      rule output, exact conversation-channel input, and raw v1 interface
- [x] 1.2 Add the behavior-discovered exact-subject/no-dual-publish census:
      every phase that authors `run.awaiting.human` through add, update, or
      non-empty reconcile must publish exactly once in that same phase
- [x] 1.3 Add decoder pins for missing/wrong subject, timestamp, source, and
      noncanonical entity ID; record the pre-fix failures

## 2. Fresh-state cutover

- [x] 2.1 Add `semdev.park-post.request` to both USER stream declarations and
      bump shipped config versions while preserving mock > live ordering
- [x] 2.2 Declare the rule's exact required JetStream output and the
      conversation-channel's matching required durable input, including
      `semdev.park_post_request`/`v1`
- [x] 2.3 Migrate all nine park rules with no bridge, alias, or dual publish
- [x] 2.4 Replace the broad park decoder/dispatch with exact-subject routing and
      strict raw-contract validation; keep the user-note lane separate

## 3. Truth and evidence

- [x] 3.1 Update `docs/port-manifest.md`, `docs/alignment-notes.md`, component
      commentary, and OpenSpec conversation-channel truth
- [x] 3.2 Extend the real station-failure journey with a forge barrier and named
      durable-consumer proof: ACK-pending before post, ACKed after post
- [x] 3.3 Run focused unit/race/conformance and the real park-post E2E proof on
      fresh NATS; record exact results in `docs/evidence-ledger.md`

## 4. Lockstep adoption and closeout

> **HELD (measured 2026-08-25).** 4.1's stated precondition is *met*: the tag
> carrying the `user.response.>` reservation (gh#952 / ADR-093,
> `33e02fe8 feat(agentic)!: reserve typed user response subjects`) is
> **v1.0.0-beta.161**, and SemDev pins beta.160. The hold is now a *different*
> gate — the operator decision of 2026-08-23 to bump only on a settled tag,
> because upstream `main` is +113 commits past beta.161 mid a repo-wide
> context-ownership refactor that is the real migration cost. Bumping to
> beta.161 today buys the ADR-093 lockstep and pays the context-ownership
> migration twice. Re-measure `git tag --list 'v1.0.0*'` in the semstreams
> checkout before treating this group as blocked.

- [ ] 4.1 After the breaking SemStreams tag containing ADR-093 exists, update
      `go.mod`/`go.sum` once (never before) and run the full clean-room gates
- [ ] 4.2 Adversarial review the complete SemDev diff and fold all findings
- [ ] 4.3 `openspec validate --all --strict`, sync the conversation-channel
      delta, and archive only after the upstream tag + SemDev PR land lockstep
