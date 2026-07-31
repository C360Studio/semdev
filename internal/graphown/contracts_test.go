package graphown_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/pkg/ownership"
	"github.com/c360studio/semstreams/pkg/projection"
	semtypes "github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/vocab"
)

// The census lives HERE rather than in test/conformance deliberately. It pins
// graphown's own derivation, and the conformance package transitively imports the
// tool packages that do not compile until the whole beta.159 write-path migration
// lands — a census that cannot run during the migration it exists to guard is not
// a guard. Cross-cutting rule/config censuses stay in test/conformance, EXCEPT the
// two route-mirror preconditions at the bottom of this file, which exist only
// because this package's contract shape depends on them.

func TestMain(m *testing.M) {
	// Contract.Validate defers predicate checks to the framework's vocabulary
	// registry, so semdev's names must be declared before any contract validates.
	vocab.Register()
	m.Run()
}

func contracts(t *testing.T) []graphown.OwnedContract {
	t.Helper()
	cs, err := graphown.Contracts()
	if err != nil {
		t.Fatalf("derive contracts: %v", err)
	}
	return cs
}

// TestProjectionContractsMatchVocabWriters is the migrate-semstreams-beta159 D1/D8
// census: the derived projection contracts PARTITION the vocab table exactly — every
// product predicate is covered EXACTLY ONCE, by a Go-writer contract, an excluded
// rule-writer Source, or the explicit declared-but-unwired set. Never two, never none.
//
// This is the anti-drift guard the whole migration rests on. It is NOT circular with
// the derivation (which groups vocab predicates by writer): it pins the Go-vs-rule
// SPLIT (graphown.ruleWriterSources) and the unwired exclusion against the vocab
// writer set, so a new predicate added to vocab without classifying its writer, or a
// writer wrongly moved across the split, fails here rather than silently producing an
// un-owned write or a phantom contract (bind of an owner with no Go writer).
func TestProjectionContractsMatchVocabWriters(t *testing.T) {
	// The predicate → (contract, owner) map, from the derivation.
	type owned struct{ contract, owner string }
	owningContract := map[string]owned{}
	for _, oc := range contracts(t) {
		record := func(p string) {
			if prev, dup := owningContract[p]; dup {
				t.Errorf("predicate %q is owned by TWO contracts (%q and %q) — G5 says one writer per predicate", p, prev.contract, oc.Contract.Name)
			}
			owningContract[p] = owned{contract: oc.Contract.Name, owner: oc.Owner}
		}
		for _, g := range oc.Contract.Groups {
			for _, p := range g.Predicates {
				record(p)
			}
		}
		for _, p := range oc.Contract.BirthPredicates {
			record(p)
		}
	}

	// Every vocab predicate is covered EXACTLY once, on exactly one side of the split.
	for _, p := range vocab.Predicates {
		got, inContract := owningContract[p.Name]
		ruleWritten := graphown.IsRuleWriter(p.Writer)
		unwired := graphown.IsUnwired(p.Name)
		switch {
		case inContract && ruleWritten:
			t.Errorf("predicate %q (writer %q) is BOTH in contract %q AND classified rule-written — the split is inconsistent", p.Name, p.Writer, got.contract)
		case inContract && unwired:
			t.Errorf("predicate %q is BOTH in contract %q AND listed unwired — it cannot be both owned and unwritten", p.Name, got.contract)
		case ruleWritten && unwired:
			t.Errorf("predicate %q (writer %q) is BOTH rule-written and listed unwired — pick one", p.Name, p.Writer)
		case !inContract && !ruleWritten && !unwired:
			t.Errorf("predicate %q (writer %q) is in NO contract, NOT a known rule-writer, and NOT listed unwired — classify it (Go writer → entityClass entry, engine writer → ruleWriterSources, no writer yet → unwiredPredicates)", p.Name, p.Writer)
		case inContract && got.owner != p.Writer:
			t.Errorf("predicate %q is owned by %q but the vocab writer is %q — they must be the same identity (G5)", p.Name, got.owner, p.Writer)
		}
	}

	// Reverse: no contract owns a predicate absent from vocab (a phantom).
	declared := map[string]bool{}
	for _, p := range vocab.Predicates {
		declared[p.Name] = true
	}
	for p, got := range owningContract {
		if !declared[p] {
			t.Errorf("contract %q owns predicate %q that is not in the vocab table (phantom)", got.contract, p)
		}
	}
}

