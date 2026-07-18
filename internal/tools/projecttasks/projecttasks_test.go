package projecttasks

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/tools/validatechange"
	"github.com/c360studio/semstreams/message"
)

// demoRevision is the content revision create_change would stamp for "demo".
const demoRevision = "sha256:demo-content-v1"

// validatedAt is the run's openspec.change.validated marker — its value is the
// content REVISION the validator blessed (D15 #0), not the slug. project_tasks
// freezes task.spec only when this equals the change's current content revision.
func validatedAt(rev string) message.Triple {
	return message.Triple{Predicate: validatechange.ValidatedPredicate, Object: rev, Source: validatechange.Source}
}

// revisionAt is the change's current content revision (openspec.change.revision,
// flat/single-change at M0 — beta.147 D1) create_change stamps; project_tasks
// binds openspec.change.validated to it.
func revisionAt(rev string) message.Triple {
	return message.Triple{Predicate: createchange.RevisionPredicate, Object: rev, Source: createchange.Source}
}

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeReader replays scripted change facts, filtered by the prefix the tool asks
// for (mirroring the graph query the NATS reader performs).
type fakeReader struct {
	facts []message.Triple
	err   error
}

func (r *fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []message.Triple
	for _, t := range r.facts {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

// fakeWriter records replace calls and returns a scripted set of already-owned
// task.spec predicates (the immutability check).
type fakeWriter struct {
	owned    []string
	replaces [][]message.Triple
}

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, _ []string) error {
	w.replaces = append(w.replaces, add)
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _ string, _ string) ([]string, error) {
	return w.owned, nil
}

// validRichTask returns index i's fully-authored rich fields (the schema-complete
// case) — mirrors the pre-D3 validTaskFacts helper's quintet, now as the real Go
// types the blob DTO carries (not JSON-in-a-string).
func validRichTask(i int) changefacts.RichTask {
	budget := 3
	return changefacts.RichTask{
		Index:       i,
		TargetFiles: []string{"h.go", "h_test.go"},
		TestCommand: "go test ./...",
		Assumptions: []string{"router wired"},
		NonGoals:    []string{},
		Budget:      &budget,
	}
}

// changeDocument builds the change document blob (beta.147 D3) for one or more
// rich tasks, all in a single "1. Fix" section — one thin task per rich task, text
// "add the guard", aligned by the SAME index (mirrors create_change's toChange
// walk: thin task i and rich task i are the same task).
func changeDocument(rich ...changefacts.RichTask) changefacts.ChangeDocument {
	tasks := &openspec.Tasks{Sections: []openspec.TaskSection{{Name: "1. Fix"}}}
	for i := range rich {
		tasks.Sections[0].Tasks = append(tasks.Sections[0].Tasks, openspec.Task{
			Number: fmt.Sprintf("1.%d", i+1),
			Text:   "add the guard",
		})
	}
	return changefacts.ChangeDocument{
		Change:    &openspec.Change{Slug: "demo", Tasks: tasks},
		RichTasks: rich,
	}
}

// documentFact wraps a document as the single openspec.change.document triple
// project_tasks reads via changefacts.HydrateDocument.
func documentFact(t *testing.T, doc changefacts.ChangeDocument) message.Triple {
	t.Helper()
	raw, err := changefacts.MarshalDocument(doc)
	if err != nil {
		t.Fatalf("marshal fixture document: %v", err)
	}
	return message.Triple{Predicate: changefacts.DocumentPredicate, Object: raw, Source: createchange.Source}
}

// run calls the Project core directly against a document holding the given rich
// tasks, and returns the stamped task.spec as a predicate→object map (empty when
// nothing was stamped), plus Project's own return values. Seeds the run's
// validated marker AND demo's current content revision as an EQUAL pair, so the
// fixtures reach the projection logic; the mismatch cases (unvalidated /
// re-authored / alternate) are pinned separately below with their own facts.
func run(t *testing.T, rich []changefacts.RichTask, w *fakeWriter) (map[string]string, int, error) {
	t.Helper()
	facts := []message.Triple{documentFact(t, changeDocument(rich...)), validatedAt(demoRevision), revisionAt(demoRevision)}
	count, err := Project(context.Background(), &fakeReader{facts: facts}, w, slog.Default(), runEntity, "demo")
	idx := map[string]string{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			idx[tr.Predicate] = tr.Object.(string)
		}
	}
	return idx, count, err
}

