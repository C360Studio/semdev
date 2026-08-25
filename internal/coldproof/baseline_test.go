package coldproof

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/verify"
)

// The baseline maps the shared cold-proof outcome to readiness: an all-green proof is
// Ready; a genuine cold failure (the warm-only-resolvable dep that fails cold) is Fail
// and NOT ready (park toward the operator); a transport fault is Retry. Offline,
// deterministic — the real cache-masked-fabrication fixture is group 3.
func TestBaselineFromEvidence(t *testing.T) {
	img := cleanroom.BuiltImage{Ref: "semdev-sandbox:abc", Digest: "sha256:abc"}

	// All green → Ready.
	pass := gather(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}}, {Result: cleanroom.Result{ExitCode: 0}},
	}})
	if b := baselineFromEvidence(img, pass); !b.Ready() || b.Outcome != verify.OutcomePass {
		t.Errorf("all-green baseline = %+v, want Ready/pass", b)
	}

	// A dependency that only resolves warm fails cold → Fail, NOT ready → park operator.
	coldFail := gather(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "go: example.com/fabricated@v9.9.9: no matching versions"}},
	}})
	if b := baselineFromEvidence(img, coldFail); b.Ready() || b.Outcome != verify.OutcomeFail {
		t.Errorf("cold-fabrication baseline = %+v, want NOT ready / fail", b)
	}

	// A transport fault → Retry, not a terminal park.
	transient := gather(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "connection refused"}},
	}})
	if b := baselineFromEvidence(img, transient); b.Ready() || b.Outcome != verify.OutcomeRetry {
		t.Errorf("transport baseline = %+v, want NOT ready / retry", b)
	}
}

// ProveBaseline fails closed (returns an error, no Baseline) when the manifest declares
// no image — offline-provable.
func TestProveBaselineNoImageFailsClosed(t *testing.T) {
	m := harness.GoProfile() // complete run fields, but Image is empty (undeclared)
	if _, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil); err == nil {
		t.Fatal("expected an error proving a baseline with no declared image")
	}
}

// A manifest requiring a creds-ref the store cannot provide fails CLOSED before building
// anything (SB2c) — no baseline, no warm fallback. Offline-provable.
func TestProveBaselineMissingSecretFailsClosed(t *testing.T) {
	m := harness.GoProfile()
	m.Image = harness.ImageDecl{Dockerfile: "Dockerfile"}
	m.SecretRefs = []string{"GITHUB_PACKAGES_TOKEN"}
	// nil store → the required ref cannot resolve → park toward the operator.
	if _, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil); err == nil {
		t.Fatal("expected an error: a required creds-ref with no store must park, not proceed")
	}
}

// An incomplete manifest (missing a run field) is a DECLARATION fault → error up front
// (park toward the operator), never a silent degrade to infra-Retry. Offline-provable.
func TestProveBaselineIncompleteManifestFailsClosed(t *testing.T) {
	base := harness.GoProfile()
	base.Image = harness.ImageDecl{Dockerfile: "Dockerfile"}
	for _, tc := range []struct {
		name string
		mut  func(*harness.Manifest)
	}{
		{"no-build", func(m *harness.Manifest) { m.BuildCmd = nil }},
		{"no-resolve", func(m *harness.Manifest) { m.ResolveCmd = nil }},
		{"no-cache-home", func(m *harness.Manifest) { m.CacheHomeEnvs = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.mut(&m)
			if _, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil); err == nil {
				t.Fatalf("expected an error for an incomplete manifest (%s)", tc.name)
			}
		})
	}
}

// The baseline's terminal check reads "build", not "tests" (SB4.1) — the operator-facing
// evidence must match what the baseline proved (a cold build). This also guards the
// relabel against a rename of verify's terminal check.
func TestBaselineRelabelsBuildCheck(t *testing.T) {
	buildFail := gather(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}},                        // resolve ok
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "build broke"}}, // cold build fails
	}})
	b := baselineFromEvidence(cleanroom.BuiltImage{Ref: "x"}, buildFail)
	var names []string
	for _, c := range b.Verdict.Checks {
		names = append(names, c.Name)
		if c.Name == "tests" {
			t.Errorf("baseline verdict carries a %q check — it must be relabelled to build (SB4.1)", c.Name)
		}
	}
	found := false
	for _, c := range b.Verdict.FailedChecks() {
		if c.Name == "build" {
			found = true
		}
	}
	if !found {
		t.Errorf("baseline build-failure did not surface a failed \"build\" check; checks = %v", names)
	}
}