// TestExclusionSetsAreNotStale is the reverse-direction guard on every hand-maintained
// table: an entry naming a predicate or Source the vocab table no longer declares.
//
// The teeth: delete a predicate from vocab but leave its Source in ruleWriterSources,
// then later add a GO-written predicate under a name that collides with that stale
// Source. It is silently classified rule-written, derives no contract, and becomes an
// un-owned write — invisible to the partition census above (which only walks predicates
// vocab still declares) and, per design D5, invisible to the owner-lease meter too.
func TestExclusionSetsAreNotStale(t *testing.T) {
	declaredPredicates := map[string]bool{}
	declaredWriters := map[string]bool{}
	for _, p := range vocab.Predicates {
		declaredPredicates[p.Name] = true
		declaredWriters[p.Writer] = true
	}

	for p := range graphown.UnwiredPredicates() {
		if !declaredPredicates[p] {
			t.Errorf("unwiredPredicates lists %q, which is not in the vocab table — a stale exclusion hides a real classification gap", p)
		}
	}
	for p := range graphown.EntityClasses() {
		if !declaredPredicates[p] {
			t.Errorf("entityClass classifies %q, which is not in the vocab table — a stale entry silently classifies a future predicate of the same name", p)
		}
	}
	for _, s := range graphown.RuleWriterSources() {
		if !declaredWriters[s] {
			t.Errorf("ruleWriterSources excludes Source %q, which writes nothing in the vocab table — a stale exclusion would silently un-own a future Go predicate under that name", s)
		}
	}
	for _, owner := range graphown.CreateOwners() {
		if !declaredWriters[owner] {
			t.Errorf("createOwners lists %q, which writes nothing in the vocab table", owner)
		}
	}
}

// TestContractModesAreConservative pins the migrate-semstreams-beta159 D3 CONSERVATIVE
// decision: every contract group is replace-owned, birth predicates appear only under
// the sanctioned create owners, and append-evidence is adopted NOWHERE. A planted
// append-evidence group (an accidental semantic change to a ledger — silent data loss)
// fails here.
func TestContractModesAreConservative(t *testing.T) {
	sawBirth := map[string]bool{}
	for _, oc := range contracts(t) {
		for _, g := range oc.Contract.Groups {
			if g.Mode != ownership.ModeReplaceOwned {
				t.Errorf("contract %q group %q has mode %q, want replace-owned — append-evidence is deferred (D3), and a ledger silently loses entries under the wrong mode", oc.Contract.Name, g.Name, g.Mode)
			}
			if g.Name != graphown.OwnedGroup {
				t.Errorf("contract %q group is named %q, want %q — call sites select the group by that one name", oc.Contract.Name, g.Name, graphown.OwnedGroup)
			}
		}
		if len(oc.Contract.BirthPredicates) > 0 {
			sawBirth[oc.Owner] = true
			if !graphown.IsCreateOwner(oc.Owner) {
				t.Errorf("contract %q (owner %q) declares birth predicates but is not a sanctioned create owner — only entity-creation owners carry birth predicates (D3/OQ2)", oc.Contract.Name, oc.Owner)
			}
		}
	}
	// Every declared create owner actually produced a birth contract (bidirectional).
	for _, owner := range graphown.CreateOwners() {
		if !sawBirth[owner] {
			t.Errorf("create owner %q has no birth predicates in its contract — the derivation dropped its creation facts", owner)
		}
	}
}

