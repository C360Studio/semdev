// Package harness is the reproducibility-contract core (design D6/D7): the
// language-agnostic manifest schema that tells the clean-room harness how to prove
// an artifact cold, plus the deterministic readiness gate (T5/D8) that decides
// whether a claim can be proven in-sandbox before dev builds toward it.
//
// The SCHEMA and the readiness logic are common; per-ecosystem specifics are
// declarative PROFILES (data, not a component per language — anti-B6). M0 ships the
// Go profile; the others are detected but implemented later. The one field every
// profile carries and the clean-room harness acts on is CacheHomeEnvs — the
// universal G4 control (a fresh cache home per proof, orthogonal to the container
// choice), because every ecosystem has a package cache that can mask a broken build.
//
// This package is pure: it declares the contract and detects the profile; it does
// not run commands or touch the filesystem. `semdev init` (the live harvest →
// propose → prove-cold → commit) and the clean-room Runner consume it.
package harness

import (
	"path/filepath"
	"strings"
)

// Ecosystem profile identifiers.
const (
	ProfileGo     = "go"
	ProfileRust   = "rust"
	ProfileNode   = "node"
	ProfilePython = "python"
	ProfileJVM    = "jvm"
)

// Tier scopes — where a test tier can be proven. The readiness gate (D8) turns on
// this distinction: a sandbox tier is provable in-sandbox and gated (G4); an
// operator-ci tier is deferred-and-noted, never faked.
const (
	TierSandbox    = "sandbox"
	TierOperatorCI = "operator-ci"
)

// Tier is one declared test tier and where it can run. The split is harvested from
// the repo's own test configuration (e.g. OSH already excludes its SITL tests), not
// invented by the operator.
type Tier struct {
	Name  string `json:"name"`
	Scope string `json:"scope"` // TierSandbox | TierOperatorCI
}

// Manifest is the reproducibility contract: how the clean-room harness resolves,
// builds, and tests an artifact cold. It holds refs and pins, never re-derived
// coordinates (D6). The hard fields (credential refs, submodule SHAs, source
// substitution, native assets) are language-agnostic and empty for simple
// ecosystems like Go; they carry the OSH/JVM profile's weight at M2.
type Manifest struct {
	// Profile is the ecosystem identifier (ProfileGo, …).
	Profile string `json:"profile"`
	// Toolchain pins the toolchain versions, e.g. {"go": "1.26"}.
	Toolchain map[string]string `json:"toolchain"`
	// CacheHomeEnvs are the package-cache env vars the harness freshens per proof —
	// the universal G4 control. For Go: GOMODCACHE, GOCACHE.
	CacheHomeEnvs []string `json:"cache_home_envs"`
	// ResolveCmd resolves dependencies from the artifact's own declarations, e.g.
	// ["go", "mod", "download"].
	ResolveCmd []string `json:"resolve_cmd"`
	// TestCmd runs the artifact's own tests, e.g. ["go", "test", "./..."].
	TestCmd []string `json:"test_cmd"`
	// Tiers is the sandbox/operator-ci split the readiness gate reads.
	Tiers []Tier `json:"tiers"`

	// Hard fields (D6) — refs and pins, empty for simple ecosystems.
	CredentialRefs     []string          `json:"credential_refs,omitempty"`
	SubmoduleSHAs      map[string]string `json:"submodule_shas,omitempty"`
	SourceSubstitution map[string]string `json:"source_substitution,omitempty"`
	NativeAssets       []string          `json:"native_assets,omitempty"`
}

// GoProfile returns the M0 Go reproducibility-contract profile (design D7). The
// cache-home envs are GOMODCACHE/GOCACHE — the two homes a fresh proof must isolate
// so a warm module cache cannot mask a fabricated dependency.
func GoProfile() Manifest {
	return Manifest{
		Profile:       ProfileGo,
		Toolchain:     map[string]string{"go": "1.26"},
		CacheHomeEnvs: []string{"GOMODCACHE", "GOCACHE"},
		ResolveCmd:    []string{"go", "mod", "download"},
		TestCmd:       []string{"go", "test", "./..."},
		Tiers:         []Tier{{Name: "unit", Scope: TierSandbox}},
	}
}

// profileMarkers maps a repo marker file to its ecosystem, checked in priority
// order — a primary language manifest wins over package.json, which frequently
// coexists as JS tooling in a repo whose product is another language.
var profileMarkers = []struct {
	Marker  string
	Profile string
}{
	{"go.mod", ProfileGo},
	{"Cargo.toml", ProfileRust},
	{"pyproject.toml", ProfilePython},
	{"build.gradle", ProfileJVM},
	{"build.gradle.kts", ProfileJVM},
	{"package.json", ProfileNode},
}

// DetectProfile infers the ecosystem profile from a repo's marker files (D7). It
// checks markers in priority order, so a Go repo carrying a package.json for
// tooling is detected as Go, not Node. ok is false when no marker is present.
//
// A ROOT-level marker beats a nested one: a Gradle product repo that happens to
// vendor a go.mod under a subdirectory (or in testdata/) detects as JVM, not Go —
// only when no root marker exists does it fall back to a nested marker (a repo
// whose sole manifest lives under cmd/). The caller (semdev init) should still pass
// curated product markers rather than an unfiltered `git ls-files`; this
// root-preference is defense in depth against a stray nested manifest, not a
// substitute for that curation.
func DetectProfile(repoFiles []string) (profile string, ok bool) {
	root := make(map[string]bool, len(repoFiles))
	nested := make(map[string]bool, len(repoFiles))
	for _, f := range repoFiles {
		slashed := filepath.ToSlash(f)
		if strings.ContainsRune(slashed, '/') {
			nested[filepath.Base(slashed)] = true
		} else {
			root[slashed] = true
		}
	}
	for _, set := range []map[string]bool{root, nested} {
		for _, m := range profileMarkers {
			if set[m.Marker] {
				return m.Profile, true
			}
		}
	}
	return "", false
}
