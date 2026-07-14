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

// serviceManagerPkg is the framework's service/runtime-wiring package (NATS
// client construction feeds it, ServiceManager, ConfigureFromServices,
// CreateService, StartAll/StopAll all live here). Only internal/boot's
// runtime.go may reach it; a main importing it directly would be wiring the
// ServiceManager independently instead of going through the shared boot.Run —
// the same half-wired-binary risk one level up from component registration.
const serviceManagerPkg = "github.com/c360studio/semstreams/service"

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

// G1 / binary parity — every cmd/*/ binary must bring up its ENTIRE runtime
// (component/tool/service registration, NATS, the ServiceManager) through the
// one shared boot.Run, and none may import the framework's registration
// (componentregistry) or service-wiring (service) packages directly. Both
// registration calls and boot.Run calls now happen inside internal/boot
// (RegisterAll/RegisterTools/RegisterLifecycle are called from
// runtime.go, not from main) — so a compliant main calls boot.Run and
// NOTHING else that starts with "Register" or reaches those two framework
// packages. A future direct `someInput.Register(reg)`, a bypassing
// `componentregistry.Register(reg)`, or a main that wires
// `service.NewServiceManager` itself instead of calling boot.Run would
// re-open the half-wired-binary class; this source scan makes that drift fail
// the build.
func TestBinariesRegisterOnlyThroughBoot(t *testing.T) {
	for bin, files := range binaryGoFiles(t) {
		var registerCalls, bootRunCalls []string
		for _, path := range files {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			fileRegisterCalls, err := registrationCalls(src)
			if err != nil {
				t.Fatalf("scan register calls %s: %v", path, err)
			}
			registerCalls = append(registerCalls, fileRegisterCalls...)

			fileRunCalls, err := runCalls(src)
			if err != nil {
				t.Fatalf("scan run calls %s: %v", path, err)
			}
			bootRunCalls = append(bootRunCalls, fileRunCalls...)

			imports, err := fileImports(src)
			if err != nil {
				t.Fatalf("scan imports %s: %v", path, err)
			}
			for _, imp := range imports {
				if imp == componentRegistryPkg {
					t.Errorf("%s imports %s directly; framework registration must route through internal/boot", filepath.Base(path), componentRegistryPkg)
				}
				if imp == serviceManagerPkg {
					t.Errorf("%s imports %s directly; runtime/service wiring must route through internal/boot.Run", filepath.Base(path), serviceManagerPkg)
				}
			}
		}
		// Every Register* call now lives inside internal/boot (runtime.go calls
		// them on the binary's behalf); a binary calling one directly would be
		// reaching around boot.Run's wiring.
		if len(registerCalls) != 0 {
			t.Errorf("binary %q calls registration functions directly: %v — registration must happen only inside boot.Run's wiring, never in main", bin, registerCalls)
		}
		if len(bootRunCalls) != 1 || bootRunCalls[0] != "boot.Run" {
			t.Errorf("binary %q boot.Run calls = %v; want exactly [boot.Run] — both binaries must bring up the runtime only through the shared boot.Run", bin, bootRunCalls)
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
	_ = boot.RegisterAll(reg, nil, nil, "")
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

// Red-first: the import scan must flag a main that imports the framework's
// service-wiring package directly — the "wires the ServiceManager
// independently instead of going through boot.Run" drift.
func TestParityScanCatchesDirectServiceManagerImport(t *testing.T) {
	bad := []byte(`package main

import "github.com/c360studio/semstreams/service"

func main() { _ = service.NewServiceManager }
`)
	imports, err := fileImports(bad)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	found := false
	for _, imp := range imports {
		if imp == serviceManagerPkg {
			found = true
		}
	}
	if !found {
		t.Errorf("import scan missed a direct semstreams/service import; got %v", imports)
	}
}

// Red-first: the run-call scan must find a boot.Run invocation — the positive
// extraction the main assertion above relies on to prove both binaries bring
// up the runtime through the shared entrypoint.
func TestParityScanCatchesBootRunCall(t *testing.T) {
	src := []byte(`package main

import "github.com/c360studio/semdev/internal/boot"

func main() {
	_ = boot.Run(nil, boot.RunOptions{})
}
`)
	calls, err := runCalls(src)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	found := false
	for _, c := range calls {
		if c == "boot.Run" {
			found = true
		}
	}
	if !found {
		t.Errorf("run-call scan missed a boot.Run call; got %v", calls)
	}
}
