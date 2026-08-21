# DR-0001 — The old-coder gauntlet: what semdev adopts, defers, and rejects

- **Status**: accepted
- **Date**: 2026-08-20
- **Subject**: [`amazingang/old-coder`](https://github.com/amazingang/old-coder) — an external agent-assurance
  pattern evaluated against semdev's arc
- **Supersedes / superseded by**: none

> **Numbering note.** `DR-nnnn` records are *semdev's* decisions and live here. `ADR-nnn` references
> throughout this repo (ADR-056, ADR-080, ADR-091 …) are **semstreams'** upstream framework decisions and
> are never renumbered here. The two namespaces are deliberately distinct.

## Context

`old-coder` is a plain-Markdown skill (Claude Code / Codex / Cursor / Aider) that packages a single-agent
assurance workflow: **SPEC → RED → GREEN → REFACTOR → GAUNTLET → EVIDENCE**. Its founding claim is
Uncle Bob's: don't read agent-authored code, *"surround the agents with extreme constraints"* and let
executable evidence carry the trust. It ships a skill (`skills/old-coder/SKILL.md` + four references), an
API-review companion skill, and a worked demo whose `evidence.md` is the point of the exercise.

Its thesis is semdev's thesis. The reason to evaluate it is not novelty — it is coverage: an independently
derived list of *which* constraints a no-human-reads-the-code claim actually requires. Where that list
exceeds ours, we have a gap; where ours exceeds theirs, the difference is worth naming so we do not
regress it.

### The structural difference (why we do not simply adopt it)

old-coder's anti-gaming rules are **prompt instructions to the same agent being graded** — "never weaken a
test", "never report a layer you didn't run". semdev's equivalents are **schema and harness properties**:
`submit_review` has no outcome field, measurement facts are stamped by the process that ran the command
(G3), and lifecycle transitions are rule-owned (G2). Their skill asks an agent to be honest; our system
makes the dishonest state unrepresentable.

That difference cuts both ways. It means we must not import their *mechanisms* (markdown SPEC/EVIDENCE
documents, an agent-graded verifier round, a hand-rolled mutation runner). It also means every gap below
should land as a **harness-executed, harness-stamped fact**, never as persona coaching.

## Mapping — their surface to ours

| old-coder | semdev surface | Verdict |
|---|---|---|
| SPEC + human approval before code | the generated OpenSpec change + the approval gate (`configs/rules/run-lifecycle/01-offer-change-approval.json`) | equivalent |
| "an answer to a question is not an approval" | D15 forward contract; `project_tasks` freezes `task.spec` only when `openspec.change.validated == openspec.change.<slug>.revision` | equivalent, with a **named forward hazard** (below) |
| RED → GREEN → REFACTOR | the bounded dev loop | **RED is not enforced** — gap 1 |
| Gauntlet: full suite | harness measurement (`internal/measurement`) | present, **no baseline** — gap 4 |
| Gauntlet: types / build | `go build ./...` in clean-room + the `source-build` floor | equivalent for Go |
| Gauntlet: lint | — | arrives via the `standards-via-lessons` `checks:` lane |
| Gauntlet: changed-line coverage | — | **absent** — gap 2 |
| Gauntlet: mutation testing | `vacuous-test` + `anti-mock` floors (static approximations) | deliberately deferred — see Rejected |
| Gauntlet: property-based tests | — | a `checks:` entry when a repo wants it |
| Gauntlet: real execution | clean-room resolve + build + test | partially covered; a smoke command is a `checks:` entry |
| Gauntlet: supply chain / secrets / capability diff | `internal/forbidden` (build-time download tripwire only); `internal/secrets` (redacts *operator* secrets from evidence) | **different axis — absent** — gap 5 |
| Gauntlet: suite health / flake / random order | — | **absent** — gap 6 |
| EVIDENCE report | `Delivery.evidenceSummary` PR body + `docs/evidence-ledger.md` | present, **collapses three states into one dash** — gap 7 |
| Independent verification (prose, experimental) | Quinn — a fresh spawn, allowlisted `[read_workspace, read_diff, submit_review, ask_human]`, never handed the developer's conversation — **plus** clean-room verify | **we are ahead**; theirs is opt-in prose, ours is structural |
| "verification is source-state-specific" | `measurement.result.commit`; delivery pushes the recorded verified SHA, never `HEAD` | equivalent, enforced in code |
| isolation: the fresh-worktree-has-no-gitignored-content trap | provisioning + `refs/semdev/base` | we solve what they only name |
| "prove a home-grown checker can fail before trusting its pass" | red-first pins on every floor (G6) — for *semdev's own* checkers | **absent for repo-authored checks** — folded into `standards-via-lessons`, see Decision A |

## Decisions

### A — Fold the checker-honesty discipline into `standards-via-lessons` now

**Accepted; folded 2026-08-20.** The in-flight `checks:` lane accepts repo-authored gate commands and
treats a non-zero exit as a rejecting finding — but nothing proves a declared check *can* fail. A repo
writes `command: "go test -cover ./... | tail -1"`, `required: true`, and that gate is green forever:
in `sh -c` a pipeline's status is the last command's, so `tail` exits 0 and the real status is laundered.
old-coder states the rule directly: *"a layer that prints a percentage and exits 0 is a report, not a
gauntlet layer, and it will sit there green while coverage falls."*

This is not hypothetical. old-coder's own demo shipped a mutation runner that reported kills for mutants
it never executed (two same-size mutants written in the same second shared a bytecode cache) — a defect
class that **can only inflate the score**, and therefore can never surface as a red gauntlet. We already
hold the same lesson in-tree: `harness.Command.UnmarshalJSON` refuses to normalize a blank string into
`sh -c "   "` precisely because that *"runs, exits 0, and reads as a false pass (the SB5 grave)"*.

Landed in the change's D1/D7 as: fail-open construct rejection at parse, an optional `proof` negative
control whose passing exit is itself a rejecting finding, an `unproven` status that never renders as a
clean pass, and transport-vs-genuine classification so a check that *could not be run* neither passes nor
rejects.

### B — Adopt the base-ref evidence bundle (gaps 1, 2, 4, 6) as a follow-on change

**Accepted, scheduled** — issues [#8][i8], [#9][i9], [#10][i10], [#11][i11]. Four layers that all key off `refs/semdev/base`, which provisioning already
maintains as the diff anchor. Together they are what makes "nobody reads the diff" defensible; separately
each is small. They are tracked as issues and expected to land as one change.

1. **Red-first proof of the model's own test.** CLAUDE.md requires red-first pins for *semdev's*
   development; semdev-the-product never proves an authored test could fail. `vacuous-test` and
   `anti-mock` are static approximations of the same worry. The harness can apply the attempt's
   test-file half to the base tree, run it, and require failure — harness-executed, harness-stamped,
   zero model input, G3-clean. It closes the "test written to fit the code I just wrote" class that
   mutation testing otherwise costs a fortune to catch. Design question, not a blocker: a compile
   failure is a weaker RED than an assertion failure, and old-coder names exactly this.
2. **Changed-line coverage.** `tests-must-exist` proves a test *exists*; nothing proves the changed
   lines are *executed*. Go ships coverprofile; the base ref gives the diff.
3. **Baseline / zero-NEW-failures.** Measurement is binary pass/fail of the whole suite, so any target
   carrying one pre-existing failure is red forever. That blocks M2 dogfood on real repos and every M3
   sibling. Measure base, hold the line at zero new failures.
4. **Suite health.** One measurement run, no shuffle, no repeat. A flaky suite can produce a false GREEN,
   and every downstream fact rests on it.

### C — Adopt the supply-chain and capability axis (gap 5) as a security follow-on

**Accepted, scheduled** — issue [#12][i12]. `internal/forbidden` covers hidden *build-time downloads*; `internal/secrets`
redacts *operator* secrets from evidence. Neither covers the model's own diff: dependencies it added,
known vulnerabilities, credentials committed, or — old-coder's sharpest framing — *"did the change start
using network / subprocess / filesystem / env it didn't before? An agent-added capability nobody asked
for is a red flag."* Directly adjacent to what `security-forge-containment` hardened: semdev now pushes
model-authored commits to a real remote.

Their SPEC-side half is the load-bearing part and is the semdev-shaped one: **new dependencies are
declared and justified in the approved artifact**, and the harness rejects a delivered diff that adds a
dependency the approved change never named. That is a harness-checkable G3 fact, not a review judgment.

### D — Adopt the evidence-honesty refinements (gap 7)

**Accepted, scheduled** — issue [#13][i13]. Three disciplines plus one live defect:

- **A defect.** `Delivery.factString` (`internal/tools/openpr/delivery.go:252-257`) returns `""` both when
  a fact is absent *and* when the graph read errors, and both render `—` in the PR body. "Not measured",
  "not applicable", and "read failed" are indistinguishable to the human reading the evidence table. That
  is a G7 honesty defect in shipped code.
- **The three-way split.** `N-A` (no such surface here) / `UNAVAILABLE` (tool missing, nothing ran) /
  `SUBSTITUTED` (something else ran — and what it cannot detect). old-coder's rule travels with it:
  **`SUBSTITUTED` may never be written as a pass.**
- **Dismissals need a citing line each.** A fix self-evidences — the test now passes. A dismissal carries
  no evidence: "not a real problem" is indistinguishable from "did not check". When a re-attempt
  dismisses one of Quinn's findings rather than fixing it, the dismissal should name the command, the
  `file:line`, or the test that disproves it.
- **Name the structural blind spot.** A layer this project cannot run at all otherwise reads as absent
  rather than as an accepted limit.

### E — Adopt per-change "Must NOT" clauses (gap 8)

**Accepted, scheduled** — issue [#14][i14]. Our spec deltas carry positive requirements and scenarios. old-coder's SPEC
carries a `## Must NOT` block — invariants that must survive the change — and every clause must appear in
the evidence mapping as a test, a layer, or an explicit skipped-with-reason line, *never silently absent*.
Repo standards (the in-flight change) cover repo-wide musts; per-change invariants are a different slot
with no home today.

### F — Defer risk tiers (gap 9)

**Deferred** — issue [#15][i15], filed so it is not lost. old-coder scales *which layers run* to blast radius (Tier 1 typo → Tier 3 money/auth/data/
concurrency, where Tier 3 opens with an explicit failure model: list the ways this change can hurt, and
add a layer per mode). semdev scales *attempts*, not *checks*. A `when:` path-glob on standards checks is
the natural carrier once the checks lane exists. Not before.

### G — Rejected

- **Their independent-verification protocol as a round-capped prose loop.** We have Quinn (structurally
  fresh-context, allowlisted, reading harness facts rather than the builder's claims) and clean-room
  verify (executable, not prose). Adding a graded prose round spends tokens on a judgment surface we
  already have, and old-coder itself labels it experimental with a single case study as evidence. What we
  *do* take from `verifier.md` is its blind-phase reasoning, which our spawn topology already satisfies.
- **Mutation testing, for now.** Their own ecosystem table admits Go has *"no mature default"* and falls
  back to manual mutation — and their manual runner is the fail-open cautionary tale above. The
  `vacuous-test` floor plus Decision B's red-first proof buys most of the value at a fraction of the cost
  and none of the fail-open risk. Revisit if a credible Go mutation tool lands.
- **Markdown SPEC and EVIDENCE artifacts as such.** Ours are graph facts and a PR body derived from
  harness-stamped predicates — strictly stronger, because a document can drift from the run and a fact
  cannot. We take their *content discipline* (Decision D), not their file format.
- **`old-coder-api` as a port.** A useful HTTP/JSON gate checklist, but it is target-repo domain policy —
  exactly what the standards file is for. It belongs in a target repo's `.semdev/standards.yaml`, not in
  semdev.

## Consequences

- One in-flight change grows a fold (A) that closes a fail-open class before the lane ships, rather than
  after a target repo declares its first laundered gate.
- Three follow-on changes enter the backlog (B, C, D) and one artifact-format change (E), each tracked as
  a GitHub issue on `C360Studio/semdev` — which is also the M2 dogfood target, so the backlog and the
  dogfood queue are the same queue.
- The port manifest gains a third donor section so this evaluation obeys the repo's own law: nothing
  enters semdev except through the manifest.

### Named forward hazard — approval staleness becomes reachable at Phase 3

old-coder is emphatic that a spec revised after approval invalidates the approval: *"any approval you
held before the question is approval of a document that no longer exists."* semdev already reasoned to
the same place — `configs/rules/run-lifecycle/01-offer-change-approval.json` documents the
content-freshness deferral (D15 forward contract #0), with the reachable guard living in `project_tasks`
and a forward-contract pin (`TestChangeApprovalGateFreshnessForwardContract`) holding the door.

The deferral rests on one premise: *"no live path re-authors a change while the run is executing."*
**Phase 3 (`draft-pr-review-surface`) is where that premise dies** (issue [#16][i16]) — a conversation that gains a diff is a
conversation that can revise the approved change. The forward contract must be honored as part of Phase 3,
not discovered during it. Recorded here so the pin is not read as optional.

## References

- `skills/old-coder/SKILL.md`, `references/gauntlet.md`, `references/templates.md`,
  `references/verifier.md` @ `amazingang/old-coder`
- Uncle Bob's originating post, quoted in their README
- semdev: `docs/constitution.md` (G3, G4, G6, G7), `docs/port-manifest.md` (donor O),
  `openspec/changes/standards-via-lessons/design.md` (D1, D7a)
- Tracking: the `evidence-gauntlet` label on `C360Studio/semdev`

[i8]: https://github.com/C360Studio/semdev/issues/8
[i9]: https://github.com/C360Studio/semdev/issues/9
[i10]: https://github.com/C360Studio/semdev/issues/10
[i11]: https://github.com/C360Studio/semdev/issues/11
[i12]: https://github.com/C360Studio/semdev/issues/12
[i13]: https://github.com/C360Studio/semdev/issues/13
[i14]: https://github.com/C360Studio/semdev/issues/14
[i15]: https://github.com/C360Studio/semdev/issues/15
[i16]: https://github.com/C360Studio/semdev/issues/16
