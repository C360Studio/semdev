// Package station is semdev's generic PUBLISH-TRIGGERED deterministic-station
// component (design simplify-m0-execution-rail R6, group 6). It is a registered
// semstreams processor that subscribes to a rule-`publish`ed core-NATS subject
// (component.<station>.>), decodes the reference-only dispatch envelope, and
// hands off to a station-specific Handler that does DETERMINISTIC Go work and
// stamps its own facts — with ZERO model turns.
//
// WHY (G1 + R6): the deterministic stations (floors, cold verify, delivery, task
// projection, change validation, sandbox provisioning) used to ride a FORCED
// single-turn coordinator loop — a `publish_agent` with tool_choice=function that
// spawned an agentic loop whose only job was to call one harness tool and
// StopLoop. Under a real LLM every such turn is a paid model call that decides
// nothing (the outcome is harness-derived, G3). The framework has no primitive
// that runs semdev's deterministic work off a fact: a rule can ROUTE facts but
// cannot invoke Go, and `publish_agent` burns a turn. The framework-aligned answer
// is the gated-DAG publish→component pattern (this package) — the same idiom
// processor/research-graph-route uses: R fires a plain `publish` action, the
// rule-engine's publisher emits it (core NATS, since semdev's rule component
// declares no matching JetStream output port), and a registered processor
// subscribing the subject turns the reference into work. Each concrete station
// registers with its OWN name + Handler and carries a registry.Entry alignment
// note (G1).
//
// TRANSPORT (M0): the dispatch rides CORE NATS (fire-and-forget, at-most-once).
// In the single-process M0 runtime every station component subscribes at Start,
// long before any run reaches it, so a normal dispatch is always delivered. The
// crash-in-the-publish→handle-window durability the old `publish_agent` inherited
// from the AGENT JetStream stream is NOT preserved here; restart-safe
// reconstruction of in-flight effects is design R8 (group 8), which rebuilds a
// station's inputs from durable facts rather than relying on a queued message.
//
// FAILURE POSTURE (honest, M0): a station stamps NO success fact until its work
// succeeds — so it never false-greens a run (fail-closed against a wrong outcome).
// The generic base RETRIES a failing handler a bounded number of times (station
// handlers are idempotent — the harness fact writers are replace-by-predicate /
// exact-triple-deduped), which restores the transient-fault resilience the forced
// coordinator loop had (its tool_choice=function loop re-called the tool on a tool
// error). But a station rule stamps its self-extinguish marker BEFORE the publish
// (restart dup-safety), and rules are EDGE-triggered on entity-state changes, so a
// PERSISTENT handler fault that stamps nothing leaves the run with the marker set,
// the success fact absent, and no triple change to re-trigger any rule: the run
// does NOT auto-park at M0. Closing that "routed-without-result" wedge (reconcile
// marker-set-∧-result-absent → park, plus effect idempotency on replay) is design
// R8 / group 8, pinned as a known gap (test/conformance rules_test.go). This base
// does not claim otherwise.
package station

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
)

// SubjectPrefix is the NATS namespace every station dispatch rides. A rule
// publishes to "component.<station>.dispatch" and the matching component
// subscribes "component.<station>.>". Kept as one const so the rule-side subject
// and the component-side subscription cannot drift by a typo.
const SubjectPrefix = "component."

// DispatchLeaf is the fixed final token a station rule publishes to
// ("component.<station>.dispatch"). The dispatch carries no data in the subject
// — the firing entity and the rule's substituted properties travel in the
// payload (see Request) — so the leaf is a constant, not an entity id (entity ids
// carry dots/colons that would fragment a NATS subject).
const DispatchLeaf = "dispatch"

// maxHandleAttempts bounds how many times the base re-runs a failing Handler
// before giving up (transient graph/exec faults self-heal; a persistent fault
// stops here and is metered). It restores the transient resilience the forced
// tool_choice=function loop had. Handlers are idempotent, so a retry is safe.
const maxHandleAttempts = 3