func TestProveArtifactForbiddenPatternFailsWithoutBuilding(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module semdev.test/app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc Add(a, b int) int { return a + b }\n")
	// The Dockerfile smuggles a build-time fetch — self-containment violation.
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM golang:1.26\nRUN curl -sL https://raw.githubusercontent.com/x/y/main/seed.sh | sh\nWORKDIR /work\n")

	// No DockerAvailable gate: the tripwire short-circuits before any docker call, so this
	// runs (and must pass) even without a daemon.
	v, err := ProveArtifact(context.Background(), "docker", root, goManifest(t, root), nil)
	if err != nil {
		t.Fatalf("ProveArtifact: %v", err)
	}
	if v.Outcome != verify.OutcomeFail {
		t.Errorf("outcome = %q, want fail (a build file with a hidden runtime download is not self-contained)", v.Outcome)
	}
	if fc := v.FailedChecks(); len(fc) != 1 || fc[0].Name != "self-contained" {
		t.Errorf("failed checks = %+v, want exactly the self-contained check (short-circuit before build/test)", fc)
	}
}

// cleanupImages removes the semdev-sandbox image built from root (best-effort), so the
// docker-gated tests do not accumulate images. The image ref is deterministic from the
// declared Dockerfile digest, so a fresh BuildImage recomputes it.
func cleanupImages(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	img, err := cleanroom.BuildImage(ctx, "docker", root, goManifest(t, root).Image)
	if err != nil {
		return
	}
	t.Cleanup(func() { _ = exec.CommandContext(ctx, "docker", "image", "rm", "-f", img.Ref, img.Digest).Run() })
}

// goManifest resolves the Go convention manifest for a repo root that declares a
// Dockerfile — the shape the provisioning station assembles.
func goManifest(t *testing.T, root string) harness.Manifest {
	t.Helper()
	decl, err := cleanroom.LocateImageInDir(root)
	if err != nil {
		t.Fatalf("locate image: %v", err)
	}
	m, err := harness.ResolveManifest(harness.ProfileGo, decl, nil)
	if err != nil {
		t.Fatalf("resolve manifest: %v", err)
	}
	return m
}

func writeGoModule(t *testing.T, root, appGo string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module semdev.test/app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "app.go"), appGo)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// semdev #28: the incompleteness error must name WHICH fields are missing. The old message
// restated all three every time, so an operator missing one field was handed a list that
// included two they had already declared — and, before the declaration surface existed,
// one they could not declare at all. Offline-provable.
func TestColdProofNamesTheMissingRunFields(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mut        func(*harness.Manifest)
		want       string
		wantAbsent []string
		prove      func(harness.Manifest) error
	}{
		{
			name:       "baseline-missing-cache-home",
			mut:        func(m *harness.Manifest) { m.CacheHomeEnvs = nil },
			want:       "cacheHomeEnvs",
			wantAbsent: []string{"resolveCommand", "buildCommand"},
			prove: func(m harness.Manifest) error {
				_, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil)
				return err
			},
		},
		{
			name:       "baseline-missing-resolve",
			mut:        func(m *harness.Manifest) { m.ResolveCmd = nil },
			want:       "resolveCommand",
			wantAbsent: []string{"cacheHomeEnvs", "buildCommand"},
			prove: func(m harness.Manifest) error {
				_, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil)
				return err
			},
		},
		{
			// Two missing fields at once — the only case that exercises the join, so a
			// regression to "report the first one" or a bad separator cannot stay green.
			name:       "baseline-missing-several",
			mut:        func(m *harness.Manifest) { m.ResolveCmd, m.BuildCmd = nil, nil },
			want:       "resolveCommand, buildCommand",
			wantAbsent: []string{"cacheHomeEnvs"},
			prove: func(m harness.Manifest) error {
				_, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil)
				return err
			},
		},
		{
			// The verify path's own cache-home defense in depth; the baseline's is pinned
			// by TestProveBaselineIncompleteManifestFailsClosed.
			name:       "verify-missing-cache-home",
			mut:        func(m *harness.Manifest) { m.CacheHomeEnvs = nil },
			want:       "cacheHomeEnvs",
			wantAbsent: []string{"resolveCommand", "testCommand"},
			prove: func(m harness.Manifest) error {
				_, err := ProveArtifact(context.Background(), "docker", "/repo", m, nil)
				return err
			},
		},
		{
			name:       "verify-missing-test",
			mut:        func(m *harness.Manifest) { m.TestCmd = nil },
			want:       "testCommand",
			wantAbsent: []string{"cacheHomeEnvs", "resolveCommand"},
			prove: func(m harness.Manifest) error {
				_, err := ProveArtifact(context.Background(), "docker", "/repo", m, nil)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := harness.GoProfile()
			m.Image = harness.ImageDecl{Dockerfile: "Dockerfile"}
			tc.mut(&m)
			err := tc.prove(m)
			if err == nil {
				t.Fatal("expected an error for an incomplete manifest")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name the missing %s", err, tc.want)
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("error = %q, names %s which the manifest DOES declare", err, absent)
				}
			}
		})
	}
}