// Task 3.3 red-first: a task whose test_command runs `go test` but whose target_files
// include NO *_test.go would have the harness measure a test file the developer was never
// given a writable contract to author — the vacuous-green route (measure over a test the
// author couldn't touch). Projection must PARK toward the human and stamp NOTHING (atomic).
func TestProjectParksWhenTargetFilesOmitTest(t *testing.T) {
	w := &fakeWriter{}
	rt := validRichTask(0)
	rt.TargetFiles = []string{"h.go"} // source only, no *_test.go
	_, _, err := run(t, []changefacts.RichTask{rt}, w)
	if err == nil {
		t.Fatal("a go-test task whose target_files omit every *_test.go must park toward the human, not project")
	}
	if !strings.Contains(err.Error(), "test") {
		t.Errorf("the park reason should name the missing test-file contract, got: %v", err)
	}
	if len(w.replaces) != 0 {
		t.Error("projection must stamp NOTHING when the test-file contract is violated (atomic)")
	}
}

// The happy path: an approved change's authored task facts project into the FLAT
// task.spec.<field> on the run entity (beta.147 D1: single-task, index out of the
// predicate), stamped with the task-projector Source.
func TestProjectStampsTaskSpec(t *testing.T) {
	w := &fakeWriter{}
	idx, count, err := run(t, []changefacts.RichTask{validRichTask(0)}, w)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if len(w.replaces) != 1 {
		t.Fatalf("expected one replace, got %d", len(w.replaces))
	}
	want := map[string]string{
		"task.spec.goal":         "add the guard",
		"task.spec.target-files": `["h.go","h_test.go"]`,
		"task.spec.test-command": "go test ./...",
		"task.spec.assumptions":  `["router wired"]`,
		"task.spec.non-goals":    "[]",
		"task.spec.budget":       "3",
	}
	for pred, obj := range want {
		if idx[pred] != obj {
			t.Errorf("%s = %q, want %q", pred, idx[pred], obj)
		}
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Source != Source {
				t.Errorf("task.spec fact Source = %q, want %q (G5)", tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("task.spec fact subject = %q, want run entity (D15)", tr.Subject)
			}
		}
	}
}

// The budget is clamped at projection: an authored 9 stamps as 5.
func TestProjectClampsBudget(t *testing.T) {
	rt := validRichTask(0)
	nine := 9
	rt.Budget = &nine
	idx, _, err := run(t, []changefacts.RichTask{rt}, &fakeWriter{})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if idx["task.spec.budget"] != "5" {
		t.Errorf("budget = %q, want clamped to 5", idx["task.spec.budget"])
	}
}

// Presence preserved on READ: an ABSENT assumptions field (nil, never authored)
// reconstructs a gap and projection fails toward the human — Project surfaces the
// gap and stamps nothing.
func TestProjectAbsentFieldFailsTowardHuman(t *testing.T) {
	w := &fakeWriter{}
	rt := validRichTask(0)
	rt.Assumptions = nil
	_, _, err := run(t, []changefacts.RichTask{rt}, w)
	if err == nil || !strings.Contains(err.Error(), "assumptions") {
		t.Fatalf("absent assumptions must gap (fail toward human), got error %v", err)
	}
	if len(w.replaces) != 0 {
		t.Error("a failed projection must stamp nothing")
	}
}

// A missing test_command likewise fails toward the human (the spec scenario).
func TestProjectMissingTestCommandFailsTowardHuman(t *testing.T) {
	w := &fakeWriter{}
	rt := validRichTask(0)
	rt.TestCommand = ""
	_, _, err := run(t, []changefacts.RichTask{rt}, w)
	if err == nil || !strings.Contains(err.Error(), "test_command") {
		t.Fatalf("missing test_command must fail toward human, got %v", err)
	}
	if len(w.replaces) != 0 {
		t.Error("a failed projection must stamp nothing")
	}
}

// A present authored-empty list (non-nil, zero-length) is NOT a gap — projection
// accepts it (the happy path already carries non_goals empty); prove an
// authored-empty assumptions also passes.
func TestProjectAuthoredEmptyListPasses(t *testing.T) {
	rt := validRichTask(0)
	rt.Assumptions = []string{}
	_, _, err := run(t, []changefacts.RichTask{rt}, &fakeWriter{})
	if err != nil {
		t.Fatalf("authored-empty assumptions must pass, got %v", err)
	}
}

// Immutability: re-projecting onto a run that already carries task.spec is refused
// — the dev loop converges on the facts, it does not redefine them.
func TestProjectRejectsReProjection(t *testing.T) {
	w := &fakeWriter{owned: []string{"task.spec.goal", "task.spec.budget"}}
	_, _, err := run(t, []changefacts.RichTask{validRichTask(0)}, w)
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("re-projection onto existing task.spec must be rejected, got %v", err)
	}
	if len(w.replaces) != 0 {
		t.Error("a rejected re-projection must stamp nothing")
	}
}

