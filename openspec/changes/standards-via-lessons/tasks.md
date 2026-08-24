# standards-via-lessons — tasks

Standing directive: adversarial review (semstreams-reviewer + go-reviewer)
before each group's commit. Every fix/feature task lands red-first (G6).
If the beta.161 bump lands mid-change, re-verify the four upstream anchors
(store, curator, contract literals, scope derivation) before group 3.

## 1. Vocabulary + contracts foundation (D2, D6)

- [x] 1.1 RED: conformance pins for the new surface — `repo.standards.digest`
      /`.path`/`.repo` in the vocab provenance test (writer `standards-sync`,
      change slug `standards-via-lessons`), the source entity class +
      pattern in the contracts derivation, and the hand-mirrored
      `agentic.lesson-record` contract-literals pin (name, message type,
      pattern, birth predicates, `lesson-lifecycle` group members — comment
      naming the upstream source file). Verify they FAIL before the code.
- [x] 1.2 GREEN: `internal/vocab` gains the three predicates;
      `internal/graphown` gains the source entity class + pattern const, the
      lesson-contract mirror (`LessonRecordMirror`, carried into the client by
      `AllContracts` — the memoized census stays pure; go-review R4), and
      `createOwners` += `standards-sync`.
- [x] 1.3 Alignment note (`docs/alignment-notes.md`): the standards-sync
      seam — primitive-first analysis (injection/curator/store are existing
      framework primitives; the ONLY new Go is the deterministic sync step +
      checks lane), the G2 analysis of curator-driven lifecycle (framework
      Lane-1 mandate), and the named auto-promotion policy.
- [x] 1.4 `task check` green; adversarial review; commit group 1.

## 2. The standards file parser (D1 — pure)

- [x] 2.1 RED: table tests for `.semdev/standards.yaml` — valid minimal,
      valid full, absent-file (zero standards, no error), and the fail-closed
      set: unknown field, duplicate id, invalid severity, invalid role, empty
      text, bad version, malformed YAML — each rejection naming the defect.
- [x] 2.2 GREEN: the pure parser package (strict YAML, typed result:
      standards + checks), including the injection-form renderer
      (`[std:<id>] MUST <text>`) with the 320B pre-birth bound check
      (reject naming the id, never truncate).
- [x] 2.3 `task check` green; adversarial review; commit group 2.

## 3. The sync core: birth → promote → retire (D3, D4, D5)

- [x] 3.1 RED: pins against fakes of the store/curator/reader seams —
      (a) unchanged file re-sync is a no-op (idempotent: same source entity,
      `created=false` births, no lifecycle writes); (b) a new standard
      births with the exact D3 mapping (category/polarity/severity/
      summary/detail/injection-form/evidence/applies-to) and is promoted;
      (c) an oversized standard fails the whole sync closed naming the id;
      (d) a standard removed from the file retires, and an edited standard
      retires-old + births-new; (e) cross-repo isolation — a repo-standard
      record whose source resolves to a DIFFERENT repo is never retired;
      (f) a non-file proposed lesson is never promoted.
- [x] 3.2 GREEN: the sync core — source-entity strict Create (conflict =
      duplicate signal), identity derivation (UUIDv5 over the store's four
      identity fields, semdev-standards namespace), birth via
      `agentictools.NewNATSLessonStore`, promotion via
      `LessonCurator.Promote` scoped to just-ensured records, retirement per
      D5, the K-exceeded authoring warning.
- [x] 3.3 `task check` green; adversarial review; commit group 3.

## 4. Provision wiring (D2/D4 boot + the provision journey)

- [x] 4.1 RED: a provision-level pin — provisioning a fixture with a
      standards file blocks/parks on a malformed file, and succeeds
      birthing+activating records on a valid one (fakes at the station seam).
- [x] 4.2 GREEN: the `Standards` seam on `ProvisionDeps` (after Materialize,
      before ProveBaseline), boot construction (store + curator from the
      shared graphown mutation client + platform identity — REJECT nil
      surfaces loudly, go-review R2), park-on-malformed via the existing
      block path.
