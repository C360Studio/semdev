package conformance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sanctionedGraphClientBuilder is the ONLY function in internal/boot permitted to
// call graphown.NewClients: it declares semdev's vocabulary with the framework
// registry first. Any other construction site is a dead entry point (see the scan
// core's comment and TestGraphClientConstructionRequiresDeclaredVocabulary).
const sanctionedGraphClientBuilder = "declaredGraphClients"

// TestBootBuildsGraphClientsOnlyThroughTheDeclaringHelper pins the class the
// semstreams review caught in launch.go: an entry point that builds the
// contract-validated mutation client without first declaring the vocabulary is
// not degraded, it is DEAD — every call fails at construction. One helper owns
// the ordering so a new entry point cannot reintroduce it.
func TestBootBuildsGraphClientsOnlyThroughTheDeclaringHelper(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "boot")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/boot: %v", err)
	}
	var offenders []string
	sanctioned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		sites, err := graphClientConstructionSites(src)
		if err != nil {
			t.Fatalf("scan %s: %v", e.Name(), err)
		}
		for _, fn := range sites {
			if fn == sanctionedGraphClientBuilder {
				sanctioned++
				continue
			}
			offenders = append(offenders, e.Name()+":"+fn)
		}
	}
	if len(offenders) > 0 {
		slices.Sort(offenders)
		t.Errorf("these internal/boot functions build the graph mutation client directly instead of through %s(), "+
			"so they run before semdev's vocabulary is declared and every construction fails "+
			"(%q): %v",
			sanctionedGraphClientBuilder,
			"canonical but not declared in the vocabulary registry",
			offenders)
	}
	if sanctioned == 0 {
		t.Errorf("no %s() construction site found in internal/boot — the helper that declares the "+
			"vocabulary before building clients must exist and must be the sole builder", sanctionedGraphClientBuilder)
	}
}

// TestGraphClientConstructionSiteScanCatchesADirectCall proves the scan can
// fail before its pass is trusted: a synthetic direct call must be reported,
// and a call routed through the helper must not be.
func TestGraphClientConstructionSiteScanCatchesADirectCall(t *testing.T) {
	direct := []byte(`package boot
func RunSomething() error {
	c, err := graphown.NewClients(nc)
	_ = c
	return err
}
func other() {}
`)
	sites, err := graphClientConstructionSites(direct)
	if err != nil {
		t.Fatalf("scan synthetic source: %v", err)
	}
	if !slices.Contains(sites, "RunSomething") {
		t.Fatalf("the scan missed a direct graphown.NewClients call; got %v", sites)
	}

	routed := []byte(`package boot
func RunSomething() error {
	c, err := declaredGraphClients(nc)
	_ = c
	return err
}
`)
	sites, err = graphClientConstructionSites(routed)
	if err != nil {
		t.Fatalf("scan routed source: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("the scan reported a construction site for a routed call; got %v", sites)
	}
}
