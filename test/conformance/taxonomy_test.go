package conformance

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/c360studio/semdev/internal/taxonomy"
)

const decisionContractDoc = "configs/personas/fragments/coordinator/10-decision-contract.md"

// T1 — the coordinator persona declares EXACTLY the canonical closed taxonomy.
// The LLM learns the vocabulary from this fragment; if it drifts from
// internal/taxonomy.Actions, the model and the rule layer disagree. Bidirectional
// set-equality, like the semteams coordinator-taxonomy contract.
func TestPersonaDeclaresExactTaxonomy(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), decisionContractDoc))
	if err != nil {
		t.Fatalf("read %s: %v", decisionContractDoc, err)
	}
	rows := markdownTableRows(src, "Valid action values (closed taxonomy for this deployment)")
	if len(rows) == 0 {
		t.Fatalf("no closed-taxonomy table found in %s", decisionContractDoc)
	}

	declared := make(map[string]bool)
	for _, cells := range rows {
		if len(cells) > 0 && cells[0] != "" {
			declared[cells[0]] = true
		}
	}
	canonical := make(map[string]bool)
	for _, a := range taxonomy.Names() {
		canonical[a] = true
	}

	for a := range canonical {
		if !declared[a] {
			t.Errorf("action %q is in the canonical taxonomy but not declared in the persona decision contract", a)
		}
	}
	for a := range declared {
		if !canonical[a] {
			t.Errorf("action %q is declared in the persona decision contract but not in the canonical taxonomy (out-of-taxonomy)", a)
		}
	}
}

// The closed action taxonomy (run-lifecycle spec): no rule may route an action
// outside the taxonomy. An out-of-taxonomy action is not routable — it surfaces
// for human attention rather than silently driving work.
func TestNoRuleRoutesOutOfTaxonomyAction(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("no rules found; the taxonomy-routing pin would pass vacuously")
	}
	for _, r := range rules {
		for _, action := range r.nextActionValues() {
			if !taxonomy.Valid(action) {
				t.Errorf("rule %s (%s) routes action %q, which is outside the closed taxonomy", r.ID, filepath.Base(r.path), action)
			}
		}
	}
}

// Red-first: both taxonomy censuses must fire. A persona set that differs from
// canonical, and a rule action outside the taxonomy, must both be caught.
func TestTaxonomyCensusesCatchDrift(t *testing.T) {
	canonical := map[string]bool{}
	for _, a := range taxonomy.Names() {
		canonical[a] = true
	}
	// A persona missing an action and adding a bogus one — both directions.
	declared := map[string]bool{"create_change": true, "totally_made_up": true}
	var missing, extra int
	for a := range canonical {
		if !declared[a] {
			missing++
		}
	}
	for a := range declared {
		if !canonical[a] {
			extra++
		}
	}
	if missing == 0 || extra == 0 {
		t.Errorf("persona census under-fires: missing=%d extra=%d", missing, extra)
	}
	// An out-of-taxonomy routed action must be invalid.
	if taxonomy.Valid("totally_made_up") {
		t.Error("taxonomy.Valid accepted an out-of-taxonomy action; routing pin would not fire")
	}
}

// Sanity: taxonomy.Actions is the eight expected actions, sorted-stable.
func TestTaxonomyIsTheEightActions(t *testing.T) {
	got := taxonomy.Names()
	want := []string{"archive_change", "ask_human", "create_change", "dev_from_task", "issue_intake", "open_pr", "respond", "verify"}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("taxonomy has %d actions, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("taxonomy[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
