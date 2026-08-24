# security-forge-containment — design

## Context

Three containment gaps on the real-forge paths, verified live on `main`
2026-08-16 (external-review triage #5/#6/#7). All three fixes reuse guards the
repo already owns (`SafeJoin`, the clone askpass pattern, the `ErrNoImage`
park); no new components, rules, predicates, or semstreams surfaces. The
change deliberately lands before the beta.161 pin bump.

Ground truth (read this session):

- `internal/runspace/patcher.go` — diff-level containment is complete
  (SafeJoin per target, `target_files` contract check before apply,
  rename/copy/symlink/binary rejection, every git write path enumerated or the
  diff is rejected). The sole gap is `commit()` at :144: `git add -A` stages
  the *whole tree*, so content no diff introduced reaches the attempt commit.
- `internal/floors/checks.go` `CleanTree` — DirtyPaths is
  `git status --porcelain` (untracked included, gitignored excluded). Residue
  from attempt N rejects attempt N; today attempt N+1's `add -A` *absorbs* the
  residue, the floor goes green, and that laundered sha is what delivery
  pushes. With targets-only staging alone, the residue instead re-rejects
  every later attempt until the budget exhausts — honest but a doomed paid
  loop.
- `internal/tools/openpr/delivery.go` — `pushURL()` embeds the token as URL
  userinfo, which rides `git push` argv (:130). `sanitize()` already scrubs
  output; argv is the uncovered lane. No `GIT_TERMINAL_PROMPT=0`, no deadline.
  `internal/forge/clone/clone.go` holds the complete safe pattern (design D3):
  `x-access-token` username only, token via `GIT_ASKPASS` env through
  `cliexec.EnvRunner`, fail-closed refusal when the runner cannot inject env,
  `GIT_TERMINAL_PROMPT=0` always. The delivery station already constructs
  `cliexec.OSRunner{}`, which satisfies `EnvRunner`.
- `internal/cleanroom/image.go` `resolveBuildPaths` — all joins are raw
  `filepath.Join`. The devcontainer branch's `build.dockerfile` /
  `build.context` are repo-AUTHORED strings: `..` traversal resolves outside
  the checkout and reads host files into the docker build context. The
  Dockerfile branch's inputs come from semdev's own discovery scan
  (repo-relative names), but it gets the same guard for uniformity.
  `runspace.SafeJoin` (attempts.go:125) is the shared containment guard the
  read/write tools already use; its own doc comment says it exists so guards
  are one implementation, not re-derived copies.

## Goals / Non-goals

Goals: close the three gaps with minimal, guard-reusing diffs; every fix lands
with its red-first pin (G6); journeys stay green (`task e2e -race`).

Non-goals: the station panic-park bypass (#8b — station change), paid-run
readiness, tree-writing-ecosystem support for CleanTree (documented M0
constraint, unchanged), multi-task apply scope (devTaskIndex stays 0), any
rule/vocabulary change.

## D1 — commit stages exactly the enumerated diff targets

`Patcher.commit` takes the already-guarded `targets` and stages them
explicitly: `git add -- <target>...` (argv array, no shell), then commits as
today. `parseDiffTargets`' invariant — every path git writes is enumerated or
the diff is rejected — is precisely what makes targets-only staging sound.
Deletions and new files both stage correctly via explicit `git add -- <path>`.

Behavior notes:
- A diff touching only gitignored paths: today `-A` silently skips them and
  "nothing to commit" fails closed; with explicit paths `git add` itself
  errors on the ignored path — still fail-closed, clearer reason. The comment
  at :150-153 is updated.
- `sandbox` spec scenario "Working-tree residue is not committed" pins this:
  residue present at commit time stays untracked (visible to CleanTree),
  never committed.

## D2 — each apply starts from the committed snapshot (reset-before-apply)

At the top of `Patcher.Apply` (after checkout resolution, before parsing), the
harness resets index AND working tree to the last committed attempt:
`git reset --hard -q` then `git clean -ffdq` (NOT `-x`: gitignored caches
survive, matching CleanTree's documented assumption that build output is
ignored). `reset --hard`, never `checkout -- .` — checkout restores from the
INDEX, so a hostile `git add`/`git rm` from in-container measurement code would
survive it and `git commit` commits the whole index (go-review H1, empirically
verified). `-ff` so a nested repo a measurement step cloned in is removed too —
exactly the residue class the model cannot remove. Staging additionally pins
each target as `:(literal)` pathspec so a glob-shaped target stages the exact
file git apply wrote, never a pattern expansion (go-review M2).

Why both D1 and D2:
- D1 alone leaves prior-attempt residue untracked forever → CleanTree rejects
  every later attempt → guaranteed budget exhaustion on a residue the model
  cannot remove (apply_patch has no verb for deleting untracked files). A
  doomed paid loop is fail-closed but wasteful.
- D2 gives every attempt the exact contract the spec already states: the diff
  applies against the committed snapshot, measurement and floors see only
  what the attempt itself introduced. It also heals a partially-dirty tree
  left by an earlier failed apply/commit.
- Evidence is unaffected: rejected attempts were already measured and
  floor-stamped as facts; the working tree between attempts is scratch, the
  committed chain is the record (G7). The dev-from-task delta's
  "Prior-attempt residue cannot launder into a later commit" scenario is
  pinned by D1+D2 together.

CleanTree itself is UNCHANGED — its role narrows back to what its comment
claims: catching within-attempt post-measure mutation. Its KNOWN-CONSTRAINT
comment gains the closed false-ACCEPT direction (currently it documents only
false-REJECT).

## D3 — push adopts the clone credential pattern via a shared helper

The askpass mechanics move from `internal/forge/clone` to a small shared
`internal/cliexec/gitcred.go` (exported helper: write-askpass + env assembly
`GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=<helper>`, `<tokenEnv>=<token>`), with
`clone.go` re-using it byte-identically (cliexec's EnvRunner comment already
anticipates exactly this consumer). Delivery then:

- builds the push URL like `cloneURL` does: `x-access-token` username only
  (non-secret convention), token NEVER in the URL → never on argv;
- runs the push through `EnvRunner.RunWithEnv` with the assembled env;
- fail-closed parity with clone: a configured token + a runner that cannot
  inject env refuses the push (never an argv fallback);
- keeps `sanitize()` as the output belt (defense in depth), now with nothing
  to scrub in the common path;
- file:// and ssh remotes pass through untouched (journeys unaffected), but
  `GIT_TERMINAL_PROMPT=0` is set on every push.

## D4 — the push is deadline-bounded

`Deliver` wraps the push in `context.WithTimeout` with a package constant
`pushTimeout = 2 * time.Minute` (generous for a small fix-alone diff; journeys
push to file:// in milliseconds). On expiry the runner kills git, the push
returns a classified error, and the station's existing bounded-retry →
`station.dispatch.failed` → park lane carries it (no new routing). The
station's cancel-only `baseCtx` rooting stays as designed — the bound is
per-push, not per-handler.

## D5 — resolveBuildPaths goes through SafeJoin

Every join in `resolveBuildPaths` (and the devcontainer.json read) resolves
via `runspace.SafeJoin(repoRoot, rel)`: `decl.Dockerfile`, the Dockerfile
branch's `decl.Context`, `decl.Devcontainer`, and the devcontainer branch's
`filepath.Join(dcDir, df)` / `filepath.Join(dcDir, buildCtx)` composites
(SafeJoin the composite relative path). A SafeJoin rejection wraps
`ErrNoImage`, so traversal fails closed into the exact same park as "no
buildable image" (the sandbox delta's scenario). SafeJoin also rejects
absolute paths — stricter than today's silent re-rooting, and correctly so: an
absolute declared path is a misdeclaration, parked loudly.

The SYMLINK lane is closed too (groups-2-3 go-review MEDIUM-1): SafeJoin is
lexical, so a repo-COMMITTED symlink under a string-legal path (a
`Dockerfile -> /host/file` link, or the devcontainer.json itself) would still
read host files. `safeBuildPath` therefore follows symlinks
(`filepath.EvalSymlinks`) and re-checks containment against the resolved
checkout root; a nonexistent path skips the check (nothing to read — the
build fails closed downstream, and the fake-root unit shapes stay pure), any
other resolution failure fails closed. Red-pinned by
`TestResolveBuildPathsRejectsSymlinkEscape` (both the symlinked-Dockerfile
and symlinked-devcontainer.json shapes).

Import direction [checked]: `runspace` imports `cleanroom`
(manifests.go/sandboxes.go), so cleanroom importing runspace would CYCLE.
SafeJoin therefore lifts move-only into a tiny `internal/pathguard` package;
`runspace.SafeJoin` becomes a thin re-export (its exported name stays, all
existing callers untouched) and `cleanroom` imports `pathguard` — still ONE
implementation (the SafeJoin comment's own rule).

## Pins (G6 — each red-first against the unfixed shape)

- **P1 (D1)**: unit — residue file present at commit time → the commit tree
  does NOT contain it; red against `add -A`.
- **P2 (D1+D2)**: integration/journey — attempt N's measure leaves a
  non-ignored artifact, floor rejects; retry applies a clean diff → attempt
  N+1's commit lacks the artifact AND floors pass (the laundering shape, red
  against today's absorb-and-green).
- **P3 (D3)**: unit with a recording EnvRunner — push argv carries no
  credential material, env carries `GIT_ASKPASS`/`GIT_TERMINAL_PROMPT=0`;
  plus the fail-closed pin: token + non-EnvRunner refuses.
- **P4 (D4)**: unit with a stalling runner — the push returns at the bound
  with a classified error.
- **P5 (D5)**: unit — a devcontainer declaring `../` dockerfile or context →
  `ErrNoImage`, nothing outside the checkout read; red against raw Join.

## Named follow-up (recorded, not in scope)

`write_change` (openspec-io) writes the rendered OpenSpec change folder into
the run's workspace "so it can be committed for the PR" — a contract that
predates commit containment: under D1+D2 those untracked files are never
committed and the next apply's clean deletes them. NOT live today (boot wires
a nil resolver that fails loudly; the M0 rail delivers the fix-alone diff),
but when the production resolver lands, the written change must route through
the delivery surface or an authorized-targets lane, never working-tree
residue. Breadcrumb comment added at the package doc
(semstreams-review MEDIUM).

## Evidence (G7 — as landed)

- Pins, each verified RED against the pre-fix shape before its fix: P1
  `TestPatcherCommitExcludesWorkingTreeResidue` (+ the go-review-H1 variant
  `TestPatcherCommitExcludesStagedResidueAndRestoresStagedDeletion`), P2
  `TestPatcherLaunderingClosedAcrossAttempts`, D2's
  `TestPatcherApplyResetsToCommittedSnapshot`, P3
  `TestDeliverPushCarriesNoCredentialOnArgv` + P3b
  `TestDeliverRefusesPushWithTokenOnNonEnvRunner`, P4
  `TestDeliverPushIsDeadlineBounded`, P5
  `TestResolveBuildPathsRejectsTraversal` +
  `TestResolveBuildPathsRejectsSymlinkEscape`. Regression guards born green
  where noted in-file: `TestPatcherCommitStagesOnlyTargetsIndependentOfReset`,
  `TestPatcherFailsClosedWhenResetFails`,
  `TestPatcherStagesNewFileAndDeletionDiffs`, `TestGitCredEnvShapes`,
  `TestSafeJoin`.
- Full `task e2e -race` docker journey suite GREEN over the final tree
  (457.6s, 2026-08-16), plus `task check` (build + lint + unit `-race`).
- Adversarial review: four passes (semstreams-reviewer + go-reviewer on group
  1 with post-fold verify passes; both again on groups 2–3), final verdicts
  all APPROVE, zero blocking/high outstanding. Both group-1 reviewers
  independently found the same HIGH (index-sourced reset) — fixed + pinned.
- No evidence-ledger entry: this change makes no run-level claim.

## Risks / trade-offs

- D2 deletes untracked files between attempts. By construction anything
  untracked did not arrive via a committed diff, and `-x` is withheld so
  caches survive; the only loss is scratch state no consumer reads.
- D3 touches the clone path (helper extraction). Mitigation: clone's own unit
  pins must stay green byte-identically; the extraction is move-only.
- D4 introduces the change's only new constant; too-tight is the failure mode
  and 2m is ~100× the observed live push time.
- D5's absolute-path strictness could park a repo that today accidentally
  worked via re-rooting. That accident was reading the WRONG in-checkout path;
  parking loudly toward the operator is the correct posture (sandbox spec:
  fail closed, never a guess).
