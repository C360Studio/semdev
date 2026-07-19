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
| `intake.actor.login` | admission-check | forge-io |
| `intake.actor.admitted` | admission-check | forge-io |
| `run.issue.ref` | issue-intake-adapter | forge-io |
| `run.change.approved` | approval-adapter | forge-io |
| `human.opt.signal` | comment-adapter | forge-io |
| `run.awaiting.human` | park-rule | run-lifecycle |
| `run.dev.kickoff` | dev-rewake-rule | dev-from-task |
| `run.projection.kickoff` | dev-projection-rule | dev-from-task |
| `delivery.pr.ref` | open-pr | forge-io |
| `openspec.change.document` | create-change-author-tool | openspec-io |
| `openspec.change.slug` | create-change-author-tool | openspec-io |
| `openspec.change.revision` | create-change-author-tool | openspec-io |
| `openspec.change.authored` | create-change-author-tool | openspec-io |
| `openspec.change.validated` | openspec-validate-harness | openspec-io |
| `openspec.change.archived` | openspec-archive-harness | openspec-io |
| `openspec.spec.document` | brownfield-spec-projector | openspec-io |
| `task.spec.goal` | task-projector | dev-from-task |
| `task.spec.budget` | task-projector | dev-from-task |
| `task.spec.assumptions` | task-projector | dev-from-task |
| `task.spec.non-goals` | task-projector | dev-from-task |
| `task.spec.target-files` | task-projector | dev-from-task |
| `task.spec.test-command` | task-projector | dev-from-task |
| `task.attempt.instance` | dev-dispatch-rule | dev-from-task |
| `attempt.commit.sha` | patch-committer | sandbox |
| `floor.finding.rejected` | floor-tools | dev-from-task |
| `floor.finding.detail` | floor-tools | dev-from-task |
| `floor.finding.attempt` | floor-tools | dev-from-task |
| `measurement.result.passed` | measurement-harness | harness-measurement |
| `measurement.result.command` | measurement-harness | harness-measurement |
| `measurement.result.commit` | measurement-harness | harness-measurement |
| `measurement.result.ran` | measurement-harness | harness-measurement |
| `measurement.result.exit-code` | measurement-harness | harness-measurement |
| `measurement.result.timed-out` | measurement-harness | harness-measurement |
| `review.verdict.value` | reviewer-quinn | harness-measurement |
| `review.findings.value` | reviewer-quinn | harness-measurement |
| `verify.cleanroom.result` | verify-harness | clean-room-verify |
| `evidence.ledger.run` | evidence-ledger | evidence-ledger |
| `sandbox.provision.marker` | sandbox-provision-rule | sandbox |
| `sandbox.provision.ready` | sandbox-provisioner | sandbox |
| `sandbox.provision.blocked` | sandbox-provisioner | sandbox |
| `sandbox.attestation.image` | sandbox-provisioner | sandbox |
| `sandbox.attestation.tier` | sandbox-provisioner | sandbox |
| `dev.developer.dispatched` | dev-dispatch-rule | dev-from-task |
| `dev.floors.dispatched` | dev-floors-rule | dev-from-task |
| `route.attempt.passed` | route-mirror | dev-from-task |
| `route.attempt.rejected` | route-mirror | dev-from-task |
| `route.review.verdict` | route-mirror | dev-from-task |
| `route.attempt.instance` | route-mirror | dev-from-task |
| `route.task.budget` | route-mirror | dev-from-task |
| `route.attempt.unclean` | dev-route-rule | dev-from-task |
| `route.attempt.routed` | dev-route-rule | dev-from-task |
| `delivery.route.routed` | dev-route-rule | dev-from-task |

## Components

semdev's own Go components and tools (G1). The framework's components are not
listed — only what semdev adds, each with a framework-alignment note
(`docs/alignment-notes.md`). Mirrors `internal/registry.Entries`, pinned to it by
the G10 census (`TestDocsComponentsMatchRegistry`).

| Name | Kind | Capability | Alignment note |
|------|------|------------|----------------|
| `delivery-station` | component | forge-io | `deterministic-station-component` |
| `projection-station` | component | dev-from-task | `deterministic-station-component` |
| `validation-station` | component | openspec-io | `deterministic-station-component` |
| `floors-station` | component | dev-from-task | `deterministic-station-component` |
| `verify-station` | component | clean-room-verify | `deterministic-station-component` |
| `provision-station` | component | sandbox | `deterministic-station-component` |
| `create_change` | tool | openspec-io | `create-change-author-tool` |
| `render_openspec` | tool | openspec-io | `render-openspec-hydrate-tool` |
| `write_change` | tool | openspec-io | `write-change-workspace-tool` |
| `github_list_comments` | tool | forge-io | `github-list-comments-tool` |
| `measure_task` | tool | harness-measurement | `measurement-tool` |
| `submit_review` | tool | harness-measurement | `submit-review-tool` |
| `apply_patch` | tool | sandbox | `apply-patch-tool` |
| `read_workspace` | tool | dev-from-task | `read-workspace-tool` |
| `read_diff` | tool | dev-from-task | `read-diff-tool` |

The rest of the M0 arc is rule packs, persona fragments, and reused framework
tools (no semdev Go component); the ingest projector (`brownfield-spec-projector`)
and archive oracle are library/design surfaces whose registered components land at
group 11 / M1 and join this table then. The `evidence-ledger` writer is likewise
reserved: its schema and G7 pins live in `internal/ledger`, and recorded entries
live in [docs/evidence-ledger.md](evidence-ledger.md) until the graph writer
lands at M1.

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
