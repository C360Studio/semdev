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

## render-openspec-hydrate-tool

- **Primitive considered:** a rule that reads `$entity.triple.openspec.change.*`
  and `publish`es a rendered document to an `output/file` component (the D1
  "rule→publish→output/file" hydrate path).
- **Why it cannot express this:** rendering a change means reconstructing the
  nested OpenSpec model (proposal, per-capability deltas, ordered tasks) from a
  flat fact set and re-serializing it to canonical markdown — the inverse of the
  format engine's `Facts()` (`ChangeFromFacts` + `RenderChangeFolder`). A rule's
  `$`-templating substitutes single predicate values into a fixed string; it
  cannot re-group deltas by capability, order tasks by index, or JSON-decode the
  scenario arrays. That reconstruction is deterministic Go in `internal/openspec`;
  the tool is the thin read adapter (a `changefacts.Reader`, query-only) that
  hands the run's facts to it. Read-only: it stamps no facts (no G5 writer) and
  its schema takes only the change slug (G3, trivially).
- **Registry entry:** `render_openspec` (`tool`)
- **Change:** m0-walking-skeleton-spine

## write-change-workspace-tool

- **Primitive considered:** a rule that reads `$entity.triple.openspec.change.*`
  and `publish`es to an `output/file` component to drop the change folder on disk.
- **Why it cannot express this:** materializing an OpenSpec change is a multi-file
  filesystem write — `proposal.md`, `tasks.md`, and one `specs/<capability>/spec.md`
  per delta — reconstructed from the flat fact set (`ChangeFromFacts`) and
  serialized by the format engine's `WriteChange`, which also prunes stale managed
  files. A rule's single-value `publish` cannot re-group deltas by capability, walk
  a variable set of capability files, or drive the authoritative directory
  rewrite. It is a graph-READ tool (a `changefacts.Reader`) plus the deterministic
  `WriteChange`; it stamps no facts (no G5 writer) and its schema takes only the
  change slug (G3). WHERE it writes is the run's checkout, resolved through an
  injected `WorkspaceResolver` seam rather than any workspace state of its own (B1).
- **Registry entry:** `write_change` (`tool`)
- **Change:** m0-walking-skeleton-spine

## brownfield-spec-projector

- **Primitive considered:** a rule/persona that reads a target repo's
  `openspec/specs/` and an LLM that interprets the artifacts into facts on the
  ingest path.
- **Why it cannot express this:** ingest MUST be deterministic and model-free
  (G3) — a model interpreting the artifacts is exactly the LLM-supplied-fact class
  the constitution forbids on the measurement/ingest path. Parsing OpenSpec
  markdown into the nested spec model and projecting it to `openspec.spec.*` facts
  is the format engine's `ParseSpec`/`Facts()` (ported, dep-free); the projector
  is the thin deterministic Go that walks the specs tree, applies the single
  owner, and retains each source file's raw bytes by content-hash reference for
  provenance. No rule can walk a directory, hash bytes, or re-group a spec's
  requirements. It is the single G5 writer of `openspec.spec.*`.
- **Registry entry:** none yet — at M0 this is the library projector core with its
  red-first pins; its registered ingest component (which wires it onto the raw
  lane and stamps the facts on their spec entities) and the matching
  `registry.Entries` entry land with the runtime boot path (group 11).
- **Change:** m0-walking-skeleton-spine
