## Context

Two M0-posture seams are the last thing between semdev and a real dogfood run
(forge-io-real-lanes task 5.4 — the outstanding M0-completion G7 requirement):

1. **Source is a static fixture.** `provisionsandbox` resolves the run's source
   through the `Sources` interface (`Resolve(ctx, runEntityID) (dir, error)`). The
   only implementation is `runspace.StaticSource{Dir}` — one operator-configured
   directory for EVERY run (the in-repo Go fixture). `sources.go` already documents
   the flip: "the M2 forge-io implementation resolves per-run … Resolve reads the
   run's coordinate and its return contract may flip from a host dir to a clone
   coordinate."
2. **The e2e journey is the only front-door publisher.** Nothing in production mints
   a run except the webhook intake (forge-io) — and an operator has no way to launch
   a run against a chosen target. `experiment.Launch` (the sanctioned
   `probe→publish→bind→stamp` seam) was built for exactly this caller; its own doc
   says the caller "does not exist yet."

The pipeline downstream of `Sources` (`Materialize → cold-prove → warm sandbox →
apply_patch → measure → clean-room verify → deliver`) is proven and stays behind the
same interfaces. The bind-by-observation and wake-composition seams the driver needs
already exist (`intake.RunResolver`, `intake.CoordinatorTask`).

## Goals / Non-Goals

**Goals:**

- A forge-clone `Sources` implementation that resolves the run's real target from its
  `run.issue.ref` coordinate and clones it, feeding the existing provisioning
  pipeline; fail-closed exactly as `StaticSource` (SB5).
- A real dogfood PR that diffs CLEANLY against the target's base branch (just the
  fix), not an unrelated-history dump — the honest-evidence bar (G7).
- An operator CLI (`semdev launch <issue-ref>`) that mints through `experiment.Launch`
  — the durable production front door beside the webhook intake.
- Both `StaticSource` (fixture) and the forge-clone path selectable by config; every
  e2e journey keeps using the fixture path byte-for-byte.
- No new predicate, no lifecycle write from Go (G2/G9).

**Non-Goals:**

