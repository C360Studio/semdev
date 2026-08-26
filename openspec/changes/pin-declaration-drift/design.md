# Design — pin the declaration the baseline proved

## D1. Attest the declaration, do not freeze it

A blanket freeze is wrong: "add dependency X" is a legitimate task that must
change the Dockerfile. The run therefore records what it proved and detects
movement, rather than forbidding it. This mirrors how the sandbox already
attests `sandbox.attestation.image` and `.tier` — the new fact joins that
package under the same writer (sandbox-provisioner, G5).

## D2. What the digest covers

Both halves of the declaration, because each can weaken measurement alone:

- the **resolved run contract** — `ResolveCmd`, `BuildCmd`, `TestCmd`,
  `CacheHomeEnvs`, `Tiers`, `SecretRefs`. `testCommand: ["true"]` is the blunt
  attack; `cacheHomeEnvs: ["NOT_A_REAL_VAR"]` is the quiet one.
- the **declaration source bytes** — the committed `Dockerfile` and
  `devcontainer.json`. `ImageDecl` names a path, not content, so a rewritten
  `FROM` leaves the resolved contract identical. Hashing the resolved manifest
  alone would miss it.

The digest is computed by `internal/harness` so the provisioner and the verify
compute it identically and cannot drift apart — the same reason `AttemptID`
is shared between its writer and its readers.

## D3. Drift is not a verdict

On mismatch the verify stamps `verify.declaration.drift` and stamps **no**
`verify.result`. This is the load-bearing choice:

- a **Fail** asserts the artifact is bad. That would be a lie — the artifact was
  never measured under the contract that was proven.
- a **Pass** is the bug this change exists to close.
- **no verdict** is the honest state: the run cannot be decided by the harness,
  so it goes to the human (G7 — evidence says what it knows).

The dev loop does not proceed on an unstamped verdict, so this is fail-closed
by the same mechanism that already handles a missing readiness attestation.

## D4. The park is rule-fired

G2: product Go never fires a lifecycle transition. The verify tool stamps a
fact; a park rule in `configs/rules/run-lifecycle/` reads it and records
`run.awaiting.human`. It follows `05-park-station-failure-run.json` exactly —
park-first action ordering (each rule action is its own KV revision, so a
marker-first crash window would strand a permanent stall), a fired-once marker
because `add_triple` appends unconditionally, and a `publish` to the exact
`semdev.park-post.request` lane so the human sees it on the issue thread.

## D5. Every divergence parks (operator decision, 2026-08-26)

Alternatives considered and rejected:

- **Re-prove the baseline and continue.** Keeps unattended runs moving and
  still forces the new environment to survive a real cold proof — but
  `testCommand: ["true"]` re-proves cold perfectly well. It catches a broken
  environment, not a weakened contract, which is the actual threat.
- **Split by field** (measurement fields park, environment fields re-prove).
  Closes the goal-seeking path while letting dependency work proceed, at the
  cost of a judgement call about which field is which — a boundary that has to
  be maintained and that an attacker only has to be right about once.
- **Reject at apply_patch.** Simplest and strictest, but semdev could then
  never do a task that changes the build environment.

The operator chose to park on any divergence. The cost is accepted and named:
a legitimate environment-changing task needs a human acknowledgement before it
can finish. If that proves too coarse in practice, the field split is the
documented next step — and it is a strictly-loosening change, so it can be made
later without re-opening the hole.

## D6. Where the tripwire goes

The #38 tripwire flips from gap-open to regression guard: same fixture, the
assertion inverted to require that no `pass` verdict is rendered. Per the
MEDIUM-3 lesson, a gap-open tripwire is a re-check hint and not a guarantee —
so the guard asserts the DRIFT FACT is stamped, not merely that pass is absent.
Absence of a pass is also what a crashed run looks like.