// handleRetryBackoff is the base inter-attempt delay (scaled by attempt number).
// Kept short so a failing dispatch does not stall its subscription's serial
// dispatcher for long; Stop's context-cancel breaks the wait immediately.
const handleRetryBackoff = 200 * time.Millisecond

// Request is the decoded rule-`publish` dispatch envelope a Handler receives. It
// is the whole channel from the rule to the station: the firing entity plus the
// rule's substituted string properties.
type Request struct {
	// EntityID is the FIRING entity of the rule that published this dispatch
	// (the rule engine's executePublish stamps it as payload.entity_id): for a
	// run-fired station rule it is the run entity; for a loop-fired rule (the
	// floors station fires on the developer-loop terminal) it is that loop
	// entity. A Handler stamps its run-level facts on the run and any loop-scoped
	// facts (e.g. the floors route mirror) on this entity.
	EntityID string
	// Properties are the rule's substituted `publish` properties — the rule
	// templates cross-entity values a station needs but the firing entity does
	// not carry (e.g. run_entity_id when the rule fires on a loop, or the change
	// slug). Only string values survive: the rule engine substitutes only string
	// properties, so a Handler reads exactly what the rule authored.
	Properties map[string]string
	// Subject is the concrete subject the dispatch arrived on (component.<name>.…).
	Subject string
}

// Prop returns the named string property, or "" if absent.
func (r Request) Prop(key string) string { return r.Properties[key] }

// Handler runs one station's deterministic work for a single dispatch and stamps
// its facts. It replaces a forced coordinator turn's tool body. A returned error
// triggers a bounded retry (see the base's FAILURE POSTURE); after the retries it
// is LOGGED and metered but not propagated to NATS (the publisher is
// fire-and-forget). The station stays fail-closed by stamping no success fact —
// but at M0 a persistent fault does NOT itself park the run (edge-triggered rules;
// the routed-without-result reconciliation is R8/group 8). Handle MUST be safe to
// run again on a retry or redelivery (the harness fact writers are replace-by-
// predicate / exact-triple-deduped), the same idempotency the tool bodies held —
// the base relies on it to retry.
type Handler interface {
	// Handle does the work and returns an error only for logging/metrics/retry.
	Handle(ctx context.Context, req Request) error
}

// dispatchEnvelope mirrors the rule engine's executePublish payload
// (processor/rule/actions.go): a stable JSON shape of {entity_id, properties, …}.
// Only the two fields a station reads are decoded; the rest (subject, timestamp,
// source, related_id) are ignored.
type dispatchEnvelope struct {
	EntityID   string         `json:"entity_id"`
	Properties map[string]any `json:"properties"`
}

// Config is the shared config shape every station component parses. A station
// declares exactly one core-NATS input port (component.<name>.>); the component
// subscribes it at Start. No other knobs at M0 — the work's own internal timeouts
// (docker exec, CLI) bound each station, and Stop's context-cancel drains an
// in-flight handler.
type Config struct {
	Ports *component.PortConfig `json:"ports,omitempty" schema:"type:ports,description:Port configuration. A station declares one nats input subscribing to component.<name>.> ,category:basic"`
}

// Validate requires at least one input port — a station with no subscription
// would start healthy and silently never fire, the exact silent-flow-break the
// constitution bars.
func (c *Config) Validate() error {
	if c.Ports == nil || len(c.Ports.Inputs) == 0 {
		return errs.WrapInvalid(errs.ErrInvalidConfig, "station", "Validate", "ports configuration with at least one input port is required")
	}
	return nil
}

// Schema is the generated config schema shared by every station registration.
var Schema = component.GenerateConfigSchema(reflect.TypeOf(Config{}))

