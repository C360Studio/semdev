package createchange

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

// richTaskCall returns a create_change call whose single task carries the given
// (rich) item fields, so a pin can author exactly one task shape.
func richTaskCall(item map[string]any) agentic.ToolCall {
	call := sampleCall()
	call.Arguments["tasks"] = []any{map[string]any{"section": "1. Fix", "items": []any{item}}}
	return call
}

// stampedFacts runs the tool and returns the stamped triples as a predicate→object
// map (plus the raw slice for Source/subject checks).
func stampedFacts(t *testing.T, call agentic.ToolCall) (map[string]string, []message.Triple) {
	t.Helper()
	w := &fakeWriter{}
	res, err := New(w, nil).Execute(context.Background(), call)
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if len(w.replaces) != 1 {
		t.Fatalf("expected one replace, got %d", len(w.replaces))
	}
	idx := map[string]string{}
	for _, tr := range w.replaces[0].add {
		idx[tr.Predicate] = tr.Object.(string)
	}
	return idx, w.replaces[0].add
}

// The execution-rich per-task fields are stamped under openspec.change.<slug>.
// task.<i>.<field> with the vocab writer Source — the graph-only facts the projector
// reads (arrays as JSON, budget as an int string).
func TestCreateChangeStampsRichTaskFacts(t *testing.T) {
	call := richTaskCall(map[string]any{
		"number":       "1.1",
		"text":         "add the guard",
		"target_files": []any{"handler.go"},
		"test_command": "go test ./...",
		"assumptions":  []any{"router is wired"},
		"non_goals":    []any{},
		"budget":       3,
	})
	idx, triples := stampedFacts(t, call)
	base := "openspec.change.fix-null-deref.task.0."
	want := map[string]string{
		base + "target_files": `["handler.go"]`,
		base + "test_command": "go test ./...",
		base + "assumptions":  `["router is wired"]`,
		base + "non_goals":    "[]",
		base + "budget":       "3",
	}
	for pred, obj := range want {
		if idx[pred] != obj {
			t.Errorf("%s = %q, want %q", pred, idx[pred], obj)
		}
	}
	// Source + subject discipline on a rich fact (G5/D15).
	for _, tr := range triples {
		if tr.Predicate == base+"target_files" {
			if tr.Source != Source {
				t.Errorf("rich fact Source = %q, want vocab writer %q", tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("rich fact subject = %q, want run entity %q", tr.Subject, runEntity)
			}
		}
	}
}

// Presence is load-bearing (the projector seam guard): an ABSENT field stamps NO
// fact (the projector reads nil = a gap), while an authored-empty list stamps "[]"
// (a real value the projector accepts). create_change must not normalize nil → [].
func TestCreateChangeRichTaskPresencePreserved(t *testing.T) {
	call := richTaskCall(map[string]any{
		"number":      "1.1",
		"text":        "t",
		"assumptions": []any{}, // authored-empty → "[]"
		// target_files, test_command, non_goals, budget ABSENT → no fact
	})
	idx, _ := stampedFacts(t, call)
	base := "openspec.change.fix-null-deref.task.0."
	if idx[base+"assumptions"] != "[]" {
		t.Errorf("authored-empty assumptions must stamp \"[]\", got %q", idx[base+"assumptions"])
	}
	for _, absent := range []string{"target_files", "test_command", "non_goals", "budget"} {
		if _, ok := idx[base+absent]; ok {
			t.Errorf("absent %q must stamp NO fact (nil = projector gap), but %q was stamped", absent, base+absent)
		}
	}
}

// A task's thin facts (text) and its rich facts (target_files) share ONE global
// zero-based index across sections — the projector's contiguous RawTask.Index — so
// the two never drift.
func TestCreateChangeRichTaskIndexAlignsAcrossSections(t *testing.T) {
	call := sampleCall()
	call.Arguments["tasks"] = []any{
		map[string]any{"section": "1. A", "items": []any{
			map[string]any{"number": "1.1", "text": "first", "target_files": []any{"a.go"}},
		}},
		map[string]any{"section": "2. B", "items": []any{
			map[string]any{"number": "2.1", "text": "second", "target_files": []any{"b.go"}},
		}},
	}
	idx, _ := stampedFacts(t, call)
	base := "openspec.change.fix-null-deref.task."
	if idx[base+"0.text"] != "first" || idx[base+"0.target_files"] != `["a.go"]` {
		t.Errorf("task.0 thin+rich misaligned: text=%q target=%q", idx[base+"0.text"], idx[base+"0.target_files"])
	}
	if idx[base+"1.text"] != "second" || idx[base+"1.target_files"] != `["b.go"]` {
		t.Errorf("task.1 thin+rich misaligned: text=%q target=%q", idx[base+"1.text"], idx[base+"1.target_files"])
	}
}

// Regression pin for the thin/rich index coupling: toChange assigns each rich
// task's index as it enters change.Tasks (one walk, no second flatten), so a rich
// field lands on the EXACT task it was authored on — across multiple sections, with
// only SOME items carrying rich fields. This guards that the single-walk indexing
// stays intact (a future change that re-derived rich indices from a separate flatten
// would reintroduce drift and fail here).
func TestCreateChangeRichTaskIndexSurvivesPartial(t *testing.T) {
	call := sampleCall()
	call.Arguments["tasks"] = []any{
		map[string]any{"section": "1. A", "items": []any{
			map[string]any{"number": "1.1", "text": "a-first"},                                 // task 0 — NO rich fields
			map[string]any{"number": "1.2", "text": "a-second", "target_files": []any{"s.go"}}, // task 1 — rich
		}},
		map[string]any{"section": "2. B", "items": []any{
			map[string]any{"number": "2.1", "text": "b-first", "test_command": "go test ./..."}, // task 2 — rich
		}},
	}
	idx, _ := stampedFacts(t, call)
	base := "openspec.change.fix-null-deref.task."
	// task 0 authored no rich fields → none stamped.
	if _, ok := idx[base+"0.target_files"]; ok {
		t.Error("task.0 authored no rich fields but a target_files fact was stamped — index drift")
	}
	// task 1's target_files must land on task 1 (a-second), not task 0 or 2.
	if idx[base+"1.text"] != "a-second" || idx[base+"1.target_files"] != `["s.go"]` {
		t.Errorf("rich target_files drifted off its task: task.1.text=%q target=%q", idx[base+"1.text"], idx[base+"1.target_files"])
	}
	// task 2's test_command must land on task 2 (b-first, second section).
	if idx[base+"2.text"] != "b-first" || idx[base+"2.test_command"] != "go test ./..." {
		t.Errorf("rich test_command drifted: task.2.text=%q cmd=%q", idx[base+"2.text"], idx[base+"2.test_command"])
	}
}

// Q1 guard (graph-only is permanent): the rich fields must NOT round-trip into
// tasks.md — reconstruction reads only the thin task keys, so a rendered tasks.md
// from the stamped facts carries checkboxes only, never target_files/test_command.
func TestCreateChangeRichFactsDoNotRoundTripToTasksMd(t *testing.T) {
	call := richTaskCall(map[string]any{
		"number":       "1.1",
		"text":         "add the guard",
		"target_files": []any{"handler.go"},
		"test_command": "go test ./...",
		"budget":       3,
	})
	_, triples := stampedFacts(t, call)
	facts := make([]openspec.Fact, 0, len(triples))
	for _, tr := range triples {
		facts = append(facts, openspec.Fact{Predicate: tr.Predicate, Object: tr.Object.(string)})
	}
	md := openspec.RenderTasks(openspec.ChangeFromFacts("fix-null-deref", facts).Tasks)
	for _, leak := range []string{"target_files", "test_command", "handler.go", "go test", "budget"} {
		if strings.Contains(md, leak) {
			t.Errorf("rich field %q leaked into rendered tasks.md (Q1 violated):\n%s", leak, md)
		}
	}
	if !strings.Contains(md, "add the guard") {
		t.Errorf("the thin task text must still render:\n%s", md)
	}
}

// G3: an outcome-shaped field smuggled onto a task (passed/verified) is ignored —
// no such fact is stamped. Extends the done-ignored discipline to the rich schema.
func TestCreateChangeIgnoresTaskOutcomeField(t *testing.T) {
	call := richTaskCall(map[string]any{
		"number":       "1.1",
		"text":         "t",
		"test_command": "go test ./...",
		"passed":       true,
		"verified":     "yes",
	})
	_, triples := stampedFacts(t, call)
	for _, tr := range triples {
		if strings.Contains(tr.Predicate, "passed") || strings.Contains(tr.Predicate, "verified") {
			t.Errorf("outcome-shaped field leaked into a fact: %q (G3)", tr.Predicate)
		}
	}
}