- The recorded live-forge run itself (still operator-gated: forge-io 5.4 / this
  change's own verification remains offline + double + bare-remote; the ONE paid live
  delivery is the operator's to run and record).
- Multi-repo / submodule / monorepo-subdir targets (single-repo clone at default
  branch; `--recursive` submodules is a named follow-up).
- Incremental / cached clones across runs (each run clones fresh — per-run isolation
  beats speed at M2; a warm mirror is a later optimization).
- A long-running operator daemon or web UI — the driver is a one-shot CLI that
  publishes to an already-running runtime and reports.

## Decisions

### D1: Forge-clone `Sources` reads the coordinate per-run from the graph

A new implementation (`internal/forge/clone` — framework-alignment note + boot DI
wiring) satisfies `provisionsandbox.Sources`. `Resolve(ctx, runEntityID)`:

1. Reads `run.issue.ref` for `runEntityID` from the graph (a read-only prefix/entity
   query — reuses the `changefacts`/graph reader the stations already inject).
2. Parses the host-neutral ref `owner/repo#N` → `owner`, `repo`.
3. Clones `<forgeBase>/owner/repo(.git)` at the remote's DEFAULT branch into a fresh
   per-run source dir and returns it.

The coordinate MUST be read at `Resolve` time (not construction) — the run entity ID
is only known per-call and the station is shared across runs. Alternatives rejected:
passing the coordinate at construction (one target per process — no better than
`StaticSource`); resolving from the wake (the wake isn't in the `Sources` call path).

`StaticSource` is untouched and remains the fixture/dev/test path.

### D2: The run's checkout PRESERVES the clone's history; the diff base is RECORDED, not the root commit

This is the load-bearing decision. Today `Materialize` copies the source, then
`initCommit` does `git init` + a synthetic "base: pristine checkout" commit, and
`Checkouts.Diff` resolves the base as `git rev-list --max-parents=0 HEAD` (the ROOT
commit). That is correct only when the source has NO history (the fixture): base..HEAD
is exactly the fix. Over a real clone with full history, `--max-parents=0` names the
repo's ORIGINAL root commit, so base..HEAD would be the entire repo plus the fix — the
measurement, the review diff, and the PR would all be wrong. And a fresh-history branch
pushed to a real repo has no common ancestor → the PR shows every file as added.

Resolution — unify both paths under a base recorded AS AN IN-REPO GIT REF (not an
in-memory map — review H3):

- `Materialize` records the base by writing a CUSTOM in-repo ref via `git update-ref
  refs/semdev/base <HEAD>` (NOT `git tag`, which would land at `refs/tags/…` and not
  resolve as `refs/semdev/base`) at the prepare-time HEAD, for BOTH paths. The ref lives
  inside the checkout, so it is exactly as durable as the checkout dir itself (same
  lifetime as today's in-memory `roots[]`), and `Diff` becomes STATELESS w.r.t. the
  `Checkouts` struct — it reads `refs/semdev/base..HEAD` from the repo, holding no
  parallel map. (`CloneForVerify`'s `git clone` does not copy `refs/semdev/*` — harmless,
  since the verify clone never calls `Diff`.)
- Two prepare strategies, chosen by whether the source already contains `.git`:
  - **No `.git` (fixture)**: unchanged — `git init` + harness-identity config +
    baseline commit; `refs/semdev/base` points at that baseline commit. Byte-identical
    to today's fixture behavior.
  - **Has `.git` (clone)**: materialize by `git clone <sourceDir> <checkout>` (NOT
    `copyTree` over a live `.git` — review M1), then set the SAME repo-local harness
    identity (`git config user.email/user.name` — clone does NOT copy identity, and a
    CI box has no global one, so omitting this fails the patcher's commit; review H2),
    create the run's working branch from the cloned tip (`git checkout -b <run-branch>`),
    and point `refs/semdev/base` at that tip. No synthetic baseline commit.
- `Checkouts.Diff` diffs `refs/semdev/base..HEAD` instead of `rev-list --max-parents=0`.
  For the fixture the base ref == the old root commit, so read_diff is unchanged.

Consequences: `apply_patch` commits atop real history (under the configured harness
identity); `CloneForVerify` clones the warm repo's committed objects (unchanged);
delivery pushes a branch that descends from the target's default-branch tip, so the PR
diff is the fix. This makes the fixture and real paths uniform, not special-cased.

**Restart reconstruction is explicitly OUT OF SCOPE (review H3).** The sandbox spec's
"reconstruct the checkout at its recorded commit" scenario is ALREADY unmet in the
current code (`provisionsandbox.Provision` no-ops once `sandbox.ready` is stamped and
never re-materializes; no durable base-commit/source-coordinate fact exists in
`vocab.go`) — a pre-existing G10 gap for the fixture path too. This change does not
close it and MUST NOT claim to: the base ref and attempt commits live in the checkout,
so a lost checkout dir parks on restart exactly as today. Closing restart-safety would
require stamping the base commit + a resolvable source coordinate as DURABLE facts (a
G9 vocabulary addition) and solving the moved-default-branch/shallow-clone refetch — a
named follow-up, filed, not smuggled into this change's "no new predicate" pledge.

Alternative rejected: strip the clone's `.git` and git-init-fresh (keeps `Materialize`
unchanged) — but then the PR has unrelated history and is not honest dogfood evidence.
Rejected on G7.

### D3: Token injection is env-only (no argv leak); extend `cliexec` with env passthrough

The clone authenticates to a private forge with a token. `cliexec.OSRunner.Run` today
takes no env, so a token would have to ride the URL or a `-c http.extraHeader` argv —
both leak in `ps`/process listings (the forge-io follow-up #2, filed for the push path,
applies identically here). Decision: extend `cliexec.Runner` with an env-aware call
(an options form or `RunWithEnv`), and clone via `GIT_ASKPASS` → a tiny per-run helper
that echoes the token from the SUBPROCESS ENV (never argv, never a file with the token).
This discharges forge-io follow-up #2 for the clone lane and sets the pattern the push
lane adopts next. Scope is small and self-contained; every existing `Run` caller is
untouched.

### D4: Forge config is reused, not reinvented

The clone lane reads the SAME `forge` config block delivery already exposes (base URL,
optional auth) plus `GITHUB_TOKEN` already threaded through `RunOptions.GitHubToken`.
No new top-level config surface; a `source` mode block selects fixture-dir vs
forge-target (see D5).

### D5: Boot selects the source mode by config, fail-closed on ambiguity

`RunOptions` today carries `SandboxSourceDir` (fixture). Add a source-mode selection:
exactly one of {fixture dir, forge-target} is configured. Boot wires `StaticSource` or
the forge-clone source accordingly. Neither configured, or both, is a loud boot error —
never a guessed default (SB5, the sandbox==nil disease). Every e2e journey sets the
fixture dir and is unchanged; the operator live run sets the forge-target mode.

### D6: The operator driver is a thin one-shot client that composes existing seams

`semdev launch <issue-ref> [--condition …]` (a `cmd/semdev` subcommand):

1. Connect NATS; load `experiment.Config` (`experiment.LoadConfig`).
2. **Fetch the issue's authored content from the forge (review H1).** `CoordinatorTask`
   carries the issue body into the wake prompt via `Intake.Event.AuthoredText` — the
   ONLY channel by which the ask reaches the arc (the M1 lane; `realllm_journey_test.go`
   sets it explicitly). A bare ref yields an EMPTY prompt → the coordinator authors
   against nothing → a plausible-but-unrelated ships-green PR. So the driver reads the
   issue through a forge issue-read lane (`github.Client.GetIssue(owner, repo, number)` —
   a new read method beside `ListComments`, returning title+body) and populates
   `AuthoredText` from the issue BODY — matching the webhook's normalize EXACTLY
   (`intake.normalize` sets `AuthoredText = Issue.Body`, body-only; review parity), so
   the two front doors produce the SAME wake content for one issue (the title serves only
   log / run-branch context, never the prompt). The webhook gets content from its
   payload, the CLI from a forge read — both content-bearing, byte-parity.
3. Build `probe` (semsource per-signal readiness — only for the semsource condition,
   else nil), `publish` (`intake.CoordinatorTask(in, model)` with the fetched content →
   `PublishToStream(intake.FrontDoorSubject, …)` — the journey-proven byte shape),
   `bindRun`, `writer` (`agentictools.NewNATSOwnedFactWriter`).
4. Call `experiment.Launch(ctx, cfg, probe, publish, bindRun, writer)` — the ONE
   sanctioned mint path — and print the bound run entity ID + condition.

**`bindRun` disambiguation (review M2).** The resolver matches the run whose stamped
`run.issue.ref` EQUALS the ref (an exact value match over the chain-execution namespace
prefix query — `natsRunResolver.ResolveRunByRef`, not a ref-prefix match; review L1).
Because `launch` publishes straight to the front door it BYPASSES the admission gate
(acceptable for a trusted operator — state it), and if a webhook already woke the same
issue two runs can share the ref; the resolver returns the first in page order, which
need not be the just-minted run. So `bindRun` SNAPSHOTS the set of run IDs carrying the
ref BEFORE publish and binds the NEW id that appears after (a clock-independent
set-difference — no assumption of driver/runtime clock coherence, cleaner than a
wall-clock post-dates check). Binding stays an OBSERVATION (a graph read), never a
lifecycle write. The resolver needs an EXPORTED constructor + the org/platform prefix
(today `natsRunResolver` is unexported, built inline in `newApprovalAdapter`; review L2),
and it must expose the run-id SET for the ref (not just the first match) for the diff.
The wake's `model` comes from config/flag (not hardcoded like the journey's `"gemini"`).

The driver does NOT boot components (the running `semdev` runtime consumes the wake and
mints the run); it is the alternative front door to the webhook. Every seam but the CLI
glue and the issue-read method already exists. The e2e journeys' hand-composed mint
stays a pinned, annotated exemption (the driver does not replace them; it makes them
redundant in production).

**Idempotency + admission bypass (impl review).** The webhook door suppresses a duplicate
run when the ref already has one; the operator door mirrors that with a PER-REF guard —
it fails closed (unless `--force`) when the pre-publish snapshot already carries the ref,
so a double-launch does not silently mint a competitor. The bind additionally fails closed
on an AMBIGUOUS result (two new runs from a concurrent front door). The door deliberately
BYPASSES admission (no authorize/opt-in, no `intake.actor.admitted` record) — the operator
already holds the shell and token, so the actor-authorization gate is moot; documented
honestly (G10) so a reader knows launched runs carry no admission record.

### D7: The condition-stamp-after-mint window is benign (evidence label, never routes)

`Launch` publishes → binds → stamps, so the arc starts before the condition label lands.
That is by design and already established in semsource-ab: the condition is an evidence
label read by the ledger post-hoc; it never routes and never selects tools (the variant
tool pack is chosen at BOOT from the config, not per-run from the label). No race to fix.

## Risks / Trade-offs

- **A moved default branch between clone and PR** → the PR's merge-base is the cloned
  tip; if the target's default branch advances before delivery, the PR still diffs
  against the CURRENT base (GitHub recomputes merge-base) — the fix commits are still
  the only ahead-commits. Acceptable; noted for the runbook.
- **Clone cost / large repos** → each run clones fresh (Non-Goal: caching). Mitigation:
  a shallow clone (`--depth`, default-branch only) keeps it bounded; deep history isn't
  needed (base = tip). Trade-off: `--depth 1` means the merge-base with an advanced
  base branch may be unreachable — mitigate by cloning the default branch at a modest
  depth or full for M2 dogfood (small repos); revisit for large targets.
- **Token handling** → env-only injection (D3) is the mitigation; a review pin asserts
  no token appears in any git argv the runner sees.
- **bindRun timeout vs a slow mint** → the driver polls with a generous, operator-set
  timeout and, on expiry, fails LOUD ("no run bound to ref yet") without stamping —
  never a half-labeled run. Mirrors `experiment.Launch`'s fail-closed order.
- **Fixture-path regression** → D2 keeps the no-`.git` path byte-identical; the existing
  read_diff / verify pins must stay green unchanged (the guard).
- **Offline journey coverage is partial (review M3)** → the offline e2e clones from a
  LOCAL bare remote over file transport, which never prompts for credentials, so the
  D3 `GIT_ASKPASS`/no-argv-leak token path is NOT exercised by the journey (only by the
  unit pin 3.4 and the gated live run); and a local double cannot reproduce GitHub's
  server-side merge-base, so "the delivered diff is the fix" is proven offline only for
  the static full-clone case. The evidence ledger must state this — the offline journey
  proves clone→develop→diff→deliver MECHANICS; token-auth and moved-base are covered by
  the unit pin + the operator-gated live run, not offline (G7 honesty).
- **An empty target repo** (`semdev-test` is currently empty) → a repo with no commits
  has no default branch to clone and no issue to develop. The clone lane fails closed on
  it (correct); the runbook must state the live-run target is SEEDED (buildable project +
  declared Dockerfile + an authored issue) before 5.4/7.4.
- **`cliexec.Runner` interface churn (review L3)** → adding a second method forces every
  fake runner to implement it; prefer an OPTIONAL interface + type-assertion
  (`RunWithEnv`) so existing fakes stay untouched, or accept the churn explicitly.
- **Token authentication is opt-in (impl review M1)** → the forge-clone lane reads a token
  ONLY when the operator names the env var (`source.forge.token_env`); an empty `token_env`
  is unauthenticated even if `GITHUB_TOKEN` is ambient — a stray token never silently
  authenticates a clone to whatever host `base_url` names. The clone error is a scrubbed
  summary (raw git stderr goes to the log, never the public block-reason comment — M4).
- **Named follow-ups (impl review LOWs, not blocking M2 dogfood)**: (a) per-run source
  clones under the clone base are not reaped after `Materialize` copies them — monotonic
  disk growth for a long-lived daemon; add a reap or a periodic sweep. (b) The clone relies
  on the ambient run context deadline with no dedicated clone timeout — a very large target
  could run long; add a bounded `context.WithTimeout` matching the I/O-timeout standard.

## Migration Plan

Additive. `StaticSource` and every e2e journey are unchanged; the forge-clone source and
the `launch` subcommand are new, opt-in by config. No data migration. Rollback = don't
configure the forge-target mode (fixture mode is the pre-change behavior). The one paid
live-forge delivery (forge-io 5.4) stays operator-gated after this lands.

## Open Questions

- **Clone depth for M2**: full vs `--depth`? Leaning full for small dogfood targets
  (simplest correct merge-base); make it a config knob if a large target appears.
- **Run-branch naming**: `semdev/run-<shortid>` vs deriving from the issue ref — settle
  in tasks; must be push-safe and collision-free across re-runs of the same issue.
- **Should the driver also support the fixture target** (launch a run against the local
  fixture without a webhook, for dev)? Cheap to allow (ref → fixture mode); decide in
  tasks whether the CLI is forge-only or mode-agnostic. NOTE: a fixture has no forge
  issue to read, so a "supported" resolution collides with the operator-launch req's "a
  bare ref with no content SHALL NOT be published" — a dev-fixture launch would have to
  supply the ask content some other way (a `--body`/`--body-file` flag), which then
  becomes the content source for that mode.
