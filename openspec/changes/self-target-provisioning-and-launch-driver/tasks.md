# Tasks: self-target-provisioning-and-launch-driver

Discipline (project law): red-first pin with every behavior (G6); the fixture
path stays byte-identical (the guard); adversarial review (go + semstreams
reviewers) before each group commit; no paid token — every task here is provable
OFFLINE or against a LOCAL bare git remote (the forge-io bare-remote pattern,
symmetric). The ONE paid live-forge delivery stays operator-gated (forge-io 5.4).

## 1. cliexec env passthrough (design D3 prerequisite; discharges forge-io follow-up #2)

- [x] 1.1 RED: pin that a command reads a value from the subprocess environment
  (`TestOSRunnerRunWithEnvExposesEnvToSubprocess`) — the value rides ENV, not argv — plus an
  `EnvRunner` conformance pin. Failed compile before the extension existed (RED confirmed).
- [x] 1.2 Extend `cliexec` with an env-aware call as an OPTIONAL interface + type-assertion
  (`EnvRunner.RunWithEnv(ctx, dir, env, name, args...)`, review L3) so existing fakes stay untouched;
  `OSRunner.RunWithEnv` sets `cmd.Env = append(os.Environ(), env...)` via a shared `runCmd` body.
  `Run` stays byte-identical (nil env → nil cmd.Env → inherits os.Environ). GREEN; build + vet clean.
- [x] 1.3 MOOT by the L3 optional-interface decision: existing suite fakes are UNCHANGED (they
  implement `Runner`, not `EnvRunner`); the clone lane's recording `EnvRunner` fake that asserts
  token-in-env-not-argv is built in group 3.4.

## 2. Recorded diff base + history-preserving materialize (design D2 — the crux)

- [x] 2.1 RED: `TestMaterializeClonePreservesHistoryAndDiffsOnlyTheAttempt` — a git source with
  history must materialize (history preserved) and `Diff` return ONLY the attempt. Failed today at
  Materialize ("nothing to commit" on the git-init-fresh over a committed copy). RED confirmed.
- [x] 2.2 `Materialize` records the base as an in-repo ref `refs/semdev/base` via `git update-ref`
  (NOT a tag — review nit; NOT an in-memory map — review H3), so `Diff` reads `refs/semdev/base..HEAD`
  statelessly. Two prepare strategies by whether the source contains `.git`: no-`.git` (fixture) →
  `initCommit` (init + `configHarnessIdentity` + baseline commit) + `recordBase`, base = that commit
  (UNCHANGED); has-`.git` (clone) → `cloneCheckout`: `git clone --no-hardlinks <src> <dest>` (NOT
  `copyTree` over `.git` — review M1) + `configHarnessIdentity` (clone copies no identity — review H2)
  + `recordBase`, base = the cloned tip. GREEN.
- [x] 2.3 GUARD: full `runspace` suite + the whole `internal/...` unit layer green UNCHANGED (read_diff,
  measurement, floors, verify, coldproof all consume the checkout/Diff) — the fixture base ref == the
  old root, so byte-identical. Regression gate passed.
- [x] 2.4 `TestCloneForVerifyOverCloneSourceCarriesCommittedAttemptOnly`: a clone-based checkout's
  cold-verify clone carries the committed attempt + the target's files and STILL excludes an
  uncommitted warm-tree residue (immutable-snapshot guarantee holds on the clone path); the diff test
  pins `apply_patch`-atop-real-history + `refs/semdev/base` → fix-alone. (Restart out of scope — 2.6.)
- [x] 2.5 RESOLVED as MOOT: no local working branch is needed — delivery pushes the recorded attempt
  sha to its OWN remote head (`delivery.BranchPrefix + runSuffix(runEntityID)`, delivery.go:110/129),
  so the local branch name is irrelevant to the PR head. The run stays on the cloned default branch.
  (No `git checkout -b`; the design open question is closed.)
- [x] 2.7 EMPTY-TARGET fail-closed (go-reviewer B1): an empty git source (`.git`, zero commits — the
  live `semdev-test`'s current state) clones with no HEAD; `cloneCheckout` detects the unborn HEAD
  (`rev-parse -q --verify HEAD`) and fails closed with an operator-facing "no commits — seed it first"
  cause (not the opaque `HEAD: not a valid SHA1`), leaving no half-materialized checkout. RED-first pin
  `TestMaterializeEmptyGitSourceFailsClosedWithActionableError` + a `sourceHasGit` fixture-invariant pin.
  Enforces the [[semdev-test-target-repo]] must-seed precondition in code.
- [x] 2.6 HONESTY (review H3): documented in the `baseRef` doc comment + design D2 — the base ref +
  attempt commits live in the checkout, so a lost checkout dir parks on restart exactly as today; this
  change does NOT claim the sandbox spec's "reconstruct at the recorded commit" scenario (already unmet
  pre-change: `Provision` no-ops once `sandbox.ready` is stamped; no durable base fact in `vocab.go`).
  The durable-base-fact restart-safety gap stays a NAMED follow-up (a future G9 addition), NOT smuggled
  into this change's no-new-predicate pledge.

