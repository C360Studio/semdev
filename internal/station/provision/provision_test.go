package provision

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/coldproof"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/provisionsandbox"
	"github.com/c360studio/semdev/internal/verify"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// --- fakes (mirroring the provision tool's, scoped to the station handler contract) ---

type fakeSources struct{ dir string }

func (f fakeSources) Resolve(context.Context, string) (string, error) { return f.dir, nil }

type fakeCheckouts struct{ root string }

func (f fakeCheckouts) Materialize(context.Context, string, string) (string, error) {
	return f.root, nil
}

type fakeManifests struct{}

func (fakeManifests) Resolve(context.Context, string) (harness.Manifest, error) {
	return harness.GoProfile(), nil
}

type fakeWarmers struct{ err error }

func (f fakeWarmers) Provision(context.Context, string, string, string, []string) (cleanroom.Sandbox, error) {
	if f.err != nil {
		return cleanroom.Sandbox{}, f.err
	}
	return cleanroom.Sandbox{WorkDir: "/work", Handle: "warm"}, nil
}

type fakeProver struct {
	baseline coldproof.Baseline
	err      error
}

func (f fakeProver) ProveBaseline(context.Context, string, string, harness.Manifest, secrets.Store) (coldproof.Baseline, error) {
	return f.baseline, f.err
}

type fakeReader struct{}

func (fakeReader) ReadFacts(context.Context, string, string) ([]message.Triple, error) {
	return nil, nil // not already ready
}

type fakeWriter struct {
	calls int
	err   error
}

func (w *fakeWriter) ReplaceTriples(context.Context, string, []message.Triple, []string) error {
	if w.err != nil {
		return w.err
	}
	w.calls++
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func readyBaseline() coldproof.Baseline {
	return coldproof.Baseline{
		Image:   cleanroom.BuiltImage{Ref: "semdev-sandbox:test", Digest: "sha256:abc"},
		Outcome: verify.OutcomePass,
	}
}

func newHandler(prover fakeProver, warmers fakeWarmers, w *fakeWriter) *handler {
	return &handler{deps: provisionsandbox.ProvisionDeps{
		Sources:     fakeSources{dir: "/src"},
		Checkouts:   fakeCheckouts{root: "/checkout"},
		Manifests:   fakeManifests{},
		Warmers:     warmers,
		Prover:      prover,
		Reader:      fakeReader{},
		Writer:      w,
		DockerCheck: func(context.Context, string) error { return nil },
		Logger:      slog.Default(),
	}}
}

func req() station.Request { return station.Request{EntityID: runEntity} }

// A proven-cold baseline with a sandbox-scope tier + a warm container up → the station
// stamps readiness and Handle returns nil (the readiness gate reads sandbox.ready).
func TestHandleReadyStampsAndSucceeds(t *testing.T) {
	w := &fakeWriter{}
	h := newHandler(fakeProver{baseline: readyBaseline()}, fakeWarmers{}, w)
	if err := h.Handle(context.Background(), req()); err != nil {
		t.Fatalf("a proven-cold ready run must succeed: %v", err)
	}
	if w.calls == 0 {
		t.Error("a ready run must stamp the readiness package")
	}
}

// A not-ready baseline is a STAMPED block (sandbox.blocked) — Handle returns nil (the park
// rule routes it), never an error that would burn the base's retry budget on a real park.
func TestHandleBlockReturnsNil(t *testing.T) {
	w := &fakeWriter{}
	notReady := coldproof.Baseline{Image: cleanroom.BuiltImage{Digest: "sha256:x"}, Outcome: verify.OutcomeFail}
	h := newHandler(fakeProver{baseline: notReady}, fakeWarmers{}, w)
	if err := h.Handle(context.Background(), req()); err != nil {
		t.Fatalf("a block is a stamped outcome (park rule routes it), not a handler error: %v", err)
	}
	if w.calls == 0 {
		t.Error("a block must stamp sandbox.blocked")
	}
}

// A warm stand-up fault is a block too (park toward the human) → Handle returns nil.
func TestHandleWarmFaultBlocksAndReturnsNil(t *testing.T) {
	w := &fakeWriter{}
	h := newHandler(fakeProver{baseline: readyBaseline()}, fakeWarmers{err: errors.New("no space left on device")}, w)
	if err := h.Handle(context.Background(), req()); err != nil {
		t.Fatalf("a warm-Up fault blocks (park), it is not a handler error: %v", err)
	}
}

// A graph-WRITE fault (the block/ready fact could not be stamped) returns an error so the
// base retries — this is the ONLY error path (transient graph faults self-heal).
func TestHandleWriteFaultReturnsError(t *testing.T) {
	w := &fakeWriter{err: errors.New("graph unavailable")}
	h := newHandler(fakeProver{baseline: readyBaseline()}, fakeWarmers{}, w)
	if err := h.Handle(context.Background(), req()); err == nil {
		t.Error("a graph-write fault must return an error so the base retries")
	}
}
