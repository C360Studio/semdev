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
	{"G1", "test/conformance/g1_parity_test.go", []string{"TestBinariesRegisterOnlyThroughBoot"},
		"both binaries register only through boot.RegisterAll (half-wired-binary class)"},
	{"G1", "test/conformance/g1_alignment_test.go", []string{"TestRegistryEntriesHaveAlignmentNotes"},
		"every registry entry links a resolvable framework-alignment note"},
	{"G2", "test/conformance/g2_lifecycle_test.go", []string{"TestNoLifecycleTransitionCallersInProductGo", "TestLifecycleExceptionTableIsEmpty"},
		"product Go fires zero lifecycle transitions; the exception table stays empty (anti-B3)"},
	{"G3", "test/conformance/g3_schema_test.go", []string{"TestNoToolSchemaAcceptsOutcomeField"},
		"no tool schema accepts a caller-supplied outcome field"},
	{"G5", "test/conformance/g5_writers_test.go", []string{"TestSingleWriterPerPredicate"},
		"every predicate has exactly one writer"},
	{"G9", "test/conformance/g9_vocab_test.go", []string{"TestVocabularyProvenance"},
		"every predicate points at its introducing change"},
	{"G7", "internal/ledger/ledger_test.go", []string{"TestValidateRejectsUnverifiedPass", "TestMockRunIsBridgeProofNotRealEvidence"},
		"the ledger never records an unverified pass; mock runs are bridge proof"},
	{"G6", "internal/mockllm/mockllm_test.go", []string{"TestEndpointIsLoopbackZeroToken"},
		"the mock ladder spends zero paid tokens (loopback-only endpoint)"},
	{"G10", "test/conformance/g10_docs_test.go", []string{"TestDocsVocabularyMatchesRegistry"},
		"the architecture docs' fact-vocabulary table matches the code registry"},
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
