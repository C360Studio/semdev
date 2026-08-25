# What a target repo declares

semdev develops a repo it does not own. Everything it needs to build, measure, and
cold-prove that repo comes from files the repo itself **commits** — semdev never
harvests, infers, or synthesizes a toolchain (SB2). This is the whole contract.

## 1. The environment — a committed Dockerfile / devcontainer

Commit a `Dockerfile` and/or `.devcontainer/devcontainer.json`. semdev builds that
image and pins its digest. A repo that declares no usable image parks toward the
operator; semdev never guesses one. Paths inside the declaration must resolve
**inside** the checkout — a traversal path fails closed exactly like no image at all.

The image is *not* declarable in the `customizations.semdev` block below. The
Dockerfile owns the environment; that block carries run fields only.

## 2. The run fields — `customizations.semdev`

Fields semdev cannot infer ride a `customizations.semdev` block in
`.devcontainer/devcontainer.json`. Anything you leave unset falls back to your
profile's built-in convention. Today only Go ships one, so a non-Go repo declares
all five run fields itself.

```jsonc
{
  "customizations": {
    "semdev": {
      "resolveCommand": ["./gradlew", "--no-daemon", "dependencies"],
      "buildCommand":   ["./gradlew", "--no-daemon", "assemble"],
      "testCommand":    ["./gradlew", "--no-daemon", "test"],
      "cacheHomeEnvs":  ["GRADLE_USER_HOME"],
      "secretRefs":     ["GH_PACKAGES"],
      "tiers": [{ "name": "unit", "scope": "sandbox", "proves": ["unit"] }]
    }
  }
}
```

| Key | What it is |
|---|---|
| `resolveCommand` | Resolves base dependencies. Run first in every cold proof. |
| `buildCommand` | Builds the artifact cold. The baseline proof is resolve+build, not tests — the task's own test may not exist yet. |
| `testCommand` | Runs the artifact's own tests. The final clean-room verify. |
| `cacheHomeEnvs` | **The package-cache env vars semdev freshens per proof.** See below. |
| `secretRefs` | Governed creds-ref **names** needed to resolve dependencies. Names only — a value never rides a manifest, log, or fact. |
| `tiers` | The sandbox / operator-CI split the readiness gate reads. |

A command may be a JSON array (literal argv, no shell) or a single string, which
becomes `sh -c "<cmd>"`. A blank string counts as **absent**, not as a command that
trivially succeeds.

### `cacheHomeEnvs` — the field that makes a cold proof mean anything

semdev mints a fresh cache home for each name before every proof, so a dependency
that only resolves from an accumulated cache **fails**. That is the property the whole
clean room exists for: without it, a fabricated dependency can pass.

So name the variables your ecosystem actually reads:

| Ecosystem | Typical |
|---|---|
| Go | `GOMODCACHE`, `GOCACHE` (the built-in convention) |
| Gradle / JVM | `GRADLE_USER_HOME` |
| Node | `npm_config_cache` |
| Python | `PIP_CACHE_DIR` |

Each entry must be a plain environment variable name (`[A-Za-z_][A-Za-z0-9_]*`). A
malformed one is rejected at resolution rather than mounted: an entry like
`"GRADLE_USER_HOME=/tmp/x"` or `"  "` would otherwise set a variable nothing reads
and leave the **real** cache home warm — a proof that reports itself cold and is not.
Repeated names are collapsed.

> ⚠ semdev cannot verify that a *well-formed* name is the *right* one. Declaring
> `NOT_A_REAL_VAR` yields a proof that freshens nothing your build uses. Check the
> verdict's isolation detail, which names the variables it freshened.

If a JVM/Gradle repo also needs its Gradle daemon suppressed, put `--no-daemon` in the
commands: a daemon surviving between proofs is warm state, the same way a cache is.

## 3. Failing closed

Manifest resolution reports **every** missing required run field at once, naming the
exact keys to add. If you get a message about a field you believe you declared, check
it is inside `customizations.semdev` and spelled as above.

Related: [`openspec/specs/sandbox/spec.md`](../openspec/specs/sandbox/spec.md) is the
authoritative capability spec; `internal/harness` is the implementation.
