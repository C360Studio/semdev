// Package pathguard is the ONE checkout-containment path guard (lifted move-only from
// runspace, security-forge-containment 3.1, so packages runspace itself imports —
// cleanroom's image builder — can share it without an import cycle). Every consumer that
// resolves a repo-authored relative path against a checkout root goes through SafeJoin;
// a divergent re-derived guard would be a silent escape surface.
package pathguard

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeJoin resolves a repo-relative path within root, rejecting any path that escapes the
// checkout (a `../` traversal or an absolute path) — the artifact's declared targets must
// stay inside its own tree.
func SafeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("pathguard: target file %q must be repo-relative, not absolute", rel)
	}
	abs := filepath.Join(root, rel)
	within, err := filepath.Rel(root, abs)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("pathguard: target file %q escapes the checkout", rel)
	}
	return abs, nil
}
