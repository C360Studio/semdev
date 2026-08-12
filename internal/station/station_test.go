package station

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

// captureHandler records every Request it is handed and returns a configurable
// error — the seam for asserting what the generic Component decoded onto the
// Handler without a live NATS subscription.
type captureHandler struct {
	reqs []Request
	err  error
}

// testRunEntity is a REAL six-position run entity id. station-harness resolves its
// projection contract by matching the DISPATCHED entity, so a bare "run-1" is no
// longer a usable stand-in (migrate-beta159 D2a) — and the old fixture, which
// could never occur in production, would have hidden that (G8).
const testRunEntity = "org.plat.agent.chain.execution.run-1"

func (h *captureHandler) Handle(_ context.Context, req Request) error {
	h.reqs = append(h.reqs, req)
	return h.err
}

// newTestComponent builds a Component wired to a capture handler, bypassing New's
// NATS-client and fact-writer requirements (handleMessage never touches the
// client; a nil writer is the log-only pre-park behavior, D1's unit-test
// tolerance).
func newTestComponent(h Handler) *Component {
	return &Component{name: "test-station", handler: h, logger: slog.Default()}
}

// replaceCall records one ReplaceTriples invocation on the capture writer.
type replaceCall struct {
	entityID string
	add      []message.Triple
}

// captureWriter is the OwnedFactWriter seam for the dispatch-outcome pins —
// records every ReplaceTriples so the tests assert exactly what the harness
// stamps (and, on the success path, that it stamps nothing).
type captureWriter struct {
	calls []replaceCall
	err   error
}

func (w *captureWriter) Reconcile(_ context.Context, m projection.ReconcileMutation) (projection.MutationReceipt, error) {
	w.calls = append(w.calls, replaceCall{entityID: m.EntityID, add: m.Desired})
	if w.err != nil {
		return projection.MutationReceipt{Commit: projection.CommitNotCommitted}, w.err
	}
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// newTestComponentWithWriter builds a direct-constructed Component carrying the
// capture writer (the booted-station shape New enforces).
func newTestComponentWithWriter(h Handler, w *captureWriter) *Component {
	c := newTestComponent(h)
	c.factWriter = graphown.NewWriter(DispatchFailedSource, w)
	return c
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
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))

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
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))
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
	cfg := Config{Ports: DefaultPorts("s"), FactWriter: graphown.NewWriter(DispatchFailedSource, &captureWriter{})}
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

// TestNewRejectsNilFactWriter is the boot-wiring census (station-failure-parks
// task 1.4): every booted station goes through New, so rejecting a nil writer
// HERE means a future station cannot silently boot writer-less and strand its
// terminal failures as the pre-M2 stall. The writer check precedes the NATS
// check, so the asserted error is unambiguously the writer's.
func TestNewRejectsNilFactWriter(t *testing.T) {
	cfg := Config{Ports: DefaultPorts("s")} // no FactWriter
	_, err := New("s", cfg, &captureHandler{}, nil, nil)
	if err == nil {
		t.Fatal("New must reject a nil FactWriter (a writer-less station strands terminal failures)")
	}
	if !strings.Contains(err.Error(), "fact writer is required") {
		t.Errorf("rejection must name the fact writer, got: %v", err)
	}
}

