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

## Notes

_None yet._ The M0 walking-skeleton spine adds no Go components — the arc is rule
packs, persona fragments, and reused framework tools. The first notes land when a
later capability group introduces G1-gated Go.
