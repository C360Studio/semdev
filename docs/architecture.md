# semdev Architecture (topology)

This document describes semdev's runtime topology — its fact vocabulary, its
own components, and its action taxonomy. Per G10 (docs tell the truth), the
tables here are **pinned to the code registries**: the G10 conformance test
compares them against `internal/vocab` and `internal/registry` and fails the
build on drift. Do not hand-edit a table out of sync with the code; change the
registry and the table together.

## Fact vocabulary

The complete predicate vocabulary (G9), each with its single writer (G5) and the
capability that owns it. Mirrors `internal/vocab.Predicates`.

| Predicate | Writer | Capability |
|-----------|--------|------------|
| `intake.actor` | admission-check | forge-io |
| `intake.admitted` | admission-check | forge-io |
| `run.issue_ref` | issue-intake-adapter | forge-io |
| `run.change_approved` | approval-adapter | forge-io |
| `human.signal` | comment-adapter | forge-io |
| `run.awaiting_human` | park-rule | run-lifecycle |
| `pr.ref` | pr-delivery-adapter | forge-io |
| `openspec.change.*` | create-change-author-tool | openspec-io |
| `openspec.spec.*` | brownfield-spec-projector | openspec-io |
| `openspec.validated` | openspec-validate-harness | openspec-io |
| `openspec.archived` | openspec-archive-harness | openspec-io |
| `task.spec.*` | task-projector | dev-from-task |
| `task.attempt` | dev-loop-harness | dev-from-task |
| `floor.finding` | floor-tools | dev-from-task |
| `measurement.result` | measurement-harness | harness-measurement |
| `review.verdict` | reviewer-quinn | harness-measurement |
| `verify.result` | verify-harness | clean-room-verify |
| `evidence.run` | evidence-ledger | evidence-ledger |

## Components

semdev's own Go components and tools (G1). The framework's components are not
listed — only what semdev adds, each with a framework-alignment note
(`docs/alignment-notes.md`). Mirrors `internal/registry.Entries`, pinned to it by
the G10 census (`TestDocsComponentsMatchRegistry`).

| Name | Kind | Capability | Alignment note |
|------|------|------------|----------------|
| `create_change` | tool | openspec-io | `create-change-author-tool` |
| `render_openspec` | tool | openspec-io | `render-openspec-hydrate-tool` |
| `write_change` | tool | openspec-io | `write-change-workspace-tool` |
| `validate_change` | tool | openspec-io | `validate-change-cli-oracle` |
| `github_list_comments` | tool | forge-io | `github-list-comments-tool` |
| `project_tasks` | tool | dev-from-task | `project-tasks-tool` |

The rest of the M0 arc is rule packs, persona fragments, and reused framework
tools (no semdev Go component); the ingest projector (`brownfield-spec-projector`)
and archive oracle are library/design surfaces whose registered components land at
group 11 / M1 and join this table then.

## Action taxonomy

The closed action taxonomy (T1), mirroring the proven OpenSpec lifecycle wrapped
with semdev's human gates and delivery:

| Action | OpenSpec checkpoint | Role |
|--------|---------------------|------|
| `issue_intake` | — (front door) | semdev |
| `create_change` | `new` | OpenSpec core |
| `dev_from_task` | `apply` | OpenSpec core |
| `verify` | — (clean-room outcome, G4) | semdev floor |
| `open_pr` | — (delivery) | semdev |
| `ask_human` | — (HITL) | semdev |
| `respond` | — (HITL) | semdev |
| `archive_change` | `archive` | OpenSpec core (M1-wired) |
