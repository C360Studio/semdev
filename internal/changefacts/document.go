package changefacts

import (
	"encoding/json"
	"fmt"

	"github.com/c360studio/semdev/internal/openspec"
)

// DocumentPredicate is the single scalar the authored change lands under on the run
// entity (beta.147 D3). The old openspec.change.<slug>.delta.<cap>.<rid>.<field> triple
// tree is 6+ segments — not canonicalizable — so create_change now serializes the WHOLE
// change to one JSON document here, and Hydrate deserializes it. An OpenSpec change is
// one artifact, not hundreds of independent facts; the blob is simpler AND more honest,
// and it shrinks the vocabulary (G9). Single writer create-change-author-tool (G5).
const DocumentPredicate = "openspec.change.document"

// ChangeDocument is the whole authored change in one scalar: the openspec.Change model
// (render + the thin task text the projector reads as a task's goal) plus the
// execution-rich, graph-only per-task fields dev-from-task needs (target_files,
// test_command, …) that OpenSpec's thin tasks.md does not carry.
type ChangeDocument struct {
	Change    *openspec.Change `json:"change"`
	RichTasks []RichTask       `json:"rich_tasks"`
}

// RichTask is one task's execution-rich fields, keyed by the flat index it took AS IT
// ENTERED change.Tasks (so it aligns with the thin task at the same index). Presence is
// load-bearing and round-trips through JSON WITHOUT omitempty: an ABSENT list/budget
// marshals to null → nil on read (a projector gap → park), while an authored-empty list
// marshals to [] → non-nil empty on read (a real value the projector accepts). Adding
// omitempty here would erase that gap-vs-empty distinction and turn authored-empty into a
// spurious gap.
type RichTask struct {
	Index       int      `json:"index"`
	TargetFiles []string `json:"target_files"`
	TestCommand string   `json:"test_command"`
	Assumptions []string `json:"assumptions"`
	NonGoals    []string `json:"non_goals"`
	Budget      *int     `json:"budget"`
}

// MarshalDocument encodes the document to the scalar object stored under
// DocumentPredicate. Go's json.Marshal is deterministic for structs (field order fixed,
// map keys sorted), so create_change's content revision can hash this string stably.
func MarshalDocument(doc ChangeDocument) (string, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal change document: %w", err)
	}
	return string(b), nil
}

// UnmarshalDocument decodes the scalar stored under DocumentPredicate. An empty string
// (never authored) yields the zero document (nil Change, nil RichTasks), NOT an error —
// the caller decides whether an empty change is a failure for its step.
func UnmarshalDocument(s string) (ChangeDocument, error) {
	if s == "" {
		return ChangeDocument{}, nil
	}
	var doc ChangeDocument
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return ChangeDocument{}, fmt.Errorf("unmarshal change document: %w", err)
	}
	return doc, nil
}
