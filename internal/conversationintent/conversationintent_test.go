package conversationintent

import (
	"sort"
	"testing"
)

// TestConversationIntentTaxonomyClosed pins the closed intent set
// (nl-conversation-intent D1): exactly {approve, reject, none}; Valid accepts only
// those; an off-taxonomy value (a hallucinated classification) is not routable.
func TestConversationIntentTaxonomyClosed(t *testing.T) {
	got := Names()
	want := []string{"approve", "none", "reject"}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("taxonomy has %d intents, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("intent[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, v := range want {
		if !Valid(v) {
			t.Errorf("Valid(%q) = false, want true", v)
		}
	}
	// An off-taxonomy classification cannot route.
	for _, bad := range []string{"ship_it", "approved", "yes", ""} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true; an off-taxonomy intent must not be routable", bad)
		}
	}
}
