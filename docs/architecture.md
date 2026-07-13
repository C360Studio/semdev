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
| `run.dev_kickoff` | dev-rewake-rule | dev-from-task |
| `run.projection_kickoff` | dev-projection-rule | dev-from-task |
| `pr.ref` | open-pr | forge-io |
| `openspec.change.*` | create-change-author-tool | openspec-io |
| `openspec.spec.*` | brownfield-spec-projector | openspec-io |
| `openspec.validated` | openspec-validate-harness | openspec-io |
| `openspec.archived` | openspec-archive-harness | openspec-io |
| `task.spec.*` | task-projector | dev-from-task |
| `task.attempt.*` | dev-measure-rule | dev-from-task |
| `floor.finding.*` | floor-tools | dev-from-task |
| `measurement.result.*` | measurement-harness | harness-measurement |
| `review.verdict.*` | reviewer-quinn | harness-measurement |
| `verify.result` | verify-harness | clean-room-verify |
| `evidence.run` | evidence-ledger | evidence-ledger |
| `sandbox.provisioned` | sandbox-provision-rule | sandbox |
| `sandbox.ready` | sandbox-provisioner | sandbox |
| `sandbox.blocked` | sandbox-provisioner | sandbox |
| `sandbox.attestation.*` | sandbox-provisioner | sandbox |
| `dev.dispatched` | dev-dispatch-rule | dev-from-task |
| `dev.measured` | dev-measure-rule | dev-from-task |
| `dev.measure_done` | measurement-harness | dev-from-task |
| `dev.floors_dispatched` | dev-floors-rule | dev-from-task |
| `dev.floors_done` | floor-tools | dev-from-task |
| `dev.gate.*` | gate-tools | dev-from-task |
| `dev.gate_decision` | gate-tools | dev-from-task |
| `dev.gate_dispatched` | dev-gate-rule | dev-from-task |
| `dev.routed` | dev-route-rule | dev-from-task |
| `dev.task_cleared.*` | dev-route-rule | dev-from-task |
| `dev.review_dispatched` | dev-review-rule | dev-from-task |
| `dev.reviewed` | reviewer-quinn | dev-from-task |
| `dev.verify_dispatched` | dev-verify-rule | dev-from-task |
| `dev.verified` | verify-harness | dev-from-task |
| `pr.coherence.*` | coherence-tools | forge-io |
| `dev.coherence_decided` | coherence-tools | dev-from-task |
| `dev.coherence_dispatched` | dev-coherence-rule | dev-from-task |
| `dev.pr_routed` | dev-pr-route-rule | dev-from-task |

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
| `measure_task` | tool | harness-measurement | `measurement-tool` |
| `submit_review` | tool | harness-measurement | `submit-review-tool` |
| `verify_artifact` | tool | clean-room-verify | `verify-artifact-tool` |
| `check_floors` | tool | dev-from-task | `floor-tools-wrapper` |
| `check_gate` | tool | dev-from-task | `check-gate-tool` |
| `check_coherence` | tool | dev-from-task | `check-coherence-tool` |
| `open_pr` | tool | forge-io | `open-pr-tool` |
| `provision_sandbox` | tool | sandbox | `provision-sandbox-tool` |
| `apply_patch` | tool | sandbox | `apply-patch-tool` |

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
