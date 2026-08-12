// Package ruleload is the OFFLINE boot-driven rule-load gate (no NATS, no docker): it
// registers semdev's canonical vocabulary then runs every rule pack through the framework's
// REAL rule-load validator, so a non-canonical/undeclared condition field or add_triple
// predicate fails at unit-test time — the mock ladder before any dockerized or paid run.
// It lives in its OWN package so it compiles independently of test/conformance during the
// beta.147 sweep (that package's pins update in a later commit).
package ruleload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/vocab"
	"github.com/c360studio/semstreams/component"
	rule "github.com/c360studio/semstreams/processor/rule"
)

// repoRoot walks up from the test's working directory to the module root (the dir with
// go.mod), so the pin finds configs/rules regardless of where `go test` runs.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from cwd")
		}
		dir = parent
	}
}

func TestRulePacksLoadCanonical(t *testing.T) {
	vocab.Register()

	rp, err := rule.NewProcessor(nil, &rule.Config{
		Ports:                  &component.PortConfig{},
		PackID:                 "semdev",
		EnableGraphIntegration: true,
		EntityWatchBuckets:     map[string][]string{"ENTITY_STATES": {"*.*.*.*.*.*"}},
	})
	if err != nil {
		t.Fatalf("build offline rule processor: %v", err)
	}

	rulesDir := filepath.Join(repoRoot(t), "configs", "rules")
	var files []string
	if err := filepath.WalkDir(rulesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk rules dir: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no rule packs found")
	}
	sort.Strings(files)

	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", filepath.Base(path), err)
			continue
		}
		var ruleMap map[string]any
		if err := json.Unmarshal(raw, &ruleMap); err != nil {
			t.Errorf("%s: unmarshal: %v", filepath.Base(path), err)
			continue
		}
		id, _ := ruleMap["id"].(string)
		if id == "" {
			t.Errorf("%s: rule has no id", filepath.Base(path))
			continue
		}
		if err := rp.ValidateConfigUpdate(map[string]any{
			"rules": map[string]any{id: ruleMap},
		}); err != nil {
			t.Errorf("%s (rule %q): %v", filepath.Base(path), id, err)
		}
	}
}
