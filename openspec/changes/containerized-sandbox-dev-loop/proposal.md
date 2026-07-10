## Why

The dev loop cannot produce honest evidence without a real, isolated,
reproducibility-proven sandbox — and today that sandbox does not exist. The M0
design (D5) deferred containerization to M2 and left the `Workspace` /
`Manifests` / `Attempts` seams nil, which silently recreates the exact failure
that killed both predecessors:

- **semspec** defaulted `SANDBOX_URL` unset → the worktree validator, commit
  guard, and verify gate all *skipped*, and the run stamped `"execution
  verified"` over **zero executions**. Its warm shared cache also masked
  fabricated dependency coordinates — a dead dependency resolved from the cache
  and every gate went green.
- **semteams** got the *shape* right (a single-component provisioner, routed on
  attestation facts, no second agentic team) but its attestation only proves the
  toolchain answers `--version`, over **shared warm caches reused across runs**;
  it never proves the project builds *cold*.

Neither donor ever proved a project builds cold in a fresh environment. That
cold-reproducibility proof is the load-bearing thing semdev must own. The
infrastructure is the make-or-break: **without a real sandbox, running a dev loop
at all is theater.** A local-folder stand-in on the developer's host is rejected
— it isolates nothing, runs untrusted agent-driven development on the host, and
proves none of the hard thing.

## What Changes

- **BREAKING (revises design D5):** the clean-room `Runner`'s **container
  implementation moves from M2 to M0**. Every run gets a per-run **containerized
  sandbox** — the simplest dockerized equal: `docker run` / `exec` / `rm` via
  `os/exec`, a **pinned official Go image**, **one fresh container + one fresh
  cache home per run**, the checkout bind-mounted. `LocalRunner` demotes to a
  unit-test / no-docker fallback shim, never the run path.
- A **provision-and-prove-cold station**, rule-triggered, runs **before** the
  dev loop: stand up the container, place the checkout, resolve + build against a
  **fresh (cold) cache**, and stamp attestation facts. This establishes a
  known-good green baseline so a later red is a real regression, not a
  cache-masked fabrication.
- **Fail closed (G2/G7):** if docker is unavailable, provisioning fails, or the
  cold proof fails, the run **parks toward the human**. Never the `sandbox==nil`
  silent skip that let semspec report success over nothing.
- **Warm within a run, cold at every gate:** dev-loop iterations share the
  provisioned sandbox's warm cache (speed); the prove-cold baseline and the
  **separate** final clean-room verify each use a fresh cold cache, so a
  fabrication introduced during the warm phase cannot survive the cold verify.
  Caches are **never shared across runs** (the semteams/semspec sin).
- A **`readiness` gate** (D8): the dev loop proceeds only once a sandbox tier has
  proven the claim; an unprovable claim parks, never fakes.
- An **`apply_patch` tool**: the developer authors the fix by emitting a diff;
  the harness applies it inside the sandbox and `measure_task` measures the
  **real** result (G3 — the model supplies the intelligence, the harness supplies
  the outcome). This is the missing code-authoring mechanism (semdev has none).
- The environment is an **operator-declared `Dockerfile` / `.devcontainer`**
  (a minimum-viable image, in the standard formats) committed to the target repo —
  **not a bespoke semdev DSL, and never harvested/inferred by semdev.** semdev
  builds it and proves it cold; the few semdev-specific run bits (test command,
  tier split, secret refs) ride a `customizations.semdev` block or convention.
- **Secrets governance (SB2c):** named creds-refs from a governed store, injected
  only via docker/devcontainer secret channels, never in the image layer, the
  artifact, logs, or attestation facts (G7).
- Wire the previously-nil seams: `Manifests` (resolve the operator-declared image
  + the few run fields), `Workspace` (resolve the run's container checkout),
  `Attempts` (resolve the developer's authored diff for the floors).
- A **Go fixture repo** (`go-health-class`) with a **real bug** (a failing
  `go test`) and a committed **`Dockerfile`/devcontainer** (`FROM golang:1.26`),
  so the whole arc is exercised against real code in a real container (G8).

## Capabilities

### New Capabilities
- `sandbox`: the per-run containerized, reproducibility-proven isolated-execution
  substrate — and the SOLE capability this change owns (all requirements
  `ADDED`, no `MODIFIED` deltas). It covers:
  - **image identification**: the container image a run needs is *declared by the
    operator* as a standard `Dockerfile`/`.devcontainer` committed to the target
    repo, and *proven cold* — never a semdev DSL, never harvested/inferred (the
    flaky path both donors struggled with; see design.md SB2/SB2b).
  - the container `Runner` (docker up/exec/down), the provision-and-prove-cold
    lifecycle, one profile instantiated at three points (cold baseline / warm dev
    iterations / cold final verify), per-run fresh cache homes never shared across
    runs, attestation facts, the readiness gate, and the fail-closed-on-absence
    contract;
  - **in-sandbox execution**: `apply_patch` (the developer authors the fix; the
    harness applies it in-container and measures the real result, G3), and the
    contract that `measure_task` / `check_floors` / `verify_artifact` all execute
    against the provisioned container checkout (via the wired `Workspace` /
    `Manifests` / `Attempts` seams).

  `dev-from-task` and `clean-room-verify` (defined in the sibling
  `m0-walking-skeleton-spine` change) **consume** this capability by reference —
  the dev loop runs in the sandbox, the final verify is a fresh cold instance of
  it — but this change introduces no delta against their specs, so it does not
  stack a `MODIFIED` delta on that not-yet-archived change. Their consumption is
  wiring (design + tasks), not a requirement change.

### Modified Capabilities
<!-- None. See the note above: all new behavior lands as ADDED requirements in
     the net-new `sandbox` capability; dev-from-task / clean-room-verify consume
     it by reference, avoiding a MODIFIED delta against the unarchived m0 change. -->

## Impact

- **Design decision D5 is reversed** (container Runner at M0). design.md records
  the revision and its rationale (the two-donor forensics).
- **New dependency at runtime:** docker (already required for NATS via compose) —
  now also for the per-run sandbox. Fail-closed when absent; unit tests use the
  `LocalRunner` / mock shims and need no docker.
- **New Go:** a container `Runner` implementation in `internal/cleanroom`; the
  `apply_patch` tool; the provisioning/attestation harness; the seam
  implementations (`Manifests` / `Workspace` / `Attempts`). Each needs a G1
  framework-alignment note + registry entry.
- **New rules:** the provision-and-prove-cold station and the readiness gate
  (rule-owned lifecycle, G2; every spawn self-extinguishing per the house
  restart-safety pattern).
- **New vocabulary:** sandbox attestation / readiness / attempt predicates,
  named in the `sandbox` spec delta (G9).
- **New fixture:** `test/fixtures/go-health-class` (a real Go repo) + its
  committed `Dockerfile`/devcontainer (G8).
- **Secrets governance:** a governed named-creds-ref store (git-ignored `.env` at
  M0) + injection plumbing (SB2c).
- **Depends on** `m0-walking-skeleton-spine` (the run-lifecycle spine,
  create_change/validate/approval/project_tasks arc, and the pure
  `verify.Decide` / `harness` / `cleanroom` libraries this change wires live).
