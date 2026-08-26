# Design — sealed-run-contract

DR-0003 carries the decision and the rejected alternatives. This file is the
implementation shape.

## D1. What `.semdev/profile.yaml` declares

The **profile only** — the ecosystem identifier, plus a schema version:

```yaml
version: 1
profile: jvm
```

Run fields stay in `customizations.semdev`. That block is a devcontainer-standard
extension point; moving it into `.semdev/` would cost portability and buy nothing
once the whole declaration surface is sealed as a set (D4). Two files, one sealed
set, minimal new vocabulary (G9).

A repo with no `.semdev/profile.yaml` parks toward the human. This is a breaking
requirement for every target repo — see D6.

## D2. What "mismatch" means

Exactly one check, at provisioning: `DetectProfile(files)` must agree with the
declared profile. Both directions are a mismatch and both park:

- declared `jvm`, detection says `go` — a marker file appeared, or the declaration
  is stale;
- declared `jvm`, detection finds nothing — the repo lost its markers.

Detection stops being the source of truth and becomes the check. That is the whole
inversion: today the marker file decides and no human can disagree; after this, the
human decides and the marker file gets to object.

Whether the declared *commands* work is not this check — that is the cold proof,
which already exists and already parks.

## D3. Carrying the contract

Provisioning resolves the contract once and stamps `sandbox.contract.resolved` on
the run: the fully-resolved manifest, serialized, as the fact's object. Writer is
sandbox-provisioner, joining the existing attestation package (G5).

Later gates **read that fact** rather than re-resolving. `verify_artifact` stops
calling `Manifests.Resolve` on the clone; it proves the committed artifact under
the carried contract. This is the difference from the withdrawn #39: drift is not
detected, it is structurally impossible, because there is only ever one resolution.

One fact, not two — an identity digest is derivable in Go wherever it is needed,
and no rule condition reads the contract's contents (rules fire on violation facts,
not on the contract). Adding a digest predicate would be vocabulary for nobody.

A gate that finds NO carried contract fails closed and parks. An absent contract
must never fall back to re-resolving; that is the hole this change closes.

## D4. The sealed set and its two enforcement points

The set is the declaration surface, shared as one definition so both enforcement
points cannot drift apart (the same reason `AttemptID` is shared):

```
.semdev/**        Dockerfile        .devcontainer/**
```

`.semdev/` alone is insufficient: #38's vector was `devcontainer.json`, and the
quiet variant is a rewritten `FROM`. Neither lives under `.semdev/`.

- **`apply_patch` refuses** a target path in the set, returning a tool error naming
  the path and the reason. The model holds `ask_human` and can escalate. This is
  the loud, early, actionable guard.
- **The commit boundary asserts zero delta** over the set —
  `git diff --name-only refs/semdev/base..<attempt>` must contain no sealed path. A
  violation stamps a fact and parks; the attempt is not measured.

Two points because `measure_task` runs the repo's **declared** test command inside
the container with the checkout bind-mounted — arbitrary code with write access to
the very files being sealed. The tool boundary covers the paths we instrumented;
the commit boundary is a property of the committed result and covers the rest.

## D5. Ordering

Profile check → contract resolution → cold proof → dev loop. All of it at
provisioning, which is the first point a checkout exists and is before any model
turn touches code. "Before the run" in the sense that matters: nothing the model
does can influence the contract it is measured against.

## D6. Migration — this breaks every existing target

Every target repo needs `.semdev/profile.yaml`. In-tree that means both fixtures
(`go-health-class`, `go-health-class-fabricated`) and, out of tree, `semdev-test`
before its next live run. The e2e journeys will park without it, which is the
correct failure and should be verified as such rather than papered over.

No back-compat fallback to detection ships. A fallback would mean the hole stays
open for any repo that has not migrated, and "we only guess when the human was
silent" is precisely the shape #38 exploited.

## D7. What this deliberately does not do

It does not stop the model from *proposing* a declaration change — it stops the
change from taking effect in the run that proposed it. A contract change rides its
own human-reviewed PR (DR-0003). If a task genuinely needs a new environment, the
park says so and a human decides, which is the intended cost of the operator's
park-on-any-divergence choice.
