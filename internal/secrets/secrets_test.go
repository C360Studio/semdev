package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A governed .env store loads named creds-refs (KEY=value), skips blanks/comments, and
// strips surrounding quotes. It resolves a ref by NAME (never a value round-trips into a
// manifest, SB2c).
func TestDotEnvStoreLoadAndResolve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	mustWrite(t, path, "# a comment\n\nGITHUB_PACKAGES_TOKEN=ghp_secretvalue123\nQUOTED=\"has spaces\"\nSINGLE='single'\nexport EXPORTED=exp\n")
	st, err := LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	cases := map[string]string{
		"GITHUB_PACKAGES_TOKEN": "ghp_secretvalue123",
		"QUOTED":                "has spaces",
		"SINGLE":                "single",
		"EXPORTED":              "exp",
	}
	for name, want := range cases {
		got, ok := st.Resolve(name)
		if !ok || got != want {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, true)", name, got, ok, want)
		}
	}
	if _, ok := st.Resolve("MISSING"); ok {
		t.Error("Resolve(MISSING) = ok, want not found")
	}
}

// A malformed line (no '=') is a broken store — fail loud, never a silently dropped
// secret.
func TestDotEnvStoreMalformedFailsLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	mustWrite(t, path, "VALID=1\nthis-line-has-no-equals\n")
	if _, err := LoadDotEnv(path); err == nil {
		t.Fatal("expected an error loading a malformed .env")
	}
}

// A missing store file is fail-closed (an error), NOT an empty store that would then
// treat every required ref as absent-but-benign.
func TestLoadDotEnvMissingFileFailsClosed(t *testing.T) {
	if _, err := LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Fatal("expected an error for a missing .env store")
	}
}

// ResolveAll fails CLOSED toward the operator: a required ref not in the store is an
// error naming the missing refs (park to register them), never a degraded fallback that
// masks the missing secret (SB2c).
func TestResolveAllFailsClosedOnMissingRef(t *testing.T) {
	st := mapStore{"PRESENT": "v"}
	_, err := ResolveAll(st, []string{"PRESENT", "ABSENT_ONE", "ABSENT_TWO"})
	if err == nil {
		t.Fatal("expected an error when a required ref is missing")
	}
	if !strings.Contains(err.Error(), "ABSENT_ONE") || !strings.Contains(err.Error(), "ABSENT_TWO") {
		t.Errorf("error should name the missing refs, got: %v", err)
	}
	var missErr *MissingRefsError
	if !errors.As(err, &missErr) {
		t.Errorf("want a *MissingRefsError, got %T", err)
	}
}

// ResolveAll with no refs returns an empty map (no secrets needed) — the common M0 case.
func TestResolveAllNoRefs(t *testing.T) {
	got, err := ResolveAll(mapStore{}, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("ResolveAll(nil) = (%v, %v), want (empty, nil)", got, err)
	}
}

// A registered-but-EMPTY value fails closed as missing — a precise "register a value"
// park, not a downstream resolution failure (SB2c).
func TestResolveAllEmptyValueIsMissing(t *testing.T) {
	_, err := ResolveAll(mapStore{"BLANK": "  "}, []string{"BLANK"})
	var missErr *MissingRefsError
	if !errors.As(err, &missErr) {
		t.Fatalf("empty-valued ref: got %v, want a *MissingRefsError", err)
	}
}

// The scrubber redacts every secret VALUE from a string so a value cannot be derived
// from any log/tool-result/attestation fact (G7).
func TestScrubberRedactsValues(t *testing.T) {
	sc := NewScrubber(map[string]string{"TOKEN": "ghp_abc123", "PASS": "p@ss"})
	in := "resolve failed: auth ghp_abc123 rejected (pass p@ss) at proxy"
	out := sc.Scrub(in)
	if strings.Contains(out, "ghp_abc123") || strings.Contains(out, "p@ss") {
		t.Errorf("scrub left a secret value: %q", out)
	}
	if !strings.Contains(out, redaction) {
		t.Errorf("scrub did not insert the redaction marker: %q", out)
	}
}

// A scrubber built from empty/whitespace values is a no-op — redacting "" would replace
// the entire string (a catastrophic false redaction), so blanks are dropped.
func TestScrubberIgnoresBlankValues(t *testing.T) {
	sc := NewScrubber(map[string]string{"EMPTY": "", "SPACES": "   "})
	in := "nothing secret here"
	if got := sc.Scrub(in); got != in {
		t.Errorf("blank-value scrubber altered the string: %q", got)
	}
}

// ExecEnvFlags builds sorted PASS-THROUGH `-e NAME` flags (name only, no value) so the
// value never rides the docker argv; ExecEnvKV builds the matching sorted NAME=value
// entries for the docker process's own environment (SB2c).
func TestExecEnvFlagsAndKV(t *testing.T) {
	env := map[string]string{"B_TOKEN": "2", "A_TOKEN": "1"}
	if got, want := ExecEnvFlags(env), []string{"-e", "A_TOKEN", "-e", "B_TOKEN"}; !slices.Equal(got, want) {
		t.Errorf("ExecEnvFlags = %v, want %v (bare, sorted)", got, want)
	}
	if got, want := ExecEnvKV(env), []string{"A_TOKEN=1", "B_TOKEN=2"}; !slices.Equal(got, want) {
		t.Errorf("ExecEnvKV = %v, want %v (sorted)", got, want)
	}
	// No value appears in the flags.
	for _, f := range ExecEnvFlags(env) {
		if f == "1" || f == "2" || strings.Contains(f, "=") {
			t.Errorf("a value leaked into the flags: %q", f)
		}
	}
	if len(ExecEnvFlags(nil)) != 0 || len(ExecEnvKV(nil)) != 0 {
		t.Error("nil map should yield no flags/kv")
	}
}

// mapStore is a trivial in-memory Store for tests.
type mapStore map[string]string

func (m mapStore) Resolve(ref string) (string, bool) { v, ok := m[ref]; return v, ok }

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