// TestBirthOnlyOwnersOwnNothing pins the framework consequence of a birth-only
// contract, because three artifacts previously claimed the opposite.
//
// Birth predicates "derive no ownership claim" (projection/contract.go:66-70). So
// admission-check registers NO claim, mints the ZERO token, needs no heartbeater, and
// its create lands with an EMPTY wire OwnerToken — which graph-ingest's checkOwnerLease
// skips unconditionally (`if ownerToken == "" { return nil }`), in BOTH enforcement
// postures. The admission record is deliberately unowned and ungated.
//
// This is pinned rather than merely documented because the composition root's
// one-bind-per-owner invariant does NOT hold for these owners — Bind returns before
// RegisterOwner, so a second bind succeeds silently instead of returning
// ErrOwnerAlreadyBound. Group 3's pin must scope to OwningOwners().
func TestBirthOnlyOwnersOwnNothing(t *testing.T) {
	owners, err := graphown.Owners()
	if err != nil {
		t.Fatalf("owners: %v", err)
	}
	owning, err := graphown.OwningOwners()
	if err != nil {
		t.Fatalf("owning owners: %v", err)
	}
	for _, oc := range contracts(t) {
		if !graphown.IsCreateOwner(oc.Owner) {
			continue
		}
		if len(oc.Contract.Groups) != 0 {
			t.Errorf("create owner %q carries %d group(s) — a birth predicate overlapping a group is rejected by Contract.Validate, so this cannot be intentional", oc.Owner, len(oc.Contract.Groups))
		}
		if slices.Contains(owning, oc.Owner) {
			t.Errorf("OwningOwners includes birth-only owner %q — it registers no claim and mints no token, so an ErrOwnerAlreadyBound pin over it would be vacuous", oc.Owner)
		}
		if !slices.Contains(owners, oc.Owner) {
			t.Errorf("Owners omits %q — a birth-only owner still binds a client, it just owns nothing", oc.Owner)
		}
	}
	if len(owning) >= len(owners) {
		t.Errorf("OwningOwners (%d) is not a strict subset of Owners (%d) — the birth-only distinction collapsed", len(owning), len(owners))
	}
}

// TestContractsClaimTheEntityClassTheirWritersStamp asserts the structural half of the
// ONE-CONTRACT-PER-(OWNER, ENTITY CLASS) shape: every contract claims one of the
// DECLARED entity classes, and ContractFor round-trips a representative entity of that
// class back to that same contract.
//
// WHAT THIS DOES NOT PROVE — read before trusting it. It cannot tell whether a
// predicate is classed onto the RIGHT entity. Re-classing every loop predicate to
// RunPattern (the original defect: one run-patterned contract per owner) still passes
// this test, both censuses above, and Contract.Validate — and then hard-fails at
// runtime on every loop-entity write, because ReplaceOwned rejects an entity outside
// the contract's pattern. Verified by planting exactly that.
//
// The real proof is BEHAVIORAL and lives at the call sites: every migrated write
// resolves its contract through graphown.ContractFor(owner, entityID) rather than a
// hardcoded name, so a predicate classed onto the wrong entity makes ContractFor
// return an error at the site that writes it — and the per-tool unit tests that
// already assert which entity each fact lands on (classifyintent's "the mirror must be
// written to the loop, not merely carry it as a Subject", checkfloors' loop-entity
// mirror pin) go red. That coupling is why ContractFor exists at all; keep it.
func TestContractsClaimTheEntityClassTheirWritersStamp(t *testing.T) {
	for pattern, entity := range representatives() {
		ok, err := semtypes.MatchEntityIDPattern(pattern, entity)
		if err != nil || !ok {
			t.Fatalf("representative entity %q does not match its own class pattern %q (ok=%v, err=%v) — the fixture is wrong, not the code", entity, pattern, ok, err)
		}
	}

	for _, oc := range contracts(t) {
		entity, declared := representatives()[oc.Contract.EntityPattern]
		if !declared {
			t.Errorf("contract %q claims entity pattern %q, which is not one of graphown's declared entity classes — an unclassified pattern is an unreviewed ownership claim", oc.Contract.Name, oc.Contract.EntityPattern)
			continue
		}
		got, err := graphown.ContractFor(oc.Owner, entity)
		if err != nil {
			t.Errorf("ContractFor(%q, %q) failed: %v — every contract must be reachable from its owner and an entity of its class", oc.Owner, entity, err)
			continue
		}
		// AgentExecPattern deliberately overlaps RunPattern, so its representative
		// resolves through the widened contract; only the non-overlapping classes
		// round-trip to the exact contract name.
		if oc.Contract.EntityPattern != graphown.AgentExecPattern && got != oc.Contract.Name {
			t.Errorf("ContractFor(%q, %q) = %q, want %q — the entity-class resolution disagrees with the derivation", oc.Owner, entity, got, oc.Contract.Name)
		}
	}
}

func representatives() map[string]string {
	return map[string]string{
		graphown.RunPattern:       runEntity,
		graphown.LoopPattern:      loopEntity,
		graphown.AdmissionPattern: "c360.semdev.forge.intake.event.evt1",
		graphown.AgentExecPattern: runEntity,
	}
}

