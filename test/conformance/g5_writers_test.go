package conformance

import (
	"testing"

	"github.com/c360studio/semdev/internal/tools/applypatch"
	"github.com/c360studio/semdev/internal/tools/checkfloors"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/tools/measuretask"
	"github.com/c360studio/semdev/internal/tools/openpr"
	"github.com/c360studio/semdev/internal/tools/projecttasks"
	"github.com/c360studio/semdev/internal/tools/provisionsandbox"
	"github.com/c360studio/semdev/internal/tools/submitreview"
	"github.com/c360studio/semdev/internal/tools/validatechange"
	"github.com/c360studio/semdev/internal/tools/verifyartifact"
	"github.com/c360studio/semdev/internal/vocab"
)

// G5 — single writer per fact. Every predicate maps to exactly one writer in the
// checked-in table. semspec's audit found four multi-writer facts, one whose
// "sole writer" claim was a false comment; here the property is a census, not a
// comment.
func TestSingleWriterPerPredicate(t *testing.T) {
	if len(vocab.Predicates) == 0 {
		t.Fatal("vocabulary is empty; the writers census would pass vacuously")
	}
	for _, v := range singleWriterViolations(vocab.Predicates) {
		t.Error(v)
	}
	for _, v := range duplicateNameViolations(vocab.Predicates) {
		t.Error(v)
	}
	for _, v := range namespaceWriterViolations(vocab.Predicates) {
		t.Error(v)
	}
}

// G5 verifiability — the Source a fact-writing tool stamps on its triples must
// equal the single writer the vocab declares for that tool's predicate namespace.
// Without this the sole-writer claim is a table string nothing ties to what
// actually lands on the graph — the "sole writer was a false comment" disease.
// One entry per fact-writing tool.
func TestToolSourceMatchesVocabWriter(t *testing.T) {
	cases := []struct {
		tool      string
		source    string
		predicate string // any predicate under the tool's namespace
	}{
		{"create_change", createchange.Source, "openspec.change.document"},
		{"validate_change", validatechange.Source, validatechange.ValidatedPredicate},
		{"project_tasks", projecttasks.Source, "task.spec.goal"},
		{"measure_task", measuretask.Source, "measurement.result.passed"},
		{"submit_review", submitreview.Source, "review.verdict.value"},
		{"submit_review", submitreview.Source, "review.findings.value"},
		{"verify_artifact", verifyartifact.Source, verifyartifact.ResultPredicate},
		{"check_floors", checkfloors.Source, "floor.finding.rejected"},
		// The route mirror (design R1): check_floors + submit_review both stamp the route.*
		// facts onto their firing loop under ONE logical writer route-mirror (a distinct
		// Source from floor-tools / reviewer-quinn, so no predicate gains two writers, G5).
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RoutePassedPredicate},
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RouteRejectedPredicate},
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RouteAttemptPredicate},
		{"submit_review", submitreview.RouteMirrorSource, submitreview.RouteVerdictPredicate},
		{"submit_review", submitreview.RouteMirrorSource, submitreview.RouteAttemptPredicate},
		{"open_pr", openpr.Source, openpr.RefPredicate},
		{"provision_sandbox", provisionsandbox.Source, provisionsandbox.ReadyPredicate},
		{"apply_patch", applypatch.Source, applypatch.CommitPredicate},
	}
	for _, c := range cases {
		writer, ok := vocab.WriterOf(c.predicate)
		if !ok {
			t.Errorf("%s: predicate %q has no vocab writer", c.tool, c.predicate)
			continue
		}
		if c.source != writer {
			t.Errorf("%s stamps Source %q but vocab declares writer %q for %q — G5 unverifiable drift", c.tool, c.source, writer, c.predicate)
		}
	}
}

// Red-first: the namespace census must flag a concrete predicate under a
// declared namespace that declares a different writer.
func TestNamespaceWriterCensusCatchesConflict(t *testing.T) {
	preds := []vocab.Predicate{
		{Name: "openspec.change.*", Writer: "author-tool", Capability: "openspec-io", IntroducedBy: "m0-walking-skeleton-spine"},
		{Name: "openspec.change.title", Writer: "some-other-writer", Capability: "openspec-io", IntroducedBy: "m0-walking-skeleton-spine"},
	}
	if len(namespaceWriterViolations(preds)) == 0 {
		t.Error("census passed a namespace member with a conflicting writer; G5 namespace pin does not fire")
	}
}

// Red-first: the census must catch a predicate stamped by two writers — the
// exact shape that bred semspec's wedge families — and a writer-less predicate.
func TestSingleWriterCensusCatchesViolations(t *testing.T) {
	multiWriter := []vocab.Predicate{
		{Name: "x.y", Writer: "alpha", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"},
		{Name: "x.y", Writer: "beta", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"},
	}
	if len(singleWriterViolations(multiWriter)) == 0 {
		t.Error("census passed a predicate with two writers; G5 pin does not fire")
	}
	noWriter := []vocab.Predicate{{Name: "x.y", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"}}
	if len(singleWriterViolations(noWriter)) == 0 {
		t.Error("census passed a writer-less predicate; G5 pin does not fire")
	}
}
