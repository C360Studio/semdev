package boot

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestWriteTodosStaysSkipped is the offline half of the beta.159 write_todos
// closure. RegisterBuiltins' hard-fail (no projection.MutationClient → boot
// error) lives in its live-NATS branch, so `go test ./...` cannot exercise the
// gate itself — reverting the skip would surface only at docker boot, late and
// loud. This pins the two facts that make the skip correct instead:
//
//  1. the skip list still names write_todos — a product shell cannot supply the
//     mutation client the tool needs (its contract lives in semstreams'
//     internal/builtinprojection, bound via service.WireOwnership), and
//  2. nothing under configs/ references the tool — the moment a persona, rule,
//     or allowlist wants write_todos this goes RED, forcing the upstream ask
//     (an exported accessor for the builtin contracts), never a local
//     re-declaration of the framework's contract (a G5 two-writer hazard).
func TestWriteTodosStaysSkipped(t *testing.T) {
	if !slices.Contains(productShellSkippedBuiltins, "write_todos") {
		t.Fatal("write_todos left the skip list — RegisterBuiltins hard-fails without a projection.MutationClient a product shell cannot supply, and that failure is reachable only at a live-NATS boot")
	}

	root := filepath.Join("..", "..", "configs")
	found := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		found++
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(raw), "write_todos") {
			t.Errorf("%s references write_todos, but the skip means its executor is never registered — a spawn advertising it would have every call rejected (beta.149 enforcement); file the upstream ask instead of referencing the tool", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if found == 0 {
		t.Fatalf("no files found under %s — the referenced-nowhere pin matched nothing and proves nothing", root)
	}
}
