package validatechange

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/vocab"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeReader serves the triples create_change would have stamped, honoring the
// scoping prefix.
type fakeReader struct{ triples []message.Triple }

func (f *fakeReader) ReadFacts(_ context.Context, _ string, prefix string) ([]message.Triple, error) {
	var out []message.Triple
	for _, t := range f.triples {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

// fakeRunner scripts one CLI result and records the invocation.
type fakeRunner struct {
	res   cliexec.Result
	err   error
	calls int
	name  string
	args  []string
	dir   string
}

func (r *fakeRunner) Run(_ context.Context, dir, name string, args ...string) (cliexec.Result, error) {
	r.calls++
	r.dir, r.name, r.args = dir, name, args
	return r.res, r.err
}

// fakeWriter records replace-by-predicate mutations (the stamp/clear of the marker).
type fakeWriter struct{ replaces []replaceCall }

type replaceCall struct {
	add    []message.Triple
	remove []string
}

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, remove []string) error {
	w.replaces = append(w.replaces, replaceCall{add: add, remove: remove})
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _ string, _ string) ([]string, error) {
	return nil, nil
}

func stamped(runEntityID string, c *openspec.Change) []message.Triple {
	var out []message.Triple
	for _, f := range c.Facts() {
		out = append(out, message.Triple{Subject: runEntityID, Predicate: f.Predicate, Object: f.Object})
	}
	return out
}

func sampleChange() *openspec.Change {
	return &openspec.Change{
		Slug:     "fix-null-deref",
		Proposal: &openspec.Proposal{Intent: "fix the crash"},
		Deltas: []openspec.Delta{{
			Capability: "handler",
			Added: []openspec.Requirement{{
				Name:      "Nil guard",
				Statement: "The system SHALL guard nil input.",
				Scenarios: []openspec.Scenario{{Name: "Nil", Steps: []openspec.Step{{Keyword: "WHEN", Text: "input is nil"}, {Keyword: "THEN", Text: "no panic"}}}},
			}},
		}},
		Tasks: &openspec.Tasks{Sections: []openspec.TaskSection{{Name: "1. Fix", Tasks: []openspec.Task{{Number: "1.1", Text: "add the guard"}}}}},
	}
}

func call(slug string) agentic.ToolCall {
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{"slug": slug},
	}
}

func newExec(reader *fakeReader, runner *fakeRunner, writer *fakeWriter) *Executor {
	return New(reader, runner, writer, nil)
}

// On a CLI pass (exit 0) the harness stamps openspec.validated=<slug> on the run
// entity with the vocab writer Source, and it shells the exact non-interactive
// invocation. The model supplied no verdict.
func TestValidatePassStampsMarker(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 0, Stdout: `{"valid":true}`}}
	w := &fakeWriter{}
	res, err := newExec(r, runner, w).Execute(context.Background(), call("fix-null-deref"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}

	// Invocation shape: openspec validate <slug> --strict --json --no-interactive.
	if runner.name != "openspec" {
		t.Errorf("shelled %q, want openspec", runner.name)
	}
	wantArgs := []string{"validate", "fix-null-deref", "--strict", "--json", "--no-interactive"}
	if strings.Join(runner.args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", runner.args, wantArgs)
	}

	// Exactly one stamp of openspec.validated on the run, Source == vocab writer.
	if len(w.replaces) != 1 || len(w.replaces[0].add) != 1 {
		t.Fatalf("want one stamp mutation with one triple, got %+v", w.replaces)
	}
	tr := w.replaces[0].add[0]
	if tr.Subject != runEntity || tr.Predicate != ValidatedPredicate {
		t.Errorf("stamped %s=%v on %s, want %s on the run", tr.Predicate, tr.Object, tr.Subject, ValidatedPredicate)
	}
	writer, _ := vocab.WriterOf(ValidatedPredicate)
	if tr.Source != Source || Source != writer {
		t.Errorf("Source %q must equal the vocab writer %q for %s (G5)", tr.Source, writer, ValidatedPredicate)
	}
	if strings.Contains(res.Content, "false") {
		t.Errorf("pass result should not report validated:false: %s", res.Content)
	}
}

// On a CLI failure (non-zero exit) the harness stamps NO pass marker — it clears
// any stale one — and returns the validator's own issues for correction. An
// invalid change thus cannot reach the approval gate (marker absent).
func TestValidateFailClearsMarkerAndReturnsIssues(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	issues := `{"valid":false,"issues":["missing scenario"]}`
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 1, Stdout: issues}}
	w := &fakeWriter{}
	res, err := newExec(r, runner, w).Execute(context.Background(), call("fix-null-deref"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("a failed validation is a verdict, not a tool error: %s", res.Error)
	}
	if len(w.replaces) != 1 || len(w.replaces[0].add) != 0 || len(w.replaces[0].remove) != 1 || w.replaces[0].remove[0] != ValidatedPredicate {
		t.Fatalf("fail must clear (remove) the marker, not stamp it: %+v", w.replaces)
	}
	if !strings.Contains(res.Content, "missing scenario") {
		t.Errorf("failure result should carry the validator's issues, got: %s", res.Content)
	}
}