const (
	runEntity  = "c360.semdev.agent.chain.execution.run1"
	loopEntity = "c360.semdev.agent.agentic-loop.execution.loop1"
)

// TestAgentExecPatternSpansBothExecutionClasses pins the ONE thing AgentExecPattern
// exists for — and the one thing the round-trip test above cannot see, because its
// representative for that class is a RUN entity.
//
// station.dispatch.failed is stamped on whatever entity the station was dispatched
// FOR. run-lifecycle/06-park-station-failure-loop fires on the LOOP class, so if the
// pattern is ever narrowed to the run class (e.g. copy-pasted to "*.*.agent.chain.*.*"),
// ContractFor errors inside stampDispatchFailed, the loop-fired station park never
// stamps, and the run stalls unattended forever — the exact failure class
// station-failure-parks exists to close. Verified: that narrowing passes every other
// test in this file.
func TestAgentExecPatternSpansBothExecutionClasses(t *testing.T) {
	for _, entity := range []string{runEntity, loopEntity} {
		ok, err := semtypes.MatchEntityIDPattern(graphown.AgentExecPattern, entity)
		if err != nil {
			t.Fatalf("match %q: %v", entity, err)
		}
		if !ok {
			t.Errorf("AgentExecPattern %q does not match %q — a station dispatched on that entity class silently loses its terminal-failure fact", graphown.AgentExecPattern, entity)
		}
		if _, err := graphown.ContractFor("station-harness", entity); err != nil {
			t.Errorf("ContractFor(station-harness, %q): %v — both execution classes must resolve", entity, err)
		}
	}
	// The narrow classes must stay narrow, so a copy-paste that widens RunPattern to
	// swallow the loop class (collapsing the D2a split) also goes red.
	if ok, _ := semtypes.MatchEntityIDPattern(graphown.RunPattern, loopEntity); ok {
		t.Error("RunPattern matches a LOOP entity — the run/loop contract split is no longer a split")
	}
	if ok, _ := semtypes.MatchEntityIDPattern(graphown.LoopPattern, runEntity); ok {
		t.Error("LoopPattern matches a RUN entity — the run/loop contract split is no longer a split")
	}
}

// TestContractForRejectsAnUnownedEntityClass pins the fail-loud half: an owner asked
// for a contract on an entity class it does not claim gets a named error, not a
// silently wrong contract. This is what turns a mis-targeted write into a call-site
// failure instead of a mutation-client rejection buried in a retry loop.
func TestContractForRejectsAnUnownedEntityClass(t *testing.T) {
	// measurement-harness writes only run facts; the admission record is not its class.
	if got, err := graphown.ContractFor("measurement-harness", "c360.semdev.forge.intake.event.evt1"); err == nil {
		t.Errorf("ContractFor(measurement-harness, <admission entity>) = %q, want an error — writing outside the owned entity class must fail loudly", got)
	}
	if _, err := graphown.ContractFor("no-such-owner", runEntity); err == nil {
		t.Error("ContractFor with an unknown owner returned no error — an unbound owner must not resolve a contract")
	}
}

// TestEveryOwnerDerivesWithoutSelfOverlap runs each owner's FULL contract set through
// projection.Derive — the same validation BindMutationClient performs at boot. It
// catches two things the per-contract checks cannot: a contract that fails
// Contract.Validate (bad pattern, undeclared predicate, duplicate group name), and an
// owner whose several contracts claim the SAME cell (self-overlap), which would reject
// the bind and take the owner's whole write path down at boot.
func TestEveryOwnerDerivesWithoutSelfOverlap(t *testing.T) {
	owners, err := graphown.Owners()
	if err != nil {
		t.Fatalf("owners: %v", err)
	}
	if len(owners) == 0 {
		t.Fatal("no projection owners derived — the vocab table produced an empty contract set")
	}
	for _, owner := range owners {
		cs, err := graphown.ContractsFor(owner)
		if err != nil {
			t.Errorf("ContractsFor(%q): %v", owner, err)
			continue
		}
		for _, c := range cs {
			if verr := c.Validate(); verr != nil {
				t.Errorf("contract %q is invalid: %v", c.Name, verr)
			}
		}
		if _, derr := projection.Derive(owner, cs...); derr != nil {
			t.Errorf("owner %q does not derive its %d contracts: %v — this is exactly what BindMutationClient rejects at boot", owner, len(cs), derr)
		}
	}
}

