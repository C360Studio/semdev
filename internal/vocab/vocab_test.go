package vocab

import "testing"

func TestWriterOfExactMatch(t *testing.T) {
	w, ok := WriterOf("run.issue_ref")
	if !ok || w != "issue-intake-adapter" {
		t.Fatalf("WriterOf(run.issue_ref) = %q, %v; want issue-intake-adapter, true", w, ok)
	}
}

// A concrete predicate under the openspec.change.* namespace resolves to the
// namespace's single writer, not false — the latent bug before namespace
// awareness.
func TestWriterOfNamespaceMember(t *testing.T) {
	w, ok := WriterOf("openspec.change.proposal")
	if !ok {
		t.Fatal("WriterOf(openspec.change.proposal) did not resolve; namespace member should map to the namespace writer")
	}
	if w != "create-change-author-tool" {
		t.Fatalf("namespace member writer = %q; want create-change-author-tool", w)
	}
}

func TestWriterOfUnknown(t *testing.T) {
	if w, ok := WriterOf("nope.nothing"); ok {
		t.Errorf("WriterOf(nope.nothing) resolved to %q; want not found", w)
	}
}
