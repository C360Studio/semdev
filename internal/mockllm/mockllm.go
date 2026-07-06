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
}

// New builds a mock-LLM harness that replays the given fixtures. Completion
// fixtures are matched by marker (first match wins); tool fixtures are consumed
// as a positional sequence in the order given — see Fixture for the full
// contract. Any completion turn that matches no fixture returns UnmatchedSentinel.
func New(fixtures ...Fixture) *Harness {
	srv := ssmock.NewOpenAIServer().WithCompletionContent(UnmatchedSentinel)

	var completions []ssmock.RoleResponse
	var toolCalls []ssmock.RoleToolCall
	for _, f := range fixtures {
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
	return &Harness{srv: srv}
}

// Start binds the harness to a random free port on loopback. The zero-token
// guarantee rests on this: a 127.0.0.1 in-process server reaches no paid
// provider. Callers can confirm it via Endpoint, which always targets loopback.
func (h *Harness) Start() error {
	return h.srv.Start("127.0.0.1:0")
}

// Stop shuts the harness down gracefully.
func (h *Harness) Stop() error {
	return h.srv.Stop()
}

// Endpoint is the OpenAI-compatible base URL to hand the agentic-model endpoint
// config (its "url" field), e.g. "http://127.0.0.1:PORT/v1". Always loopback.
func (h *Harness) Endpoint() string {
	return h.srv.URL() + "/v1"
}

// RequestCount is the number of chat-completion requests served — the accounting
// hook for "every model call in this run hit the mock, none hit a paid provider".
func (h *Harness) RequestCount() int {
	return h.srv.RequestCount()
}
