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

- [ ] 4.1 RED: a provision-level pin — provisioning a fixture with a
      standards file blocks/parks on a malformed file, and succeeds
      birthing+activating records on a valid one (fakes at the station seam).
- [ ] 4.2 GREEN: the `Standards` seam on `ProvisionDeps` (after Materialize,
      before ProveBaseline), boot construction (store + curator from the
      shared graphown mutation client + platform identity — REJECT nil
      surfaces loudly, go-review R2), park-on-malformed via the existing
      block path.
- [ ] 4.2b The `RunLaunch` vocab-registration gap (semstreams-review HIGH,
      PRE-EXISTING): `internal/boot/launch.go` builds the mutation client
      without `vocab.Register()`, which should fail contract validation —
      the launch lane dead since beta.159. VERIFY empirically against docker
      NATS first (the reviewer could not execute it), then fix
      (`vocab.Register()` at the top of RunLaunch) + pin per what the
      verification shows.
- [ ] 4.3 Docker journey (red-first): provision a fixture repo carrying a
      stripped standards file → assert records born `active` in the graph
      with resolving evidence, idempotent on re-provision (D9 items 1).
- [ ] 4.4 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 4.

## 5. The checks lane (D7)

- [ ] 5.1 RED: parser table extension (D1 fold, DR-0001) — each denied
      fail-open construct rejects naming its defect: suppression
      (`|| true`, `|| :`, trailing `; true`, `2>/dev/null`, `2>&-`), vacuity
      (`true`, `:`, bare `echo …`), and an unquoted top-level pipeline on a
      `required` check (non-required warns). Pins the scanner's two traps:
      `||` is not a pipe, and a pipe inside quotes is not top-level. Plus the
      `proof` field's parse (optional, non-empty when present, same denials).
- [ ] 5.2 GREEN: extend `validateCheck`/`Check` in `internal/standards` for
      5.1. Extends the group-2 parser; does not amend its commit.
- [ ] 5.3 RED: floors-level pins — (a) a required check whose command exits
      non-zero stamps a rejecting `repo-check:<name>` finding and the rail
      does not advance; (b) a non-required failure stamps non-rejecting;
      (c) zero-checks file/absent file leaves floors byte-identical;
      (d) the base-ref read — an attempt-modified standards file does NOT
      change the executed checks (the refs/semdev/base copy governs).
- [ ] 5.4 RED: gate-honesty pins (D7a) — (a) a declared `proof` exiting
      non-zero marks the check `proven` and the check then runs normally;
      (b) a `proof` exiting ZERO stamps the un-failing-gate finding, rejecting
      iff `required` (a non-required check with a passing control never
      rejects); (c) an absent `proof` gates as declared and stamps `unproven`;
      (d) a check the runner could not execute stamps `not-run`, is never a
      pass, and is distinguishable in the finding text from ran-and-failed —
      red-first against a deliberately dead container, the measure_task
      exit-vs-transport contract reused.
- [ ] 5.5 GREEN: `checkfloors.RunFloors` checks stage — base-ref file read,
      in-container `runner.Exec` per check (control first when declared),
      findings stamped via the existing writer carrying the gate status; the
      two new narrow deps wired at the floors station.
- [ ] 5.6 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 5.

## 6. The judgment lane + the full bridge proof (D8, D9)

- [ ] 6.1 Persona fragments: `reviewer/10-standards-contract.md` +
      `developer/10-standards.md` (cite-the-id duty; standards tighten,
      never weaken); persona-dir conformance stays green.
- [ ] 6.2 RED then GREEN: `TestBridgeProofRepoStandardsReachBriefsAndGate` —
      the full D9 journey: birth+activation, role-scoped brief content via
      captured mock prompts (developer sees developer standards, not
      reviewer-only), the required-check gate (failing check → floors reject
      → no review), and the green path end-to-end.
- [ ] 6.3 Full offline suite + `task e2e -race` green; adversarial review;
      commit group 6.

## 7. Conformance hardening + close

- [ ] 7.1 G8/B10: extend the fixture-vocabulary scan to `.yaml`/`.yml`; the
      journey fixtures' standards files pass; the meta-pin proves the scan
      fires on a planted coaching term.
- [ ] 7.2 `openspec validate standards-via-lessons --strict` green;
      `/opsx:verify`; evidence named in the change (pins + the green e2e
      run; no evidence-ledger entry — no run-level claim).
- [ ] 7.3 Sync deltas + archive (expect the dev-from-task floors-requirement
      re-merge if PR #7 archived first — reconcile the merged text).