// Codex P1 red-first: a run with NO openspec.change.validated marker cannot have
// its task.spec frozen — project_tasks fails closed rather than freeze an
// unvalidated change (nothing stamped). demo's content revision is present but no
// validation blessed it.
func TestProjectRejectsUnvalidatedSlug(t *testing.T) {
	facts := []message.Triple{documentFact(t, changeDocument(validRichTask(0))), revisionAt(demoRevision)}
	w := &fakeWriter{}
	_, err := Project(context.Background(), &fakeReader{facts: facts}, w, slog.Default(), runEntity, "demo")
	if err == nil || !strings.Contains(err.Error(), "validated") {
		t.Fatalf("an unvalidated slug must be refused, got %v", err)
	}
	if len(w.replaces) != 0 {
		t.Error("a refused projection must stamp nothing")
	}
}

// Codex P1 red-first: the run's validated content is "other"'s revision, but demo's
// task facts + a DIFFERENT demo revision are present (a stale/alternate authored
// package). A call for "demo" must be refused — the validated revision does not
// equal demo's current revision — so a later gate cannot prove the wrong work.
func TestProjectRejectsAlternateValidatedSlug(t *testing.T) {
	facts := []message.Triple{
		documentFact(t, changeDocument(validRichTask(0))),
		validatedAt("sha256:other-content"),
		revisionAt(demoRevision),
	}
	w := &fakeWriter{}
	_, err := Project(context.Background(), &fakeReader{facts: facts}, w, slog.Default(), runEntity, "demo")
	if err == nil || !strings.Contains(err.Error(), "validated") {
		t.Fatalf("an alternate validated slug must be refused, got %v", err)
	}
	if len(w.replaces) != 0 {
		t.Error("a refused projection must stamp nothing")
	}
}

// Codex P1 red-first (D15 #0): demo was validated at revision v1, then RE-AUTHORED
// with changed content (revision now v2) but NOT re-validated. openspec.change.validated
// still holds v1 while demo's current revision is v2 — project_tasks must refuse to
// freeze task.spec from the superseded validation, even though the slug "matches".
// This is the stale-same-slug false-green the content-revision binding closes.
func TestProjectRejectsReauthoredUnrevalidatedChange(t *testing.T) {
	facts := []message.Triple{
		documentFact(t, changeDocument(validRichTask(0))),
		validatedAt("sha256:demo-content-v1"), // validation blessed v1
		revisionAt("sha256:demo-content-v2"),  // re-author bumped to v2
	}
	w := &fakeWriter{}
	_, err := Project(context.Background(), &fakeReader{facts: facts}, w, slog.Default(), runEntity, "demo")
	if err == nil || !strings.Contains(err.Error(), "current content") {
		t.Fatalf("a re-authored-since-validation change must be refused, got %v", err)
	}
	if len(w.replaces) != 0 {
		t.Error("a refused projection must stamp nothing")
	}
}

// No change document (the change was not authored, or the slug is wrong) is an
// error, not an empty projection.
func TestProjectNoTaskFactsErrors(t *testing.T) {
	_, _, err := run(t, nil, &fakeWriter{})
	if err == nil {
		t.Fatal("expected an error when no tasks exist in the change document")
	}
}

// beta.147 D1: task.spec is flat-keyed (no per-task index), so a multi-task change
// would silently clobber all but the last spec and mislabel the survivor as task 0.
// project_tasks now fails CLOSED on >1 task — parking toward the human (G2) —
// rather than developing a scrambled, partial task set. This REPLACES the pre-D1
// "multiple tasks project independently" happy path: at M0 single-task, more than
// one authored task is a refusal, not a feature.
func TestProjectRejectsMultipleTasks(t *testing.T) {
	w := &fakeWriter{}
	facts := []message.Triple{
		documentFact(t, changeDocument(validRichTask(0), validRichTask(1))),
		validatedAt(demoRevision),
		revisionAt(demoRevision),
	}
	_, err := Project(context.Background(), &fakeReader{facts: facts}, w, slog.Default(), runEntity, "demo")
	if err == nil {
		t.Fatal("a change authoring more than one task must be refused at M0 (task.spec is single-keyed)")
	}
	if !strings.Contains(err.Error(), "single-keyed") || !strings.Contains(err.Error(), "2 tasks") {
		t.Errorf("the refusal should name the authored task count and the single-keyed reason, got: %v", err)
	}
	if len(w.replaces) != 0 {
		t.Error("a refused multi-task projection must stamp nothing")
	}
}
