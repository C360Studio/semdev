package runspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/pathguard"
	"github.com/c360studio/semstreams/message"
)

// Attempts resolves a task's current dev-loop attempt from the run's checkout — the
// contents of the task's declared target files as they stand now — into the pure
// floors.Attempt the deterministic checks consume. It is the concrete checkfloors.Attempts
// seam (design SB4/SB6). At M0 the "attempt" is the current checkout state of the target
// files (what apply_patch has authored so far); a file the developer has not yet authored
// is absent from Files but still LISTED in TargetFiles — the resolver's job is to REPORT
// that absence faithfully, not to judge it.
//
// ⚠ This resolver does NOT gate on presence, and the current floors do NOT reject an
// all-absent attempt (an attempt that authored NONE of its declared targets reads green —
// the SB5 "verified over zero executions" theater). The presence gate that rejects/parks
// an attempt missing its declared targets belongs in check_floors / the floors (compare
// len(Files) against TargetFiles), NOT here — carry-forward, group 4B/7. Not reachable at
// M0: the go-health-class fixture ships both target files, and check_floors is not yet in
// the live journey.
type Attempts struct {
	reader    changefacts.Reader
	checkouts *Checkouts
}

// NewAttempts builds the Attempts resolver over a fact reader and the checkout registry.
func NewAttempts(reader changefacts.Reader, checkouts *Checkouts) *Attempts {
	return &Attempts{reader: reader, checkouts: checkouts}
}

// Resolve reads the task's declared target files from the frozen task.spec and their
// current contents from the run's checkout. It fails CLOSED when no checkout exists or
// the task was never projected (no target_files) — a floor verdict must never be computed
// over a missing or unprojected artifact.
func (a *Attempts) Resolve(ctx context.Context, runEntityID string, taskIndex int) (floors.Attempt, error) {
	root, err := a.checkouts.Root(ctx, runEntityID)
	if err != nil {
		return floors.Attempt{}, err
	}
	targets, err := readTargetFiles(ctx, a.reader, runEntityID, taskIndex)
	if err != nil {
		return floors.Attempt{}, err
	}
	if len(targets) == 0 {
		return floors.Attempt{}, fmt.Errorf("runspace: task.spec.%d has no target_files on %s — was the change projected (project_tasks)?", taskIndex, runEntityID)
	}

	files := make([]floors.File, 0, len(targets))
	for _, rel := range targets {
		abs, err := SafeJoin(root, rel)
		if err != nil {
			return floors.Attempt{}, err
		}
		content, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				continue // declared but not yet authored — listed in TargetFiles, absent from Files
			}
			return floors.Attempt{}, fmt.Errorf("runspace: read target file %s in checkout: %w", rel, err)
		}
		files = append(files, floors.File{Path: rel, Content: string(content)})
	}

	// Capture the working tree's divergence from the committed attempt — the clean-tree
	// floor rejects on it (floors read the working tree; cold verify clones the commit, so
	// a dirty tree means they would prove different bytes — evidence tampering, G7). git
	// status is the impure read that belongs here in the resolver, not in the pure floors.
	dirty, err := a.checkouts.gitStatusPorcelain(ctx, root)
	if err != nil {
		return floors.Attempt{}, err
	}
	return floors.Attempt{Files: files, TargetFiles: targets, DirtyPaths: dirty}, nil
}

// readTargetFiles reads the JSON-array task.spec.<idx>.target_files fact off the run
// entity into the declared target paths — the approved write contract for task idx. Shared
// by the Attempts resolver (floor evaluation) and the Patcher (apply-scope enforcement) so
// both read the SAME contract from the SAME single owner.
func readTargetFiles(ctx context.Context, reader changefacts.Reader, runEntityID string, idx int) ([]string, error) {
	prefix := devtask.TaskSpecKeyPrefix(idx)
	triples, err := reader.ReadFacts(ctx, runEntityID, prefix)
	if err != nil {
		return nil, fmt.Errorf("runspace: read %s on %s: %w", prefix, runEntityID, err)
	}
	want := prefix + devtask.FactTargetFiles
	for _, tr := range triples {
		if tr.Predicate != want {
			continue
		}
		return decodeStringArray(tr, want)
	}
	return nil, nil
}

// decodeStringArray decodes a JSON-array fact object into a string slice.
func decodeStringArray(tr message.Triple, predicate string) ([]string, error) {
	s, ok := tr.Object.(string)
	if !ok {
		return nil, fmt.Errorf("runspace: %s has non-string object %T", predicate, tr.Object)
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("runspace: %s is not a JSON array: %w", predicate, err)
	}
	return out, nil
}

// SafeJoin resolves a repo-relative path within root, rejecting any path that escapes the
// checkout (a `../` traversal or an absolute path) — the artifact's declared targets must
// stay inside its own tree. Exported so the read-side tools (read_workspace) share the SAME
// containment guard the write side (Attempts, the Patcher) enforces — one path-guard
// implementation, not a re-derived copy (DRY; a divergent read-side guard would be a silent
// escape surface). The implementation lives in pathguard so packages runspace itself
// imports (cleanroom's image builder) share the same guard without a cycle; this re-export
// keeps every existing caller and its call shape intact.
func SafeJoin(root, rel string) (string, error) {
	return pathguard.SafeJoin(root, rel)
}
