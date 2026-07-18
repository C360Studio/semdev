package vocab

import "testing"

func TestWriterOfExactMatch(t *testing.T) {
	w, ok := WriterOf("run.issue.ref")
	if !ok || w != "issue-intake-adapter" {
		t.Fatalf("WriterOf(run.issue.ref) = %q, %v; want issue-intake-adapter, true", w, ok)
	}
}

// A concrete predicate under the openspec.spec.* namespace (the deferred brownfield
// living-spec tree, the one remaining ".*" census entry at M0) resolves to the
// namespace's single writer, not false — the latent bug before namespace awareness.
func TestWriterOfNamespaceMember(t *testing.T) {
	w, ok := WriterOf("openspec.spec.auth.title")
	if !ok {
		t.Fatal("WriterOf(openspec.spec.auth.title) did not resolve; namespace member should map to the namespace writer")
	}
	if w != "brownfield-spec-projector" {
		t.Fatalf("namespace member writer = %q; want brownfield-spec-projector", w)
	}
}

func TestWriterOfUnknown(t *testing.T) {
	if w, ok := WriterOf("nope.nothing"); ok {
		t.Errorf("WriterOf(nope.nothing) resolved to %q; want not found", w)
	}
}
