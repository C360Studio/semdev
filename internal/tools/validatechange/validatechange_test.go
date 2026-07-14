package validatechange

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/vocab"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// sampleRevision is the content revision create_change would have stamped at
// openspec.change.revision. Validate echoes it into openspec.validated (it does
// not recompute), so any sentinel serves — the value is opaque here.
const sampleRevision = "sha256:0123456789abcdef"

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

// stamped returns the triples create_change would have written for a change: its
// content facts plus the slug-scoped content revision Validate reads and echoes
// into openspec.validated.
func stamped(runEntityID string, c *openspec.Change) []message.Triple {
	var out []message.Triple
	for _, f := range c.Facts() {
		out = append(out, message.Triple{Subject: runEntityID, Predicate: f.Predicate, Object: f.Object})
	}
	out = append(out, message.Triple{Subject: runEntityID, Predicate: createchange.SlugRevisionPredicate(c.Slug), Object: sampleRevision})
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

// On a CLI pass (exit 0) Validate stamps openspec.validated=<content revision> on
// the run entity with the vocab writer Source, and it shells the exact
// non-interactive invocation. The marker's VALUE is the revision create_change
// stamped (echoed, not recomputed) — not the slug — so it binds to the exact
// content the CLI blessed (D15 #0).
func TestValidatePassStampsMarker(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 0, Stdout: `{"valid":true}`}}
	w := &fakeWriter{}
	res, err := Validate(context.Background(), r, runner, w, runEntity, "fix-null-deref")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !res.Validated {
		t.Fatalf("want Validated true on a CLI pass, got %+v", res)
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
	// The marker binds to the content revision, not the slug (D15 #0).
	if tr.Object != sampleRevision {
		t.Errorf("openspec.validated = %q, want the content revision %q (not the slug)", tr.Object, sampleRevision)
	}
	if res.Revision != sampleRevision {
		t.Errorf("Result.Revision = %q, want %q", res.Revision, sampleRevision)
	}
	writer, _ := vocab.WriterOf(ValidatedPredicate)
	if tr.Source != Source || Source != writer {
		t.Errorf("Source %q must equal the vocab writer %q for %s (G5)", tr.Source, writer, ValidatedPredicate)
	}
}

// On a CLI failure (non-zero exit) Validate stamps NO pass marker — it clears any
// stale one — and returns the validator's own issues for correction. An invalid
// change thus cannot reach the approval gate (marker absent).
func TestValidateFailClearsMarkerAndReturnsIssues(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	issues := `{"valid":false,"issues":["missing scenario"]}`
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 1, Stdout: issues}}
	w := &fakeWriter{}
	res, err := Validate(context.Background(), r, runner, w, runEntity, "fix-null-deref")
	if err != nil {
		t.Fatalf("a failed validation is a verdict, not an error: %v", err)
	}
	if res.Validated {
		t.Fatalf("want Validated false on a CLI failure, got %+v", res)
	}
	if len(w.replaces) != 1 || len(w.replaces[0].add) != 0 || len(w.replaces[0].remove) != 1 || w.replaces[0].remove[0] != ValidatedPredicate {
		t.Fatalf("fail must clear (remove) the marker, not stamp it: %+v", w.replaces)
	}
	if !strings.Contains(res.Issues, "missing scenario") {
		t.Errorf("failure result should carry the validator's issues, got: %s", res.Issues)
	}
}

// If the oracle cannot be run at all (binary missing / timeout), it's a transport
// failure — no verdict is recorded (neither stamp nor clear) — and the error wraps
// ErrOracleUnrunnable so callers can classify it as retryable.
func TestValidateRunnerErrorRecordsNothing(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	runner := &fakeRunner{err: exec.ErrNotFound}
	w := &fakeWriter{}
	_, err := Validate(context.Background(), r, runner, w, runEntity, "fix-null-deref")
	if err == nil {
		t.Fatal("expected a transport error when the CLI cannot be run")
	}
	if !errors.Is(err, ErrOracleUnrunnable) {
		t.Errorf("runner-run failure must wrap ErrOracleUnrunnable (retryable), got: %v", err)
	}
	if len(w.replaces) != 0 {
		t.Errorf("a run failure must record no verdict, got %+v", w.replaces)
	}
}

// D15 #0 red-first: a change present on the run but carrying NO content revision
// (openspec.change.revision absent) is refused before the CLI runs — Validate will
// not stamp a content-unbound marker, since a bare-slug marker would reopen the
// stale-same-slug false-green. create_change always stamps the revision, so an
// absent one is an authoring/ordering gap.
func TestValidateFailsWithoutRevision(t *testing.T) {
	// Seed the change CONTENT facts but strip the slug-scoped revision fact
	// Validate reads (run-level may remain — validate binds to the slug's own).
	slugRev := createchange.SlugRevisionPredicate(sampleChange().Slug)
	var content []message.Triple
	for _, tr := range stamped(runEntity, sampleChange()) {
		if tr.Predicate != slugRev {
			content = append(content, tr)
		}
	}
	r := &fakeReader{triples: content}
	runner := &fakeRunner{}
	_, err := Validate(context.Background(), r, runner, &fakeWriter{}, runEntity, "fix-null-deref")
	if err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("a change with no content revision must be refused, got %v", err)
	}
	if runner.calls != 0 {
		t.Error("the CLI must not run when the change carries no content revision")
	}
}

// An un-authored/misnamed slug hydrates empty and is rejected before the CLI runs.
func TestValidateFailsOnEmptyChange(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	runner := &fakeRunner{}
	_, err := Validate(context.Background(), r, runner, &fakeWriter{}, runEntity, "never-authored")
	if err == nil {
		t.Error("expected an error validating a change with no facts")
	}
	if runner.calls != 0 {
		t.Error("the CLI must not run for an empty change")
	}
}

func TestValidateRejectsUnsafeSlug(t *testing.T) {
	runner := &fakeRunner{}
	_, err := Validate(context.Background(), &fakeReader{}, runner, &fakeWriter{}, runEntity, "../../etc")
	if err == nil {
		t.Error("expected a rejection for a traversal slug")
	}
	if runner.calls != 0 {
		t.Error("the CLI must not run for an unsafe slug")
	}
}

// End-to-end against the REAL OpenSpec CLI (skipped if not installed): a valid
// hydrated change passes the oracle and Validate stamps openspec.validated.
func TestValidateEndToEndWithRealCLI(t *testing.T) {
	if _, err := exec.LookPath("openspec"); err != nil {
		t.Skip("openspec CLI not on PATH; skipping the real-oracle e2e")
	}
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	w := &fakeWriter{}
	res, err := Validate(context.Background(), r, cliexec.OSRunner{}, w, runEntity, "fix-null-deref")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !res.Validated {
		t.Fatalf("real-CLI validation should pass a valid change, got %+v", res)
	}
	if len(w.replaces) != 1 || len(w.replaces[0].add) != 1 || w.replaces[0].add[0].Predicate != ValidatedPredicate {
		t.Fatalf("a valid change should stamp openspec.validated via the real CLI, got %+v", w.replaces)
	}
}
