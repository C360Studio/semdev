package createchange

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

// fakePublisher records the triples a tool writes, so the fact-emission path is
// testable without a live NATS graph.
type fakePublisher struct {
	batches [][]message.Triple
}

func (f *fakePublisher) CreateEntityWithTriples(_ context.Context, _ string, _ message.Type, triples []message.Triple) error {
	f.batches = append(f.batches, triples)
	return nil
}
func (f *fakePublisher) AddTriple(_ context.Context, t message.Triple) error {
	f.batches = append(f.batches, []message.Triple{t})
	return nil
}
func (f *fakePublisher) AddTriplesBatch(_ context.Context, triples []message.Triple) error {
	f.batches = append(f.batches, triples)
	return nil
}

const runEntity = "org.plat.agent.chain.execution.run-1"

func sampleCall() agentic.ToolCall {
	return agentic.ToolCall{
		ID:       "c1",
		Name:     ToolName,
		Metadata: map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{
			"slug":     "fix-null-deref",
			"proposal": map[string]any{"intent": "fix the crash", "scope_in": []any{"the handler"}},
			"deltas": []any{map[string]any{
				"capability": "handler",
				"added": []any{map[string]any{
					"name":      "Nil guard",
					"statement": "The system SHALL guard nil input.",
					"scenarios": []any{map[string]any{
						"name": "Nil input",
						"steps": []any{
							map[string]any{"kw": "WHEN", "text": "input is nil"},
							map[string]any{"kw": "THEN", "text": "no panic occurs"},
						},
					}},
				}},
			}},
			"tasks": []any{map[string]any{
				"section": "1. Fix",
				"items":   []any{map[string]any{"number": "1.1", "text": "add the guard", "done": false}},
			}},
		},
	}
}

// The tool stamps openspec.change.* facts, all on the RUN entity (D15), and never
// an outcome fact.
func TestCreateChangeStampsFactsOnRunEntity(t *testing.T) {
	pub := &fakePublisher{}
	res, err := New(pub, nil).Execute(context.Background(), sampleCall())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if len(pub.batches) != 1 {
		t.Fatalf("expected one atomic batch, got %d", len(pub.batches))
	}
	triples := pub.batches[0]
	if len(triples) == 0 {
		t.Fatal("no triples stamped")
	}

	var sawIntent, sawReq, sawTask bool
	for _, tr := range triples {
		if tr.Subject != runEntity {
			t.Errorf("triple subject = %q, want the run entity %q (D15)", tr.Subject, runEntity)
		}
		if !strings.HasPrefix(tr.Predicate, "openspec.change.") {
			t.Errorf("predicate %q is not under openspec.change.*", tr.Predicate)
		}
		if strings.Contains(tr.Predicate, "outcome") || strings.HasSuffix(tr.Predicate, ".validated") || strings.HasSuffix(tr.Predicate, ".pass") {
			t.Errorf("tool stamped an outcome-shaped fact %q (G3 violation)", tr.Predicate)
		}
		switch {
		case tr.Predicate == "openspec.change.fix-null-deref.proposal.intent":
			sawIntent = tr.Object == "fix the crash"
		case strings.Contains(tr.Predicate, ".delta.handler.nil-guard.statement"):
			sawReq = true
		case strings.Contains(tr.Predicate, ".task.0.text"):
			sawTask = true
		}
	}
	if !sawIntent {
		t.Error("proposal intent fact not stamped")
	}
	if !sawReq {
		t.Error("requirement delta fact not stamped")
	}
	if !sawTask {
		t.Error("task fact not stamped")
	}
}

// Missing run-entity metadata fails loudly (the silent-subject trap).
func TestCreateChangeFailsWithoutRunEntity(t *testing.T) {
	call := sampleCall()
	call.Metadata = nil
	res, _ := New(&fakePublisher{}, nil).Execute(context.Background(), call)
	if res.Error == "" {
		t.Error("expected an error when agent.run_entity_id is missing")
	}
}

// A nil publisher fails loudly rather than dropping facts.
func TestCreateChangeFailsWithoutPublisher(t *testing.T) {
	res, _ := New(nil, nil).Execute(context.Background(), sampleCall())
	if res.Error == "" {
		t.Error("expected an error when no publisher is wired")
	}
}

// The schema advertises the tool with content-only input (no outcome field).
func TestSchemaHasNoOutcomeField(t *testing.T) {
	defs := New(nil, nil).ListTools()
	if len(defs) != 1 || defs[0].Name != ToolName {
		t.Fatalf("want one tool %q, got %+v", ToolName, defs)
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	for _, forbidden := range []string{"pass", "passed", "exit_code", "success", "outcome", "validated", "resolved"} {
		if _, ok := props[forbidden]; ok {
			t.Errorf("schema exposes outcome-shaped field %q (G3)", forbidden)
		}
	}
}
