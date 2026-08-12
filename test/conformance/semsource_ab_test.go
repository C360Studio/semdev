package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The semsource A/B pins (integrate-semsource-ab-harness). The experiment's
// whole validity rests on "the ONLY variable is the developer's tool set" —
// these pins make that a CHECKED property, not a hope.

// semsourceTools is the exact advertised delta the variant pack appends —
// must match semsourceproxy.ToolNames (asserted in TestVariantParity below
// via the raw JSON, keeping this file independent of the Go package).
var semsourceTools = []string{"code_context", "code_impact", "code_search", "doc_context"}

// variantPairs maps each variant rule file to its baseline sibling.
func variantPairs(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	rulesDir := filepath.Join(root, "configs", "rules")
	pairs := map[string]string{}
	err := filepath.WalkDir(rulesDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, "-semsource.json") {
			return nil
		}
		pairs[path] = strings.TrimSuffix(path, "-semsource.json") + ".json"
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", rulesDir, err)
	}
	return pairs
}

// THE PARITY PIN (D2, the load-bearing one): each variant rule is IDENTICAL
// to its baseline sibling except (a) the `_semsource` id suffix, (b) the
// ` [semsource condition]` name suffix, and (c) each developer publish_agent
// action's tools array, whose delta is EXACTLY the four semsource read tools
// APPENDED (extending, never replacing). Any prompt/condition/budget/action
// drift fails — this is what makes "the only variable is the tool set" a
// checked property. Compared as parsed JSON (formatting-independent).
func TestVariantParity(t *testing.T) {
	pairs := variantPairs(t)
	if len(pairs) != 3 {
		t.Fatalf("want exactly 3 variant rules (04/06c/07b — the developer-spawning dispatch rules), got %d: %v", len(pairs), pairs)
	}
	for variantPath, basePath := range pairs {
		variant := parseRuleJSON(t, variantPath)
		base := parseRuleJSON(t, basePath)

		// (a)/(b): the id/name transformation is exact.
		baseID, _ := base["id"].(string)
		if got, _ := variant["id"].(string); got != baseID+"_semsource" {
			t.Errorf("%s: id = %q, want %q", filepath.Base(variantPath), got, baseID+"_semsource")
		}
		baseName, _ := base["name"].(string)
		if got, _ := variant["name"].(string); got != baseName+" [semsource condition]" {
			t.Errorf("%s: name = %q, want the baseline name + \" [semsource condition]\"", filepath.Base(variantPath), got)
		}

		// (c): each publish_agent tools array appends exactly the four.
		baseActions, _ := base["on_enter"].([]any)
		variantActions, _ := variant["on_enter"].([]any)
		if len(baseActions) != len(variantActions) {
			t.Errorf("%s: action count differs from baseline (%d vs %d)", filepath.Base(variantPath), len(variantActions), len(baseActions))
			continue
		}
		for i := range baseActions {
			ba, _ := baseActions[i].(map[string]any)
			va, _ := variantActions[i].(map[string]any)
			if ba["type"] == "publish_agent" {
				baseTools := stringSlice(ba["tools"])
				variantTools := stringSlice(va["tools"])
				// stringSlice drops non-string entries; a raw-vs-parsed length
				// mismatch means a non-string snuck into a tools array and would
				// otherwise escape both the string-view delta and (post-
				// neutralization) the whole-document diff.
				if rawVariant, _ := va["tools"].([]any); len(rawVariant) != len(variantTools) {
					t.Errorf("%s action %d: tools array carries a non-string entry — invisible to the parity delta", filepath.Base(variantPath), i)
				}
				want := append(append([]string{}, baseTools...), semsourceTools...)
				if !reflect.DeepEqual(variantTools, want) {
					t.Errorf("%s action %d: tools = %v, want the baseline set + exactly the four semsource tools appended (%v)", filepath.Base(variantPath), i, variantTools, want)
				}
				// Neutralize the checked deltas, then everything else must be equal.
				va["tools"] = ba["tools"]
			}
		}
		variant["id"] = base["id"]
		variant["name"] = base["name"]
		if !reflect.DeepEqual(variant, base) {
			t.Errorf("%s: differs from its baseline beyond the id/name suffix and the appended tools — the parity pin exists to kill exactly this drift (prompt/conditions/budget/actions must be byte-identical across conditions)", filepath.Base(variantPath))
		}
	}
}

