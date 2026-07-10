// Package harness is the reproducibility-contract core (design D6/D7, reshaped by the
// containerized-sandbox-dev-loop change, SB2): the manifest that tells the clean-room
// harness how to prove an artifact cold, plus the deterministic readiness gate
// (T5/D8) that decides whether a claim can be proven in-sandbox before dev builds
// toward it.
//
// SB2 reshaped the manifest: the toolchain/environment is NOT modeled here — it is an
// OPERATOR-DECLARED Dockerfile / devcontainer committed to the target repo (ImageDecl
// references it; internal/cleanroom builds it and proves it cold). So this package no
// longer carries toolchain pins, submodule SHAs, source-substitution, or native-asset
// fields (the last two modeled semspec's fatal harness-injected resolution — banned).
// What remains is the RUN contract: the declared image, the resolve/build/test
// commands, the sandbox/operator-ci tier split, and the governed secret refs. Those
// few run fields ride a convention default per profile, overlaid by a
// customizations.semdev block (customizations.go) — never a bespoke environment DSL.
//
// The one field every profile carries and the clean-room harness acts on is
// CacheHomeEnvs — the universal G4 control (a fresh cache home per proof, orthogonal
// to the container choice), because every ecosystem has a package cache that can mask
// a broken build.
//
// This package is pure: it declares the contract, detects the profile, and reads the
// operator's run fields; it does not run commands or touch the filesystem. `semdev
// init` (the live harvest → propose → prove-cold → commit) and the clean-room Runner
// consume it.
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

// Tier is one declared test tier: where it can run AND which claims it proves. The
// split is harvested from the repo's own test configuration (e.g. OSH already
// excludes its SITL tests), not invented by the operator. Proves makes readiness
// claim-specific (D8): a sandbox tier proves ONLY the claims it lists, so an
// unrelated sandbox tier cannot make a claim ready that only an operator-ci tier
// actually proves.
type Tier struct {
	Name   string   `json:"name"`
	Scope  string   `json:"scope"` // TierSandbox | TierOperatorCI
	Proves []string `json:"proves"`
}

// ImageDecl locates the OPERATOR-DECLARED image source (SB2): a committed Dockerfile
// and/or devcontainer.json semdev builds and proves cold. semdev never harvests,
// infers, or synthesizes the toolchain — the declaration is the operator's, in a
// portable standard format. The image is BUILT by internal/cleanroom (which returns
// the digest-pinned ref); this declaration carries only where the source lives, not
// the build output.
type ImageDecl struct {
	// Dockerfile is the repo-relative path to a committed Dockerfile (M0's form).
	Dockerfile string `json:"dockerfile,omitempty"`
	// Devcontainer is the repo-relative path to a committed devcontainer.json, when the
	// image is declared that way instead of a bare Dockerfile.
	Devcontainer string `json:"devcontainer,omitempty"`
	// Context is the docker build context dir relative to the repo root; empty means the
	// repo root.
	Context string `json:"context,omitempty"`
}

// Declared reports whether the operator declared any image source. A manifest with no
// declared image fails closed toward the operator (SB2) — semdev never guesses one.
func (d ImageDecl) Declared() bool {
	return strings.TrimSpace(d.Dockerfile) != "" || strings.TrimSpace(d.Devcontainer) != ""
}

// Manifest is the RUN contract (SB2 reshape): the operator-declared image plus how
// the clean-room harness resolves, builds, and tests the artifact cold. It models no
// toolchain — the declared image owns the environment. It holds refs, never re-derived
// coordinates (D6); task-introduced weight (submodules, native blobs) is committed to
// the artifact and proven by the cold --recursive verify, not carried here.
type Manifest struct {
	// Profile is the ecosystem identifier (ProfileGo, …); it drives the convention
	// defaults for the run commands and cache-home control.
	Profile string `json:"profile"`
	// Image declares the operator's committed image source (SB2). Empty ImageDecl means
	// no image declared — the run fails closed toward the operator.
	Image ImageDecl `json:"image"`
	// CacheHomeEnvs are the package-cache env vars the harness freshens per proof —
	// the universal G4 control. For Go: GOMODCACHE, GOCACHE.
	CacheHomeEnvs []string `json:"cache_home_envs"`
	// ResolveCmd resolves base dependencies from the artifact's own declarations, e.g.
	// ["go", "mod", "download"].
	ResolveCmd []string `json:"resolve_cmd"`
	// BuildCmd builds the artifact cold — the second half of the resolve+build baseline
	// proof (SB4.1), distinct from the tests (the task's own test may not exist yet),
	// e.g. ["go", "build", "./..."].
	BuildCmd []string `json:"build_cmd"`
	// TestCmd runs the artifact's own tests, e.g. ["go", "test", "./..."].
	TestCmd []string `json:"test_cmd"`
	// Tiers is the sandbox/operator-ci split the readiness gate reads.
	Tiers []Tier `json:"tiers"`
	// SecretRefs names the governed creds-refs base-dep resolution needs (SB2c) — NAMES
	// only, never values; a value never rides a manifest, log, or fact (G7).
	SecretRefs []string `json:"secret_refs,omitempty"`
}

// GoProfile returns the M0 Go run convention (design D7, SB2 reshape). The cache-home
// envs are GOMODCACHE/GOCACHE — the two homes a fresh proof must isolate so a warm
// module cache cannot mask a fabricated dependency. The image is declared per repo
// (ImageDecl), so this convention leaves it empty; a resolver fills it in.
func GoProfile() Manifest {
	return Manifest{
		Profile:       ProfileGo,
		CacheHomeEnvs: []string{"GOMODCACHE", "GOCACHE"},
		ResolveCmd:    []string{"go", "mod", "download"},
		BuildCmd:      []string{"go", "build", "./..."},
		TestCmd:       []string{"go", "test", "./..."},
		Tiers:         []Tier{{Name: "unit", Scope: TierSandbox, Proves: []string{ClaimUnit}}},
	}
}

// Convention returns the run-field defaults for a detected profile and whether one is
// known. Only the Go convention ships at M0; the JVM/Node/Rust/Python defaults land
// with those profiles. An unknown profile returns ok=false, so ResolveManifest fails
// closed unless a customizations.semdev block supplies the commands explicitly.
func Convention(profile string) (Manifest, bool) {
	switch profile {
	case ProfileGo:
		return GoProfile(), true
	default:
		return Manifest{}, false
	}
}

// ClaimUnit is the M0 claim the Go unit suite proves — the whole artifact under
// `go test ./...`. Richer claim vocabularies (per-requirement, SITL) arrive with
// the JVM/OSH profiles at M2.
const ClaimUnit = "unit"

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