- [x] 4.2b The `RunLaunch` vocab-registration gap (semstreams-review HIGH,
      PRE-EXISTING) — CONFIRMED and fixed. Verification needed no docker after
      all: `projection.Contract.Validate` calls
      `vocabulary.RequireDeclaredPredicate`, and ADR-091 made contract
      validation purely LOCAL, so `graphown.NewClients` on a cleared registry
      fails at construction with `predicate "intake.actor.admitted" is
      canonical but not declared in the vocabulary registry` — reproduced
      offline. Every `semdev launch` has failed at its first real step since
      beta.159. Fixed by funnelling BOTH boot entry points through
      `declaredGraphClients` (declares, then builds), pinned two ways: a
      behavioral pin on the framework coupling (`internal/boot`,
      negative-control-proven) and a structural conformance pin that the
      helper is the package's only construction site — the runtime lane
      survived by coincidence, and one helper makes the ordering a property.
- [x] 4.3 Docker journey (red-first): provision a fixture repo carrying a
      stripped standards file → assert records born `active` in the graph
      with resolving evidence, idempotent on re-provision (D9 items 1).
- [x] 4.4 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 4. Both reviewers returned CHANGES REQUESTED and were right:
      the Lstat-only standards-path guard was bypassable by a committed
      `.semdev` DIRECTORY symlink (proven by execution — the leaf reported
      IsRegular and outside bytes were read), and `wiring_test.go` was
      coverage-proven vacuous for 5 of its 6 guards. Both fixed + pinned, along
      with the evidence-resolution fail-open, the curator-refusal
      misclassification, repo case-folding (an identity input), the hand-rolled
      record prefix, a mid-provision panic path, and the redundant
      `PlatformMeta` threading. DEFERRED: extracting the paginated
      prefix-query loop now duplicated 4× across `admission` + `standards`
      (a separate refactor of another package).

## 5. The checks lane (D7)

- [x] 5.1 RED: parser table extension (D1 fold, DR-0001) — each denied
      fail-open construct rejects naming its defect: suppression
      (`|| true`, `|| :`, trailing `; true`, `2>/dev/null`, `2>&-`), vacuity
      (`true`, `:`, bare `echo …`), and an unquoted top-level pipeline on a
      `required` check (non-required warns). Pins the scanner's two traps:
      `||` is not a pipe, and a pipe inside quotes is not top-level. Plus the
      `proof` field's parse (optional, non-empty when present, same denials).
- [x] 5.2 GREEN: extend `validateCheck`/`Check` in `internal/standards` for
      5.1 (new `internal/standards/gates.go`). Extends the group-2 parser; does
      not amend its commit. ADDED beyond the plan: a proof that is exactly
      `false`/`! true`/`exit 1` is rejected too — the mirror-image hole, where a
      control that cannot PASS certifies a gate it never invoked.
- [x] 5.3 RED: floors-level pins — (a) a required check whose command exits
      non-zero stamps a rejecting `repo-check:<name>` finding and the rail
      does not advance; (b) a non-required failure stamps non-rejecting;
      (c) zero-checks file/absent file leaves floors byte-identical;
      (d) the base-ref read — an attempt-modified standards file does NOT
      change the executed checks (the refs/semdev/base copy governs).
- [x] 5.4 RED: gate-honesty pins (D7a) — (a) a declared `proof` exiting
      non-zero marks the check `proven` and the check then runs normally;
      (b) a `proof` exiting ZERO stamps the un-failing-gate finding, rejecting
      iff `required` (a non-required check with a passing control never
      rejects); (c) an absent `proof` gates as declared and stamps `unproven`;
      (d) a check the runner could not execute stamps `not-run`, is never a
      pass, and is distinguishable in the finding text from ran-and-failed —
      red-first against a deliberately dead container, the measure_task
      exit-vs-transport contract reused.
