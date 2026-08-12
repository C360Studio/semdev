package harness

import (
	"slices"
	"testing"
)

// A customizations command may be authored as a JSON array (literal argv, run
// shell-free) or as a JSON string (a convenience that becomes `sh -c "<cmd>"`,
// matching how measure_task runs a task's test command). Both normalize to argv.
func TestCommandUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		json string
		want []string
	}{
		{"array", `["go","test","./..."]`, []string{"go", "test", "./..."}},
		{"string-wraps-sh-c", `"go test ./..."`, []string{"sh", "-c", "go test ./..."}},
		{"empty-array", `[]`, nil},
		{"empty-string", `""`, nil},
		{"null", `null`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got Command
			if err := got.UnmarshalJSON([]byte(c.json)); err != nil {
				t.Fatalf("unmarshal %s: %v", c.json, err)
			}
			if !slices.Equal([]string(got), c.want) {
				t.Errorf("Command(%s) = %v, want %v", c.json, []string(got), c.want)
			}
		})
	}
}

// A non-string/non-array command is a malformed declaration — fail loud, never
// silently drop the operator's intent.
func TestCommandUnmarshalRejectsWrongType(t *testing.T) {
	var got Command
	if err := got.UnmarshalJSON([]byte(`42`)); err == nil {
		t.Fatal("expected error unmarshalling a number as a Command")
	}
}

// ReadCustomizations extracts ONLY the run fields from customizations.semdev — a
// devcontainer.json's image/toolchain modeling is the Dockerfile's job (SB2: this is
// not an environment DSL), so unrelated devcontainer keys are ignored.
func TestReadCustomizations(t *testing.T) {
	devcontainer := []byte(`{
	  "name": "go-health",
	  "build": {"dockerfile": "Dockerfile"},
	  "customizations": {
	    "vscode": {"extensions": ["golang.go"]},
	    "semdev": {
	      "resolveCommand": ["go","mod","download"],
	      "buildCommand": ["go","build","./..."],
	      "testCommand": "go test ./...",
	      "tiers": [{"name":"unit","scope":"sandbox","proves":["unit"]}],
	      "secretRefs": ["GITHUB_PACKAGES_TOKEN"]
	    }
	  }
	}`)
	c, found, err := ReadCustomizations(devcontainer)
	if err != nil {
		t.Fatalf("ReadCustomizations: %v", err)
	}
	if !found {
		t.Fatal("customizations.semdev present but found=false")
	}
	if !slices.Equal([]string(c.ResolveCommand), []string{"go", "mod", "download"}) {
		t.Errorf("resolve = %v", c.ResolveCommand)
	}
	if !slices.Equal([]string(c.TestCommand), []string{"sh", "-c", "go test ./..."}) {
		t.Errorf("test = %v", c.TestCommand)
	}
	if len(c.Tiers) != 1 || c.Tiers[0].Scope != TierSandbox {
		t.Errorf("tiers = %+v", c.Tiers)
	}
	if !slices.Equal(c.SecretRefs, []string{"GITHUB_PACKAGES_TOKEN"}) {
		t.Errorf("secretRefs = %v", c.SecretRefs)
	}
}

