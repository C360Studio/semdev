package openpr

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakeWriter struct{ replaces [][]message.Triple }

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, _ []string) error {
	w.replaces = append(w.replaces, add)
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func callOpenPR() agentic.ToolCall {
	return agentic.ToolCall{
		ID:       "c1",
		Name:     ToolName,
		Metadata: map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
	}
}

// open_pr stamps pr.ref (an M0 local delivery stub) on the run with the open-pr Source and
// StopLoops.
func TestOpenPRStampsRef(t *testing.T) {
	w := &fakeWriter{}
	res, err := New(w, nil).Execute(context.Background(), callOpenPR())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if !res.StopLoop {
		t.Error("open_pr must StopLoop (single forced turn)")
	}
	var ref string
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != RefPredicate {
				t.Errorf("open_pr wrote %q — it must write only %q", tr.Predicate, RefPredicate)
			}
			if tr.Subject != runEntity {
				t.Errorf("pr.ref subject = %q, want run entity", tr.Subject)
			}
			if tr.Source != Source {
				t.Errorf("pr.ref Source = %q, want %q (G5)", tr.Source, Source)
			}
			ref = tr.Object.(string)
		}
	}
	// M0 honesty: the ref is a LOCAL stub (not a live PR URL), deterministic from the run.
	if !strings.HasPrefix(ref, localStubPrefix) {
		t.Errorf("pr.ref = %q, want the %q M0 local-delivery stub prefix", ref, localStubPrefix)
	}
	if !strings.Contains(ref, runEntity) {
		t.Errorf("pr.ref = %q, want it derived deterministically from the run", ref)
	}
}

// Schema-only registration (nil writer) fails loudly.
func TestOpenPRFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil).Execute(context.Background(), callOpenPR())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness open_pr must fail loudly")
	}
}

// G3: the schema takes no arguments — the harness forms the ref.
func TestOpenPRSchemaTakesNoInput(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("schema exposes %d properties, want 0 — open_pr takes no input (G3): %v", len(props), props)
	}
}
