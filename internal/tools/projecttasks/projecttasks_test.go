package projecttasks

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/tools/validatechange"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

// demoRevision is the content revision create_change would stamp for "demo".
const demoRevision = "sha256:demo-content-v1"

// validatedAt is the run's openspec.validated marker — its value is the content
// REVISION the validator blessed (D15 #0), not the slug. project_tasks freezes
// task.spec only when this equals the slug's current content revision.
func validatedAt(rev string) message.Triple {
	return message.Triple{Predicate: validatechange.ValidatedPredicate, Object: rev, Source: validatechange.Source}
}

// slugRevisionAt is the change's current content revision (openspec.change.<slug>.
// revision) create_change stamps; project_tasks binds openspec.validated to it.
func slugRevisionAt(slug, rev string) message.Triple {
	return message.Triple{Predicate: createchange.SlugRevisionPredicate(slug), Object: rev, Source: createchange.Source}
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

func callFor(slug string) agentic.ToolCall {
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{"slug": slug},
	}
}

func changeFact(i int, field, obj string) message.Triple {
	return message.Triple{
		Predicate: fmt.Sprintf("openspec.change.demo.task.%d.%s", i, field),
		Object:    obj,
		Source:    "create-change-author-tool",
	}
}

// validTaskFacts is one fully-authored task's change facts (thin text + the five
// rich fields), so a test can drop one field and prove the gap in isolation.
func validTaskFacts(i int) []message.Triple {
	return []message.Triple{
		changeFact(i, "text", "add the guard"),
		changeFact(i, "target_files", `["h.go"]`),
		changeFact(i, "test_command", "go test ./..."),
		changeFact(i, "assumptions", `["router wired"]`),
		changeFact(i, "non_goals", "[]"),
		changeFact(i, "budget", "3"),
	}
}

func withoutField(facts []message.Triple, field string) []message.Triple {
	suffix := "." + field
	var out []message.Triple
	for _, f := range facts {
		if !strings.HasSuffix(f.Predicate, suffix) {
			out = append(out, f)
		}
	}
	return out
}

func withField(facts []message.Triple, i int, field, obj string) []message.Triple {
	return append(withoutField(facts, field), changeFact(i, field, obj))
}

