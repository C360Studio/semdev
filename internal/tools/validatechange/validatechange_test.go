package validatechange

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/vocab"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
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
	add      []message.Triple
	contract string
	group    string
}

func (w *fakeWriter) Reconcile(_ context.Context, m projection.ReconcileMutation) (projection.MutationReceipt, error) {
	// The explicit remove list is gone: ReplaceOwned clears the owner's whole
	// replace-owned group and re-adds Desired, so a clear is Desired == nil
	// (migrate-beta159 D3a).
	w.replaces = append(w.replaces, replaceCall{add: m.Desired, contract: m.Contract, group: m.Group})
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// writerFor wraps the fake in the owner-bound seam the production code takes, so
// every test exercises graphown.ContractFor for real — the behavioral proof that
// this owner's predicates are classed onto the entity class it actually writes.
func writerFor(w *fakeWriter) *graphown.Writer { return graphown.NewWriter(Source, w) }

// stamped returns the triples create_change would have written for a change
// (beta.147 D3): the whole change serialized into the ONE document blob
// changefacts.Hydrate reads, plus the flat content revision (beta.147 D1 — no
// longer slug-scoped) Validate reads and echoes into openspec.change.validated.
func stamped(runEntityID string, c *openspec.Change) []message.Triple {
	doc := changefacts.ChangeDocument{Change: c}
	raw, err := changefacts.MarshalDocument(doc)
	if err != nil {
		panic("marshal fixture document: " + err.Error()) // test fixture construction only
	}
	return []message.Triple{
		{Subject: runEntityID, Predicate: changefacts.DocumentPredicate, Object: raw},
		{Subject: runEntityID, Predicate: createchange.RevisionPredicate, Object: sampleRevision},
	}
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
	res, err := Validate(context.Background(), r, runner, writerFor(w), runEntity, "fix-null-deref")
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
	res, err := Validate(context.Background(), r, runner, writerFor(w), runEntity, "fix-null-deref")
	if err != nil {
		t.Fatalf("a failed validation is a verdict, not an error: %v", err)
	}
	if res.Validated {
		t.Fatalf("want Validated false on a CLI failure, got %+v", res)
	}
	// Clearing the marker is now an EMPTY Desired: ReplaceOwned removes the owner's
	// whole replace-owned group — which for openspec-validate-harness is exactly
	// {openspec.change.validated} — and re-adds nothing (migrate-beta159 D3a). The
	// CONTRACT assertion is what makes "exactly that group" checkable: an empty
	// Desired under any other contract would clear a different owner's package.
	if len(w.replaces) != 1 || len(w.replaces[0].add) != 0 {
		t.Fatalf("fail must clear the marker (one write, empty Desired), not stamp it: %+v", w.replaces)
	}
	if got := w.replaces[0]; got.contract != Source || got.group != graphown.OwnedGroup {
		t.Errorf("the clear targeted contract %q group %q, want %q/%q — an empty Desired under another owner's contract wipes THAT owner's package", got.contract, got.group, Source, graphown.OwnedGroup)
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
	_, err := Validate(context.Background(), r, runner, writerFor(w), runEntity, "fix-null-deref")
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
// not stamp a content-unbound marker, since a bare marker would reopen the
// stale-same-slug false-green. create_change always stamps the revision, so an
// absent one is an authoring/ordering gap.
func TestValidateFailsWithoutRevision(t *testing.T) {
	// Seed the change document but strip the flat revision fact Validate reads
	// (beta.147 D1: the revision is run-level, no longer slug-scoped).
	var content []message.Triple
	for _, tr := range stamped(runEntity, sampleChange()) {
		if tr.Predicate != createchange.RevisionPredicate {
			content = append(content, tr)
		}
	}
	r := &fakeReader{triples: content}
	runner := &fakeRunner{}
	_, err := Validate(context.Background(), r, runner, writerFor(&fakeWriter{}), runEntity, "fix-null-deref")
	if err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("a change with no content revision must be refused, got %v", err)
	}
	if runner.calls != 0 {
		t.Error("the CLI must not run when the change carries no content revision")
	}
}

// A run with NO authored change document at all hydrates empty and is rejected
// before the CLI runs. Under beta.147 D3 (single flat document at M0) the document
// is no longer slug-scoped — Hydrate reads by fixed predicate, ignoring the slug
// argument for the read itself — so an "empty change" fixture must omit the
// document fact entirely, not merely address it under a different slug (the
// pre-D3 "misnamed slug" case has no analogue at single-change M0).
func TestValidateFailsOnEmptyChange(t *testing.T) {
	r := &fakeReader{} // nothing authored — no openspec.change.document fact at all
	runner := &fakeRunner{}
	_, err := Validate(context.Background(), r, runner, writerFor(&fakeWriter{}), runEntity, "never-authored")
	if err == nil {
		t.Error("expected an error validating a run with no authored change")
	}
	if runner.calls != 0 {
		t.Error("the CLI must not run for an empty change")
	}
}

func TestValidateRejectsUnsafeSlug(t *testing.T) {
	runner := &fakeRunner{}
	_, err := Validate(context.Background(), &fakeReader{}, runner, writerFor(&fakeWriter{}), runEntity, "../../etc")
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
	res, err := Validate(context.Background(), r, cliexec.OSRunner{}, writerFor(w), runEntity, "fix-null-deref")
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
