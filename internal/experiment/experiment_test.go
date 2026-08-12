package experiment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/forge/semsource"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

type fakeWriter struct {
	entities  []string
	triples   [][]message.Triple
	err       error
	contracts []string
}

// A real six-position run entity id — see the launch suite for why a bare "run"
// no longer stands in for one (migrate-beta159 D2a, G8).
const (
	runEntity  = "c360.semdev.agent.chain.execution.run-1"
	runEntity2 = "c360.semdev.agent.chain.execution.run-2"
)

func (w *fakeWriter) Reconcile(_ context.Context, m projection.ReconcileMutation) (projection.MutationReceipt, error) {
	if w.err != nil {
		return projection.MutationReceipt{Commit: projection.CommitNotCommitted}, w.err
	}
	w.entities = append(w.entities, m.EntityID)
	w.triples = append(w.triples, m.Desired)
	w.contracts = append(w.contracts, m.Contract)
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// writerFor wraps the fake in the owner-bound seam StampCondition takes, so the
// suite exercises graphown.ContractFor for real — the behavioral proof that
// experiment.run.condition is classed onto the entity class this writer stamps.
func writerFor(w *fakeWriter) *graphown.Writer {
	return graphown.NewWriter(Source, w)
}

type fakeStatus struct {
	status semsource.Status
	err    error
}

func (f fakeStatus) Status(_ context.Context) (semsource.Status, error) { return f.status, f.err }

func ready() semsource.Status {
	return semsource.Status{
		Phase:     "ready",
		Index:     semsource.Signal{Available: true, Ready: true, State: "ready"},
		Embedding: semsource.Signal{Available: true, Ready: true, State: "ready"},
	}
}

// The config surface: unknown conditions and an endpoint-less semsource
// condition are rejected at load, loudly — never at first tool call.
func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"unconfigured is the baseline default", Config{}, ""},
		{"explicit baseline", Config{Condition: ConditionBaseline}, ""},
		{"semsource with endpoint", Config{Condition: ConditionSemsource, SemsourceEndpoint: "http://localhost:8080"}, ""},
		{"unknown condition", Config{Condition: "chaos"}, "unknown condition"},
		{"semsource without endpoint", Config{Condition: ConditionSemsource}, "requires semsource_endpoint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// LoadConfig reads semdev's own `experiment` section from the SAME bootstrap
// file the framework loader reads (which ignores unknown top-level keys); a
// file without the section is the zero-value baseline default.
func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	with := filepath.Join(dir, "with.json")
	if err := os.WriteFile(with, []byte(`{"platform":{},"experiment":{"condition":"semsource","semsource_endpoint":"http://localhost:8080"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(with)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Condition != ConditionSemsource || cfg.SemsourceEndpoint != "http://localhost:8080" {
		t.Fatalf("LoadConfig = %+v", cfg)
	}

	without := filepath.Join(dir, "without.json")
	if err := os.WriteFile(without, []byte(`{"platform":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(without)
	if err != nil {
		t.Fatalf("LoadConfig (no section): %v", err)
	}
	if cfg != (Config{}) {
		t.Fatalf("a file without the experiment section must yield the zero config (baseline default), got %+v", cfg)
	}
}

// The D4 per-signal gate: BOTH index.ready AND embedding.ready are required —
// an aggregate-ready phase with a cold embedding pipeline must FAIL the probe
// (a phase-only probe admits silent code_search degradation).
func TestCheckReadinessPerSignal(t *testing.T) {
	if err := CheckReadiness(context.Background(), fakeStatus{status: ready()}); err != nil {
		t.Fatalf("both signals ready must pass: %v", err)
	}

	coldEmbedding := ready()
	coldEmbedding.Embedding.Ready = false
	err := CheckReadiness(context.Background(), fakeStatus{status: coldEmbedding})
	if err == nil || !strings.Contains(err.Error(), "embedding") {
		t.Fatalf("aggregate-ready + cold embeddings MUST fail naming the embedding signal (the silent code_search degradation hole), got %v", err)
	}

	coldIndex := ready()
	coldIndex.Index.Ready = false
	err = CheckReadiness(context.Background(), fakeStatus{status: coldIndex})
	if err == nil || !strings.Contains(err.Error(), "structural index") {
		t.Fatalf("cold structural index must fail naming the index signal, got %v", err)
	}

	unavailable := ready()
	unavailable.Index.Available = false
	if err := CheckReadiness(context.Background(), fakeStatus{status: unavailable}); err == nil {
		t.Fatal("an unavailable signal (fetch failure upstream) must fail the probe")
	}

	if err := CheckReadiness(context.Background(), fakeStatus{err: errors.New("connection refused")}); err == nil {
		t.Fatal("an unreachable status surface must fail the probe")
	}
}

// Task 1.3: a minted run carries the DECLARED condition (stamped by the single
// writer experiment-intake as a single-valued upsert on the run entity), and an
// UNCONFIGURED launch stamps nothing (baseline default, zero new facts).
func TestLaunchStampsDeclaredCondition(t *testing.T) {
	w := &fakeWriter{}
	publishOK := func(context.Context) error { return nil }
	bindRun := func(context.Context) (string, error) { return "org.plat.agent.chain.execution.run-1", nil }

	runID, err := Launch(context.Background(), Config{Condition: ConditionBaseline}, nil, publishOK, bindRun, writerFor(w))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if len(w.triples) != 1 || len(w.triples[0]) != 1 {
		t.Fatalf("want exactly one stamped triple, got %+v", w.triples)
	}
	tr := w.triples[0][0]
	if tr.Subject != runID || tr.Predicate != ConditionPredicate || tr.Object != ConditionBaseline || tr.Source != Source {
		t.Fatalf("stamp = %+v, want %s=%q on %s from writer %s", tr, ConditionPredicate, ConditionBaseline, runID, Source)
	}
	// Single-valued upsert is now expressed by the owner's replace-owned GROUP:
	// experiment-intake owns exactly {experiment.run.condition}, so ReplaceOwned
	// clears that predicate and re-adds the one desired triple (migrate-beta159 D3a).
	// The pin is therefore that the write carries exactly ONE condition triple.
	if len(w.triples[0]) != 1 || w.triples[0][0].Predicate != ConditionPredicate {
		t.Fatalf("the condition must be a single-valued upsert (one triple, the condition predicate), got %v", w.triples[0])
	}

	w = &fakeWriter{}
	if _, err := Launch(context.Background(), Config{}, nil, publishOK, bindRun, writerFor(w)); err != nil {
		t.Fatalf("unconfigured Launch: %v", err)
	}
	if len(w.triples) != 0 {
		t.Fatalf("an unconfigured launch must stamp NOTHING (baseline default, zero new facts), got %+v", w.triples)
	}
}

// Task 4.1 (D4, fail-closed): a failed readiness probe aborts the launch
// BEFORE anything is published — no wake, no run, no condition fact, no
// half-labeled evidence.
func TestLaunchSemsourceProbeFailsClosed(t *testing.T) {
	published := false
	bound := false
	w := &fakeWriter{}
	cfg := Config{Condition: ConditionSemsource, SemsourceEndpoint: "http://localhost:1"}

	_, err := Launch(context.Background(), cfg,
		func(context.Context) error { return errors.New("index not ready") },
		func(context.Context) error { published = true; return nil },
		func(context.Context) (string, error) { bound = true; return runEntity, nil },
		writerFor(w))
	if err == nil || !strings.Contains(err.Error(), "no run minted") {
		t.Fatalf("a failed probe must abort the launch loudly, got %v", err)
	}
	if published || bound || len(w.triples) != 0 {
		t.Fatalf("a failed probe must leave NOTHING behind (published=%t bound=%t stamps=%d)", published, bound, len(w.triples))
	}

	// A nil probe under the semsource condition is a wiring fault, not a pass.
	_, err = Launch(context.Background(), cfg, nil,
		func(context.Context) error { published = true; return nil },
		func(context.Context) (string, error) { return runEntity, nil }, writerFor(w))
	if err == nil || !strings.Contains(err.Error(), "wiring fault") {
		t.Fatalf("a nil probe under the semsource condition must fail, got %v", err)
	}
	if published {
		t.Fatal("a nil-probe fault must not publish")
	}

	// The probe passing lets the launch proceed and stamp the semsource label.
	w = &fakeWriter{}
	runID, err := Launch(context.Background(), cfg,
		func(context.Context) error { return nil },
		func(context.Context) error { return nil },
		func(context.Context) (string, error) { return runEntity2, nil }, writerFor(w))
	if err != nil || runID != runEntity2 {
		t.Fatalf("Launch after a passing probe: %v (run %q)", err, runID)
	}
	if len(w.triples) != 1 || w.triples[0][0].Object != ConditionSemsource {
		t.Fatalf("the semsource condition must be stamped after a passing probe, got %+v", w.triples)
	}
}
