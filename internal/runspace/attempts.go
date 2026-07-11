package runspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/floors"
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
	targets, err := a.readTargetFiles(ctx, runEntityID, taskIndex)
	if err != nil {
		return floors.Attempt{}, err
	}
	if len(targets) == 0 {
		return floors.Attempt{}, fmt.Errorf("runspace: task.spec.%d has no target_files on %s — was the change projected (project_tasks)?", taskIndex, runEntityID)
	}

	files := make([]floors.File, 0, len(targets))
	for _, rel := range targets {
		abs, err := safeJoin(root, rel)
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
	return floors.Attempt{Files: files, TargetFiles: targets}, nil
}

// readTargetFiles reads the JSON-array task.spec.<idx>.target_files fact off the run
// entity into the declared target paths.
func (a *Attempts) readTargetFiles(ctx context.Context, runEntityID string, idx int) ([]string, error) {
	prefix := devtask.TaskSpecKeyPrefix(idx)
	triples, err := a.reader.ReadFacts(ctx, runEntityID, prefix)
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

// safeJoin resolves a repo-relative path within root, rejecting any path that escapes the
// checkout (a `../` traversal or an absolute path) — the artifact's declared targets must
// stay inside its own tree.
func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("runspace: target file %q must be repo-relative, not absolute", rel)
	}
	abs := filepath.Join(root, rel)
	within, err := filepath.Rel(root, abs)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("runspace: target file %q escapes the checkout", rel)
	}
	return abs, nil
}
