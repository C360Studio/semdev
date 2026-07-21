package conformance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c360studio/semdev/internal/conversationintent"
)

const conversationContractDoc = "configs/personas/fragments/conversation/10-decision-contract.md"

// TestConversationPersonaDeclaresExactIntentTaxonomy — the conversation classifier
// persona declares EXACTLY the canonical closed intent taxonomy
// (nl-conversation-intent D1). The LLM learns the vocabulary from this fragment; if
// it drifts from internal/conversationintent, the model and the rule layer disagree
// (a hallucinated intent could route, or a real one could be dropped). Bidirectional
// set-equality, mirroring the coordinator taxonomy census.
//
// The routing-RULE arm (no rule routes an off-taxonomy intent; every directive
// intent has exactly one route) is TestConversationRoutingRulesMatchTaxonomy in
// conversation_rules_test.go — landed with the rules in group 4.
func TestConversationPersonaDeclaresExactIntentTaxonomy(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), conversationContractDoc))
	if err != nil {
		t.Fatalf("read %s: %v", conversationContractDoc, err)
	}
	rows := markdownTableRows(src, "Valid intent values (closed taxonomy for this deployment)")
	if len(rows) == 0 {
		t.Fatalf("no closed-taxonomy table found in %s", conversationContractDoc)
	}

	declared := make(map[string]bool)
	for _, cells := range rows {
		if len(cells) > 0 && cells[0] != "" {
			declared[cells[0]] = true
		}
	}
	canonical := make(map[string]bool)
	for _, v := range conversationintent.Names() {
		canonical[v] = true
	}

	for v := range canonical {
		if !declared[v] {
			t.Errorf("intent %q is canonical but not declared in the conversation persona decision contract", v)
		}
	}
	for v := range declared {
		if !canonical[v] {
			t.Errorf("intent %q is declared in the conversation persona decision contract but not canonical (out-of-taxonomy)", v)
		}
	}
}

// TestConversationIntentCensusCatchesDrift is the red-first meta-test proving the
// bidirectional census above actually FIRES on planted drift — a persona that drops a
// canonical intent AND one that adds a bogus intent must both be caught (mirroring the
// coordinator taxonomy's TestTaxonomyCensusesCatchDrift). Guards against a census that
// passes vacuously.
func TestConversationIntentCensusCatchesDrift(t *testing.T) {
	canonical := map[string]bool{}
	for _, v := range conversationintent.Names() {
		canonical[v] = true
	}
	// A persona missing "reject" and adding a bogus intent — both directions.
	declared := map[string]bool{"approve": true, "none": true, "totally_made_up": true}
	var missing, extra int
	for v := range canonical {
		if !declared[v] {
			missing++
		}
	}
	for v := range declared {
		if !canonical[v] {
			extra++
		}
	}
	if missing == 0 || extra == 0 {
		t.Errorf("conversation-intent census under-fires: missing=%d extra=%d (want both > 0)", missing, extra)
	}
	// An off-taxonomy intent must be invalid (the routing pin would not fire otherwise).
	if conversationintent.Valid("totally_made_up") {
		t.Error("conversationintent.Valid accepted an off-taxonomy intent; the routing pin would not fire")
	}
}
