package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// componentRegistryPkg is the framework registration entrypoint. Only
// internal/boot may reach it; a main importing it directly bypasses the single
// shared registration path.
const componentRegistryPkg = "github.com/c360studio/semstreams/componentregistry"

// binaryGoFiles returns the non-test .go files of every cmd/*/ binary, grouped
// by binary directory. Globbing (not a hardcoded list) means a third binary
// cannot silently escape the parity net, and scanning every file in the dir
// (not just main.go) means a sibling like cmd/semdev/wire.go is covered too.
func binaryGoFiles(t *testing.T) map[string][]string {
	t.Helper()
	cmdDir := filepath.Join(repoRoot(t), "cmd")
	bins, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatalf("read cmd dir: %v", err)
	}
	out := make(map[string][]string)
	for _, bin := range bins {
		if !bin.IsDir() {
			continue
		}
		binPath := filepath.Join(cmdDir, bin.Name())
		entries, err := os.ReadDir(binPath)
		if err != nil {
			t.Fatalf("read %s: %v", binPath, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			out[bin.Name()] = append(out[bin.Name()], filepath.Join(binPath, name))
		}
	}
	if len(out) == 0 {
		t.Fatal("no cmd/*/ binaries found; the parity pin would pass vacuously")
	}
	return out
}

// G1 / binary parity — every cmd/*/ binary must register components ONLY through
// the one shared boot.RegisterAll, and none may import the framework
// registration package directly. A future direct `someInput.Register(reg)` or a
// bypassing `componentregistry.Register(reg)` in any binary file would re-open
// the half-wired-binary class; this source scan makes that drift fail the build.
func TestBinariesRegisterOnlyThroughBoot(t *testing.T) {
	for bin, files := range binaryGoFiles(t) {
		var calls []string
		for _, path := range files {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			fileCalls, err := registrationCalls(src)
			if err != nil {
				t.Fatalf("scan calls %s: %v", path, err)
			}
			calls = append(calls, fileCalls...)

			imports, err := fileImports(src)
			if err != nil {
				t.Fatalf("scan imports %s: %v", path, err)
			}
			for _, imp := range imports {
				if imp == componentRegistryPkg {
					t.Errorf("%s imports %s directly; framework registration must route through internal/boot", filepath.Base(path), componentRegistryPkg)
				}
			}
		}
		if len(calls) != 1 || calls[0] != "boot.RegisterAll" {
			t.Errorf("binary %q registration calls = %v; want exactly [boot.RegisterAll] — a direct Register call re-opens the half-wired-binary class", bin, calls)
		}
	}
}

// Red-first: the scan must flag a main that calls a component's Register directly
// instead of routing through boot.
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

// Red-first: the import scan must flag a main that imports the framework
// registration package directly.
func TestParityScanCatchesDirectComponentRegistryImport(t *testing.T) {
	bad := []byte(`package main

import "github.com/c360studio/semstreams/componentregistry"

func main() { _ = componentregistry.Register }
`)
	imports, err := fileImports(bad)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	found := false
	for _, imp := range imports {
		if imp == componentRegistryPkg {
			found = true
		}
	}
	if !found {
		t.Errorf("import scan missed a direct componentregistry import; got %v", imports)
	}
}
