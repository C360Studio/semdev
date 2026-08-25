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

Out of scope: shipping the JVM convention itself, and the cold-cache cost that
makes it infra-hard (issue #29, which this unblocks).

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
