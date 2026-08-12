package vocab

import "testing"

func TestWriterOfExactMatch(t *testing.T) {
	// run.issue.ref's writer MOVED to the rule pack (forge-io-real-lanes D2
	// as-built, per the mint rule's recorded deferred_issue_ref plan): the run
	// does not exist at wake time, so the coordinator-pack issue-ref rule — not
	// the intake component — stamps it.
	w, ok := WriterOf("run.issue.ref")
	if !ok || w != "issue-ref-rule" {
		t.Fatalf("WriterOf(run.issue.ref) = %q, %v; want issue-ref-rule, true", w, ok)
	}
}

// The brownfield living-spec tree is now the single concrete blob predicate
// openspec.spec.document (beta.150 flatten — the last ".*" namespace was retired), so it
// resolves by EXACT match to its writer. The old sub-field lookup (openspec.spec.auth.title)
// no longer resolves — there is no such predicate anymore.
func TestWriterOfSpecDocumentIsConcrete(t *testing.T) {
	w, ok := WriterOf("openspec.spec.document")
	if !ok || w != "brownfield-spec-projector" {
		t.Fatalf("WriterOf(openspec.spec.document) = %q, %v; want brownfield-spec-projector, true", w, ok)
	}
	if _, ok := WriterOf("openspec.spec.auth.title"); ok {
		t.Error("openspec.spec.auth.title resolved — the retired namespace fallback is still live (should be exact-match only)")
	}
}

func TestWriterOfUnknown(t *testing.T) {
	if w, ok := WriterOf("nope.nothing"); ok {
		t.Errorf("WriterOf(nope.nothing) resolved to %q; want not found", w)
	}
}
