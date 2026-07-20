# Tasks: self-target-provisioning-and-launch-driver

Discipline (project law): red-first pin with every behavior (G6); the fixture
path stays byte-identical (the guard); adversarial review (go + semstreams
reviewers) before each group commit; no paid token — every task here is provable
OFFLINE or against a LOCAL bare git remote (the forge-io bare-remote pattern,
symmetric). The ONE paid live-forge delivery stays operator-gated (forge-io 5.4).

## 1. cliexec env passthrough (design D3 prerequisite; discharges forge-io follow-up #2)

- [ ] 1.1 RED: pin that a command can read a value from the subprocess environment and
  that the value appears in NO process argument the runner receives (a `TestRunnerEnvNotInArgv`
  shape) — fails before the extension exists.
- [ ] 1.2 Extend `cliexec` with an env-aware call as an OPTIONAL interface + type-assertion
  (`RunWithEnv(ctx, dir, env, name, args...)`, review L3) so existing fakes stay untouched;
  `OSRunner` sets `cmd.Env = append(os.Environ(), env...)`. Every existing `Run` caller is UNCHANGED
  (the new path is additive). GREEN 1.1.
- [ ] 1.3 Pin the fake runner used across the suite gains the env parameter with a recording
  shape so downstream clone pins can assert on env (not argv).

## 2. Recorded diff base + history-preserving materialize (design D2 — the crux)

- [ ] 2.1 RED: pin that `Checkouts.Diff` over a checkout whose source carried PRE-EXISTING history
  returns ONLY the attempt's change (`refs/semdev/base..HEAD`), NOT the whole history — fails today
  because `Diff` uses `git rev-list --max-parents=0 HEAD` (the root commit).
- [ ] 2.2 `Materialize` records the base as an IN-REPO ref (`refs/semdev/base`, a lightweight tag at
  the prepare-time HEAD) — NOT an in-memory map (review H3) — so `Diff` reads `refs/semdev/base..HEAD`
  statelessly, durable as long as the checkout exists. Two prepare strategies by whether the source
  contains `.git`: no-`.git` (fixture) → `git init` + harness-identity config + baseline commit, base
  ref = that commit (UNCHANGED behavior); has-`.git` (clone) → materialize via `git clone <sourceDir>
  <checkout>` (NOT `copyTree` over `.git` — review M1), THEN set the repo-local harness identity
  (`git config user.email/user.name` — clone copies no identity, CI has no global one; omitting it
  fails the patcher commit — review H2), `git checkout -b <run-branch>` from the tip, base ref = the
  tip. GREEN 2.1.
- [ ] 2.3 GUARD: the existing fixture pins stay green UNCHANGED — `runspace` Materialize/Diff unit
  tests, `read_diff` tests, and the cold-verify clone tests (the fixture base ref == the old root, so
  byte-identical). Run them explicitly as the regression gate.
- [ ] 2.4 Pin the has-`.git` prepare: a clone-shaped source materializes on a working branch off the
  tip UNDER the configured harness identity, `apply_patch` commits atop real history succeed,
  `CloneForVerify` clones the committed objects, and `attempt.commit.sha` + `refs/semdev/base` resolve
  the cumulative diff to the fix alone. (Restart reconstruction is OUT OF SCOPE — see 2.6.)
- [ ] 2.5 Pin the run-branch name is push-safe and collision-free across re-runs of the same coordinate
  (design Open Question) — settle the scheme and pin it.
- [ ] 2.6 HONESTY (review H3): the base ref + attempt commits live in the checkout, so a lost checkout
  dir parks on restart exactly as today — this change does NOT claim the sandbox spec's "reconstruct
  at the recorded commit" scenario (already unmet pre-change: `Provision` no-ops once `sandbox.ready`
  is stamped; no durable base-commit/source-coordinate fact in `vocab.go`). File the pre-existing
  restart-safety gap as a NAMED follow-up (durable base-commit + source-coordinate facts = a future
  G9 addition + moved-base/shallow refetch); do NOT smuggle it into this change's no-new-predicate
  pledge.

## 3. Forge-clone Sources implementation (design D1; forge-io clone lane)

- [ ] 3.1 RED: pin `Resolve(ctx, runEntityID)` reads `run.issue.ref` from the graph, parses
  `owner/repo#N`, and clones `<forgeBase>/owner/repo` at the default branch into a fresh per-run dir —
  fails before the impl exists. Use a LOCAL bare git remote (seeded with history) as the forge double.
- [ ] 3.2 Implement the forge-clone `Sources` (`internal/forge/clone` — framework-alignment note +
  registry/DI wiring): graph reader for the coordinate, ref parse (reuse the host-neutral parser the
  approval/intake lane uses), shallow-or-full clone via `cliexec` env-token (GIT_ASKPASS reading the
  token from the subprocess env — D3), return the source dir. GREEN 3.1.
- [ ] 3.3 FAIL-CLOSED pins (SB5): no coordinate on the run → error (park); unparseable ref → error;
  unknown/unreachable repo → error; auth fault → error. NONE returns a guessed dir.
- [ ] 3.4 NO-ARGV-LEAK pin: the token appears in no argument the recording runner sees; it rides only
  the subprocess env (asserts against the group-1 recording runner).
- [ ] 3.5 Coordinate → clone-URL mapping pin (owner/repo extraction; base-URL scoping; `.git` suffix
  handling) including a ref with an org that contains dots / a repo with a hyphen.

## 4. Boot source-mode selection (design D5 — fail-closed on ambiguity)

- [ ] 4.1 RED: pin boot selects EXACTLY ONE source mode — fixture dir XOR forge-target — and errors
  loudly when neither or both are configured (no guessed default, SB5).
- [ ] 4.2 Wire the mode in `internal/boot`: fixture mode → `runspace.StaticSource` (today's path);
  forge-target mode → the group-3 clone source, reading the reused `forge` config block +
  `RunOptions.GitHubToken`. `provisionsandbox`, `Materialize` interface, and the sandbox stages are
  untouched (same `Sources` interface). GREEN 4.1.
- [ ] 4.3 GUARD: every existing e2e journey + boot integration test still selects the fixture mode and
  passes UNCHANGED (the fixture default is the pre-change behavior).
- [ ] 4.4 Config census pin: the new `source` mode block and the `forge` clone fields are documented
  in the bootstrap config shape and rejected-if-malformed at boot (no silent skip).

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