// A fully declared non-Go manifest must get PAST the declaration gate. It then fails on
// the absent docker BINARY — an infra fault, which is the correct next failure and proves
// the operator's declaration was accepted. The repo root is a real temp dir carrying a real
// Dockerfile so the run reaches the docker invocation itself: pointing at a nonexistent
// root would fail earlier, at image-path resolution, and the test would claim a docker
// fault it never provoked. Hermetic — no docker process is ever spawned.
func TestColdProofAcceptsFullyDeclaredNonGoManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM scratch\n")
	devcontainer := []byte(`{
	  "customizations": {"semdev": {
	    "resolveCommand": ["./gradlew","--no-daemon","dependencies"],
	    "buildCommand": ["./gradlew","--no-daemon","assemble"],
	    "testCommand": ["./gradlew","--no-daemon","test"],
	    "cacheHomeEnvs": ["GRADLE_USER_HOME"]
	  }}
	}`)
	m, err := harness.ResolveManifest(harness.ProfileJVM, harness.ImageDecl{Dockerfile: "Dockerfile"}, devcontainer)
	if err != nil {
		t.Fatalf("ResolveManifest: %v", err)
	}
	_, err = ProveBaseline(context.Background(), "docker-not-installed-"+t.Name(), root, m, nil)
	if err == nil {
		t.Fatal("expected an infra error from the absent docker binary")
	}
	// Assert the error is the INFRA one from the next stage, not a declaration fault.
	// Asserting merely that some message is absent would pass vacuously against any
	// wording — the proof of progress is that the run reached the docker invocation.
	if !strings.Contains(err.Error(), "docker unavailable") {
		t.Errorf("error = %q — a fully declared manifest must pass the declaration gate and fail on infra", err)
	}
}

// The G4 control is only a control if what it names is real. A blank or whitespace entry
// has length, so it clears every `len(...) == 0` guard, and docker accepts a mount at
// `/caches/  ` with an env var literally named "  " — the run then reports a PASSING
// isolation check while the REAL cache home (GOMODCACHE, GRADLE_USER_HOME) stays warm for
// both proofs. That is a false green on the one thing that makes a cold proof mean
// anything, so a malformed name must never reach the Runner.
func TestResolveManifestRejectsMalformedCacheHomeNames(t *testing.T) {
	for _, tc := range []struct{ name, decl string }{
		{"whitespace", `["  "]`},
		{"empty-string", `[""]`},
		{"traversal", `["../../etc"]`},
		{"assignment-typo", `["GRADLE_USER_HOME=/tmp/x"]`},
		{"leading-digit", `["1CACHE"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := []byte(`{"customizations":{"semdev":{"cacheHomeEnvs":` + tc.decl + `}}}`)
			_, err := harness.ResolveManifest(harness.ProfileGo, harness.ImageDecl{Dockerfile: "Dockerfile"}, dc)
			if err == nil {
				t.Fatalf("expected an error: %s is not a usable cache-home env name", tc.decl)
			}
		})
	}
}

// A repeated cache-home name states one intent twice. Passed through it would ask docker
// for two mounts at one container path, which docker REJECTS — surfacing as a transport
// fault that is retried as infra, parking a run on a harmless typo. Collapsing preserves
// the operator's meaning; rejecting would not.
func TestResolveManifestCollapsesDuplicateCacheHomes(t *testing.T) {
	dc := []byte(`{"customizations":{"semdev":{"cacheHomeEnvs":["GOMODCACHE","GOCACHE","GOMODCACHE"]}}}`)
	m, err := harness.ResolveManifest(harness.ProfileGo, harness.ImageDecl{Dockerfile: "Dockerfile"}, dc)
	if err != nil {
		t.Fatalf("ResolveManifest: %v", err)
	}
	if got := m.CacheHomeEnvs; len(got) != 2 || got[0] != "GOMODCACHE" || got[1] != "GOCACHE" {
		t.Errorf("CacheHomeEnvs = %v, want the duplicate collapsed in declaration order", got)
	}
}
