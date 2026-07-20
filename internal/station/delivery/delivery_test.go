package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/openpr"
	"github.com/c360studio/semstreams/message"
)

// The handler is a thin driver over openpr.Delivery (unit-pinned in its own
// package); these tests pin the STATION wiring — the firing entity is the run,
// a delivery fault propagates so the base retries/exhausts/parks, and a
// redispatch is a no-op through the graph guard.

type fakeWriter struct {
	replaces [][]message.Triple
	err      error
}

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, _ []string) error {
	if w.err != nil {
		return w.err
	}
	w.replaces = append(w.replaces, add)
	return nil
}

func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

type fakeReader struct {
	triples []message.Triple
	err     error
}

func (r *fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []message.Triple
	for _, t := range r.triples {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

type fakeForge struct {
	created int
}

func (f *fakeForge) FindPRByHead(context.Context, string, string, string, string) (*github.PR, error) {
	return nil, nil
}

func (f *fakeForge) CreatePR(context.Context, string, string, github.PRRequest) (*github.PR, error) {
	f.created++
	return &github.PR{Number: 1, HTMLURL: "https://forge.example/o/r/pull/1", State: "open"}, nil
}

type okRunner struct{}

func (okRunner) Run(context.Context, string, string, ...string) (cliexec.Result, error) {
	return cliexec.Result{ExitCode: 0}, nil
}

type fixedRoots struct{}

func (fixedRoots) Root(context.Context, string) (string, error) { return "/tmp/checkout", nil }

func newTestHandler(reader *fakeReader, writer *fakeWriter) (*handler, *fakeForge) {
	// A delivering run always carries the verified snapshot pointer (Deliver
	// pushes THAT sha and fails closed without it — openpr's own pins).
	if reader.err == nil {
		reader.triples = append(reader.triples, message.Triple{
			Subject: "run-7", Predicate: "attempt.commit.sha", Object: "abc123def", Source: "patch-committer",
		})
	}
	forge := &fakeForge{}
	return &handler{
		delivery: &openpr.Delivery{
			Reader: reader,
			Writer: writer,
			API:    forge,
			Roots:  fixedRoots{},
			Runner: okRunner{},
			Forge:  openpr.ForgeConfig{Owner: "o", Repo: "r", RemoteURL: "file:///tmp/bare.git"},
		},
		logger: slog.Default(),
	}, forge
}

func TestHandleDeliversAndStampsPRRefOnFiringEntity(t *testing.T) {
	w := &fakeWriter{}
	h, forge := newTestHandler(&fakeReader{}, w)
	// The delivery rule fires on the RUN, so req.EntityID is the run.
	if err := h.Handle(context.Background(), station.Request{EntityID: "run-7"}); err != nil {
		t.Fatalf("Handle returned %v, want nil", err)
	}
	if forge.created != 1 {
		t.Fatalf("PRs created = %d, want 1", forge.created)
	}
	if len(w.replaces) != 1 || len(w.replaces[0]) != 1 {
		t.Fatalf("expected exactly one pr.ref triple stamped, got %v", w.replaces)
	}
	tr := w.replaces[0][0]
	if tr.Subject != "run-7" || tr.Predicate != openpr.RefPredicate || tr.Source != openpr.Source {
		t.Errorf("stamped %s on %s (Source %s), want %s on run-7 (Source %s)",
			tr.Predicate, tr.Subject, tr.Source, openpr.RefPredicate, openpr.Source)
	}
	if !strings.HasPrefix(tr.Object.(string), "https://") {
		t.Errorf("pr.ref = %v, want a REAL PR URL (the stub is deleted)", tr.Object)
	}
}

func TestHandleFailsClosedOnDeliveryFault(t *testing.T) {
	h, _ := newTestHandler(&fakeReader{err: errors.New("graph down")}, &fakeWriter{})
	// A delivery fault returns an error (logged + metered by the generic
	// Component); after the base's retries exhaust, station.dispatch.failed
	// lands and the run parks (station-failure-parks).
	if err := h.Handle(context.Background(), station.Request{EntityID: "run-7"}); err == nil {
		t.Error("Handle must return an error on a delivery fault (fail closed — no false delivery)")
	}
}

// TestStationConfigEmbeddingFillsBothHalves locks the JSON promotion behavior
// the factory depends on: one document fills BOTH the embedded station ports
// and the sibling forge block (review finding — an accidental custom
// UnmarshalJSON upstream would silently drop one half).
func TestStationConfigEmbeddingFillsBothHalves(t *testing.T) {
	raw := []byte(`{
		"ports": {"inputs": [{"name": "dispatch", "type": "nats", "subject": "component.delivery-station.>"}]},
		"forge": {"owner": "acme", "repo": "widgets", "remote_url": "file:///tmp/b.git", "base_branch": "trunk"}
	}`)
	var cfg stationConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Ports == nil || len(cfg.Ports.Inputs) != 1 || cfg.Ports.Inputs[0].Subject != "component.delivery-station.>" {
		t.Errorf("embedded station ports not filled: %+v", cfg.Ports)
	}
	if cfg.Forge.Owner != "acme" || cfg.Forge.Repo != "widgets" || cfg.Forge.BaseBranch != "trunk" {
		t.Errorf("sibling forge block not filled: %+v", cfg.Forge)
	}
}

// A re-fired dispatch on an already-delivered run touches nothing (the graph
// guard) — proves the reader→handler→Deliver wiring end to end.
func TestHandleIsIdempotentOnRedispatch(t *testing.T) {
	w := &fakeWriter{}
	r := &fakeReader{}
	h, forge := newTestHandler(r, w)

	if err := h.Handle(context.Background(), station.Request{EntityID: "run-7"}); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	r.triples = append(r.triples, w.replaces[0]...)

	if err := h.Handle(context.Background(), station.Request{EntityID: "run-7"}); err != nil {
		t.Fatalf("redispatch Handle: %v", err)
	}
	if len(w.replaces) != 1 || forge.created != 1 {
		t.Errorf("redispatch re-delivered (writes=%d, creates=%d) — an already-delivered run must no-op", len(w.replaces), forge.created)
	}
}
