package boot

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/experiment"
	"github.com/c360studio/semstreams/config"
	"github.com/c360studio/semstreams/types"
)

// ruleCfg builds a minimal rule-processor component config carrying rules_files.
func ruleCfg(t *testing.T, files ...string) *config.Config {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"rules_files": files})
	if err != nil {
		t.Fatal(err)
	}
	return &config.Config{Components: config.ComponentConfigs{
		"rule": types.ComponentConfig{Name: ruleProcessorFactory, Config: raw},
	}}
}

func rulesFiles(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(cfg.Components["rule"].Config, &raw); err != nil {
		t.Fatal(err)
	}
	items, _ := raw["rules_files"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(string))
	}
	return out
}

// The semsource condition SUBSTITUTES each listed baseline whose -semsource
// sibling exists on disk — never appends (a double-load double-fires the
// developer spawn; the offline mutual-exclusion pin guards the same
// invariant), and never touches entries with no variant.
func TestVariantPackSubstitutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"rules/04-dispatch.json", "rules/04-dispatch-semsource.json", "rules/05-floors.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := ruleCfg(t, "rules/04-dispatch.json", "rules/05-floors.json")
	exp := experiment.Config{Condition: experiment.ConditionSemsource, SemsourceEndpoint: "http://x"}
	if err := applyExperimentVariantPack(cfg, exp, dir, slog.Default()); err != nil {
		t.Fatalf("applyExperimentVariantPack: %v", err)
	}
	got := rulesFiles(t, cfg)
	want := []string{"rules/04-dispatch-semsource.json", "rules/05-floors.json"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("rules_files = %v, want the variant SUBSTITUTED (not appended) and the variant-less entry untouched: %v", got, want)
	}
}

// A baseline (or unconfigured) condition never touches the rule list.
func TestVariantPackBaselineIsUntouched(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"rules/04-dispatch.json", "rules/04-dispatch-semsource.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, cond := range []string{"", experiment.ConditionBaseline} {
		cfg := ruleCfg(t, "rules/04-dispatch.json")
		if err := applyExperimentVariantPack(cfg, experiment.Config{Condition: cond}, dir, slog.Default()); err != nil {
			t.Fatalf("condition %q: %v", cond, err)
		}
		got := rulesFiles(t, cfg)
		if len(got) != 1 || got[0] != "rules/04-dispatch.json" {
			t.Fatalf("condition %q must not touch rules_files, got %v", cond, got)
		}
	}
}

// The semsource condition with NO variant file on disk fails the boot LOUDLY —
// a silent baseline run under a semsource label is the half-labeled-evidence
// class the condition gate exists to kill (D4).
func TestVariantPackMissingFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules/04-dispatch.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ruleCfg(t, "rules/04-dispatch.json")
	exp := experiment.Config{Condition: experiment.ConditionSemsource, SemsourceEndpoint: "http://x"}
	err := applyExperimentVariantPack(cfg, exp, dir, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "variant pack is missing") {
		t.Fatalf("a declared semsource condition with no variant files must fail the boot loudly, got %v", err)
	}
}

// A HAND-LISTED variant entry is a misconfiguration in EVERY condition —
// substitution is the only sanctioned load path. Listed alongside its
// baseline, two distinct rule ids would both spawn the developer on the same
// decide event (the framework loader collapses exact-id duplicates only) —
// the double-dispatch class the offline mutual-exclusion pin guards on the
// SHIPPED config; this guards the config actually handed to boot.
func TestVariantPackRejectsHandListedVariant(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"rules/04-dispatch.json", "rules/04-dispatch-semsource.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, cond := range []string{"", experiment.ConditionBaseline, experiment.ConditionSemsource} {
		cfg := ruleCfg(t, "rules/04-dispatch.json", "rules/04-dispatch-semsource.json")
		exp := experiment.Config{Condition: cond}
		if cond == experiment.ConditionSemsource {
			exp.SemsourceEndpoint = "http://x"
		}
		err := applyExperimentVariantPack(cfg, exp, dir, slog.Default())
		if err == nil || !strings.Contains(err.Error(), "variants load ONLY by boot substitution") {
			t.Fatalf("condition %q: a hand-listed variant entry must fail the boot loudly, got %v", cond, err)
		}
	}
}

// The REAL bootstrap + the REAL variant pack: loading the repo's own config
// under the semsource condition swaps EXACTLY the three developer-spawning
// dispatch rules — proven against the actual files, not a fixture (G8).
func TestVariantPackAgainstRepoBootstrap(t *testing.T) {
	// repo root: walk up from CWD to go.mod (this test runs in internal/boot).
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for !fileExists(filepath.Join(dir, "go.mod")) {
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
	configPath := filepath.Join(dir, "configs", "semdev-bootstrap.json")

	cfg, err := config.NewLoader().LoadFile(configPath)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	exp := experiment.Config{Condition: experiment.ConditionSemsource, SemsourceEndpoint: "http://x"}
	if err := applyExperimentVariantPack(cfg, exp, filepath.Dir(configPath), slog.Default()); err != nil {
		t.Fatalf("applyExperimentVariantPack over the repo bootstrap: %v", err)
	}
	var swapped []string
	for _, f := range rulesFiles(t, cfg) {
		if strings.HasSuffix(f, "-semsource.json") {
			swapped = append(swapped, filepath.Base(f))
		}
	}
	want := map[string]bool{
		"04-dispatch-developer-semsource.json": true,
		"06c-route-retry-semsource.json":       true,
		"07b-review-retry-semsource.json":      true,
	}
	if len(swapped) != len(want) {
		t.Fatalf("swapped %v, want exactly the three developer-spawning dispatch variants", swapped)
	}
	for _, f := range swapped {
		if !want[f] {
			t.Fatalf("unexpected swap %q", f)
		}
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
