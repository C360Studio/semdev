# DR-0003 — The run contract is declared, sealed, and resolved before the run

- **Status**: accepted
- **Date**: 2026-08-26
- **Subject**: who decides what a run's clean-room environment is, and when
- **Supersedes / superseded by**: supersedes the `pin-declaration-drift` approach (PR #39, closed
  unmerged) — detection-and-park at the verify gate

## Context

semdev's clean room is only meaningful if the contract it proves against is fixed. Today it is not.

Measured on `main` at `7a22bc7`:

| Decision | Source | Resolved when | Model-editable |
|---|---|---|---|
| Ecosystem profile | `DetectProfile` over marker files | every resolution — provision *and* verify | yes |
| Container image | committed `Dockerfile` / devcontainer | every proof | yes |
| resolve/build/test commands | Go convention, else `customizations.semdev` | every resolution | yes |
| Cache homes, tiers, secret refs | same | same | yes |

`DetectProfile` has exactly one production caller (`internal/runspace/manifests.go:34`), inside
`Resolve`, which runs at provision **and** again at verify with no relationship between the two.
There is no profile override anywhere: no field on `Customizations`, no config key, no CLI flag. The
`semdev init` seam that would let a human declare one is referenced in a comment at
`internal/harness/manifest.go:175` and does not exist.

SB2 ("the operator declares; semdev never harvests, infers, or synthesizes") was written to prevent
semdev from inventing environments — the semspec grave — and it succeeded. But it located the
declaration *inside the mutable artifact*, which made "the operator" and "the work under test" the
same file.

Issue #38 is the proof, not the theory. Against real docker, source byte-identical to
`TestProveArtifactRealTestsFailIsFail` (which correctly proves `fail`) renders:

```
resolved TestCmd = [true]
verify outcome   = "pass"
```

The cold clean-room verify — the last gate before a PR — passed an artifact whose own tests fail,
because the committed devcontainer said so.

## Decision

**A run's contract is declared by a human, sealed for the run's duration, and resolved before the
run starts.**

1. **Declared.** A repo carries `.semdev/profile.yaml` naming its profile. Detection becomes a
   *check* against that declaration rather than the source of truth. Precedent:
   `.semdev/standards.yaml` (`internal/standards/standards.go:29`) is already a semdev-owned file
   committed to the target repo.
2. **Sealed.** The declaration surface — `.semdev/**`, `Dockerfile`, `.devcontainer/**` — may not
   change as a side effect of a run. `.semdev/` alone is insufficient: #38's vector was
   `devcontainer.json` and its quiet variant is a rewritten `FROM`, neither of which lives there.
3. **Pre-resolved and carried.** The contract is resolved once, before the dev loop, and carried as
   run facts. Later gates use the carried contract; nothing re-derives it from the artifact.
4. **Mismatch parks.** Declaration disagreeing with repo reality is a human decision, never an
   automatic reconciliation.

### Why sealing needs two enforcement points, not one

The developer spawn advertises exactly five tools —
`["query_entity", "read_workspace", "apply_patch", "measure_task", "ask_human"]`
(`configs/rules/dev-from-task/04-dispatch-developer.json:34`) — with no shell or exec, and since
beta.149 the executor rejects any call outside the advertised set. So `apply_patch` is the model's
only *tool* write path.

It is not the only write path. `measure_task` runs the repo's **declared** test command inside the
container with the checkout bind-mounted: arbitrary code with write access to the files being
sealed. Therefore:

- **`apply_patch` refuses** a write under the sealed set — loud, early, actionable, and the model
  already holds `ask_human` to escalate.
- **The commit boundary asserts zero delta** over the sealed set — a property of the committed
  result, independent of how the bytes arrived. This is the guard that actually holds.

Enforcing only at the tool boundary would cover the paths we remembered to instrument.

### Contract changes ride their own PR

The seal is not "never" — it is "never as a side effect of a code task." A task whose actual job is
to change the contract ("add a standard", "upgrade the toolchain") is a separate, human-reviewed
change. This is the same rule mature teams already apply to CI config and CODEOWNERS, and it is the
whole answer to *who decides*: a human does, visibly, in a diff that is about that decision.

## Consequences

**#29 is answered by chronology rather than containment.** Every warm-cache candidate in that issue
keeps a cache warm *during* a run and then argues that nothing the attempt authored can enter it.
That is a containment argument, and containment arguments are where #38 came from. With the profile
approved before the run, the image and its dependency cache can be built **before the attempt
exists** — so "nothing the attempt authored is in the cache" stops being a property to enforce and
becomes a fact about time. It is also ecosystem-independent: no `GRADLE_RO_DEP_CACHE` special case
is required to make it *sound* (only to make it *mount*).

**A pinned contract makes toolchain-upgrade tasks fail their own verify.** Accepted, and it is why
mismatch parks rather than fails: the park says "this task outgrew its environment," which is
actionable, where a build failure is merely confusing.

**Semdev cannot develop a repo that has not declared a profile.** Already true in practice — a repo
with no committed Dockerfile parks today, which is why `semdev-test` had to be seeded. This decision
makes the requirement explicit and gives it a human-approved surface.

## Alternatives considered

- **Detect and park at the verify gate** (PR #39, drafted then withdrawn). Smallest change, and it
  closes #38's proven hole. Rejected because it treats the last gate only: provisioning and the dev
  loop still re-derive the contract from editable state, so every gate added later needs the same
  guard bolted on. Detection also missed `Manifest.Profile` in its own digest — a hole of exactly
  the class it existed to close, which is itself evidence that per-gate detection is the wrong shape.
- **Re-prove the baseline on divergence and continue.** Keeps unattended runs moving and still forces
  the new environment through a real cold proof — but `testCommand: ["true"]` re-proves cold
  perfectly well. Catches a broken environment, not a weakened contract, which is the actual threat.
- **Split by field** — measurement fields park, environment fields re-prove. Closes the goal-seeking
  path while letting dependency work proceed, at the cost of a boundary that must be maintained and
  that an attacker need only be right about once. Recorded as the documented follow-up if
  park-on-any-divergence proves too coarse; it is strictly loosening, so it can be adopted later
  without reopening the hole.
- **Move the declaration out of the target repo** into semdev-side per-target config. Genuinely not
  co-mutable, but semdev would hold per-repo knowledge, could not touch an unconfigured repo, and the
  repo would stop being self-describing — giving up the SB2 property that the declaration is portable
  and belongs to the repo. Rejected: pre-resolution plus sealing already answers co-mutability.
- **Refuse declaration edits at `apply_patch` only.** Insufficient — see `measure_task` above.
