# semdev

> GitHub issue in → reviewed, clean-room-verified pull request out.

semdev is an agentic development system built on the
[semstreams](https://github.com/c360studio/semstreams) framework — rules,
personas, and facts as the primary programming surface, deterministic tools
where the constitution admits them, and **no bespoke lifecycle state
machine**.

It is the successor to semspec, taking the shape proven by semteams and the
verification floors semspec earned the hard way:

- **Two human gates**: approve the generated OpenSpec change; review the PR.
  Unattended and budget-bounded in between.
- **Harness-owned measurement**: no schema accepts an LLM-supplied outcome.
- **Clean-room verification** before any PR: fresh isolated environment,
  build from the artifact's own declarations, run its own tests.
- **Full audit trail**: every run's trajectory is captured durably;
  `semdev trajectory <run>` renders it as a self-contained static archive.
  Questions to humans surface as issue/PR comments.

## Read first

| Document | Purpose |
|----------|---------|
| [docs/brief.md](docs/brief.md) | Product brief, milestone ladder, non-claims |
| [docs/constitution.md](docs/constitution.md) | The ten guardrails and their enforcement pins |
| [docs/port-manifest.md](docs/port-manifest.md) | Precisely what enters from semteams/semspec — and what is banned |

## Status

Foundational — docs and OpenSpec scaffolding only. The M0 walking skeleton
(mock-LLM, fixture repo, full issue→PR arc) is the first change.