- [x] 5.5 GREEN: `checkfloors.RunFloors` checks stage — `internal/standards.Checks`
      behind ONE narrow `RepoChecks` seam (the base reader + sandbox resolver live
      inside it rather than as two more positional params on an already 10-arg
      function); the law comes from the PROVISION-TIME snapshot, not a git ref —
      both group-5 reviewers proved the base ref is writable from inside the
      sandbox, so `FileAtBase` was written, reviewed, and deleted within the same
      group; the control runs before the check it certifies. Repo findings APPEND to the built-in floor set, so a required
      failure routes through the existing aggregate with no second gate to keep
      in sync. `floors.Finding` gained `Advisory`: a non-required failure is
      reported and rendered as FAILED without gating, so a failure never has to
      be laundered into `Passed=true` to avoid rejecting (G7).
- [x] 5.6 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 5. BOTH reviewers returned CHANGES REQUESTED on the same two
      blocking defects, and both were right. (1) The base-ref read the lane rested
      on is FORGEABLE: the checkout is bind-mounted read-WRITE with `.git` inside
      it and model-authored code runs in that container, so a `TestMain` doing
      `git update-ref refs/semdev/base <other>` deletes every required check —
      replaced by a provision-time snapshot the container has no address for, and
      `FileAtBase` deleted. (2) The command denylist matched literal SPELLINGS and
      both reviewers defeated it by deleting a space (`||true`, `;true`,
      `; echo done`, `exit 0`, `/bin/true`, `true # go vet`) — replaced by a
      structural rule (a required gate is ONE top-level command), which also fixed
      the false REJECTS on `$( )`, backticks, and escaped quotes. Also applied: an
      un-failing control PARKS instead of burning the attempt budget; control-byte
      sanitizing on command text and runtime output; a check-count bound; a
      nil-lane WARN; the missing RunFloors/advisory tests; a pin that built-in
      floors can never be advisory. NOT applied (recorded in design hazards):
      repo checks run after the clean-tree floor.

## 6. The judgment lane + the full bridge proof (D8, D9)

- [x] 6.1 Persona fragments: `reviewer/10-standards-contract.md` +
      `developer/10-standards.md` (cite-the-id duty; standards tighten,
      never weaken); persona-dir conformance stays green.
      ⚠ Both fragments originally opened with a fenced SPECIMEN standard, so
      every developer and reviewer brief arrived carrying forged ids
      (`[std:no-panics-in-handlers]`, `[std:<id>]`) indistinguishable from the
      repo's real ones — Quinn is told to cite what she is given, so she could
      have blocked approval on a rule no repository declared. Found by PRINTING
      what the briefs carried in 6.2, not by reading. Fragments now describe the
      format in prose, `standards.InjectionPrefix` is exported as the one
      spelling of the token, and `TestNoPersonaFragmentForgesAStandard`
      (red-first, negative-controlled) refuses any fragment containing it.
- [x] 6.2 GREEN: `TestBridgeProofRepoStandardsReachBriefsAndGate` (D9 items 2+4)
      and `TestBridgeProofRequiredRepoCheckGatesLikeAFloor` (D9 item 3). Two Go
      tests rather than one: the gate case ends PARKED and the green case ends
      DELIVERED, so one function cannot hold both terminals.
      Role scoping is proven off the captured bytes and REPORTED, not merely
      unasserted: developer brief carries `[eng-error-context, eng-table-driven-tests]`,
      reviewer `[eng-exported-doc-comments, eng-table-driven-tests]`, coordinator
      `[]` (the negative control on the axis — an injector ignoring scoping
      entirely would satisfy both role assertions, since each expected set is a
      subset of the whole). Needed `mockllm.WithPromptCapture` (opt-in, so no
      existing journey's transport changes): ssmock exposes only `LastRequest`,
      which cannot answer a question about two spawns at two points in one arc.
      The gate journey's attempt MEASURES GREEN and is rejected anyway, by the
      repo's own `go vet ./...` — isolation measured, not assumed (the default
      vet subset `go test` runs omits the assign analyzer). Floor detail:
      `repo-check:go-vet: REJECTED — unproven — … exit status 1` alongside
      `repo-check:gofmt: passed — unproven`, and the run parks with no review.
