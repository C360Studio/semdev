package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

// mainFiles are the two binary entrypoints that must register components
// identically. A component registered in one but not the other is the
// half-wired-binary silent-flow-break class.
var mainFiles = []string{
	filepath.Join("cmd", "semdev", "main.go"),
	filepath.Join("cmd", "e2e-semdev", "main.go"),
}

// G1 / binary parity — both mains must register components ONLY through the one
// shared boot.RegisterAll. A future direct `someInput.Register(reg)` in one main
// would compile, pass review, and re-introduce the half-wired-binary class; this
// source scan makes that drift fail the build offline.
func TestBinariesRegisterOnlyThroughBoot(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range mainFiles {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		calls, err := registrationCalls(src)
		if err != nil {
			t.Fatalf("scan %s: %v", rel, err)
		}
		if len(calls) != 1 || calls[0] != "boot.RegisterAll" {
			t.Errorf("%s registration calls = %v; want exactly [boot.RegisterAll] — a direct Register call re-opens the half-wired-binary class", rel, calls)
		}
	}
}

// Red-first: the scan must flag a main that calls a component's Register
// directly instead of routing through boot.
func TestParityScanCatchesDirectRegister(t *testing.T) {
	bad := []byte(`package main

import (
	"github.com/c360studio/semdev/internal/boot"
	githubwebhook "github.com/c360studio/semstreams/input/github-webhook"
	"github.com/c360studio/semstreams/component"
)

func main() {
	reg := component.NewRegistry()
	_ = boot.RegisterAll(reg)
	_ = githubwebhook.Register(reg) // drift: bypasses boot
}
`)
	calls, err := registrationCalls(bad)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	sawDrift := false
	for _, c := range calls {
		if c == "githubwebhook.Register" {
			sawDrift = true
		}
	}
	if !sawDrift {
		t.Errorf("parity scan missed a direct component Register call; got %v", calls)
	}
}
