// Package mockllm is semdev's deterministic mock-LLM harness (port S6). It fronts
// the agentic-model endpoint with an in-process, OpenAI-compatible HTTP server
// that replays scripted fixtures. The harness itself reaches no provider and
// carries no API key — it binds only a loopback listener — so it spends zero paid
// tokens by construction (G6). Run-level zero-token additionally requires the
// journey to point the agentic-model config at Endpoint() with no fallback
// provider, and to assert RequestCount equals the expected number of turns.
//
// It wraps semstreams' test/e2e/mock.OpenAIServer; semdev owns the fixture shape
// and the zero-token guarantee. This is test-harness infrastructure, not a
// product component — it takes no G1 registry entry and writes no facts.
package mockllm

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	ssmock "github.com/c360studio/semstreams/test/e2e/mock"
)

// UnmatchedSentinel is the completion content returned for any completion turn
// that matches no fixture Marker. It is deliberately not valid model output, so
// an unscripted or mistyped turn fails loudly and greppably in the journey
// instead of silently returning the framework mock's default sensor JSON.
const UnmatchedSentinel = "__MOCKLLM_UNMATCHED_MARKER__"

// Fixture is one deterministic model turn. There are two kinds, matched by
// different mechanisms in the underlying mock — mind the difference:
//
//   - Completion fixture (Tool == nil): marker-keyed. The mock scans ALL
//     completion fixtures in declaration order and returns the Content of the
//     first whose Marker is a substring of any single system or user message
//     (case-sensitive, per-message — a marker cannot span a system→user
//     boundary). An unmatched completion turn returns UnmatchedSentinel.
//
//   - Tool fixture (Tool != nil): a positional SEQUENCE, not a keyed map. Tool
//     fixtures are consumed against an advancing cursor in declaration order —
//     the Nth tool-call turn is matched against the Nth tool fixture only.
//     Marker is a GUARD, not a lookup key: it asserts the expected turn arrived,
//     and the request must also advertise Tool.Name. On a marker miss OR an
//     unadvertised tool, the mock does NOT error — it falls through to its own
//     heuristic (the first advertised tool with empty args, else a completion).
//     So order tool fixtures to match the journey's turn order, and have the
//     journey assert RequestCount and the returned tool args so a misfire
//     surfaces rather than passing green with the wrong call.
type Fixture struct {
	// Marker is a substring searched for in each system/user message. For a
	// completion fixture it selects the turn (first match wins); for a tool
	// fixture it guards the cursor's current entry. Keep markers specific.
	Marker string
	// Content is the completion text returned when a completion fixture's Marker
	// matches. Ignored when Tool is set.
	Content string
	// Tool, when non-nil, makes this a tool fixture: the turn emits a scripted
	// tool call (see the sequence semantics above) instead of completion text.
	Tool *ToolCall
	// Error, when true, makes this an ERROR fixture: any chat-completion request whose
	// body contains Marker gets an HTTP 500 (an infrastructure failure), which the
	// agentic-model client maps to a StatusError response and the loop engine classifies as
	// an agent.loop.terminal-reason="model_error" terminal — the transient-failure path
	// (adopt-reason-aware-escalate). This is a MARKER-KEYED gate (not a positional cursor
	// entry): EVERY request matching Marker 500s (including the client's retries), and none
	// is forwarded to the underlying server, so it does not consume a tool/completion turn.
	// Choose Marker unique to the turn you want to fail (e.g. a phrase in the initial
	// dispatch prompt but NOT the transient-retry prompt, else the retry loop 500s too).
	Error bool
}

// ToolCall scripts a single tool invocation the mock emits for a matching turn.
type ToolCall struct {
	// Name is the function name to call; the request must advertise it.
	Name string
	// Args is serialized to JSON as the call arguments. A nil map is treated as
	// an empty object ("{}").
	Args map[string]any
}