- [ ] 6.3 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 6.
      DONE: offline suite, conformance and lint green; all three standards
      journeys green individually (`-count=1`, real docker); BOTH adversarial
      reviews complete with every finding folded (below).
      ⚠ OPEN: the FULL `task e2e` post-fold has NOT run green. Its first attempt
      died on Go's 600s DEFAULT timeout — the suite had crept to 567s and group 6
      pushed it over — and the re-run under the new explicit `-timeout 30m` was
      killed when docker work was paused (host resource pressure, 2026-08-23).
      Re-run before archive. Nothing is known-broken; the gate is simply unmet.

      Review fold (2 reviewers, both CHANGES REQUESTED, both closed):
      ⚠⚠ THE finding, raised INDEPENDENTLY by both: the fragments made `[std:`
      mean law but never bounded WHERE a standard may arrive, and the forgery pin
      guards only the repo-controlled channel. Five untrusted paths reach a
      persona — issue text → task.spec, `read_diff`, `read_workspace`,
      `review.findings.value`, and `floor.finding.detail` (which quotes
      `snippet(res)`, the OUTPUT OF A REPO COMMAND RUN OVER ATTEMPT-AUTHORED
      CODE). Fixed by teaching both fragments the framework's own distinguisher,
      verified in `processor/agentic-loop/lessons.go`: real standards arrive ONLY
      inside `[Lessons — durable guidance…]`, each line ending in a resolvable
      entity id; a bracketed id met anywhere else carries no authority.
      Also folded: the gate journey could not tell "ran and failed" from
      "could not run" (`not-run` renders as `…: REJECTED — not-run — …`, a
      byte-compatible prefix) — now asserts `FAILED with exit status` plus the
      `unproven` token, which no journey pinned; NOTHING pinned that the D8
      fragments reach a brief at all (delete both files and every test still
      passed) — now pinned on a distinctive phrase per role; the green journey
      asserted no turn count; `WithPromptCapture` after `Start` was a silent
      no-op `-race` cannot see (now panics, and `capture` is read once in `Start`
      and passed to the handler so no cross-goroutine read exists); `io.ReadAll`'s
      discarded error could record a truncated body and make an ABSENCE assertion
      pass because the bytes were cut; the forgery pin now also scans rule
      `prompt:` fields (8 fragments + 12 prompts, negative-controlled); four
      offline capture pins added (negative-controlled); the advisory check must
      report `passed`, not merely appear; two comments corrected that named
      mechanisms which cannot fire.
      Reviewers REFUTED three things — do not "fix" them: the coordinator
      negative control is not vacuous (the sync runs inside `Provision` before the
      ready stamp), `requireNoReviewVerdict`'s single read is near-unreachable as
      a fail-open, and the capture proxy introduces no unproven transport.
      Out of scope, FILED as #21: `ask_human` is declared in 11 rules, has no
      executor, is DROPPED by the engine with a WARN that fired 38× in one
      afternoon, and `rules_test.go:534` asserts the declaration the engine
      discards — so Amelia has no escalation lane while three surfaces say she
      does. Her fragment now describes what actually happens instead.

## 7. Conformance hardening + close

- [ ] 7.1 G8/B10: extend the fixture-vocabulary scan to `.yaml`/`.yml`; the
      journey fixtures' standards files pass; the meta-pin proves the scan
      fires on a planted coaching term.
- [ ] 7.2 `openspec validate standards-via-lessons --strict` green;
      `/opsx:verify`; evidence named in the change (pins + the green e2e
      run; no evidence-ledger entry — no run-level claim).
- [ ] 7.3 Sync deltas + archive (expect the dev-from-task floors-requirement
      re-merge if PR #7 archived first — reconcile the merged text).