// run executes the tool and returns the stamped task.spec as a predicate→object
// map (empty when nothing was stamped) plus the tool result.
func run(t *testing.T, facts []message.Triple, w *fakeWriter) (map[string]string, agentic.ToolResult) {
	t.Helper()
	// Seed the run's validated marker AND demo's current content revision as an
	// EQUAL pair, so the fixtures reach the projection logic; the mismatch cases
	// (unvalidated / re-authored / alternate) are pinned separately below.
	facts = append(facts, validatedAt(demoRevision), slugRevisionAt("demo", demoRevision))
	res, err := New(&fakeReader{facts: facts}, w, nil).Execute(context.Background(), callFor("demo"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	idx := map[string]string{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			idx[tr.Predicate] = tr.Object.(string)
		}
	}
	return idx, res
}

// The happy path: an approved change's authored task facts project into
// task.spec.<i>.<field> on the run entity, stamped with the task-projector Source.
func TestProjectStampsTaskSpec(t *testing.T) {
	w := &fakeWriter{}
	idx, res := run(t, validTaskFacts(0), w)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if !res.StopLoop {
		t.Error("projection result must set StopLoop — projection is single-shot; without it the forced-function loop takes another turn and re-refuses the now-immutable spec")
	}
	if len(w.replaces) != 1 {
		t.Fatalf("expected one replace, got %d", len(w.replaces))
	}
	want := map[string]string{
		"task.spec.0.goal":         "add the guard",
		"task.spec.0.target_files": `["h.go"]`,
		"task.spec.0.test_command": "go test ./...",
		"task.spec.0.assumptions":  `["router wired"]`,
		"task.spec.0.non_goals":    "[]",
		"task.spec.0.budget":       "3",
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
	idx, res := run(t, withField(validTaskFacts(0), 0, "budget", "9"), &fakeWriter{})
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if idx["task.spec.0.budget"] != "5" {
		t.Errorf("budget = %q, want clamped to 5", idx["task.spec.0.budget"])
	}
}

// Presence preserved on READ: an ABSENT assumptions fact reconstructs a nil field
// (a gap) and projection fails toward the human — the tool surfaces the gap and
// stamps nothing.
func TestProjectAbsentFieldFailsTowardHuman(t *testing.T) {
	w := &fakeWriter{}
	_, res := run(t, withoutField(validTaskFacts(0), "assumptions"), w)
	if res.Error == "" || !strings.Contains(res.Error, "assumptions") {
		t.Fatalf("absent assumptions must gap (fail toward human), got error %q", res.Error)
	}
	if len(w.replaces) != 0 {
		t.Error("a failed projection must stamp nothing")
	}
}

// A missing test_command likewise fails toward the human (the spec scenario).
func TestProjectMissingTestCommandFailsTowardHuman(t *testing.T) {
	w := &fakeWriter{}
	_, res := run(t, withoutField(validTaskFacts(0), "test_command"), w)
	if res.Error == "" || !strings.Contains(res.Error, "test_command") {
		t.Fatalf("missing test_command must fail toward human, got %q", res.Error)
	}
	if len(w.replaces) != 0 {
		t.Error("a failed projection must stamp nothing")
	}
}

// A present authored-empty list ("[]") is NOT a gap — projection accepts it (the
// happy path already carries non_goals "[]"); prove an authored-empty assumptions
// also passes.
func TestProjectAuthoredEmptyListPasses(t *testing.T) {
	_, res := run(t, withField(validTaskFacts(0), 0, "assumptions", "[]"), &fakeWriter{})
	if res.Error != "" {
		t.Fatalf("authored-empty assumptions must pass, got %q", res.Error)
	}
}

// Immutability: re-projecting onto a run that already carries task.spec is refused
// — the dev loop converges on the facts, it does not redefine them.
func TestProjectRejectsReProjection(t *testing.T) {
	w := &fakeWriter{owned: []string{"task.spec.0.goal", "task.spec.0.budget"}}
	_, res := run(t, validTaskFacts(0), w)
	if res.Error == "" || !strings.Contains(res.Error, "immutable") {
		t.Fatalf("re-projection onto existing task.spec must be rejected, got %q", res.Error)
	}
	if len(w.replaces) != 0 {
		t.Error("a rejected re-projection must stamp nothing")
	}
}

// Codex P1 red-first: a run with NO openspec.validated marker cannot have its
// task.spec frozen — project_tasks fails closed rather than freeze an unvalidated
// change (nothing stamped). demo's content revision is present but no validation
// blessed it.
func TestProjectRejectsUnvalidatedSlug(t *testing.T) {
	facts := append(validTaskFacts(0), slugRevisionAt("demo", demoRevision))
	w := &fakeWriter{}
	res, err := New(&fakeReader{facts: facts}, w, nil).Execute(context.Background(), callFor("demo"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "validated") {
		t.Fatalf("an unvalidated slug must be refused, got %q", res.Error)
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
	facts := append(validTaskFacts(0), validatedAt("sha256:other-content"), slugRevisionAt("demo", demoRevision))
	w := &fakeWriter{}
	res, err := New(&fakeReader{facts: facts}, w, nil).Execute(context.Background(), callFor("demo"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "validated") {
		t.Fatalf("an alternate validated slug must be refused, got %q", res.Error)
	}
	if len(w.replaces) != 0 {
		t.Error("a refused projection must stamp nothing")
	}
}

// Codex P1 red-first (D15 #0): demo was validated at revision v1, then RE-AUTHORED
// with changed content (revision now v2) but NOT re-validated. openspec.validated
// still holds v1 while demo's current revision is v2 — project_tasks must refuse to
// freeze task.spec from the superseded validation, even though the slug "matches".
// This is the stale-same-slug false-green the content-revision binding closes.
func TestProjectRejectsReauthoredUnrevalidatedChange(t *testing.T) {
	facts := append(validTaskFacts(0),
		validatedAt("sha256:demo-content-v1"),            // validation blessed v1
		slugRevisionAt("demo", "sha256:demo-content-v2"), // re-author bumped to v2
	)
	w := &fakeWriter{}
	res, err := New(&fakeReader{facts: facts}, w, nil).Execute(context.Background(), callFor("demo"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "current content") {
		t.Fatalf("a re-authored-since-validation change must be refused, got %q", res.Error)
	}
	if len(w.replaces) != 0 {
		t.Error("a refused projection must stamp nothing")
	}
}

// No task facts (the change was not authored, or the slug is wrong) is an error,
// not an empty projection.
func TestProjectNoTaskFactsErrors(t *testing.T) {
	_, res := run(t, nil, &fakeWriter{})
	if res.Error == "" {
		t.Fatal("expected an error when no task facts exist")
	}
}

// Multiple tasks project in index order, each frozen into its own task.spec.<i>.
func TestProjectMultipleTasks(t *testing.T) {
	facts := append(validTaskFacts(0), validTaskFacts(1)...)
	idx, res := run(t, facts, &fakeWriter{})
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if idx["task.spec.0.goal"] == "" || idx["task.spec.1.goal"] == "" {
		t.Errorf("both tasks must project: %v", idx)
	}
}

// Schema-only registration (nil reader/writer) fails loudly if executed, never
// silently drops the projection.
func TestProjectFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, nil).Execute(context.Background(), callFor("demo"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness projection must fail loudly")
	}
}