// Component is the generic station processor. It owns the framework boilerplate
// — Discoverable + LifecycleComponent, the core-NATS subscription, the dispatch
// decode, health/flow counters — so each concrete station is only a Handler.
type Component struct {
	name    string
	config  Config
	handler Handler
	nats    *natsclient.Client
	logger  *slog.Logger

	// One mutex guards the lifecycle flag + startTime + the base context so a
	// concurrent Health/DataFlow read cannot see a torn read (mirrors the framework
	// processors' shape).
	mu        sync.RWMutex
	started   bool
	startTime time.Time
	wg        sync.WaitGroup

	// baseCtx is the COMPONENT-LIFETIME context every handler runs under —
	// cancelled at Stop so an in-flight handler drains, but NOT bounded by the
	// framework's 30s per-message subscription context (a cold docker build or
	// container up runs for minutes; the 30s cap would kill a healthy slow build
	// mid-flight). Created in Start, cancelled in Stop.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	// NATS subscriptions kept for cleanup at Stop.
	subscriptions []*natsclient.Subscription

	// Atomic counters for DataFlow / observability.
	messagesProcessed int64
	errors            int64
	lastActivity      atomic.Value // time.Time
}

// Ensure interface compliance at build time.
var (
	_ component.Discoverable       = (*Component)(nil)
	_ component.LifecycleComponent = (*Component)(nil)
)

// New builds a station Component. name is the registered component name (also the
// component.<name>.> namespace); handler is the station's work body; nats is the
// live client the component subscribes on (nil is rejected — a station cannot
// subscribe without one, and a silent no-subscription start is the flow break the
// constitution bars). The factory of each concrete station calls this after
// building its Handler from the framework + semdev dependencies.
func New(name string, config Config, handler Handler, nats *natsclient.Client, logger *slog.Logger) (*Component, error) {
	if name == "" {
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, "station", "New", "component name is required")
	}
	if handler == nil {
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, name, "New", "handler is required")
	}
	if nats == nil {
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, name, "New", "NATS client is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Component{name: name, config: config, handler: handler, nats: nats, logger: logger.With(slog.String("component", name))}, nil
}

// Initialize is part of the LifecycleComponent contract. A station has nothing to
// initialise pre-Start — the subscription is wired in Start so a subscribe fault
// fails loudly there.
func (c *Component) Initialize() error { return nil }

// Start creates the component-lifetime handler context, subscribes to every
// configured core-NATS input port, and marks the component started. Each dispatch
// runs synchronously in its subscription's dispatcher goroutine (tracked by wg so
// Stop drains in-flight work).
func (c *Component) Start(ctx context.Context) error {
	if ctx == nil {
		return errs.WrapInvalid(errs.ErrInvalidConfig, c.name, "Start", "context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return errs.WrapInvalid(err, c.name, "Start", "context already cancelled")
	}

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errs.WrapFatal(errs.ErrAlreadyStarted, c.name, "Start", "already started")
	}
	// The component-lifetime handler context: independent of the framework's
	// 30s-bounded per-message subscription context, cancelled only at Stop.
	c.baseCtx, c.baseCancel = context.WithCancel(context.Background())
	c.mu.Unlock()

	if err := c.subscribeInputs(ctx); err != nil {
		return err
	}

	c.mu.Lock()
	c.started = true
	c.startTime = time.Now()
	c.mu.Unlock()

	c.logger.Info("station component started", slog.Int("input_ports", len(c.config.Ports.Inputs)))
	return nil
}

// subscribeInputs wires a core-NATS subscription for each nats input port.
func (c *Component) subscribeInputs(ctx context.Context) error {
	for _, port := range c.config.Ports.Inputs {
		if port.Subject == "" {
			continue
		}
		if port.Type != "nats" {
			c.logger.Warn("unsupported station port type; skipping", slog.String("port", port.Name), slog.String("type", port.Type))
			continue
		}
		// The callback's msgCtx is the framework's 30s-bounded per-message context;
		// handleMessage deliberately runs the handler under the component-lifetime
		// baseCtx instead (a docker station runs longer than 30s), so msgCtx is not
		// forwarded — it would silently cap a healthy slow build.
		sub, err := c.nats.Subscribe(ctx, port.Subject, func(_ context.Context, msg *nats.Msg) {
			c.handleMessage(msg.Subject, msg.Data)
		})
		if err != nil {
			return errs.WrapTransient(err, c.name, "Start", fmt.Sprintf("subscribe to %s", port.Subject))
		}
		c.subscriptions = append(c.subscriptions, sub)
		c.logger.Debug("subscribed to station subject", slog.String("port", port.Name), slog.String("subject", port.Subject))
	}
	return nil
}