// If the oracle cannot be run at all (binary missing / timeout), it's a transport
// failure — no verdict is recorded (neither stamp nor clear).
func TestValidateRunnerErrorRecordsNothing(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	runner := &fakeRunner{err: exec.ErrNotFound}
	w := &fakeWriter{}
	res, _ := newExec(r, runner, w).Execute(context.Background(), call("fix-null-deref"))
	if res.Error == "" {
		t.Error("expected a transport error when the CLI cannot be run")
	}
	if res.ErrorKind != agentic.ToolErrorNetwork {
		t.Errorf("runner-run failure kind = %v, want network (retryable)", res.ErrorKind)
	}
	if len(w.replaces) != 0 {
		t.Errorf("a run failure must record no verdict, got %+v", w.replaces)
	}
}

// An un-authored/misnamed slug hydrates empty and is rejected before the CLI runs.
func TestValidateFailsOnEmptyChange(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	runner := &fakeRunner{}
	res, _ := newExec(r, runner, &fakeWriter{}).Execute(context.Background(), call("never-authored"))
	if res.Error == "" {
		t.Error("expected an error validating a change with no facts")
	}
	if runner.calls != 0 {
		t.Error("the CLI must not run for an empty change")
	}
}

func TestValidateRejectsUnsafeSlug(t *testing.T) {
	runner := &fakeRunner{}
	res, _ := newExec(&fakeReader{}, runner, &fakeWriter{}).Execute(context.Background(), call("../../etc"))
	if res.Error == "" {
		t.Error("expected a rejection for a traversal slug")
	}
	if runner.calls != 0 {
		t.Error("the CLI must not run for an unsafe slug")
	}
}

func TestValidateFailsWithoutRunEntity(t *testing.T) {
	c := call("fix-null-deref")
	c.Metadata = nil
	res, _ := newExec(&fakeReader{}, &fakeRunner{}, &fakeWriter{}).Execute(context.Background(), c)
	if res.Error == "" {
		t.Error("expected an error when agent.run_entity_id is missing")
	}
}

func TestValidateFailsWhenNotWired(t *testing.T) {
	if res, _ := New(nil, &fakeRunner{}, &fakeWriter{}, nil).Execute(context.Background(), call("x")); res.Error == "" {
		t.Error("expected an error with a nil reader")
	}
	if res, _ := New(&fakeReader{}, nil, &fakeWriter{}, nil).Execute(context.Background(), call("x")); res.Error == "" {
		t.Error("expected an error with a nil runner")
	}
	if res, _ := New(&fakeReader{}, &fakeRunner{}, nil, nil).Execute(context.Background(), call("x")); res.Error == "" {
		t.Error("expected an error with a nil writer")
	}
}

// The schema advertises content-only input — slug, no outcome/valid/pass field (G3).
func TestSchemaHasNoOutcomeField(t *testing.T) {
	defs := New(nil, nil, nil, nil).ListTools()
	if len(defs) != 1 || defs[0].Name != ToolName {
		t.Fatalf("want one tool %q, got %+v", ToolName, defs)
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if _, ok := props["slug"]; !ok {
		t.Error("schema is missing the slug property")
	}
	for _, forbidden := range []string{"valid", "validated", "pass", "passed", "outcome", "success", "exit_code", "result"} {
		if _, ok := props[forbidden]; ok {
			t.Errorf("schema exposes forbidden outcome field %q (G3)", forbidden)
		}
	}
}

// End-to-end against the REAL OpenSpec CLI (skipped if not installed): a valid
// hydrated change passes the oracle and the harness stamps openspec.validated.
func TestValidateEndToEndWithRealCLI(t *testing.T) {
	if _, err := exec.LookPath("openspec"); err != nil {
		t.Skip("openspec CLI not on PATH; skipping the real-oracle e2e")
	}
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	w := &fakeWriter{}
	res, err := New(r, cliexec.OSRunner{}, w, nil).Execute(context.Background(), call("fix-null-deref"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("real-CLI validation errored: %s", res.Error)
	}
	if len(w.replaces) != 1 || len(w.replaces[0].add) != 1 || w.replaces[0].add[0].Predicate != ValidatedPredicate {
		t.Fatalf("a valid change should stamp openspec.validated via the real CLI, got %+v", w.replaces)
	}
}
