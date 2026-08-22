# DR-0002 — Disciplined Agentic Engineering: the tier rule, and where a small model is safe

- **Status**: accepted
- **Date**: 2026-08-22
- **Subject**: [`swingerman/disciplined-agentic-engineering`](https://github.com/swingerman/disciplined-agentic-engineering)
  (DAE) @ `1adbf3c` — an external agent-assurance methodology kit evaluated against semdev's arc
- **Supersedes / superseded by**: none. Extends [DR-0001](0001-old-coder-gauntlet-evaluation.md), whose
  numbering note (`DR-nnnn` is semdev's, `ADR-nnn` is semstreams') applies here unchanged.

> **Name collision, resolved.** DR-0001's donor uses *gauntlet* for a set of evidence layers. DAE uses
> *gauntlet loop* for something different — a builder/critic loop against a tangible reference artifact.
> Where this record says gauntlet it means DAE's loop. The `evidence-gauntlet` label continues to mean
> DR-0001's sense, because it is also the M2 dogfood queue.

## Context

DAE is three Claude Code plugins (`engineer`, `atdd`, `crap-analyzer`) implementing an eight-checkpoint
pipeline — onboard → feature-init → discover-acs → atdd → plan → implement → refine → arch-check/CRAP →
mutate — over a stack of artifacts (`feature.md → acs.md → spec.md → plan.md`), with 21 stdlib-only
Python validators, explicit `economy`/`inherit`/`frontier` model classes, and a host-capability seam.
Influences: Uncle Bob's `empire-2025` and Acceptance-Pipeline-Specification, spec-kit, ATDD.

It is a materially stronger donor than DR-0001's. Most of its checks **compute their own answers**:
`dae_arch.py` walks the import graph (Tarjan), `dae_ontology.py` does the AC↔scenario set difference,
`compute_crap.py` parses real coverage reports. Those are honest gates, and the ideas around them —
model classes, the review panel, the gap-analysis vocabulary, the gauntlet's stop conditions — are the
best externally-derived material we have evaluated.

### The two structural findings

**1. The spine gate is self-attested.** `dae_handoff.py:102` is the entire checkpoint contract:

```python
return all(c["met"] is True for c in rec["exit_criteria"])
```

`met` is written into the handoff frontmatter *by the agent that just did the work*. Their README's claim
— *"an agent can talk itself out of an instruction; it cannot talk itself out of a non-zero exit code"* —
is true of the script and false of the pipeline: an agent cannot talk itself out of a non-zero exit, but
it can talk itself **into a zero one** by typing `met: true`. This is our G3 shape, wrapped in something
that looks deterministic. Their own docstring (`dae_handoff.py:39-43`) records the incident: a verify
handoff carrying `met: partial` slipped through while `feature.md` still claimed `status: done`.

**2. The gates are invoked by prompt.** `skills/atdd-team/SKILL.md:65` *instructs* the agent to run
`dae_handoff.py --through <prior-cp>` before dispatching the next checkpoint. The only harness-level
enforcement in the repo is one `PreToolUse` hook that prints a `systemMessage` and `exit 0`s
unconditionally (`hooks/scripts/check-specs-exist.sh`). The *check* is deterministic; the decision to run
it and honour its verdict remains an instruction to the graded agent.

This is DR-0001's finding one level up, and it is the axis semdev is built on: rules fire on
harness-stamped facts, and the model never gets a vote on whether the gate runs.

### What confirms our own bet

From `engineer/references/ontology.md`: *"Across 121 skill invocations in a three-week sample,
`consistency-check` ran once. A constraint that only holds when someone remembers to ask for it is not a
constraint."* That is the argument for G2 and the floors lane, written by someone who measured it. Their
remedy was to move the check to "the ledger position"; ours is that a rule fires on a fact.

Their hard-won modelling lesson lands in the same place: a first version that produced *"71 errors on one
repo, nearly all false"*, and the conclusion that **severity is part of the model** because *"a noisy gate
gets disabled"*. That is `floors.Finding.Advisory`, added last week for the same reason.

## Mapping — their surface to ours

| DAE | semdev surface | Verdict |
|---|---|---|
| Checkpoint handoff as the gate | run phases + rule-owned transitions on harness-stamped facts | **we are ahead** — theirs is self-attested (finding 1) |
| Gates invoked from SKILL.md prose | rules fire on facts; the model cannot skip a floor | **we are ahead** (finding 2) |
| Verifier ≠ implementer (`disjoint` constraint) | Quinn: fresh spawn, allowlisted, never handed Amelia's conversation, plus clean-room verify | equivalent, ours structural |
| `closure`: every AC has a scenario | `openspec validate --strict` | equivalent, already enforced |
| Review panel: adviser + advocate, findings recorded whether accepted or rejected | Quinn alone; no disposition record | **gap for Phase 3** — decision E |
| Gap analysis: *which phase leaked*, closed vocabulary | — | **absent** — decision A |
| Model classes `economy`/`inherit`/`frontier`, assigned per dispatch | `model_registry.capabilities`: four roles, one endpoint | **seam exists, unused** — decision B |
| Gauntlet stop conditions: clear / cap / no-progress / regression | attempt budget only (= cap) | **two absent** — decision C |
| `dae_arch.py`: forbidden patterns, naming, file size | — | **absent** — decision D |
| `dae_arch.py`: layering, import cycles | — | rejected as declarative; belongs in `command:` (Tier 2) |
| `compute_crap.py`: changed-line coverage, diff-scoped | — | already tracked as [#9][i9]; design note folded |
| `dae_introvert.py`: vacuous-test static pre-filter | `vacuous-test` floor | detector half folded into [#8][i8] |
| Fixture parity: two failure directions | G8, B10 | corroboration folded into [#11][i11] |
| `dae_impact.py`: run only diff-affected tests | full suite in clean room | rejected — fail-open in our lane |
| Host-capability seam (capability, not product) | config-bound endpoints and ports | equivalent in spirit |

## The adoption rule — the durable part of this record

The operator constraint on this evaluation was explicit: **no Python dependencies, and no complex AST
work**, because AST work means carrying a parser per language a target repo might be written in.

Worth recording that **DAE did not solve multi-language either.** `dae_arch.py`'s `SOURCE_EXTENSIONS` is
`.py/.js/.jsx/.ts/.tsx/.mjs/.cjs` — no Go — and its import resolution is two hardcoded functions,
`_resolve_python` and `_resolve_js`. `dae_introvert.py` defers the real analysis to a per-language backend
it does not ship. The one script that *is* broadly language-capable, `compute_crap.py`, reaches that
breadth by **never parsing structure**: complexity is heuristic token counting (*"not a replacement for a
proper static analyzer"* — its own docstring) and coverage comes from each language's own report.

Breadth comes from delegating to what the language already emits. That yields the rule.

### Three tiers — a check lands in exactly one, decided by what it must know

1. **Harness-deterministic, language-agnostic by construction.** Operates only on bytes, paths, counts,
   git output, or a tool's own machine-readable output. No parser, no dependency, Go stdlib. Examples:
   forbidden-pattern regex, path naming, file size, changed-line × coverage intersection, baseline
   failure-set difference.
2. **Repo-supplied command.** Anything needing a model *of the code* — layering, import cycles, type
   checks, lint. The repo brings its own language-native tool; we run it and stamp the exit status. This
   is what the `checks:` lane exists for, and it is how we avoid growing a per-language toolbox.
3. **Small-LLM judgment, advisory or ratchet-only.** Never stamps an outcome that lets work through.

### Three safe shapes for a small model, and the one that stays forbidden

G3 says measurement facts are stamped by the harness that ran the command and tool schemas take no
outcome booleans. Tier 3 is compatible with G3 in exactly three shapes:

- **Classifier** — the output is a token from a closed taxonomy that *routes*, not a grade. The existing
  house pattern (persona → closed taxonomy → routing fact → rule).
- **Extractor-with-verification** — the model pulls structure out of raw output and the harness confirms
  every extracted item appears **verbatim in the captured bytes**. Fabrication becomes detectable rather
  than merely unlikely, and the stamped fact is the harness's.
- **Ratchet-only** — the model's answer can only make a gate *stricter*. A wrong answer costs money or
  noise, never a false pass.

Forbidden, unchanged: a model that reports *covered* / *passed* / *verified* with anything downstream
believing it.

**Ratchet-only is also injection-resistant, which is why it is the preferred shape here.** The run's
checkout is bind-mounted read-write into the sandbox with model code executing in it, so anything a
checker reads out of the working tree is attacker-authored — including text aimed at the checker. An
additive-only check can be *neutralised* by injection, returning it to the status quo of no check at all;
it cannot be *weaponised*. That property holds only while **"no findings" is never read as a positive
assertion** — the same `unproven`-is-not-`pass` distinction `standards-via-lessons` D7a already draws.

## Decisions

### A — Adopt the gap-analysis vocabulary, ahead of the ops agent

**Accepted, scheduled** — issue [#17][i17]. `engineer/skills/fix/references/gap-analysis-categories.md`
answers one question after every shipped defect — *which phase leaked?* — with a closed vocabulary, a
blocker rule (`architecture_violation` always blocks), and a mandatory explicit `none` (empty is not an
acceptable classification).

semdev has no vocabulary for "why did this run park". semstreams is building lessons primitives and
reporting with the **ops agent as semdev's concern, not the framework's** — and an ops agent distilling
parked runs into evidence-cited `agent.lesson.*` records needs a closed taxonomy to classify into.
Designing it under the pressure of "the primitives just landed" is how it ends up open-vocabulary and
useless for aggregation. The semdev-shaped vocabulary is in the issue.

Tier 3, classifier shape. Nothing gates on it; records are born `proposed` and reach `active` only through
the curator's Lane 1. Debrief-derived promotion stays operator-gated, per `sop-standards-revival`.

### B — Assign a model class per role

**Accepted, scheduled** — issue [#18][i18]. `configs/semdev-live-gemini.json:73-99` already has the seam:
`model_registry.capabilities` maps `coordinator` / `developer` / `reviewer` / `conversation` to `preferred`
endpoint lists — and all four point at `gemini`. DAE's measured shape over 21 sessions: the top class took
**23% of spend for 4% of the output**, the right shape for a one-shot adviser and the wrong shape for
anything that loops (*"never put `frontier` in an agentic harness"*).

Their numbers come from a Claude Code cost structure where 98% of input tokens were cache reads; ours is
Gemini. **The shape transfers, the numbers do not** — we have a token-reconciled ledger, so this is a
hypothesis to measure against our own runs.

One trap, from our own config: `coordinator` is not one role. `configs/rules/coordinator/02-create-change-spawn.json`
states that the authoring loop *is* a coordinator loop scoped to author, so economising the capability
would economise `create_change` — the highest-judgment turn in the arc. Leave it, or split authoring into
its own capability first.

### C — Adopt the no-progress and regression stop conditions

**Accepted, scheduled** — issue [#19][i19]. DAE checks four stop conditions every round; our route
partition has the equivalent of one (cap, via the attempt budget). Absent:

- **no-progress** — two consecutive attempts on the same root failure. Ours burns the full budget on paid
  model turns, then escalates blaming the developer's work. Ratchet-only shape: a wrong "same" parks
  early toward the human; a wrong "different" costs exactly what we pay today.
- **regression** — an attempt introduced a failure that was not in the baseline. *"The behavior contract
  outranks the bar."* Depends on [#10][i10]. Extractor-with-verification shape: the model extracts test
  names from arbitrary runner output; the harness confirms each appears verbatim in the attempt's bytes
  and not in the baseline's.

Also taken, as a design idiom rather than a feature: **opt-in by the presence of a declared artifact, not
a flag** — *"a feature with no `gauntlet:` block runs no gauntlet — silently."* That is the checks lane's
design, independently arrived at.

### D — Adopt a declarative check kind, narrowly

**Accepted, scheduled** — issue [#20][i20]. A declarative check has no exit status to launder, so the
fail-open arms race that `standards-via-lessons` group 5 had to win at parse (top-level `;`, newline,
`|`, `||`) simply does not apply. Strictly stronger G3 posture for the checks it can express.

Scope is Tier 1 only — forbidden-pattern regex per glob, path naming, file size — and **layering and
import cycles are explicitly excluded**, because they need a per-language import resolver. Those go to
`command:`, where the repo brings its own tool.

### E — Carry the review panel's disposition rule into Phase 3

**Accepted, folded as design input** — issue [#16][i16]. Two roles, not five: an **adviser** (bounded
one-shot, constructive) and an **advocate** (adversarial — *"assume at least one confident claim is
false"*), dispatched concurrently and blind to each other. Their report is that the pair produces
non-overlapping findings by construction. It is also the two-reviewer discipline we already run on
semdev's own commits.

The rule that must be settled *before* the draft-PR review surface exists, not during it:
**recording a rejected finding is not optional** — *"an undocumented rejection means the next agent
re-litigates the same point, which is exactly the cost the panel exists to avoid."* Findings carry role,
severity, claim, location, `accepted`, and a `disposition` either way; silence is not addressing a
finding. A review surface is exactly where a rejected finding either becomes a durable record or
evaporates into thread scrollback.

### F — Rejected

- **Their handoff / exit-criteria contract.** That is finding 1. Our facts do the same job without the
  self-attestation, and adopting the artifact would import the defect.
- **`dae_impact.py` (run only diff-affected scenarios).** The clean room must run everything; test impact
  analysis there is a fail-open. Their own fail-direction reasoning is the reason to skip it: *"a false
  skip is a missed regression; a false run only costs time."*
- **Mutation testing.** DR-0001's rejection stands; nothing here changes the Go tooling picture. What is
  reusable *if* we revisit is `dae_mutmap.py`'s manifest-as-result-cache shape — safe to commit because
  every entry is re-verified against current hashes, so a stale entry is re-mutated rather than wrongly
  skipped.
- **The Gherkin/IR pipeline and the markdown artifact formats.** Ours are graph facts and OpenSpec, and
  `openspec validate --strict` already enforces their `closure` constraint.
- **`compute_crap.py`'s complexity score.** Heuristic token counting by its own admission. We take the
  diff-scoping, not the ranking — see [#9][i9].
- **Per-language AST analysis in any form**, per the tier rule.

## Consequences

- Four issues enter the backlog ([#17][i17]–[#20][i20]) and four existing ones gain design input
  ([#8][i8], [#9][i9], [#11][i11], [#16][i16]), all on the `evidence-gauntlet` label — which remains the
  M2 dogfood queue, so the backlog and the dogfood queue stay one queue.
- The port manifest gains a **fourth donor section (E1–E9)**, so this evaluation obeys the repo's own law.
- `standards-via-lessons` is untouched. Groups 6–7 stay exactly as scoped; decision D follows the lane it
  extends rather than growing it.
- The tier rule and the three Tier-3 shapes are the reusable output. They are the answer to give the next
  time a checker is proposed, and they are why decision A is safe as a model and [#9][i9] is not.

## References

- DAE @ `1adbf3c`: `engineer/references/{gauntlet,model-classes,review-panel,ontology,fixture-parity,host-capabilities}.md`,
  `engineer/skills/fix/references/{gap-analysis-categories,regression-spec-template}.md`,
  `engineer/scripts/{dae_handoff,dae_arch,dae_introvert,dae_impact,dae_mutmap}.py`,
  `crap-analyzer/skills/crap-analyzer/scripts/compute_crap.py`, `hooks/`
- semdev: `docs/constitution.md` (G2, G3, G6, G7, G8), `docs/port-manifest.md` (donor E),
  `docs/decisions/0001-old-coder-gauntlet-evaluation.md`,
  `openspec/changes/standards-via-lessons/design.md` (D1, D7a)
- Tracking: the `evidence-gauntlet` label on `C360Studio/semdev`

[i8]: https://github.com/C360Studio/semdev/issues/8
[i9]: https://github.com/C360Studio/semdev/issues/9
[i10]: https://github.com/C360Studio/semdev/issues/10
[i11]: https://github.com/C360Studio/semdev/issues/11
[i16]: https://github.com/C360Studio/semdev/issues/16
[i17]: https://github.com/C360Studio/semdev/issues/17
[i18]: https://github.com/C360Studio/semdev/issues/18
[i19]: https://github.com/C360Studio/semdev/issues/19
[i20]: https://github.com/C360Studio/semdev/issues/20