// TestRouteMirrorSitesWriteDisjointEntityPopulations pins the precondition the
// route-mirror contract's SAFETY rests on — the one invariant in this migration that
// is asserted by nothing else.
//
// route-mirror has ONE loop-class group of all seven route.* predicates, written by
// two sites with DIFFERENT subsets: check_floors stamps passed/rejected/budget/
// transient/attempt.instance/transient.instance, submit_review stamps
// review.verdict/budget/attempt.instance. Under ReplaceOwned each site's write REMOVES
// the whole group and re-adds only its own subset — strictly wider than the old
// explicit remove lists (submit_review removed 2 predicates; now it removes all 7).
// That is safe ONLY while the two sites land on disjoint entity populations.
//
// If submit_review were ever advertised to a developer spawn, its write would delete
// route.attempt.passed/rejected/transient from L_n — and ownedFactsMatch verifies the
// group EQUALS Desired, so the deletion reads as success: CommitVerified on silent
// data loss. Splitting the group per site is not available (Contract.Validate forbids
// one predicate in two groups, and the sites overlap on budget + attempt.instance),
// and splitting the write breaks the D7 atomicity invariant. So the pin goes here.
func TestRouteMirrorSitesWriteDisjointEntityPopulations(t *testing.T) {
	rules := loadRules(t)
	sawReviewer, sawFloorsDispatch := false, false

	for _, r := range rules {
		for _, action := range r.OnEnter {
			// (1) submit_review — the review mirror — only ever on a reviewer spawn.
			if action.Type == "publish_agent" && slices.Contains(action.Tools, "submit_review") {
				sawReviewer = true
				if action.Role != "reviewer" {
					t.Errorf("%s advertises submit_review to a %q spawn — the review route mirror would land on a non-reviewer loop and its ReplaceOwned group-wipe would silently delete that loop's route.attempt.* facts", r.path, action.Role)
				}
			}
			// (2) the floors station — the floors mirror — only ever off a developer loop.
			if action.Type == "publish" && strings.Contains(action.Subject, "floors-station") {
				sawFloorsDispatch = true
				if !hasCondition(r, "agent.loop.role", "eq", "developer") {
					t.Errorf("%s dispatches the floors station without pinning the firing entity to a developer loop (agent.loop.role eq developer) — the floors mirror could land on a reviewer loop and group-wipe its route.review.verdict", r.path)
				}
			}
		}
	}

	if !sawReviewer {
		t.Error("no rule advertises submit_review — the review-mirror half of the disjointness pin matched nothing, so it proves nothing (did the tool or spawn move?)")
	}
	if !sawFloorsDispatch {
		t.Error("no rule dispatches the floors station — the floors-mirror half of the disjointness pin matched nothing, so it proves nothing (did the station subject move?)")
	}
}

type ruleDoc struct {
	path       string
	Conditions []struct {
		Field    string `json:"field"`
		Operator string `json:"operator"`
		Value    any    `json:"value"`
	} `json:"conditions"`
	OnEnter []struct {
		Type    string   `json:"type"`
		Role    string   `json:"role"`
		Tools   []string `json:"tools"`
		Subject string   `json:"subject"`
	} `json:"on_enter"`
}

func hasCondition(r ruleDoc, field, operator, value string) bool {
	for _, c := range r.Conditions {
		if c.Field == field && c.Operator == operator && c.Value == value {
			return true
		}
	}
	return false
}

// loadRules reads every shipped rule document. It fails rather than skips on a read
// error: a pin that silently matches nothing is worse than no pin.
func loadRules(t *testing.T) []ruleDoc {
	t.Helper()
	root := filepath.Join("..", "..", "configs", "rules")
	var out []ruleDoc
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		var doc ruleDoc
		if jerr := json.Unmarshal(raw, &doc); jerr != nil {
			return fmt.Errorf("%s: %w", path, jerr)
		}
		doc.path = filepath.Base(path)
		out = append(out, doc)
		return nil
	})
	if err != nil {
		t.Fatalf("load rule configs from %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("no rule documents found under %s — the disjointness pin would pass vacuously", root)
	}
	return out
}