## 3. Forge-clone Sources implementation (design D1; forge-io clone lane)

- [x] 3.1 Happy-path pin `TestResolveClonesTargetFromCoordinate`: `Resolve` reads `run.issue.ref`,
  parses `owner/repo#N`, clones `<base>/owner/repo.git` into a fresh per-run dir — proven against a
  LOCAL bare git remote seeded with history (`seedBareRemote` file:// double). NOTE: for this new
  package the pins were authored ALONGSIDE the impl (all green first run), not strict red-then-green.
- [x] 3.2 Implemented the forge-clone `Sources` (`internal/forge/clone`, G1 framework-alignment note in
  the package doc — no new primitive, satisfies the EXISTING `provisionsandbox.Sources` seam): graph
  reader for the coordinate, LOCAL `parseOwnerRepo` (kept out of `intake` to avoid a low→high forge-layer
  dependency), full clone via `cliexec` with a GIT_ASKPASS env-token (D3). Boot/DI wiring is group 4.
- [x] 3.3 FAIL-CLOSED pins `TestResolveFailsClosed` (no coordinate / unparseable ref / reader fault) +
  `TestResolveUnknownRepoFailsClosed` (missing remote → clone fails). None returns a guessed dir; a
  failed clone reaps its half-materialized dir.
- [x] 3.4 NO-ARGV-LEAK pin `TestResolveTokenRidesEnvNotArgv`: with a token, it rides `SEMDEV_FORGE_TOKEN`
  in the subprocess env via GIT_ASKPASS, appears in NO argv, the URL carries only the non-secret
  `x-access-token` username, and the runner MUST be an `EnvRunner` (fail-closed, never an argv fallback).
- [x] 3.5 Coordinate → clone-URL mapping pin `TestResolveCoordinateToURLMapping` (hyphen repo, dotted
  org; `.git` suffix; base-URL scoping via `net/url`).

## 4. Boot source-mode selection (design D5 — fail-closed on ambiguity)

- [x] 4.1 Pin `TestSourceSpecFailsClosedOnAmbiguity`: `sourceSpec` errors loudly when BOTH a fixture
  dir and a forge source are set, or a forge source has no base URL (design D5). NOTE: neither set →
  fixture mode with an empty dir → runtime park (SB5, the pre-change default), NOT a boot error — keeps
  the existing empty-`SandboxSourceDir` integration boots working.
- [x] 4.2 Wired the mode: `provision.SourceSpec{FixtureDir | *clone.Config}` threaded through
  `RegisterAll` → `provision.Register` → `newProcessor`, which builds Sources via `buildSources`
  (fixture → `runspace.StaticSource`; forge → `clone.NewSource` reusing the station's fact reader).
  `provisionsandbox`, the `Sources`/`Materialize` interfaces, and the sandbox stages UNCHANGED. Pins
  `TestBuildSourcesSelectsMode` + `TestSourceSpecSelection`.
- [x] 4.3 GUARD: full `internal/...` unit layer + `test/conformance` (G1 census updated to
  `provision.SourceSpec{}`) green UNCHANGED — the fixture default is the pre-change behavior.
- [x] 4.4 `LoadForgeSourceConfig` reads the config file's `source.forge` block (base_url/token_env; the
  experiment.LoadConfig second-read pattern) — present → parsed, absent → nil, malformed/missing → loud
  error. `cmd/semdev` wires it into `RunOptions.ForgeSource` at the composition edge (parity-scan-safe).
  Pin `TestLoadForgeSourceConfig`.

## 5. Operator launch driver (design D6 — composes existing seams)

- [ ] 5.1 Forge issue-read lane (review H1): add `github.Client.GetIssue(ctx, owner, repo, number)`
  → title+body (beside `ListComments`), token-authed, base-URL-scoped, fail-closed on a missing/
  unreachable issue. RED pin first (a recorded HTTP double), then implement. This is the content
  channel the launch driver needs.
- [ ] 5.2 Export a resolver constructor (review L2): `natsRunResolver` is unexported + built inline in
  `newApprovalAdapter` — add an exported constructor taking the NATS client + org/platform prefix so
  the CLI can build its `bindRun`. No behavior change to the approval path.
- [ ] 5.3 RED: pin the `semdev launch <issue-ref>` path fetches issue content (5.1) and populates
  `Intake.Event.AuthoredText` from the issue BODY ONLY — matching the webhook's `intake.normalize`
  (`AuthoredText = Issue.Body`) so the two front doors produce byte-identical wake content (the parity
  the review flagged); composes `intake.CoordinatorTask` → `PublishToStream(FrontDoorSubject)`, binds
  the run minted AFTER this publish (review M2), stamps the condition via `experiment.Launch` — asserts
  the probe→publish→bind→stamp ORDER and that the wake carries the fetched body (not empty).
- [ ] 5.4 Implement the `launch` subcommand in `cmd/semdev`: NATS connect, `experiment.LoadConfig`,
  fetch issue content, build probe (semsource readiness, nil for baseline) / publish / bindRun / writer
  (`NewNATSOwnedFactWriter`), call `experiment.Launch`, print the bound run id + condition. It boots NO
  components (thin client to the running runtime). GREEN 5.3.
- [ ] 5.5 FAIL-CLOSED / G2 pins: an unreadable issue aborts before publish (no wake, no run — review H1);
  a semsource condition with a failing probe aborts BEFORE publish; an unbound run within the window
  fails loud and stamps NOTHING; the driver writes no lifecycle-advancing fact (the bind is a graph
  read). Baseline stamps no condition.
- [ ] 5.6 M2 pins: the driver publishes STRAIGHT to the front door, bypassing the admission gate
  (document the trusted-operator posture); `bindRun` SNAPSHOTS the run-id set carrying the ref BEFORE
  publish and binds the NEW id that appears after (clock-independent set-difference, not a wall-clock
  post-dates check), NOT any pre-existing run sharing the ref (the two-runs-one-ref case). The exported
  resolver must expose the id SET for a ref, not just the first match. Pin the disambiguation.
- [ ] 5.7 Pin the driver reuses the SAME wake byte-shape AND content the intake produces (share the
  helper or pin equivalence; `AuthoredText` body-only per 5.3) so the operator and webhook front doors
  are not two shapes — a literal wake-equivalence pin for one issue across both doors.

## 6. Offline end-to-end + operator lane + docs

- [ ] 6.1 A `-race` docker journey that provisions from the forge-clone source against a LOCAL bare git
  remote seeded WITH history: clone → develop the arc → the cumulative diff is the FIX ALONE (not the
  history) → deliver → the delivered PR-equivalent diff is the fix (reuses the forge-io bare-remote +
  recording-double harness). Proves self-target end-to-end with zero paid tokens. HONESTY ANNOTATION
  (review M3): file-transport clone never prompts for credentials, so the D3 no-argv-leak token path
  is NOT exercised here (only unit pin 3.4 + the gated live run), and the local double cannot reproduce
  GitHub's server-side merge-base — so this journey proves clone→develop→diff→deliver MECHANICS for the
  static full-clone case; token-auth + moved-base are covered by the unit pin + the operator-gated live
  run. State this in the ledger.
- [ ] 6.2 Taskfile operator lane for `semdev launch` (probe/launch/status shape, mirroring the
  `realllm:` lane); `docs/real-llm-runbook.md` (or a sibling) gains the live-target launch sequence
  (operator picks a disposable repo, sets `GITHUB_TOKEN` + webhook secret + the source-mode/forge config).
- [ ] 6.3 Docs match reality (G10): `docs/brief.md` milestone status, CLAUDE.md status line,
  `docs/port-manifest.md` if a new port is implied; update the memory pointers.

## 7. Verification + review + evidence

- [ ] 7.1 Full offline ladder green (`task check` — build + lint + unit `-race`, censuses) + full
  `task e2e` green `-race` uncached (the prior journeys UNCHANGED + the new self-target journey).
- [ ] 7.2 Adversarial review — BOTH reviewers (go-reviewer + semstreams-reviewer), zero blocking/high,
  all findings applied (standing directive). Focus: the D2 history/base change (fixture regression), the
  no-argv-leak token path, the fail-closed source resolution, and the driver's G2 posture.
- [ ] 7.3 `openspec validate --strict` green; sync-specs at archive folds these three deltas.
- [ ] 7.4 OPERATOR-GATED, deliberately open (the M0-completion G7 requirement, = forge-io 5.4): ONE
  recorded live-forge delivery against the disposable `semdev-test` repo — now END-TO-END RUNNABLE via
  EITHER front door (webhook: label an issue `semdev`, content from the payload; OR `semdev launch <ref>`,
  content read from the forge issue lane). PREREQUISITE: `semdev-test` is currently EMPTY and must be
  SEEDED first (a buildable project + a declared Dockerfile/devcontainer per the sandbox spec + an
  authored issue) — an empty repo has no default branch to clone and no issue to develop. This change
  makes the run runnable; it does not run it. Cross-link forge-io task 5.4.
