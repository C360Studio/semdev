# Give the cache-home control a declaration surface

## Why

`CacheHomeEnvs` is the G4 control: it names the package-cache homes the harness
freshens per proof, "so a warm module cache cannot mask a fabricated dependency"
(`internal/harness/manifest.go:97-99`). Only `ProfileGo` ships a convention that
populates it (`manifest.go:141-148`), and the `customizations.semdev` overlay sets
five fields — resolve, build, test, tiers, secretRefs — but **not** that one
(`internal/harness/customizations.go:127-142`).

So a Gradle/Node/Python repo detects its profile correctly, declares every command
it can declare, and is still rejected at `internal/coldproof/baseline.go:60` with

```
coldproof: manifest for profile "jvm" is incomplete (needs resolve, build,
and cache-home fields) — declare the run fields (SB2)
```

The operator is told to declare a field that has no declaration surface. Failing
closed is right; the instruction is impossible to follow. A profile that cannot
name its cache homes cannot be clean-room proven at all, so this is the blocking
item for every non-Go profile — not a wording bug.

## What Changes

- `Customizations` gains `cacheHomeEnvs`, symmetric with the other five overlay
  fields; `ResolveManifest` overlays it.
- `ResolveManifest` fails **closed** when the resolved manifest names no cache
  home, at the declaration boundary where the operator can act, rather than
  letting a G4-unprovable manifest travel to the prover. It joins the existing
  "no declared image" and "no test command" guards.
- The `coldproof` incompleteness errors name **which** fields are missing and
  point at the declaration surface, instead of listing all three every time.
  They stay as defense in depth.
- No convention changes: the Go path is byte-identical, because `GoProfile`
  already supplies the field and an unset customization keeps the convention.

- Cache-home names are validated at the same boundary: a malformed entry
  (`""`, `"  "`, `"../../etc"`, `"GRADLE_USER_HOME=/tmp/x"`) is rejected and a
  repeated one collapsed. Adversarial review proved `["  "]` clears every
  `len(...) == 0` guard, mounts at `/caches/␠␠`, and yields a **PASSING isolation
  check while the real cache home stays warm for both proofs** — a false green on
  the G4 control itself.
- The cold-proof evidence now names the env vars it freshened, not just the
  anonymous volume IDs, so a wrong-but-well-formed declaration is visible in the
  verdict (G7).

Out of scope: shipping the JVM convention itself, and the cold-cache cost that
makes it infra-hard (issue #29, which this unblocks).

## Residual risk

`cacheHomeEnvs` is now operator-supplied and, like the other run fields, is read
from the **committed** artifact at verify time — so it is reachable by the model
that authored the change. Unlike the others it governs the isolation *guardrail*
rather than what is measured: a well-formed name that no ecosystem reads
(`NOT_A_REAL_VAR`) produces a proof that freshens nothing. semdev cannot know every
ecosystem's cache variable, so this is accepted, not closed. Two mitigations ship
here — malformed names are rejected, and the verdict names what was freshened, so
the condition is legible in the evidence rather than silent. The broader class (a
model editing its own declared run fields) is pre-existing and unchanged by this
change.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `sandbox`: the cache-home control is operator-declarable, and a manifest that
  names no cache home fails closed at resolution.

## Impact

- **Code/tests:** `internal/harness/customizations.go`, `internal/coldproof/baseline.go`,
  and their offline pins. No rule, config, fact, or component surface changes.
- **Operators:** a non-Go repo can declare `cacheHomeEnvs` in its
  `customizations.semdev` block and be cold-proven today.
- **Issues:** closes #28; unblocks #29.
