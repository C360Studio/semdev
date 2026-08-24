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
- [x] 6.3 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 6.
      DONE: offline suite, conformance and lint green; all three standards
      journeys green individually (`-count=1`, real docker); BOTH adversarial
      reviews complete with every finding folded (below).
      GATE CLOSED 2026-08-24: the full post-fold suite ran GREEN —
      `go test -race -tags=e2e -count=1 -timeout 30m ./test/e2e/...` →
      `ok github.com/c360studio/semdev/test/e2e 507.947s`, zero failures.
      Recorded as the exact command rather than `task e2e` (G7): the two
      gate-honesty flags live on branch `e2e-gate-honesty` (PR #22), not here, so
      bare `task e2e` on this branch is still cache-eligible and still carries
      Go's 600s default — the two traps that killed the earlier attempts. The
      explicit form is strictly stronger than what the task asked for.
      SKIP ACCOUNTING (the suite ran without `-v`, where a skip is INVISIBLE — a
      bare `ok` is not evidence a test ran): 22 of 24 executed. The 2 skips are
      env-gated by design and were verified unset —
      `TestBridgeProofSemsourceConditionPlumbing` (needs
      `SEMDEV_SEMSOURCE_E2E_ENDPOINT`, an external semsource compose) and
      `TestRealLLMJourneyIssueToPR` (needs `SEMDEV_REAL_LLM`; correctly closed,
      zero paid tokens). The third guard, `TestBridgeProofSelfTargetForgeCloneToPR`'s
      git check, did not fire (git 2.50.1 present). All three standards journeys
      carry NO skip guard and therefore ran:
      `TestBridgeProofRepoStandardsBornAndActivated`,
      `TestBridgeProofRepoStandardsReachBriefsAndGate`, and the checks-lane
      `TestBridgeProofRequiredRepoCheckGatesLikeAFloor`.

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

- [x] 7.1 G8/B10: extend the fixture-vocabulary scan to `.yaml`/`.yml`; the
      journey fixture's standards file passes; the meta-pin proves the scan
      fires on a planted coaching term.
      DONE, but NOT as written — see the scope narrowing below.
      `test/conformance/g8_fixtures_test.go` now carries ONE declarative policy
      table, `fixtureScanKinds`, that all three pins derive from. Each kind
      declares a matcher, a `specimen` the reach pin plants, and a `treeFloor`
      — so a newly declared kind cannot be added without proving the walk
      reaches it, and its tree floor must be CLASSIFIED. `treeFloor` is a typed
      string with no valid zero value, not a bool, precisely so omission is not
      a silent "exempt": the reach pin fatals on the empty value.
      SCOPE NARROWED (reviewer HIGH): the task says `.yaml`/`.yml`, but the spec
      (`specs/repo-standards/spec.md:133`) says every fixture STANDARDS file, and
      `internal/standards.Path` fixes that at `.semdev/standards.yaml` with no
      override — so no other YAML can ever BE a standards file. Scanning all YAML
      is a demonstrated build-breaking false positive: a `.golangci.yml` enabling
      godox declares `keywords: [TODO, FIXME, HACK, BUG]` (the repo configuring
      the linter that bans coaching) and a `.github` issue template is little but
      the word "bug". This lint fails the build, so that blocks a legitimately
      realistic fixture — the opposite of G8. The kind is keyed off
      `standards.Path` itself, so the pin follows the const if it ever moves.
      RATIONALE CORRECTED (reviewer HIGH): the first draft justified the scope as
      "agent-read content". That is FALSE — `read_workspace` serves Amelia any
      repo-relative path with no extension filter
      (`internal/tools/readworkspace/readworkspace.go:107-176`), advertised in
      every developer dispatch, so by that test every fixture file qualifies and
      the line collapses. The true line is content PUSHED INTO EVERY BRIEF
      UNBIDDEN: standards text is minted as `agent.lesson.injection-form`
      (`internal/standards/sync.go:276`) and rendered verbatim by the framework.
      `.json` is excluded for a stronger reason than taste — semdev's SB2 contract
      MANDATES the literal token `semdev` as the devcontainer customizations key
      (`internal/harness/customizations.go:77`, the enforcing struct tag), so scanning `.json` is
      PERMANENTLY UNSATISFIABLE, proven by declaring it and watching the committed
      fixture go red.
      RED-FIRST, three proofs, each run and reverted:
      (1) a kind whose specimen its own matcher rejects → `TestFixtureScanReaches
      EveryDeclaredKind` fatals (the earlier hardcoded version stayed GREEN with
      three unreached extensions declared — both reviewers found this
      independently);
      (2) widening back to all `.yaml`/`.yml` → `TestRealisticRepoConfigStaysOut
      OfScope` reds on `.golangci.yml` and the issue template;
      (3) declaring `.json` a scanned kind → reds on the committed
      `devcontainer.json` with `[semdev]`.
      Also folded: violation output is now sorted (map order was nondeterministic,
      hurting CI log diffability); the negative control asserts what the walk
      OPENED (a total count) rather than only what it flagged, which was one typo
      from vacuous; every reach specimen carries the same `// TODO` marker so the
      pin measures reachability alone (the `.md` plant had rested entirely on the
      `amelia` vocab entry, coupling reachability to the vocabulary list);
      `scanFixtureTree` documents that its maps are PARTIAL when err != nil.
      ⚠ RESIDUALS, recorded not fixed:
      - `\bBUG\b` matches ordinary English ("Found a bug? Open an issue.") in
        prose kinds. Pre-existing and currently dormant — no fixture ships `.md` —
        but this change promotes `.md` to live forward coverage, so it will bite
        the first fixture README. Filed as issue #24.
      - `filepath.WalkDir` does not follow symlinks, so a fixture tree reachable
        only through a symlinked directory is silently invisible. Pre-existing
        WalkDir semantics; the per-kind counters catch the total-loss case.
      - NO fixture ships `.md` at all: the original pin declared it yet had never
        opened a file, hidden behind three `.go` files by a single whole-walk
        total. The kind stays declared with `requiredInTree: false`; the reach pin
        proves the walk opens it synthetically.
      SECOND ROUND (both reviewers re-ran in isolated worktrees; semstreams
      APPROVE, go-reviewer no-blocking/no-high). Both independently found the
      same latent defeat, now fixed and red-proven: a DUPLICATE `label` merges
      the scan counter, so a kind matching nothing real inherits another kind's
      files and satisfies all four guards at once — green with a dead kind
      declared. The reach pin now fatals on a duplicate label, on a nil matcher
      (previously a SIGSEGV raised from a different test), and on an unclassified
      `treeFloor`.
      Also folded: `TestRealisticRepoConfigStaysOutOfScope` now drives the REAL
      walk instead of only the classifier — widening scope INSIDE
      `scanFixtureTree` had left every pin green, verified and now red; the
      standards matcher is case-folded, because on a case-insensitive host FS the
      product's `filepath.Join(checkoutRoot, Path)` would open a case-variant the
      pin never scanned; and the shadowed-kind failure message no longer names
      the wrong cause (the file WAS opened, just attributed to an earlier kind —
      `fixtureKindOf` returns the FIRST match).
      Five red-first proofs total, each run and reverted; `go test
      ./test/conformance/ -count=1` green, `-count=2 -shuffle=on -race` green.

- [ ] 7.2 `openspec validate standards-via-lessons --strict` green;
      `/opsx:verify`; evidence named in the change (pins + the green e2e
      run; no evidence-ledger entry — no run-level claim).
- [ ] 7.3 Sync deltas + archive (expect the dev-from-task floors-requirement
      re-merge if PR #7 archived first — reconcile the merged text).