// handleMessage decodes the dispatch envelope and runs the station handler (with
// a bounded retry) under the component-lifetime context. It runs synchronously in
// the subscription's serial dispatcher goroutine — there is no per-dispatch
// goroutine — so wg tracks it for Stop's drain.
func (c *Component) handleMessage(subject string, data []byte) {
	// wg.Add is the first statement so Stop's drain (Unsubscribe → wg.Wait) counts
	// this dispatch. A vanishingly small window exists between NATS invoking this
	// callback and this Add executing (Unsubscribe stops NEW callbacks but does not
	// join an already-dispatched one); it is unreachable at M0 (Stop runs at
	// shutdown, after the single serialized run has drained). If concurrent runs
	// land, move the accounting ahead of dispatch.
	c.wg.Add(1)
	defer c.wg.Done()
	// A Handler panic must not crash the runtime (a docker-exec'ing station is the
	// likely source). Recover it into a metered error — the station stays
	// fail-closed (no success fact) rather than taking the process down.
	defer func() {
		if r := recover(); r != nil {
			atomic.AddInt64(&c.errors, 1)
			c.logger.Error("station handler panicked; recovered (run fails closed, no success fact)",
				slog.String("subject", subject), slog.Any("panic", r))
		}
	}()
	atomic.AddInt64(&c.messagesProcessed, 1)
	c.lastActivity.Store(time.Now())

	var env dispatchEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.logger.Error("station: could not decode dispatch envelope; ignoring", slog.String("subject", subject), slog.Any("error", err))
		atomic.AddInt64(&c.errors, 1)
		return
	}
	if env.EntityID == "" {
		c.logger.Error("station: dispatch envelope has no entity_id; ignoring", slog.String("subject", subject))
		atomic.AddInt64(&c.errors, 1)
		return
	}

	req := Request{EntityID: env.EntityID, Properties: stringProps(env.Properties), Subject: subject}
	if err := c.runHandler(req); err != nil {
		// Fire-and-forget: the error cannot propagate to the publisher. After the
		// bounded retries, log + meter it. The station stamped no success fact
		// (fail-closed, never a false green) — but at M0 a persistent fault does
		// NOT itself park the run (edge-triggered rules; the marker is already set):
		// the routed-without-result reconciliation is R8/group 8 (see the package
		// doc + the pinned known-gap test).
		c.logger.Error("station handler failed after retries; run does not advance (no auto-park at M0 — R8/group 8 owns the routed-without-result reconciliation)",
			slog.String("entity_id", env.EntityID), slog.String("subject", subject), slog.Any("error", err))
		atomic.AddInt64(&c.errors, 1)
		return
	}
	c.logger.Debug("station handled dispatch", slog.String("entity_id", env.EntityID))
}

// runHandler runs the handler under the component-lifetime context, retrying a
// transient failure up to maxHandleAttempts (handlers are idempotent). It stops
// early — without retrying — once the base context is cancelled (Stop in flight),
// so a shutdown drains promptly rather than burning the retry budget.
func (c *Component) runHandler(req Request) error {
	ctx := c.handlerCtx()
	var err error
	for attempt := 1; attempt <= maxHandleAttempts; attempt++ {
		if err = c.handler.Handle(ctx, req); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err // shutting down — do not retry
		}
		if attempt < maxHandleAttempts {
			c.logger.Warn("station handler failed; retrying (idempotent)",
				slog.String("entity_id", req.EntityID), slog.Int("attempt", attempt), slog.Any("error", err))
			select {
			case <-time.After(handleRetryBackoff * time.Duration(attempt)):
			case <-ctx.Done():
				return err
			}
		}
	}
	return err
}

