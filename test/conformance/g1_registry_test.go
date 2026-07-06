package conformance

import (
	"sort"
	"testing"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/registry"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/componentregistry"
)

// frameworkFactories returns the set of factory names registered by the
// semstreams framework alone (componentregistry.Register), the baseline against
// which semdev's own additions are measured.
func frameworkFactories(t *testing.T) map[string]bool {
	t.Helper()
	reg := component.NewRegistry()
	if err := componentregistry.Register(reg); err != nil {
		t.Fatalf("register framework components: %v", err)
	}
	return factoryNameSet(reg)
}

// semdevFactories returns the full set semdev's binaries register via the one
// shared boot.RegisterAll.
func semdevFactories(t *testing.T) map[string]bool {
	t.Helper()
	reg := component.NewRegistry()
	if err := boot.RegisterAll(reg); err != nil {
		t.Fatalf("boot.RegisterAll: %v", err)
	}
	return factoryNameSet(reg)
}

func factoryNameSet(reg *component.Registry) map[string]bool {
	out := make(map[string]bool)
	for name := range reg.ListFactories() {
		out[name] = true
	}
	return out
}

// G1 — primitive-first. Every component semdev registers beyond the framework
// baseline must have a registry.Entry (and thus an alignment note). A component
// wired into boot.RegisterAll with no entry — the unreviewed-Go-addition shape —
// fails the build. At M0 the added set is empty; the pin guards the boundary as
// Go lands.
func TestSemdevComponentsAreRegistered(t *testing.T) {
	framework := frameworkFactories(t)
	full := semdevFactories(t)

	added := make(map[string]bool)
	for name := range full {
		if !framework[name] {
			added[name] = true
		}
	}

	declared := registry.ComponentNames()

	// Every semdev-added component must be declared with an alignment note.
	for _, msg := range undeclaredRegistrations("component", added, declared) {
		t.Error(msg)
	}
	// No phantom entries: every declared component must actually be registered.
	for name := range declared {
		if !full[name] {
			t.Errorf("registry declares component %q that boot.RegisterAll does not register", name)
		}
	}
}

// Red-first: the census must flag a registered surface that is absent from the
// declaration — the same core guards both the component and tool censuses.
func TestRegistrationCensusCatchesUndeclared(t *testing.T) {
	added := map[string]bool{"semdev-measurement": true}
	declared := map[string]bool{}
	if len(undeclaredRegistrations("component", added, declared)) == 0 {
		t.Error("census passed an undeclared registered component; G1 pin does not fire")
	}
	if len(undeclaredRegistrations("tool", added, declared)) == 0 {
		t.Error("census passed an undeclared registered tool; G1 pin does not fire")
	}
}

// Sanity: the framework baseline is non-empty, so the diff is meaningful and the
// pin cannot pass merely because both sets are empty.
func TestFrameworkBaselineNonEmpty(t *testing.T) {
	if n := len(frameworkFactories(t)); n == 0 {
		t.Fatal("framework registered zero factories; the G1 diff would be meaningless")
	}
	full := semdevFactories(t)
	names := make([]string, 0, len(full))
	for n := range full {
		names = append(names, n)
	}
	sort.Strings(names)
	t.Logf("boot.RegisterAll registers %d factories", len(names))
}
