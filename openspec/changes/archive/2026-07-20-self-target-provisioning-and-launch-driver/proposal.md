## Why

Two M0-posture seams still block M2 dogfood and the operator-gated live-forge run
(forge-io-real-lanes task 5.4 — the outstanding M0-completion G7 requirement): the
e2e journey is the ONLY front-door publisher (nothing in production mints a run),
and every run develops one static fixture directory (`runspace.StaticSource`),
never the real repo its issue names. Both are documented carry-forwards — the
`Sources.Resolve` flip in `internal/runspace/sources.go`, and the `experiment.Launch`
caller its own doc says "does not exist yet." This change fills them so an operator
can launch a real run against a real target.

## What Changes

- **Forge-clone source (Half A)**: a real `provisionsandbox.Sources` implementation
  reads the run's `run.issue.ref` coordinate from the graph, resolves the forge repo
  it names, and git-clones that repo at its default branch into a per-run source
  directory — returned into the EXISTING `Materialize → checkout → cold-prove →
  sandbox` pipeline unchanged. `runspace.StaticSource` stays as the fixture/test
  path (selected by config). Fail-closed exactly as `StaticSource` is: no coordinate,
  an unparseable ref, or a clone failure errors toward the operator (the run parks
  via the existing provision-blocked / station-failure lanes), never a guessed
  target (SB5).
- **Operator launch driver (Half B)**: a `semdev launch` operator CLI that mints a
  run through the SANCTIONED `experiment.Launch` seam (`probe → publish → bind →
  stamp`) — fetching the target issue's authored content from the forge (a new
  read-only issue lane) so the wake is never content-empty, composing it via
  `intake.CoordinatorTask` + `PublishToStream`, binding the run it minted by
  OBSERVATION (the exact-`run.issue.ref` resolver, disambiguated to the post-publish
  run), and stamping the operator-declared A/B condition. This is the durable
  production front door the e2e journey has stood in for; the journey's hand-composed
  mint stays a pinned, annotated exemption.
- **No new vocabulary**: both halves compose existing seams and predicates. The
  source's resolved host path stays OUT of the graph (infra state, like the checkout
  dir); the driver reuses `experiment.run.condition` (writer `experiment-intake`,
  G5) and `run.issue.ref` (writer `issue-ref-rule`). No new predicate, no new
  lifecycle write from Go (G2/G9).

## Capabilities

### New Capabilities

- `operator-launch`: the durable operator front door — an operator CLI mints a run
  against a named target through the fail-closed `experiment.Launch` seam
  (`probe → publish → bind → stamp`), the sole production publisher of a front-door
  wake besides the webhook intake; binds the coordinator-minted run by observation
  and stamps the declared condition. No lifecycle transition is fired from Go.

### Modified Capabilities

- `sandbox`: the run's source is resolved PER-RUN from its `run.issue.ref`
  coordinate (the real target), not from one operator-configured static directory;
  the fixture directory becomes an explicitly-selected test/dev path. Same
  fail-closed contract (absent/unresolvable source parks toward the operator).
- `forge-io`: the forge gains a read-only CLONE lane — given a run's coordinate it
  provides that repo's source at its default branch (token-authed, base-URL-scoped),
  fail-closed on an unknown repo, an auth fault, or a clone failure.

## Impact

- **New Go**: a new `internal/forge/clone` — the forge-clone `Sources` implementation
  reading the coordinate + shelling git via `cliexec` (framework-alignment note +
  registry/DI wiring at boot); `github.Client.GetIssue` (the issue-content read lane)
  + an exported run-resolver constructor; `cmd/semdev` gains a `launch` subcommand (the
  operator driver) composing `experiment.Launch`.
- **Wiring**: `internal/boot` selects `StaticSource` vs the forge-clone source by
  config (`RunOptions.SandboxSourceDir` becomes one of two source modes). The
  `provisionsandbox` tool and sandbox stages are UNCHANGED (same `Sources`
  interface); `runspace.Checkouts` gains a history-preserving materialize mode (a
  real clone is materialized via `git clone` and branches from the tip) and records the
  diff base as a durable in-repo ref (`git update-ref refs/semdev/base`, read as
  `refs/semdev/base..HEAD` — not the root commit) so a real target produces a clean-diff
  PR — the fixture path stays byte-identical (design D2), and `cliexec` gains env
  passthrough for no-argv-leak token injection (design D3, discharging forge-io
  follow-up #2).
- **Config**: a bootstrap source-mode block (fixture dir vs forge target) and the
  forge base URL/token the clone lane reads (reusing the delivery `forge` block +
  `GITHUB_TOKEN` already threaded through `RunOptions`).
- **Ops**: a Taskfile operator lane for `semdev launch`; the runbook gains the
  live-target launch sequence. Unblocks forge-io-real-lanes task 5.4 end-to-end.
- **Docs**: `docs/brief.md` milestone status, `docs/evidence-ledger.md` (the 5.4
  path becomes runnable), CLAUDE.md status.
