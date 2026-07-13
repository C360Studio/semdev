package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

// regressionManifest is the G6 pin-of-pins: it names the load-bearing guardrail
// tests that must not silently disappear. A refactor that deletes or renames one
// of these makes this manifest go red with the reason the pin existed — so a
// guardrail cannot be quietly removed along with the code it guarded.
var regressionManifest = []struct {
	guardrail string
	path      string
	tests     []string
	why       string
}{
	{"G1", "test/conformance/g1_registry_test.go", []string{"TestSemdevComponentsAreRegistered"},
		"every semdev-added component must carry a registry entry + alignment note"},
	{"G1", "test/conformance/g1_tools_test.go", []string{"TestSemdevToolsAreRegistered"},
		"every semdev-added tool must carry a KindTool registry entry + alignment note"},
	{"G1", "test/conformance/g1_parity_test.go", []string{"TestBinariesRegisterOnlyThroughBoot"},
		"both binaries bring up their entire runtime only through boot.Run — registration, NATS, and the ServiceManager never wired independently (half-wired-binary class)"},
	{"G1", "test/conformance/g1_alignment_test.go", []string{"TestRegistryEntriesHaveAlignmentNotes"},
		"every registry entry links a resolvable framework-alignment note"},
	{"G2", "test/conformance/g2_lifecycle_test.go", []string{"TestNoLifecycleTransitionCallersInProductGo", "TestLifecycleExceptionTableIsEmpty"},
		"product Go fires zero lifecycle transitions; the exception table stays empty (anti-B3)"},
	{"G3", "test/conformance/g3_schema_test.go", []string{"TestNoToolSchemaAcceptsOutcomeField"},
		"no tool schema accepts a caller-supplied outcome field"},
	{"G5", "test/conformance/g5_writers_test.go", []string{"TestSingleWriterPerPredicate", "TestToolSourceMatchesVocabWriter"},
		"every predicate has exactly one writer; each tool's stamped Source equals its declared vocab writer"},
	{"G9", "test/conformance/g9_vocab_test.go", []string{"TestVocabularyProvenance"},
		"every predicate points at its introducing change"},
	{"G7", "internal/ledger/ledger_test.go", []string{"TestValidateRejectsUnverifiedPass", "TestMockRunIsBridgeProofNotRealEvidence"},
		"the ledger never records an unverified pass; mock runs are bridge proof"},
	{"G6", "internal/mockllm/mockllm_test.go", []string{"TestEndpointIsLoopbackZeroToken"},
		"the mock ladder spends zero paid tokens (loopback-only endpoint)"},
	{"G10", "test/conformance/g10_docs_test.go", []string{"TestDocsVocabularyMatchesRegistry", "TestDocsComponentsMatchRegistry"},
		"the architecture docs' fact-vocabulary and component/tool tables match the code registry"},
	{"T1", "test/conformance/taxonomy_test.go", []string{"TestPersonaDeclaresExactTaxonomy", "TestNoRuleRoutesOutOfTaxonomyAction"},
		"the coordinator persona declares exactly the closed taxonomy; no rule routes an out-of-taxonomy action"},
	{"run-lifecycle", "test/conformance/rules_test.go", []string{"TestLifecycleTransitionsTargetValidAgentRunEdges", "TestChangeApprovalGateOrdering", "TestChangeApprovalGateFreshnessForwardContract", "TestParkRuleStampsAwaitingHuman", "TestLifecycleTransitionRulesExcludeParkedRuns"},
		"lifecycle transitions target valid agent-run edges; gate ordering, deferred content-freshness tripwire (D15 #0), park, and park-exclusion (D15) hold"},
	{"dev-from-task", "test/conformance/rules_test.go", []string{"TestDevRewakeIsSelfExtinguishing", "TestProjectionSpawnIsSelfExtinguishing", "TestDevRewakeGatedOnProjection", "TestDispatchDeveloperIsSelfExtinguishing", "TestMeasureTriggerIsSelfExtinguishing", "TestFloorsTriggerIsSelfExtinguishing", "TestGateTriggerIsSelfExtinguishing", "TestGateRoutersAreSelfExtinguishing", "TestOnlySanctionedDeveloperSpawners"},
		"the dev re-wake + projection + dispatch-developer + measure + floors + gate + router publish_agent spawns are fired-once via self-extinguishing markers (run.dev_kickoff / run.projection_kickoff / dev.dispatched / dev.measured / dev.floors_dispatched / dev.gate_dispatched / dev.routed); the dev re-wake is gated behind task.spec projection (Codex P1); measure fires only on the developer loop's SUCCESSFUL terminal and appends the per-task attempt counter; floors chains off the measure loop and the gate off the floors loop via loop markers (warn-free, not #519 field-to-field triggers); the gate routes advance/retry/escalate; and ONLY dispatch-developer (04) + the gate's retry router (08b) may spawn a developer — the load-bearing one-in-flight serialization invariant that replaced the token handshake"},
	{"H1", "internal/floors/checks_test.go", []string{"TestPresenceFloor"},
		"the presence floor rejects an attempt that authored NONE of its declared targets — closes the g4 hole where an all-absent attempt reads GREEN through the five vacuous floors (the SB5 'verified over zero executions' theater)"},
	{"T7", "test/conformance/host_neutrality_test.go", []string{"TestNoArcRuleReferencesHostSpecificField"},
		"no arc rule names a code host in a predicate position (forge-io host-neutrality — swap-the-adapter contract)"},
	{"G8", "test/conformance/g8_fixtures_test.go", []string{"TestBannedFixtureTermsScan", "TestFixturesFreeOfOrchestrationVocabulary"},
		"dev-loop fixtures read like a real repo — no coaching markers or harness/orchestration vocabulary (realistic fixtures; the theater that let both predecessors succeed against a scaffold)"},
	{"G4", "internal/coldproof/baseline_test.go", []string{"TestProveBaselineRealReady", "TestProveBaselineRealFabricationNotReady"},
		"the operator-declared image proves the repo resolves+builds COLD before the dev loop relies on it; a fabricated dependency fails cold and parks — never a pass over an unproven sandbox (the make-or-break both donors died on)"},
	{"G4", "test/fixtures/fixtures_test.go", []string{"TestGoHealthClassCompilesButTestsFail", "TestFabricatedVariantNotReady"},
		"the committed fixture is a real module with a real failing test (builds but tests red); its cache-masked-fabrication variant is rejected cold on the committed artifact"},
	{"SB6", "internal/runspace/patcher_test.go", []string{"TestPatcherRejectsPathEscape", "TestPatcherFailsClosedWithoutCheckout"},
		"apply_patch's containment: a diff that escapes the checkout is rejected before git runs (nothing lands on the host) and a run with no materialized checkout fails closed — the security guarantee g7's in-container refactor must not silently drop"},
	{"SB6", "internal/runspace/patcher_fixture_test.go", []string{"TestPatcherFixesFixtureRedToGreen"},
		"apply_patch authors the fixture's REAL fix and the previously-failing go test goes green — the author→measure loop proven against real code, not a claim (the non-theater proof)"},
	{"SB4", "internal/tools/checkgate/checkgate_test.go", []string{"TestDecideRoutingTable", "TestDecideBudgetBoundaryIsExact", "TestExecuteFailsClosedOnMissingMeasurement", "TestExecuteCountsDistinctAttemptObjects"},
		"the dev-loop gate routes advance/retry/escalate over the whole passed×rejected×count-vs-budget space (the live loop only drives the happy path); it fails CLOSED (escalate) on a missing judgment fact — never a false retry; the budget boundary is exact; and it counts DISTINCT attempt objects so the at-least-once append never over-counts toward premature escalation"},
	{"SB3", "internal/coldproof/baseline_test.go", []string{"TestProveArtifactRealPass", "TestProveArtifactRealTestsFailIsFail", "TestProveArtifactRealFabricationIsFail"},
		"the clean-room FINAL verify proves the COMMITTED artifact's own TESTS cold in a fresh throwaway container — a passing artifact verifies, an artifact that builds but whose tests FAIL cold rejects (the distinct-from-baseline behavior: verify runs tests, the baseline only builds), and a fabricated dependency a warm cache would mask fails cold; the make-or-break both predecessors faked (semspec's harness-fixup that 401'd on a clean checkout)"},
	{"SB4", "internal/runspace/runspace_test.go", []string{"TestCloneForVerifyIsFreshAndNonDestructive", "TestCloneForVerifyFailsClosedAndReaps"},
		"the cold-verify clone is a FRESH copy of the committed artifact that NEVER touches the warm checkout (the applied diff survives for a retry — the group-4 destructive-re-materialize trap), fails closed with no warm checkout, and reaps prior clones"},
	{"SB3", "internal/forbidden/forbidden_test.go", []string{"TestScanCatchesRawURLFetchInDockerfile", "TestScanCatchesNetworkToolsAndGradle", "TestScanCatchesFetchSmuggledIntoInvokedScript", "TestScanIgnoresNonBuildFiles"},
		"the build-file tripwire catches a hidden runtime download (a raw-URL fetch, a semdev network tool) in a committed build file (Dockerfile/Gradle) AND in a script the build INVOKES (`RUN ./setup.sh` — the semspec init.d substitution vector, and the sole static control since the cold container is networked), while ignoring a URL in source/docs; a non-self-contained artifact that would dodge the cold-resolution proof is caught statically"},
	{"SB3", "internal/coldproof/baseline_test.go", []string{"TestProveArtifactForbiddenPatternFailsWithoutBuilding"},
		"a committed build file with a hidden runtime download fails the cold verify (Fail) via the tripwire short-circuit BEFORE any image build — the 'a fix that builds only via a would-be harness fixup fails cold' guard, honest (only the evaluated self-contained check is reported)"},
}