// TestRetriesExhaustedStampsDispatchFailed pins station-failure-parks D1: after
// the bounded retries exhaust, the harness stamps station.dispatch.failed on
// the DISPATCHED entity — subject = the envelope's entity, object names the
// station + the sanitized error, Source = the G5 station-harness writer, and
// the write reconciles station-harness's single-predicate group (no explicit
// remove list exists under ReplaceOwned — the group IS the removal).
func TestRetriesExhaustedStampsDispatchFailed(t *testing.T) {
	h := &captureHandler{err: errors.New("projection refused: target_files lacks a test")}
	w := &captureWriter{}
	c := newTestComponentWithWriter(h, w)
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))

	if len(h.reqs) != maxHandleAttempts {
		t.Fatalf("handler called %d times, want %d (the stamp happens AFTER the bounded retries)", len(h.reqs), maxHandleAttempts)
	}
	if len(w.calls) != 1 {
		t.Fatalf("ReplaceOwned called %d times, want exactly 1 after retries exhaust", len(w.calls))
	}
	call := w.calls[0]
	if call.entityID != testRunEntity {
		t.Errorf("stamped entity = %q, want the dispatched entity %q", call.entityID, testRunEntity)
	}
	// Upsert with no collateral is now structural: station-harness owns exactly
	// {station.dispatch.failed}, so the group wipe clears only that predicate before
	// re-adding it (migrate-beta159 D3a). The pin is that the write carried exactly
	// the one triple.

	if len(call.add) != 1 {
		t.Fatalf("add carries %d triples, want exactly 1 (the dispatch-outcome fact)", len(call.add))
	}
	tr := call.add[0]
	if tr.Subject != testRunEntity || tr.Predicate != DispatchFailedPredicate {
		t.Errorf("triple = %s %s, want run-1 %s", tr.Subject, tr.Predicate, DispatchFailedPredicate)
	}
	if tr.Source != DispatchFailedSource {
		t.Errorf("Source = %q, want %q (G5: the harness that ran the retries is the writer)", tr.Source, DispatchFailedSource)
	}
	obj, _ := tr.Object.(string)
	if !strings.HasPrefix(obj, "test-station: ") {
		t.Errorf("object %q must name the station first", obj)
	}
	if !strings.Contains(obj, "projection refused") {
		t.Errorf("object %q must carry the handler error for the human's resume decision", obj)
	}
}

// TestDispatchFailedObjectIsBounded pins the 512-byte sanitize posture: a
// pathological handler error is cut at the byte limit ON A RUNE BOUNDARY
// (never mojibake), so the graph object stays bounded no matter what the
// handler produced. The rune is 3 BYTES ("€") and 512 mod 3 = 2, so the naive
// byte cut strands a partial rune — a truncate that dropped the rune-boundary
// handling would fail the ValidString assert here (a 2-byte rune with an even
// limit would not discriminate).
func TestDispatchFailedObjectIsBounded(t *testing.T) {
	huge := strings.Repeat("€", 2000)
	h := &captureHandler{err: errors.New(huge)}
	w := &captureWriter{}
	c := newTestComponentWithWriter(h, w)
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))

	if len(w.calls) != 1 || len(w.calls[0].add) != 1 {
		t.Fatalf("want exactly one stamped triple, got calls=%d", len(w.calls))
	}
	obj, _ := w.calls[0].add[0].Object.(string)
	// station prefix + bounded error + ellipsis: comfortably under 600 bytes.
	if len(obj) > len("test-station: ")+dispatchFailedErrLimit+len("…") {
		t.Errorf("object is %d bytes, want ≤ %d (bounded error)", len(obj), len("test-station: ")+dispatchFailedErrLimit+len("…"))
	}
	if !utf8.ValidString(obj) {
		t.Error("object must stay valid UTF-8 after the cut (rune-boundary truncate)")
	}
	if !strings.HasSuffix(obj, "…") {
		t.Errorf("a truncated object must end with the ellipsis marker, got tail %q", obj[len(obj)-8:])
	}
	// The cut dropped EXACTLY the stranded partial rune: 512 - (512 mod 3) = 510
	// bytes of error text survive between the prefix and the ellipsis.
	body := strings.TrimSuffix(strings.TrimPrefix(obj, "test-station: "), "…")
	if len(body) != 510 {
		t.Errorf("truncated error body is %d bytes, want 510 (512 cut back to the 3-byte rune boundary)", len(body))
	}
}

// TestDispatchFailedStripsHandlerStationPrefix pins the de-stutter: handlers
// conventionally prefix their errors with their own station name, and the
// harness must not stamp "<station>: <station>: ..." (observed in the first
// green journey run before the strip landed).
func TestDispatchFailedStripsHandlerStationPrefix(t *testing.T) {
	h := &captureHandler{err: errors.New("test-station: boom")}
	w := &captureWriter{}
	c := newTestComponentWithWriter(h, w)
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))

	if len(w.calls) != 1 || len(w.calls[0].add) != 1 {
		t.Fatalf("want exactly one stamped triple, got calls=%d", len(w.calls))
	}
	obj, _ := w.calls[0].add[0].Object.(string)
	if obj != "test-station: boom" {
		t.Errorf("object = %q, want exactly %q (one station prefix, not stuttered)", obj, "test-station: boom")
	}
}

