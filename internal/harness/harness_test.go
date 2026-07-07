package harness

import (
	"encoding/json"
	"slices"
	"testing"
)

// The Go profile carries the universal G4 control — the two Go cache homes a fresh
// proof must isolate — plus the resolve/test commands and a sandbox tier.
func TestGoProfile(t *testing.T) {
	p := GoProfile()
	if p.Profile != ProfileGo {
		t.Errorf("profile = %q, want %q", p.Profile, ProfileGo)
	}
	if !slices.Equal(p.CacheHomeEnvs, []string{"GOMODCACHE", "GOCACHE"}) {
		t.Errorf("cache-home envs = %v, want GOMODCACHE, GOCACHE (the G4 control)", p.CacheHomeEnvs)
	}
	if !slices.Equal(p.ResolveCmd, []string{"go", "mod", "download"}) {
		t.Errorf("resolve cmd = %v", p.ResolveCmd)
	}
	if !slices.Equal(p.TestCmd, []string{"go", "test", "./..."}) {
		t.Errorf("test cmd = %v", p.TestCmd)
	}
	if len(p.Tiers) != 1 || p.Tiers[0].Scope != TierSandbox {
		t.Errorf("Go profile should declare a single sandbox tier, got %+v", p.Tiers)
	}
}

// The manifest round-trips through JSON — it is the checked-in .semdev/harness
// contract, so its schema must serialize cleanly (empty hard fields omitted).
func TestManifestJSONRoundTrip(t *testing.T) {
	in := GoProfile()
	buf, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(buf); got == "" {
		t.Fatal("empty marshal")
	}
	var out Manifest
	if err := json.Unmarshal(buf, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Profile != in.Profile || !slices.Equal(out.CacheHomeEnvs, in.CacheHomeEnvs) {
		t.Errorf("round-trip mismatch: %+v vs %+v", out, in)
	}
}

func TestDetectProfile(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
		ok    bool
	}{
		{"go", []string{"go.mod", "main.go"}, ProfileGo, true},
		{"rust", []string{"Cargo.toml", "src/lib.rs"}, ProfileRust, true},
		{"python", []string{"pyproject.toml"}, ProfilePython, true},
		{"jvm", []string{"build.gradle"}, ProfileJVM, true},
		{"jvm-kts", []string{"build.gradle.kts"}, ProfileJVM, true},
		{"node", []string{"package.json"}, ProfileNode, true},
		// A Go repo with JS tooling detects as Go — primary manifest wins.
		{"go-with-tooling", []string{"go.mod", "package.json"}, ProfileGo, true},
		// A lone nested marker (no root marker) still detects — a repo whose sole
		// manifest lives under cmd/.
		{"nested-only", []string{"cmd/app/go.mod"}, ProfileGo, true},
		// A ROOT marker beats a nested one: a Gradle repo vendoring a go.mod under a
		// subdir detects as JVM, not Go.
		{"root-beats-nested", []string{"build.gradle", "vendor/x/go.mod"}, ProfileJVM, true},
		{"testdata-gomod-shadowed", []string{"pyproject.toml", "testdata/fixture/go.mod"}, ProfilePython, true},
		{"none", []string{"README.md", "LICENSE"}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := DetectProfile(c.files)
			if got != c.want || ok != c.ok {
				t.Errorf("DetectProfile(%v) = (%q, %v), want (%q, %v)", c.files, got, ok, c.want, c.ok)
			}
		})
	}
}

// The T5 readiness gate (D8) is CLAIM-SPECIFIC: only a tier that proves the claim
// counts. A sandbox tier proving it → Ready; only an operator-ci tier proving it →
// Deferred; no tier proving it → Park.
func TestAssessReadiness(t *testing.T) {
	unitSandbox := Tier{Name: "unit", Scope: TierSandbox, Proves: []string{"unit"}}
	sitlOperator := Tier{Name: "sitl", Scope: TierOperatorCI, Proves: []string{"sitl"}}
	mixed := []Tier{unitSandbox, sitlOperator}

	cases := []struct {
		name  string
		tiers []Tier
		claim string
		want  Readiness
	}{
		{"sandbox-proves-claim", []Tier{unitSandbox}, "unit", ReadinessReady},
		{"operator-ci-proves-claim", []Tier{sitlOperator}, "sitl", ReadinessDeferred},
		// The reviewer's case: a SITL-only claim must DEFER, not read Ready off the
		// unrelated sandbox unit tier.
		{"sitl-only-claim-defers-despite-sandbox-unit", mixed, "sitl", ReadinessDeferred},
		{"unit-claim-ready-in-mixed", mixed, "unit", ReadinessReady},
		{"no-tier-proves-claim", mixed, "e2e", ReadinessPark},
		{"none", nil, "unit", ReadinessPark},
		{"unknown-scope-parks", []Tier{{Name: "x", Scope: "bogus", Proves: []string{"unit"}}}, "unit", ReadinessPark},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AssessReadiness(c.tiers, c.claim); got != c.want {
				t.Errorf("AssessReadiness(%+v, %q) = %q, want %q", c.tiers, c.claim, got, c.want)
			}
		})
	}
}

// The Go profile is Ready for its unit claim out of the box — its single sandbox
// tier proves the unit suite in-sandbox.
func TestGoProfileIsSandboxReady(t *testing.T) {
	if got := AssessReadiness(GoProfile().Tiers, ClaimUnit); got != ReadinessReady {
		t.Errorf("Go profile unit readiness = %q, want ready", got)
	}
}
