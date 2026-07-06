package changefacts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeReader serves a fixed triple set (as the graph would after create_change
// stamped it) and records the scoping prefix Hydrate asked for.
type fakeReader struct {
	triples   []message.Triple
	err       error
	gotPrefix string
}

// ReadFacts honors the prefix via the production filterByPrefix, exactly as the
// NATS reader does — so a test that relies on scoping proves the scoping, not
// ChangeFromFacts's downstream slug filter.
func (f *fakeReader) ReadFacts(_ context.Context, _ string, prefix string) ([]message.Triple, error) {
	f.gotPrefix = prefix
	if f.err != nil {
		return nil, f.err
	}
	return filterByPrefix(f.triples, prefix), nil
}

// stampFacts projects a change to the triples create_change would have written on
// the run entity, so a hydrate round-trip is exercised end-to-end without NATS.
func stampFacts(runEntityID string, c *openspec.Change) []message.Triple {
	var out []message.Triple
	for _, f := range c.Facts() {
		out = append(out, message.Triple{Subject: runEntityID, Predicate: f.Predicate, Object: f.Object})
	}
	return out
}

func sampleChange() *openspec.Change {
	return &openspec.Change{
		Slug:     "fix-null-deref",
		Proposal: &openspec.Proposal{Intent: "fix the crash", ScopeIn: []string{"the handler"}},
		Deltas: []openspec.Delta{{
			Capability: "handler",
			Added: []openspec.Requirement{{
				Name:      "Nil guard",
				Statement: "The system SHALL guard nil input.",
				Scenarios: []openspec.Scenario{{
					Name:  "Nil input",
					Steps: []openspec.Step{{Keyword: "WHEN", Text: "input is nil"}, {Keyword: "THEN", Text: "no panic occurs"}},
				}},
			}},
		}},
		Tasks: &openspec.Tasks{Sections: []openspec.TaskSection{{
			Name:  "1. Fix",
			Tasks: []openspec.Task{{Number: "1.1", Text: "add the guard"}},
		}}},
	}
}

// Hydrate reconstructs the change the author stamped: read facts → ChangeFromFacts
// → semantically equal to the source. This is the read-side round-trip the tools
// depend on.
func TestHydrateRoundTripsAuthoredFacts(t *testing.T) {
	src := sampleChange()
	triples := stampFacts(runEntity, src)
	// Foreign facts sharing the run entity — a sibling change and a non-openspec
	// owner — must NOT bleed into the hydrated change. This is what the read-side
	// prefix scoping (filterByPrefix) exists to prevent.
	triples = append(triples,
		message.Triple{Subject: runEntity, Predicate: "openspec.change.other-change.proposal.intent", Object: "a different change"},
		message.Triple{Subject: runEntity, Predicate: "run.issue_ref", Object: "gh#7"},
	)
	r := &fakeReader{triples: triples}

	got, err := Hydrate(context.Background(), r, runEntity, src.Slug)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	// The read must be scoped to the change's owned prefix (not an unscoped pull).
	if want := openspec.ChangeEntityPrefix(src.Slug); r.gotPrefix != want {
		t.Errorf("read prefix = %q, want %q", r.gotPrefix, want)
	}
	// Semantic equivalence: re-render both and compare (the round-trip contract is
	// semantic, not byte — same as the engine's own round-trip pins).
	if a, b := openspec.RenderChangeFolder(got), openspec.RenderChangeFolder(src); a != b {
		t.Errorf("hydrated change is not semantically equal to source.\n--- got ---\n%s\n--- want ---\n%s", a, b)
	}
}

// A read scoped to a wrong slug yields no facts → an all-nil Change (absent, not
// an error): the tool layer, not the substrate, decides that is a failure.
func TestHydrateEmptyWhenNoFacts(t *testing.T) {
	r := &fakeReader{triples: stampFacts(runEntity, sampleChange())}
	got, err := Hydrate(context.Background(), r, runEntity, "some-other-change")
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if got.Proposal != nil || got.Tasks != nil || len(got.Deltas) != 0 {
		t.Errorf("expected an empty change for an unknown slug, got %+v", got)
	}
}

// A reader failure propagates (wrapped) — a hydrate never silently returns an
// empty change on a read error.
func TestHydratePropagatesReadError(t *testing.T) {
	r := &fakeReader{err: errors.New("boom")}
	if _, err := Hydrate(context.Background(), r, runEntity, "fix-null-deref"); err == nil {
		t.Fatal("expected the read error to propagate")
	}
}

func TestHydrateRequiresSlug(t *testing.T) {
	if _, err := Hydrate(context.Background(), &fakeReader{}, runEntity, ""); err == nil {
		t.Error("expected an error for an empty slug")
	}
}

// ReadErrorKind maps a classified graph error (entity_not_found and friends) to
// an internal-kind result and a bare transport failure to network-kind, so the
// tools retry the right class.
func TestReadErrorKindClassifies(t *testing.T) {
	classified := errs.ClassifiedCode(errs.ErrorFatal, "entity_not_found", errors.New("no such entity"))
	if got := ReadErrorKind(classified); got != agentic.ToolErrorInternal {
		t.Errorf("classified error kind = %v, want %v", got, agentic.ToolErrorInternal)
	}
	if got := ReadErrorKind(errors.New("dial tcp: connection refused")); got != agentic.ToolErrorNetwork {
		t.Errorf("transport error kind = %v, want %v", got, agentic.ToolErrorNetwork)
	}
}

// filterByPrefix is the load-bearing scope on the real NATS path: the entity
// query returns EVERY owner's triples, so a foreign predicate must be dropped
// before ChangeFromFacts sees it. A regression here (e.g. HasPrefix→Contains, or
// dropping the filter) would leak a sibling change's facts into the render.
func TestFilterByPrefixDropsForeignOwners(t *testing.T) {
	const prefix = "openspec.change.fix-null-deref."
	triples := []message.Triple{
		{Predicate: "openspec.change.fix-null-deref.proposal.intent", Object: "mine"},
		{Predicate: "openspec.change.fix-null-deref.task.0.text", Object: "mine too"},
		{Predicate: "openspec.change.other-change.proposal.intent", Object: "not mine"},
		{Predicate: "run.issue_ref", Object: "gh#7"},
		{Predicate: "prefixed.openspec.change.fix-null-deref.x", Object: "substring, not prefix"},
	}
	got := filterByPrefix(triples, prefix)
	if len(got) != 2 {
		t.Fatalf("filterByPrefix kept %d triples, want 2 (only the target slug's own facts):\n%+v", len(got), got)
	}
	for _, tr := range got {
		if !strings.HasPrefix(tr.Predicate, prefix) {
			t.Errorf("kept a triple outside the owned prefix: %q", tr.Predicate)
		}
	}
}

// objectString renders a non-string object rather than dropping it, so a
// hand-written or legacy numeric/bool fact still reaches ChangeFromFacts.
func TestObjectStringHandlesNonStrings(t *testing.T) {
	cases := map[any]string{"prose": "prose", true: "true", 42: "42", nil: ""}
	for in, want := range cases {
		if got := objectString(in); got != want {
			t.Errorf("objectString(%v) = %q, want %q", in, got, want)
		}
	}
}