// Harness is a running mock-LLM endpoint. Point the agentic-model endpoint's URL
// at Endpoint(); the harness needs no provider and no API key.
type Harness struct {
	srv *ssmock.OpenAIServer
	// errorMarkers are the Error-fixture markers (see Fixture.Error). When non-empty, Start
	// runs a thin reverse-proxy IN FRONT of srv that returns HTTP 500 for any request body
	// containing one of these markers and forwards everything else — so semdev can exercise
	// the model_error terminal without an upstream ssmock change. Endpoint returns the proxy.
	errorMarkers []string
	proxy        *http.Server
	proxyURL     string
}

// New builds a mock-LLM harness that replays the given fixtures. Completion
// fixtures are matched by marker (first match wins); tool fixtures are consumed
// as a positional sequence in the order given — see Fixture for the full
// contract. Any completion turn that matches no fixture returns UnmatchedSentinel.
func New(fixtures ...Fixture) *Harness {
	srv := ssmock.NewOpenAIServer().WithCompletionContent(UnmatchedSentinel)

	var completions []ssmock.RoleResponse
	var toolCalls []ssmock.RoleToolCall
	var errorMarkers []string
	for _, f := range fixtures {
		if f.Error {
			errorMarkers = append(errorMarkers, f.Marker)
			continue
		}
		if f.Tool != nil {
			args := f.Tool.Args
			if args == nil {
				args = map[string]any{}
			}
			toolCalls = append(toolCalls, ssmock.RoleToolCall{
				Marker:   f.Marker,
				ToolName: f.Tool.Name,
				Args:     args,
			})
			continue
		}
		completions = append(completions, ssmock.RoleResponse{
			Marker:  f.Marker,
			Content: f.Content,
		})
	}
	if len(completions) > 0 {
		srv.WithRoleResponses(completions)
	}
	if len(toolCalls) > 0 {
		srv.WithRoleToolCallSequence(toolCalls)
	}
	return &Harness{srv: srv, errorMarkers: errorMarkers}
}

// Start binds the harness to a random free port on loopback. The zero-token
// guarantee rests on this: a 127.0.0.1 in-process server reaches no paid
// provider. Callers can confirm it via Endpoint, which always targets loopback.
// When Error fixtures are present, Start also binds a thin reverse-proxy in front
// (also loopback) that 500s any request whose body contains an error marker.
func (h *Harness) Start() error {
	if err := h.srv.Start("127.0.0.1:0"); err != nil {
		return err
	}
	if len(h.errorMarkers) == 0 {
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("mockllm: bind error-proxy listener: %w", err)
	}
	h.proxyURL = "http://" + ln.Addr().String()
	h.proxy = &http.Server{Handler: h.errorProxyHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = h.proxy.Serve(ln) }()
	return nil
}

// errorProxyHandler returns 500 for any request whose body contains an error marker
// (the model_error terminal path), and forwards everything else verbatim to the wrapped
// ssmock server — so a 500'd turn (and the client's retries of it) never reaches ssmock and
// does not consume a scripted turn.
func (h *Harness) errorProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		for _, m := range h.errorMarkers {
			if m != "" && strings.Contains(string(body), m) {
				http.Error(w, `{"error":{"message":"mockllm injected transient failure","type":"server_error"}}`, http.StatusInternalServerError)
				return
			}
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, h.srv.URL()+r.URL.Path, bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

// Stop shuts the harness (and the error-proxy, if any) down gracefully.
func (h *Harness) Stop() error {
	if h.proxy != nil {
		_ = h.proxy.Close()
	}
	return h.srv.Stop()
}

// Endpoint is the OpenAI-compatible base URL to hand the agentic-model endpoint
// config (its "url" field), e.g. "http://127.0.0.1:PORT/v1". Always loopback. Returns
// the error-proxy URL when Error fixtures are present, else the ssmock server directly.
func (h *Harness) Endpoint() string {
	if h.proxyURL != "" {
		return h.proxyURL + "/v1"
	}
	return h.srv.URL() + "/v1"
}

// RequestCount is the number of chat-completion requests served — the accounting
// hook for "every model call in this run hit the mock, none hit a paid provider".
func (h *Harness) RequestCount() int {
	return h.srv.RequestCount()
}