// G6 — a named regression pin cannot silently disappear. This manifest fails if
// a guardrail test named here is deleted or renamed. semspec's floors were
// discovered severed from production; this keeps the pins attached.
func TestRegressionManifest(t *testing.T) {
	root := repoRoot(t)
	for _, entry := range regressionManifest {
		src, err := os.ReadFile(filepath.Join(root, entry.path))
		if err != nil {
			t.Errorf("[%s] cannot read %s: %v — pin file deleted? (%s)", entry.guardrail, entry.path, err, entry.why)
			continue
		}
		names, err := testFunctionNames(src)
		if err != nil {
			t.Errorf("[%s] parse %s: %v", entry.guardrail, entry.path, err)
			continue
		}
		have := make(map[string]bool, len(names))
		for _, n := range names {
			have[n] = true
		}
		for _, req := range entry.tests {
			if !have[req] {
				t.Errorf("[%s] %s is missing from %s — %s (a named pin cannot silently disappear, G6)", entry.guardrail, req, entry.path, entry.why)
			}
		}
	}
}

// Red-first: the extraction must find real Test functions and ignore non-tests
// and methods, so the manifest cannot pass vacuously by matching nothing.
func TestManifestExtractionFindsTests(t *testing.T) {
	src := []byte(`package x
func TestReal(t *testing.T) {}
func TestAnother(t *testing.T) {}
func helper() {}
func (r rcv) TestMethod() {}
`)
	names, err := testFunctionNames(src)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["TestReal"] || !got["TestAnother"] {
		t.Errorf("extraction missed real test functions: %v", names)
	}
	if got["helper"] || got["TestMethod"] {
		t.Errorf("extraction wrongly included a non-test or method: %v", names)
	}
}
