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
	{"dev-from-task", "test/conformance/rules_test.go", []string{"TestDevRewakeIsSelfExtinguishing"},
		"the dev re-wake publish_agent is fired-once via a self-extinguishing marker (run.dev_kickoff) — no duplicate coordinator on graph replay (restart-safety)"},
	{"T7", "test/conformance/host_neutrality_test.go", []string{"TestNoArcRuleReferencesHostSpecificField"},
		"no arc rule names a code host in a predicate position (forge-io host-neutrality — swap-the-adapter contract)"},
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
