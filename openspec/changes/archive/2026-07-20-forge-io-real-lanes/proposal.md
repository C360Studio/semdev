# Proposal: forge-io-real-lanes

## Why

M1 is claimed; M2 (dogfood — semdev works its own GitHub issues) needs the forge seam to become real. Today NOTHING consumes a live webhook (the e2e journey is the only front-door publisher; `run.issue.ref` is deliberately unstamped, deferred to "the intake component" per the mint rule's metadata), delivery records the `local-delivery:` stub (`internal/tools/openpr`), and both human gates run on journey stand-in writes (approval) or nothing (ask_human posting). The admission logic, the issue-content wake lane (proven by the M1 run), and a token-authed GitHub client (`internal/forge/github`: permission checks, comments) all exist — this change wires them into the three real lanes and discharges the reshape's deferred group 8 (real forge delivery), which it subsumes.

## What Changes

- **Intake component** (registered runtime component, framework-alignment note + registry entry per G1): consumes the framework `github_webhook` input's `github.event.issue` subjects from the GITHUB stream → `intake.Normalize` → the existing admission `Decide` (authorized, opted-in actors only; zero tokens for rejects) → on admission, publishes the coordinator wake via `intake.CoordinatorTask` (the issue's authored content rides the wake — the M1-proven lane) and stamps `run.issue.ref`... — NOTE: the run does not exist at wake time (the mint rule creates it), so the ref/content facts the arc needs on the RUN are stamped per the mint rule's `deferred_issue_ref` metadata plan: the vocab writer for `run.issue.ref` moves to the spawn-rule/intake seam settled in the design (no new lifecycle writes from Go — G2).
- **Approval adapter**: an authorized actor's approval signal on the issue (comment command or label — design settles the v1 signal) → `run.change.approved` on the run (Source `approval-adapter`, exactly the fact the journeys stand-in write today; the resume rule is already live). Actor authorization reuses the admission Event invariant (the signal binds to ITS actor).
- **ask_human posting**: the park lane's `publish user.response.*` becomes a real comment on the issue via the GitHub client; the parked run's message (e.g. `run.awaiting.human`) reaches the human where they live.
- **Real PR delivery** (discharges reshape group 8): `open_pr` speaks the real forge API through the adapter seam (branch push + PR create with the evidence summary in the body), the `local-delivery:` stub path is DELETED, idempotency hardened at the forge level (query-existing-PR-by-head-branch on top of the existing read-before-create replay guard — the reshape's stated M2 caveat), and e2e drives a protocol-faithful local forge double that records real request shapes.
- Mock ladder stays green with zero live-GitHub dependence: the journeys speak to the local double; live-forge paths are env-gated like the real-LLM journey.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `forge-io`: "Host-agnostic issue intake" gains the REGISTERED intake component scenarios (webhook lane consumption, admission gating live, `run.issue.ref` stamped by its declared writer); "Pull request delivery carries evidence" gains the stub-deletion + forge-level idempotency scenarios; "Human communication rides the seam" gains the real approval-adapter and ask_human-comment scenarios.

## Impact

- New `internal/intake` component wiring (the component shell; Normalize/Decide/CoordinatorTask exist) + registry entry + framework-alignment note.
- `internal/forge/github`: PR create + branch push + query-by-head-branch + comment posting additions to the existing client.
- `internal/tools/openpr`: real adapter path replaces the stub; `internal/forge` gains the local double for e2e.
- `configs/`: intake component config surface (repo allowlist, token env via the dotenv lane — never a token in config), bootstrap registration.
- Vocab: `run.issue.ref` writer moves per its recorded deferral; any new predicate named in the design carries its single writer (G5/G9).
- e2e: new journeys against the local forge double; existing 7 journeys' contracts unchanged; a recorded real-forge run (disposable repo — operator decision) lands in the evidence ledger before any M2 claim (G7).
- NON-goals: restart reconstruction (reshape 7.1–7.3 stays deferred), comment-driven steering beyond approval + park-response (the respond flow's upstream flattened-payload gaps stand as documented), multi-repo generality, the operator CLI driver (separate change).
