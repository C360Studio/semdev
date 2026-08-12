package createchange

import (
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
)

// richTaskCall returns a create_change call whose single task carries the given
// (rich) item fields, so a pin can author exactly one task shape.
func richTaskCall(item map[string]any) agentic.ToolCall {
	call := sampleCall()
	call.Arguments["tasks"] = []any{map[string]any{"section": "1. Fix", "items": []any{item}}}
	return call
}

// The execution-rich per-task fields are stamped inside the document blob's
// RichTasks array (beta.147 D3 — no longer standalone
// openspec.change.<slug>.task.<i>.<field> facts): the graph-only fields the
// projector reads (arrays/budget as their real Go types, not JSON-in-a-string).
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
	doc, triples := stampedDocument(t, call)
	if len(doc.RichTasks) != 1 {
		t.Fatalf("expected 1 rich task, got %d", len(doc.RichTasks))
	}
	rt := doc.RichTasks[0]
	if rt.Index != 0 {
		t.Errorf("rich task index = %d, want 0", rt.Index)
	}
	if !reflect.DeepEqual(rt.TargetFiles, []string{"handler.go"}) {
		t.Errorf("target_files = %v, want [handler.go]", rt.TargetFiles)
	}
	if rt.TestCommand != "go test ./..." {
		t.Errorf("test_command = %q, want %q", rt.TestCommand, "go test ./...")
	}
	if !reflect.DeepEqual(rt.Assumptions, []string{"router is wired"}) {
		t.Errorf("assumptions = %v, want [router is wired]", rt.Assumptions)
	}
	if rt.NonGoals == nil || len(rt.NonGoals) != 0 {
		t.Errorf("authored-empty non_goals must decode to a non-nil empty slice, got %v", rt.NonGoals)
	}
	if rt.Budget == nil || *rt.Budget != 3 {
		t.Errorf("budget = %v, want 3", rt.Budget)
	}
	// Source + subject discipline on the document blob itself (G5/D15).
	for _, tr := range triples {
		if tr.Predicate == changefacts.DocumentPredicate {
			if tr.Source != Source {
				t.Errorf("document Source = %q, want vocab writer %q", tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("document subject = %q, want run entity %q", tr.Subject, runEntity)
			}
		}
	}
}

// Presence is load-bearing (the projector seam guard): an ABSENT field decodes to
// nil (the projector reads nil = a gap), while an authored-empty list decodes to a
// non-nil empty slice (a real value the projector accepts). The DTO round-trips
// this through JSON with no omitempty (beta.147 D3) — create_change must not
// normalize nil to [].
func TestCreateChangeRichTaskPresencePreserved(t *testing.T) {
	call := richTaskCall(map[string]any{
		"number":      "1.1",
		"text":        "t",
		"assumptions": []any{}, // authored-empty → non-nil empty
		// target_files, test_command, non_goals, budget ABSENT → nil (a projector gap)
	})
	doc, _ := stampedDocument(t, call)
	if len(doc.RichTasks) != 1 {
		t.Fatalf("expected 1 rich task, got %d", len(doc.RichTasks))
	}
	rt := doc.RichTasks[0]
	if rt.Assumptions == nil || len(rt.Assumptions) != 0 {
		t.Errorf("authored-empty assumptions must decode to a non-nil empty slice, got %#v", rt.Assumptions)
	}
	if rt.TargetFiles != nil {
		t.Errorf("absent target_files must decode to nil (a projector gap), got %#v", rt.TargetFiles)
	}
	if rt.TestCommand != "" {
		t.Errorf("absent test_command must decode to \"\", got %q", rt.TestCommand)
	}
	if rt.NonGoals != nil {
		t.Errorf("absent non_goals must decode to nil (a projector gap), got %#v", rt.NonGoals)
	}
	if rt.Budget != nil {
		t.Errorf("absent budget must decode to nil (a projector gap), got %v", *rt.Budget)
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
	doc, _ := stampedDocument(t, call)
	if doc.Change.Tasks == nil || len(doc.Change.Tasks.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %+v", doc.Change.Tasks)
	}
	if len(doc.RichTasks) != 2 {
		t.Fatalf("expected 2 rich tasks, got %d", len(doc.RichTasks))
	}
	firstText := doc.Change.Tasks.Sections[0].Tasks[0].Text
	secondText := doc.Change.Tasks.Sections[1].Tasks[0].Text
	if firstText != "first" || !reflect.DeepEqual(doc.RichTasks[0].TargetFiles, []string{"a.go"}) {
		t.Errorf("task 0 thin+rich misaligned: text=%q target=%v", firstText, doc.RichTasks[0].TargetFiles)
	}
	if secondText != "second" || !reflect.DeepEqual(doc.RichTasks[1].TargetFiles, []string{"b.go"}) {
		t.Errorf("task 1 thin+rich misaligned: text=%q target=%v", secondText, doc.RichTasks[1].TargetFiles)
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
	doc, _ := stampedDocument(t, call)
	if len(doc.RichTasks) != 3 {
		t.Fatalf("expected one rich-task DTO per thin task (even when it authored no rich fields), got %d", len(doc.RichTasks))
	}
	// task 0 authored no rich fields → its rich entry decodes all-absent.
	if doc.RichTasks[0].TargetFiles != nil {
		t.Errorf("task 0 authored no rich fields but target_files decoded non-nil — index drift: %#v", doc.RichTasks[0].TargetFiles)
	}
	// task 1's target_files must land on task 1 (a-second), not task 0 or 2.
	if got := doc.Change.Tasks.Sections[0].Tasks[1].Text; got != "a-second" || !reflect.DeepEqual(doc.RichTasks[1].TargetFiles, []string{"s.go"}) {
		t.Errorf("rich target_files drifted off its task: task.1.text=%q target=%v", got, doc.RichTasks[1].TargetFiles)
	}
	// task 2's test_command must land on task 2 (b-first, second section).
	if got := doc.Change.Tasks.Sections[1].Tasks[0].Text; got != "b-first" || doc.RichTasks[2].TestCommand != "go test ./..." {
		t.Errorf("rich test_command drifted: task.2.text=%q cmd=%q", got, doc.RichTasks[2].TestCommand)
	}
}

// Q1 guard (graph-only is permanent): the rich fields must NOT round-trip into
// tasks.md. Under beta.147 D3 this is now enforced by the TYPE the render side
// consumes — openspec.Task carries only Number/Text/Done, so RichTasks (a sibling
// field on the document DTO, never merged into Change.Tasks) has no path into
// RenderTasks at all.
func TestCreateChangeRichFactsDoNotRoundTripToTasksMd(t *testing.T) {
	call := richTaskCall(map[string]any{
		"number":       "1.1",
		"text":         "add the guard",
		"target_files": []any{"handler.go"},
		"test_command": "go test ./...",
		"budget":       3,
	})
	doc, _ := stampedDocument(t, call)
	md := openspec.RenderTasks(doc.Change.Tasks)
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
// it never reaches the stamped document. Extends the done-ignored discipline to
// the rich schema.
func TestCreateChangeIgnoresTaskOutcomeField(t *testing.T) {
	call := richTaskCall(map[string]any{
		"number":       "1.1",
		"text":         "t",
		"test_command": "go test ./...",
		"passed":       true,
		"verified":     "yes",
	})
	_, triples := stampedDocument(t, call)
	raw := objectOf(triples, changefacts.DocumentPredicate)
	if strings.Contains(raw, "passed") || strings.Contains(raw, "verified") {
		t.Errorf("outcome-shaped field leaked into the stamped document (G3): %s", raw)
	}
}
