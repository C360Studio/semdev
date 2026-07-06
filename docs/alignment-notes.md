# Framework-Alignment Notes (G1)

Every Go component or tool semdev adds must first clear the primitive-first gate
(constitution G1): before writing Go, prove a rule, a persona, a fact, or an
existing semstreams component cannot express the behavior. This file is the
record of that proof — one section per entry in `internal/registry`
(`registry.Entries`). The G1 conformance pin fails the build if a semdev-added
component has no note here, so the note is not optional documentation; it is part
of the addition.

Ported and hardened from semteams' tool-accretion review discipline
(`cmd/semteams/tools/README.md`): semteams kept it as review culture; semdev pins
it.

## Note format

Each note is a level-2 heading whose text is the `AlignmentNote` anchor of its
registry entry, followed by:

- **Primitive considered** — the rule / persona / fact / existing component that
  was evaluated first.
- **Why it cannot express this** — the specific gap that forces Go.
- **Registry entry** — the `registry.Entries` name and kind this note backs.
- **Change** — the OpenSpec change slug that introduced it.

Template:

```markdown
## <anchor>

- **Primitive considered:** …
- **Why it cannot express this:** …
- **Registry entry:** `<name>` (`component` | `tool`)
- **Change:** <change-slug>
```

## create-change-author-tool

- **Primitive considered:** a rule that authors the OpenSpec change directly, or
  the reused framework `decide`/agentic tools.
- **Why it cannot express this:** authoring a change means turning a model's
  structured content into the full `openspec.change.*` fact set via the OpenSpec
  format engine (`internal/openspec`), then stamping it atomically on the run
  entity. That is a deterministic mapping + a graph write no rule or generic tool
  performs; the engine mapping is code (ported, dep-free), and the tool is the
  single G5 writer of `openspec.change.*`. Its schema takes content only (G3).
- **Registry entry:** `create_change` (`tool`)
- **Change:** m0-walking-skeleton-spine
