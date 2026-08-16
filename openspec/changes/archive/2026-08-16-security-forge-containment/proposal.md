# security-forge-containment

## Why

Three verified containment gaps sit on the paths that touch a real forge, re-confirmed live on `main` 2026-08-16 (external-review findings #5/#6/#7, triaged 2026-07-21). (1) `apply_patch` commits with `git add -A`, so files outside the task contract reach the attempt commit — and the clean-tree floor *launders* residue across retries: attempt N's stray file fails the floor, attempt N+1 commits it uninspected, and that sha is what delivery pushes to a real PR. (2) Delivery puts the GitHub token on `git push` argv (visible to every process, re-exposed per station retry), with no `GIT_TERMINAL_PROMPT=0` and no deadline — while `clone.go` already refuses exactly this pattern. (3) The devcontainer image-build path joins repo-authored paths without traversal containment, letting `..` escape the checkout and read host files into the build context; `runspace.SafeJoin` exists and is unused here. All three gate any further live-forge run.

## What Changes

- **Commit containment (`internal/runspace/patcher.go`)**: the attempt commit stages only paths authorized by the task contract instead of `git add -A`; unauthorized residue in the tree is surfaced (fail toward the human / rejection), never silently committed. The clean-tree floor's false-ACCEPT direction (retry laundering) is closed and documented alongside its existing false-REJECT note (`checks.go`).
- **Credential containment on push (`internal/tools/openpr/delivery.go`)**: the push adopts clone's askpass pattern — token via environment, never argv — plus `GIT_TERMINAL_PROMPT=0` and a bounded deadline, so a paid run cannot hang on a credential prompt and the station retry loop no longer re-exposes the token.
- **Checkout confinement for image builds (`internal/cleanroom/image.go`)**: devcontainer-declared Dockerfile/context paths resolve through `runspace.SafeJoin` (the guard the read-side tools already share); `..` traversal outside the checkout fails closed into the existing no-image park.
- Each fix lands with its red-first offline pin for the exact failure shape (G6): a residue-laundering journey/pin, an argv-token pin, and a traversal pin.
- No new predicates and no new components anticipated — existing failure/park lanes carry the outcomes (named in the spec deltas if design finds otherwise).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `dev-from-task`: the attempt-commit contract tightens — the commit SHALL contain only contract-authorized paths, and the clean-tree requirement gains the false-ACCEPT (retry-laundering) scenario, not just the dirty-tree rejection.
- `forge-io`: pull-request delivery gains credential-containment requirements — no credential material on process argv, prompt-free non-interactive git, bounded push deadline.
- `sandbox`: the operator-declared-image requirement gains checkout confinement — repo-authored devcontainer paths SHALL NOT resolve outside the checkout; traversal fails closed to the park.

## Impact

- Code: `internal/runspace/patcher.go` (staging), `internal/runspace/checks.go` (clean-tree floor docs/behavior), `internal/tools/openpr/delivery.go` (+ its runner env plumbing — `OSRunner` already satisfies `EnvRunner`, so no new plumbing expected), `internal/cleanroom/image.go` (adopt `SafeJoin` from `internal/runspace/attempts.go`).
- Tests: new red-first pins per fix; existing journey suite must stay green (`task e2e -race`) — the residue scenario likely extends a bridge-proof journey.
- Dependencies: none. No semstreams surface is touched; the change lands before the beta.161 pin bump by design, keeping that migration diff clean.
- Out of scope (tracked elsewhere): the station panic-park bypass (#8b, station change), paid-run readiness (#4/#11/#12), evidence-ledger schema drift (#15), small fixes (#16).
