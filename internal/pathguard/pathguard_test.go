package pathguard

import (
	"path/filepath"
	"strings"
	"testing"
)

// The ONE guard's contract, pinned directly (groups-2-3 go-review LOW-2a) so it
// survives consumer refactors: repo-relative resolution inside root, rejection of
// traversal and absolute paths, and acceptance of a clean-to-inside composite.
func TestSafeJoin(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	cases := []struct {
		name string
		rel  string
		want string // "" = expect rejection
	}{
		{"plain file", "pkg/f.go", filepath.Join(root, "pkg/f.go")},
		{"dot", ".", root},
		{"clean-to-inside composite", "a/../b", filepath.Join(root, "b")},
		{"devcontainer-style up-and-back", ".devcontainer/..", root},
		{"traversal", "../outside", ""},
		{"deep traversal", "a/../../outside", ""},
		{"bare dotdot", "..", ""},
		{"absolute", "/etc/passwd", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SafeJoin(root, c.rel)
			if c.want == "" {
				if err == nil {
					t.Fatalf("SafeJoin(%q) = %q, want rejection", c.rel, got)
				}
				if !strings.Contains(err.Error(), "escapes the checkout") && !strings.Contains(err.Error(), "repo-relative") {
					t.Errorf("rejection message %q lacks the containment shape", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SafeJoin(%q): %v", c.rel, err)
			}
			if got != c.want {
				t.Errorf("SafeJoin(%q) = %q, want %q", c.rel, got, c.want)
			}
		})
	}
}