// A devcontainer with no semdev block yields found=false so the caller falls back to
// convention — never an error (many repos declare only image/extensions).
func TestReadCustomizationsAbsent(t *testing.T) {
	_, found, err := ReadCustomizations([]byte(`{"name":"x","customizations":{"vscode":{}}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("no semdev block, want found=false")
	}
}

// ResolveManifest overlays customizations.semdev onto the profile convention: fields
// the operator set win; absent fields keep the convention default. The image
// declaration and cache-home control ride through unchanged.
func TestResolveManifestOverlay(t *testing.T) {
	img := ImageDecl{Dockerfile: "Dockerfile"}
	devcontainer := []byte(`{
	  "customizations": {"semdev": {
	    "testCommand": ["go","test","-race","./..."],
	    "secretRefs": ["GH_PACKAGES"]
	  }}
	}`)
	m, err := ResolveManifest(ProfileGo, img, devcontainer)
	if err != nil {
		t.Fatalf("ResolveManifest: %v", err)
	}
	// Overridden field wins.
	if !slices.Equal(m.TestCmd, []string{"go", "test", "-race", "./..."}) {
		t.Errorf("TestCmd = %v, want the customized command", m.TestCmd)
	}
	// Absent fields fall back to the Go convention.
	if !slices.Equal(m.ResolveCmd, []string{"go", "mod", "download"}) {
		t.Errorf("ResolveCmd = %v, want the Go convention default", m.ResolveCmd)
	}
	if !slices.Equal(m.BuildCmd, []string{"go", "build", "./..."}) {
		t.Errorf("BuildCmd = %v, want the Go convention default", m.BuildCmd)
	}
	// The cache-home control and image declaration ride through.
	if !slices.Equal(m.CacheHomeEnvs, []string{"GOMODCACHE", "GOCACHE"}) {
		t.Errorf("CacheHomeEnvs = %v", m.CacheHomeEnvs)
	}
	if m.Image.Dockerfile != "Dockerfile" {
		t.Errorf("Image = %+v, want the declared Dockerfile", m.Image)
	}
	if !slices.Equal(m.SecretRefs, []string{"GH_PACKAGES"}) {
		t.Errorf("SecretRefs = %v", m.SecretRefs)
	}
}

// With no devcontainer at all, ResolveManifest is pure convention for the profile —
// the M0 bare-Dockerfile Go path.
func TestResolveManifestConventionOnly(t *testing.T) {
	m, err := ResolveManifest(ProfileGo, ImageDecl{Dockerfile: "Dockerfile"}, nil)
	if err != nil {
		t.Fatalf("ResolveManifest: %v", err)
	}
	if !slices.Equal(m.TestCmd, []string{"go", "test", "./..."}) {
		t.Errorf("TestCmd = %v, want the Go convention", m.TestCmd)
	}
	if got := AssessReadiness(m.Tiers, ClaimUnit); got != ReadinessReady {
		t.Errorf("convention Go manifest readiness = %q, want ready", got)
	}
}

// Fail closed: a profile with no convention AND no customizations test command has
// nothing to run — ResolveManifest errors rather than yield a manifest that would
// silently measure nothing (the semspec "verified over zero executions" grave).
func TestResolveManifestFailsClosedWithNothingToRun(t *testing.T) {
	// An unknown profile has no convention; an empty devcontainer supplies no command.
	if _, err := ResolveManifest("cobol", ImageDecl{Dockerfile: "Dockerfile"}, nil); err == nil {
		t.Fatal("expected an error when neither convention nor customizations supply a test command")
	}
}

// Fail closed: a whitespace-only test command normalizes to absent, so it cannot slip
// past the guard as a `sh -c "   "` that exits 0 and reads green (the SB5
// measures-nothing-reads-green class). An unknown profile is used so the convention
// cannot backfill a real command and mask the check.
func TestResolveManifestFailsClosedOnBlankTestCommand(t *testing.T) {
	devcontainer := []byte(`{"customizations":{"semdev":{"testCommand":"   "}}}`)
	if _, err := ResolveManifest("cobol", ImageDecl{Dockerfile: "Dockerfile"}, devcontainer); err == nil {
		t.Fatal("expected an error: a whitespace-only test command must not become a runnable no-op")
	}
}

// Fail closed (SB2): with no declared image there is nothing to build or prove cold —
// ResolveManifest errors so the provisioning rule parks toward the operator, never a
// guessed image.
func TestResolveManifestFailsClosedWithoutDeclaredImage(t *testing.T) {
	if _, err := ResolveManifest(ProfileGo, ImageDecl{}, nil); err == nil {
		t.Fatal("expected an error when no image is declared (SB2 fail-closed)")
	}
}
