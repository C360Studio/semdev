# standards-via-lessons

## Why

A target repo has no channel to tell semdev's runs "in this repo, X is a must" — Quinn reviews against the change spec and the fixed floors alone, and M1 run 1's includes-test gap showed the failure class: an enforced-but-uncommunicated standard. semspec solved this with SOP machinery whose *mechanism* (standards.json→graph write-through) is B1-adjacent and stays dead, but whose *value* was left behind by omission — the port-manifest neither ports nor bans it. The framework now carries the native home: the ADR-080 lesson substrate survived beta.160 intact (contract-bound `LessonCurator`, pure `lessonmatch` selector, brief-assembly injection already wired in every semdev spawn — currently listing zero records), and its curator explicitly sanctions a product auto-promotion policy. This change brings the standards value back in semdev's idiom and is simultaneously the deferred lesson-substrate adoption (user decision 2026-08-11: adopt after the final wave; the wave is over).

## What Changes

- **Declaration**: a target repo declares standards in one small structured file (`.semdev/standards.yaml` — exact format pinned in design): per standard an id, terse text, `must|should|may`, optional role scoping; plus a `checks` section of deterministic commands. Human-authored, versioned with the repo, PR-reviewable — "your AGENTS.md, with teeth."
- **Birth (T5 slot)**: a deterministic provision-time step reads the file and births each standard as an `agent.lesson.record` entity (framework-canonical predicates — no bulk vocab, B2) with content-derived idempotent IDs, citing the standards-file source entity as its evidence (provenance = semspec's `origin: sop:<file>` reborn, honestly satisfying the substrate's ≥1-evidence rule).
- **Promotion**: repo-file-derived records auto-promote proposed→active through the contract-bound curator under an explicit named policy — the git commit/PR review of the standards file IS the human gate (ADR-080-sanctioned). Nothing else auto-promotes; `emit_lesson` stays unadvertised to every loop.
- **Injection**: zero new plumbing — the existing role-tag-scoped brief assembly delivers active standards to developer/reviewer spawns; `must|should|may` maps to `critical|warning|info`, and severity-first ordering means musts always make the K=10 cut. Standards must fit the substrate's bounds (injection form ≤320B; ~4KB per brief) — the sync step rejects oversized standards loudly at birth, never truncates.
- **Enforcement, split by kind**: *judgment* standards enter Quinn's review contract as additive constraints — findings citing a `must` standard's id block approval (findings never weaken the spec, T4); *deterministic* checks run in the floors/measure lane — the harness executes each declared command in-container and stamps the finding (G3), a failing `required` check rejecting like any floor. Because a repo-authored command is a gate semdev has never watched fail, the parse rejects commands that cannot fail by construction, a check may declare a negative control that must exit non-zero (a control that passes marks the gate un-failing), and a check with no control still gates but is stamped `unproven` and never renders as a clean pass (DR-0001).
- **Idempotency/lifecycle**: re-provisioning the same file re-births the same IDs (strict-Create conflict = the duplicate signal, beta.160); a standard removed from the file is retired by the same sync step through the curator.
- **B10 guard**: mock-ladder fixtures carrying standards get the S7 stripping discipline — no orchestration-vocabulary coaching rides the standards text; a conformance pin enforces it.

## Capabilities

### New Capabilities

- `repo-standards`: how a target repo declares standards and checks, how they are born/promoted/retired as lesson records with honest provenance, and how they reach the right roles' briefs within the substrate's bounds.

### Modified Capabilities

- `dev-from-task`: the deterministic-floors requirement gains repo-declared checks — operator commands from the standards file run post-measure in-container, harness-stamped, `required` failures rejecting like built-in floors.
- `harness-measurement`: the adversarial-review requirement gains standards compliance — the reviewer receives the active standards in its brief and a finding citing a violated `must` standard blocks approval; standards findings cite the standard id.

## Impact

- Code: one new deterministic tool/step in the provision pipeline (standards sync: parse → validate → birth/retire via the shared mutation client; G1 framework-alignment note + registry entry required), the `lessonRecordProjectionContract` mirror in bootstrap (the CLAUDE.md-recorded precondition for running the lesson lifecycle), persona fragment updates (developer + Quinn standards awareness), and the checks lane extension in the floors/measure harness. Injection needs NO code (wired since beta.154).
- Vocabulary: `agent.lesson.*` is framework-canonical (not semdev vocab). The only candidate NEW semdev predicates are the standards-file source entity's (2–3, single writer = the sync step) — the G1/G9 analysis in design decides whether an existing entity serves instead; any new predicate is named in the spec delta with its writer.
- Dependencies: semstreams v1.0.0-beta.160 as pinned — no bump required. Independent of the beta.161 wave and of PR #7 (no shared files beyond configs).
- Out of scope (named follow-ups): AGENTS.md free-text ingestion (the 320B/4KB injection bounds make free text dishonest through lessons — needs its own channel), the debrief seam promoting recurring review findings into proposed standards (operator-gated, the M2.5 lessons opportunity), any Slack/UI curation surface.