// THE MUTUAL-EXCLUSION PIN (D2): no bootstrap rules_files list may contain
// both a rule file and its -semsource sibling, and (belt-and-suspenders over
// the loaded set) the parsed rule IDs must never contain both an id and its
// _semsource sibling. Boot loads variants by SUBSTITUTION, never addition —
// a double-load double-fires the developer spawn on the same decide event
// (the shared dispatched-marker guard is a race across two rules, and
// publish_agent is not idempotent).
func TestVariantMutualExclusion(t *testing.T) {
	root := repoRoot(t)
	cfg, err := loadBootstrap(root)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	listed := map[string]bool{}
	for _, rel := range cfg.Components.Rule.Config.RulesFiles {
		listed[filepath.ToSlash(rel)] = true
	}
	for rel := range listed {
		if strings.HasSuffix(rel, "-semsource.json") && listed[strings.TrimSuffix(rel, "-semsource.json")+".json"] {
			t.Errorf("bootstrap lists BOTH %q and its baseline sibling — a double-load double-fires the developer spawn (the variant must SUBSTITUTE, never join)", rel)
		}
	}

	ids := map[string]bool{}
	rules, err := loadRules(root)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	for _, r := range rules {
		ids[r.ID] = true
	}
	for id := range ids {
		if strings.HasSuffix(id, "_semsource") && ids[strings.TrimSuffix(id, "_semsource")] {
			// Both EXIST on disk by design; the runtime never loads both (boot
			// substitutes). This guards the id NAMESPACE: a variant id must pair
			// with a real baseline id, or the parity pin lost its anchor.
			continue
		}
		if strings.HasSuffix(id, "_semsource") {
			t.Errorf("variant rule id %q has no baseline sibling id — an orphan variant (its baseline was renamed or deleted) silently drifts out of the parity pin", id)
		}
	}
}

// D3 WHOLE-DOCUMENT LINT: no rule document in any pack — conditions, actions,
// prompts, substitution tokens, metadata — references an experiment.* field.
// The condition is an EVIDENCE LABEL: a conditions-only lint would miss a
// `$entity.triple.experiment…` prompt substitution that resolves differently
// per condition while passing the parity pin (the parity pin compares variant
// to baseline, not either to "no experiment channel at all").
func TestNoRuleDocumentReferencesExperimentFields(t *testing.T) {
	root := repoRoot(t)
	rulesDir := filepath.Join(root, "configs", "rules")
	err := filepath.WalkDir(rulesDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "experiment.") {
			t.Errorf("rule document %s references an experiment.* field — the condition is an EVIDENCE LABEL, never a routing input (D3): no rule may read, substitute, or mention it anywhere in the document", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", rulesDir, err)
	}
}

// REVIEWER ISOLATION (D6/task 3.5): Quinn's dispatch (06a route-advance) is
// NOT varianted — the reviewer's allowlist is identical across conditions, so
// the experiment isolates the DEVELOPER's tool set as the only variable. A
// 06a-semsource file appearing on disk fails here before it can contaminate
// the arm.
func TestReviewerDispatchIsNotVarianted(t *testing.T) {
	for variantPath := range variantPairs(t) {
		base := filepath.Base(variantPath)
		if strings.HasPrefix(base, "06a-") {
			t.Errorf("06a (Quinn's dispatch) must NOT have a semsource variant — the reviewer stays identical across conditions to isolate the developer's tool set as the only variable, got %s", base)
		}
	}
	// And no baseline rule beyond the three developer-spawning dispatch rules
	// may grow a variant without this pin being revisited deliberately.
	allowed := map[string]bool{
		"04-dispatch-developer-semsource.json": true,
		"06c-route-retry-semsource.json":       true,
		"07b-review-retry-semsource.json":      true,
	}
	for variantPath := range variantPairs(t) {
		if !allowed[filepath.Base(variantPath)] {
			t.Errorf("unexpected variant rule %s — the semsource condition varies ONLY the three developer-spawning dispatch rules (04/06c/07b); varianting anything else changes more than the developer's tool set", filepath.Base(variantPath))
		}
	}
}

// BASELINE ZERO-ADVERTISEMENT (task 2.5): no BASELINE rule's spawn advertises
// a semsource tool — only the -semsource variant files may. (The live half —
// a baseline boot constructs zero live semsource clients — is enforced by
// boot.RegisterTools reading the experiment condition and pinned in the
// runtime integration test.)
func TestBaselineAdvertisesZeroSemsourceTools(t *testing.T) {
	root := repoRoot(t)
	rules, err := loadRules(root)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	proxy := map[string]bool{}
	for _, n := range semsourceTools {
		proxy[n] = true
	}
	for _, r := range rules {
		if strings.HasSuffix(r.ID, "_semsource") {
			continue
		}
		for _, a := range r.OnEnter {
			for _, tool := range a.Tools {
				if proxy[tool] {
					t.Errorf("BASELINE rule %q advertises semsource tool %q — only the -semsource variant pack may advertise the proxies (arm purity: a baseline loop advertising a tool that errors on call contaminates the baseline condition)", r.ID, tool)
				}
			}
		}
	}
}

// parseRuleJSON parses a rule file into a generic map for structural diffing.
func parseRuleJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func stringSlice(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
