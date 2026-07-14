package station

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/c360studio/semstreams/component"
)

// captureHandler records every Request it is handed and returns a configurable
// error — the seam for asserting what the generic Component decoded onto the
// Handler without a live NATS subscription.
type captureHandler struct {
	reqs []Request
	err  error
}

func (h *captureHandler) Handle(_ context.Context, req Request) error {
	h.reqs = append(h.reqs, req)
	return h.err
}

// newTestComponent builds a Component wired to a capture handler, bypassing New's
// NATS-client requirement (handleMessage never touches the client).
func newTestComponent(h Handler) *Component {
	return &Component{name: "test-station", handler: h, logger: slog.Default()}
}

func TestHandleMessageDecodesDispatchOntoHandler(t *testing.T) {
	h := &captureHandler{}
	c := newTestComponent(h)
	// The rule engine's executePublish envelope shape: entity_id is the firing
	// entity, properties are the substituted string props, plus fields the station
	// ignores (subject/timestamp/source).
	payload := `{"entity_id":"run-42","subject":"component.test-station.dispatch","timestamp":"t","source":"rule_engine","properties":{"slug":"journey","phase":"deliver"}}`
	c.handleMessage("component.test-station.dispatch", []byte(payload))

	if len(h.reqs) != 1 {
		t.Fatalf("handler called %d times, want 1", len(h.reqs))
	}
	got := h.reqs[0]
	if got.EntityID != "run-42" {
		t.Errorf("EntityID = %q, want run-42 (the firing entity from the envelope)", got.EntityID)
	}
	if got.Prop("slug") != "journey" || got.Prop("phase") != "deliver" {
		t.Errorf("properties = %v, want slug=journey phase=deliver", got.Properties)
	}
	if got.Subject != "component.test-station.dispatch" {
		t.Errorf("Subject = %q, want the dispatch subject", got.Subject)
	}
	if n := atomic.LoadInt64(&c.errors); n != 0 {
		t.Errorf("error count = %d, want 0 on a clean dispatch", n)
	}
}

func TestHandleMessageIgnoresEmptyEntityID(t *testing.T) {
	h := &captureHandler{}
	c := newTestComponent(h)
	// A dispatch with no entity_id cannot target a run — the station must NOT call
	// the handler (which would stamp on ""), and must count the error.
	c.handleMessage("component.test-station.dispatch", []byte(`{"properties":{}}`))

	if len(h.reqs) != 0 {
		t.Errorf("handler called %d times on an entity_id-less dispatch, want 0", len(h.reqs))
	}
	if n := atomic.LoadInt64(&c.errors); n != 1 {
		t.Errorf("error count = %d, want 1", n)
	}
}

func TestHandleMessageIgnoresMalformedEnvelope(t *testing.T) {
	h := &captureHandler{}
	c := newTestComponent(h)
	c.handleMessage("component.test-station.dispatch", []byte(`{not json`))

	if len(h.reqs) != 0 {
		t.Errorf("handler called on malformed JSON, want 0")
	}
	if n := atomic.LoadInt64(&c.errors); n != 1 {
		t.Errorf("error count = %d, want 1", n)
	}
}

func TestHandleMessageRetriesThenMetersError(t *testing.T) {
	h := &captureHandler{err: errors.New("boom")}
	c := newTestComponent(h)
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"run-1","properties":{}}`))

	// A persistent fault is RETRIED (transient resilience, restoring the forced
	// loop's re-call behavior): the idempotent handler runs maxHandleAttempts times,
	// stamps no success fact (fail-closed), and the error is metered ONCE so the
	// fault is observable rather than silent.
	if len(h.reqs) != maxHandleAttempts {
		t.Fatalf("handler called %d times, want %d (bounded retry)", len(h.reqs), maxHandleAttempts)
	}
	if n := atomic.LoadInt64(&c.errors); n != 1 {
		t.Errorf("error count = %d, want 1 (metered once after the retries)", n)
	}
}

func TestHandleMessageStopsRetryOnCancelledContext(t *testing.T) {
	h := &captureHandler{err: errors.New("boom")}
	c := newTestComponent(h)
	// A cancelled base context (Stop in flight) must abort the retry loop after the
	// first attempt rather than burning the whole budget with backoff sleeps.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.baseCtx = ctx
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"run-1","properties":{}}`))
	if len(h.reqs) != 1 {
		t.Errorf("handler called %d times under a cancelled context, want 1 (no retry during shutdown)", len(h.reqs))
	}
}

func TestStringPropsDropsNonStrings(t *testing.T) {
	// The rule engine substitutes only string property values; a non-string entry
	// (a raw number/bool the rule authored) is dropped rather than coerced.
	got := stringProps(map[string]any{"slug": "x", "count": 3, "flag": true})
	if len(got) != 1 || got["slug"] != "x" {
		t.Errorf("stringProps = %v, want only the string prop {slug:x}", got)
	}
}

func TestConfigValidateRequiresInputPort(t *testing.T) {
	if err := (&Config{}).Validate(); err == nil {
		t.Error("Config.Validate must reject a config with no input port (a station that never fires is a silent flow break)")
	}
	cfg := Config{Ports: DefaultPorts("floors-station")}
	if err := cfg.Validate(); err != nil {
		t.Errorf("DefaultPorts config should validate, got %v", err)
	}
}

func TestNewRejectsMissingDeps(t *testing.T) {
	cfg := Config{Ports: DefaultPorts("s")}
	h := &captureHandler{}
	// nil NATS client / nil handler / empty name each fail loudly — a station
	// cannot subscribe or dispatch without them, and a silent no-op start is the
	// flow break the constitution bars.
	if _, err := New("", cfg, h, nil, nil); err == nil {
		t.Error("New must reject an empty name")
	}
	if _, err := New("s", cfg, nil, nil, nil); err == nil {
		t.Error("New must reject a nil handler")
	}
	if _, err := New("s", cfg, h, nil, nil); err == nil {
		t.Error("New must reject a nil NATS client")
	}
}

func TestDefaultPortsSubjectMatchesPrefix(t *testing.T) {
	ports := DefaultPorts("floors-station")
	if len(ports.Inputs) != 1 {
		t.Fatalf("DefaultPorts inputs = %d, want 1", len(ports.Inputs))
	}
	want := SubjectPrefix + "floors-station.>"
	if got := ports.Inputs[0].Subject; got != want {
		t.Errorf("dispatch subject = %q, want %q (the rule publishes component.<name>.dispatch, the wildcard matches)", got, want)
	}
	if ports.Inputs[0].Type != "nats" {
		t.Errorf("dispatch port type = %q, want nats (core NATS)", ports.Inputs[0].Type)
	}
}

// Compile-time proof the generic Component satisfies the framework contracts even
// as the concrete stations only supply a Handler.
var _ component.LifecycleComponent = (*Component)(nil)