// TestDispatchFailedIsUpsertNotAppend pins D1's crash-loop convergence: a
// SECOND terminal failure of the same dispatch replaces the fact (each write is
// a ReplaceTriples of the ONE predicate — replace-by-predicate at the graph
// layer), never a growing append pile.
func TestDispatchFailedIsUpsertNotAppend(t *testing.T) {
	h := &captureHandler{err: errors.New("boom-1")}
	w := &captureWriter{}
	c := newTestComponentWithWriter(h, w)
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))
	h.err = errors.New("boom-2")
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))

	if len(w.calls) != 2 {
		t.Fatalf("ReplaceOwned called %d times, want 2 (one per exhausted dispatch)", len(w.calls))
	}
	for i, call := range w.calls {
		if len(call.add) != 1 || call.add[0].Predicate != DispatchFailedPredicate {
			t.Errorf("call %d: add = %+v, want exactly the one dispatch-outcome predicate (upsert, not append)", i, call.add)
		}
	}
	obj, _ := w.calls[1].add[0].Object.(string)
	if !strings.Contains(obj, "boom-2") {
		t.Errorf("second stamp %q must carry the LATEST error (replace, not append)", obj)
	}
}

// TestSuccessPathStampsNothing pins the fail-closed posture the spec makes
// explicit: a SUCCESSFUL Handle stamps neither a failure fact nor any success
// fact — the station's own completion facts alone drive the arc, so a harness
// fault can never read as completion (G3).
func TestSuccessPathStampsNothing(t *testing.T) {
	h := &captureHandler{}
	w := &captureWriter{}
	c := newTestComponentWithWriter(h, w)
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))

	if len(h.reqs) != 1 {
		t.Fatalf("handler called %d times, want 1", len(h.reqs))
	}
	if len(w.calls) != 0 {
		t.Errorf("ReplaceOwned called %d times on SUCCESS, want 0 (no harness outcome fact, ever)", len(w.calls))
	}
}

// TestNilWriterStaysLogOnly is D1's explicit unit-test tolerance: a direct-
// constructed Component with no writer keeps today's log-only exhaustion
// behavior — no panic, the error metered once (New forbids this shape for
// booted stations; see TestNewRejectsNilFactWriter).
func TestNilWriterStaysLogOnly(t *testing.T) {
	h := &captureHandler{err: errors.New("boom")}
	c := newTestComponent(h) // no writer
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))
	if n := atomic.LoadInt64(&c.errors); n != 1 {
		t.Errorf("error count = %d, want 1 (metered once, log-only)", n)
	}
}

// TestShutdownAbortDoesNotStampDispatchFailed guards the false-terminal edge:
// a Stop-cancelled context drains the retry loop EARLY, so the bounded retries
// were NOT exhausted — stamping station.dispatch.failed there would park a run
// over a process shutdown, not a station failure. The spec's trigger is
// "fails after its bounded retries"; a shutdown abort stays unstamped (the
// restart half of the wedge is R8/group 8, upstream-blocked, tripwired).
func TestShutdownAbortDoesNotStampDispatchFailed(t *testing.T) {
	h := &captureHandler{err: errors.New("boom")}
	w := &captureWriter{}
	c := newTestComponentWithWriter(h, w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.baseCtx = ctx
	c.handleMessage("component.test-station.dispatch", []byte(`{"entity_id":"org.plat.agent.chain.execution.run-1","properties":{}}`))

	if len(h.reqs) != 1 {
		t.Fatalf("handler called %d times under a cancelled context, want 1 (no retry during shutdown)", len(h.reqs))
	}
	if len(w.calls) != 0 {
		t.Errorf("ReplaceOwned called %d times on a shutdown abort, want 0 (retries not exhausted — no false terminal)", len(w.calls))
	}
}

func TestDefaultPortsSubjectMatchesPrefix(t *testing.T) {
	ports := DefaultPorts("floors-station")
	if len(ports.Inputs) != 1 {
		t.Fatalf("DefaultPorts inputs = %d, want 1", len(ports.Inputs))
	}
	want := SubjectPrefix + "floors-station.>"
	np, ok := ports.Inputs[0].Config.(component.NATSPort)
	if !ok {
		t.Fatalf("dispatch port config = %T, want component.NATSPort (core NATS)", ports.Inputs[0].Config)
	}
	if np.Subject != want {
		t.Errorf("dispatch subject = %q, want %q (the rule publishes component.<name>.dispatch, the wildcard matches)", np.Subject, want)
	}
}

// Compile-time proof the generic Component satisfies the framework contracts even
// as the concrete stations only supply a Handler.
var _ component.LifecycleComponent = (*Component)(nil)
