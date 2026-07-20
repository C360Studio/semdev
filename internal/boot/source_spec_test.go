package boot

import (
	"os"
	"path/filepath"
	"testing"
)

// sourceSpec selects fixture vs forge mode and FAILS CLOSED on an ambiguous config (design D5).
func TestSourceSpecSelection(t *testing.T) {
	forge, err := sourceSpec(RunOptions{ForgeSource: &ForgeSourceConfig{BaseURL: "https://github.com", TokenEnv: "GH"}})
	if err != nil {
		t.Fatalf("forge mode: %v", err)
	}
	if forge.Forge == nil || forge.FixtureDir != "" {
		t.Errorf("want forge spec, got %+v", forge)
	}
	if forge.Forge.BaseURL != "https://github.com" || forge.Forge.TokenEnv != "GH" {
		t.Errorf("forge config not threaded: %+v", forge.Forge)
	}

	fixture, err := sourceSpec(RunOptions{SandboxSourceDir: "/fix"})
	if err != nil {
		t.Fatalf("fixture mode: %v", err)
	}
	if fixture.Forge != nil || fixture.FixtureDir != "/fix" {
		t.Errorf("want fixture spec, got %+v", fixture)
	}

	// Neither → fixture mode with an empty dir (resolves fail-closed at runtime, the pre-change
	// default), NOT a boot error.
	none, err := sourceSpec(RunOptions{})
	if err != nil {
		t.Fatalf("neither: %v", err)
	}
	if none.Forge != nil || none.FixtureDir != "" {
		t.Errorf("want empty fixture spec, got %+v", none)
	}
}

// Both modes set, or a forge source with no base URL, is a LOUD boot error (design D5) — never
// a guessed target.
func TestSourceSpecFailsClosedOnAmbiguity(t *testing.T) {
	if _, err := sourceSpec(RunOptions{
		SandboxSourceDir: "/fix",
		ForgeSource:      &ForgeSourceConfig{BaseURL: "https://github.com"},
	}); err == nil {
		t.Error("both a fixture dir and a forge source configured must fail closed")
	}
	if _, err := sourceSpec(RunOptions{ForgeSource: &ForgeSourceConfig{}}); err == nil {
		t.Error("a forge source without a base URL must fail closed")
	}
}

// LoadForgeSourceConfig reads the `source.forge` block: present → parsed, absent → nil,
// malformed/missing → error.
func TestLoadForgeSourceConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	with := write("with.json", `{"source":{"forge":{"base_url":"https://github.com","token_env":"GH_TOKEN"}}}`)
	fs, err := LoadForgeSourceConfig(with)
	if err != nil {
		t.Fatalf("with forge: %v", err)
	}
	if fs == nil || fs.BaseURL != "https://github.com" || fs.TokenEnv != "GH_TOKEN" {
		t.Errorf("forge block not parsed: %+v", fs)
	}

	without := write("without.json", `{"nats":{"urls":["nats://x"]}}`)
	fs2, err := LoadForgeSourceConfig(without)
	if err != nil {
		t.Fatalf("without forge: %v", err)
	}
	if fs2 != nil {
		t.Errorf("want nil forge source, got %+v", fs2)
	}

	bad := write("bad.json", `{not json`)
	if _, err := LoadForgeSourceConfig(bad); err == nil {
		t.Error("a malformed config must error")
	}

	if _, err := LoadForgeSourceConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing config file must error")
	}
}
