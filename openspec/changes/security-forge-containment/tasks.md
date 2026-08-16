# security-forge-containment — tasks

Standing directive: adversarial review (semstreams-reviewer + go-reviewer)
before each group's commit. Every fix task lands with its red-first pin (G6).

## 1. Commit containment (D1 + D2 — sandbox + dev-from-task deltas)

- [ ] 1.1 RED: pin P1 — a patcher unit test plants an untracked residue file
      in the checkout, applies a valid in-contract diff, and asserts the
      resulting commit tree does NOT contain the residue (`git ls-tree` on the
      returned sha). Verify it FAILS against `git add -A`.
- [ ] 1.2 RED: pin P2 — an integration test drives the laundering shape:
      attempt N's measure leaves a non-ignored artifact → CleanTree rejects;
      attempt N+1 applies a clean diff → assert N+1's commit lacks the
      artifact AND floors pass on N+1. Verify it FAILS today (the floor goes
      green by absorbing the residue).
- [ ] 1.3 GREEN: `Patcher.commit` takes the enumerated targets and stages
      `git add -- <target>...` (argv array); update the nothing-to-commit /
      gitignored-path comment (:150-153) for the explicit-add error shape.
- [ ] 1.4 GREEN: reset-before-apply in `Patcher.Apply` (after checkout
      resolution): `git checkout -- .` + `git clean -fd` (never `-x`), each
      fail-closed on non-zero exit.
- [ ] 1.5 Extend the `CleanTree` KNOWN-CONSTRAINT comment with the closed
      false-ACCEPT (laundering) direction — floor behavior itself unchanged.
- [ ] 1.6 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 1.

## 2. Credential containment + bounded push (D3 + D4 — forge-io delta)

- [ ] 2.1 Extract the askpass/env assembly from `internal/forge/clone` into
      `internal/cliexec/gitcred.go` (move-only; clone re-uses it and clone's
      existing unit pins stay green unmodified — the byte-identity evidence).
- [ ] 2.2 RED: pin P3 — a recording EnvRunner asserts the delivery push argv
      contains no credential material and env carries
      `GIT_ASKPASS`/`GIT_TERMINAL_PROMPT=0`/the token var; plus the
      fail-closed pin: configured token + a non-EnvRunner runner refuses the
      push. Verify the argv pin FAILS against today's token-in-URL push.
- [ ] 2.3 RED: pin P4 — a stalling runner proves the push returns at
      `pushTimeout` with a classified error. Verify it FAILS (hangs/no bound)
      today.
- [ ] 2.4 GREEN: delivery builds the push URL with `x-access-token` username
      only, pushes via `RunWithEnv` with the shared env assembly, wraps the
      push in `context.WithTimeout(pushTimeout)`; `sanitize` stays as the
      output belt.
- [ ] 2.5 Journey check: file:// pushes in the e2e suite run unchanged
      (`GIT_TERMINAL_PROMPT=0` present, no askpass without a token).
- [ ] 2.6 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 2.

## 3. Build-path confinement (D5 — sandbox delta)

- [ ] 3.1 Lift `SafeJoin` move-only into `internal/pathguard`;
      `runspace.SafeJoin` becomes a thin re-export (existing callers and
      tests untouched — the compile is the evidence).
- [ ] 3.2 RED: pin P5 — a devcontainer fixture declaring `../` in
      `build.dockerfile` (and a second case: `build.context`) asserts
      `resolveBuildPaths` returns `ErrNoImage` and nothing outside the
      checkout is read. Verify it FAILS against raw `filepath.Join`.
- [ ] 3.3 GREEN: route every `resolveBuildPaths` join (both branches + the
      devcontainer.json read) through `pathguard.SafeJoin`, wrapping
      rejections in `ErrNoImage` so the uniform no-image park carries them.
- [ ] 3.4 Full offline suite + `task e2e -race` green (the cold-proof docker
      journeys exercise the declared-image path); adversarial review; commit
      group 3.

## 4. Verify + close

- [ ] 4.1 `openspec validate security-forge-containment --strict` green;
      `/opsx:verify` against all three deltas.
- [ ] 4.2 Evidence: name P1–P5 + the green `task e2e -race` run in the change
      (G7); no evidence-ledger entry (no run-level claim is made).
- [ ] 4.3 Sync deltas (`/opsx:sync`) and archive the change.