// handlerCtx returns the component-lifetime context under a read lock (Stop swaps
// nothing but cancels it; the field is set once in Start). A nil baseCtx (a test
// bypassing Start) degrades to Background.
func (c *Component) handlerCtx() context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.baseCtx != nil {
		return c.baseCtx
	}
	return context.Background()
}

// stringProps narrows the decoded property map to string values (the only kind
// the rule engine substitutes), dropping any non-string entry.
func stringProps(in map[string]any) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// Stop drains in-flight handlers (bounded by timeout), unsubscribes, and flips
// started under c.mu.
func (c *Component) Stop(timeout time.Duration) error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = false
	cancel := c.baseCancel
	c.mu.Unlock()

	// Cancel the component-lifetime context FIRST so an in-flight handler (and its
	// retry wait) unwinds promptly, then unsubscribe and drain the wg.
	if cancel != nil {
		cancel()
	}

	for _, sub := range c.subscriptions {
		if sub == nil {
			continue
		}
		if err := sub.Unsubscribe(); err != nil {
			c.logger.Debug("station unsubscribe failed during stop", slog.Any("error", err))
		}
	}
	c.subscriptions = nil

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		c.logger.Warn("station Stop timeout reached with handlers in flight", slog.Duration("timeout", timeout))
	}
	return nil
}

// Meta implements Discoverable.
func (c *Component) Meta() component.Metadata {
	return component.Metadata{Name: c.name, Type: "processor", Description: "semdev deterministic station (publish-triggered component, R6)", Version: "0.1.0"}
}

// InputPorts implements Discoverable.
func (c *Component) InputPorts() []component.Port {
	ports := make([]component.Port, 0, len(c.config.Ports.Inputs))
	for _, p := range c.config.Ports.Inputs {
		ports = append(ports, component.Port{
			Name:      p.Name,
			Direction: component.DirectionInput,
			Required:  p.Required,
			Config:    component.NATSPort{Subject: p.Subject},
		})
	}
	return ports
}

// OutputPorts implements Discoverable. A station emits no NATS output — it stamps
// facts via graph.mutation.* inside its Handler.
func (c *Component) OutputPorts() []component.Port { return []component.Port{} }

// ConfigSchema implements Discoverable.
func (c *Component) ConfigSchema() component.ConfigSchema { return Schema }

// Health implements Discoverable.
func (c *Component) Health() component.HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return component.HealthStatus{
		Healthy:    c.started,
		LastCheck:  time.Now(),
		ErrorCount: int(atomic.LoadInt64(&c.errors)),
		Uptime:     time.Since(c.startTime),
	}
}

// DataFlow implements Discoverable.
func (c *Component) DataFlow() component.FlowMetrics {
	processed := atomic.LoadInt64(&c.messagesProcessed)
	errCount := atomic.LoadInt64(&c.errors)
	var errRate float64
	if processed > 0 {
		errRate = float64(errCount) / float64(processed)
	}
	last, _ := c.lastActivity.Load().(time.Time)
	return component.FlowMetrics{ErrorRate: errRate, LastActivity: last}
}

// DefaultPorts builds the standard single-input PortConfig for a station whose
// dispatch subject is component.<name>.>. Concrete-station DefaultConfig helpers
// and the bootstrap config both build from this so the subject cannot drift.
func DefaultPorts(name string) *component.PortConfig {
	return &component.PortConfig{
		Inputs: []component.PortDefinition{{
			Name:        "dispatch",
			Type:        "nats",
			Subject:     SubjectPrefix + name + ".>",
			Required:    true,
			Description: "The station rule's publish target; the firing entity + properties travel in the payload.",
		}},
		Outputs: []component.PortDefinition{},
	}
}
