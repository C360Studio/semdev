package mockllm

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"testing"

	ssmock "github.com/c360studio/semstreams/test/e2e/mock"
)

// chatComplete posts an OpenAI-shaped request to the harness and decodes the
// reply. It mirrors what the agentic-model client sends on the wire.
func chatComplete(t *testing.T, h *Harness, req ssmock.ChatCompletionRequest) ssmock.ChatCompletionResponse {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(h.Endpoint()+"/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out ssmock.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func userReq(marker string, tools ...ssmock.Tool) ssmock.ChatCompletionRequest {
	return ssmock.ChatCompletionRequest{
		Model:    "mock",
		Messages: []ssmock.ChatMessage{{Role: "user", Content: marker}},
		Tools:    tools,
	}
}

// The harness must replay a completion fixture deterministically: the same
// marker selects the same content on every call, and every call is accounted.
func TestFixtureReplayIsDeterministic(t *testing.T) {
	const marker = "PLAN THE CHANGE"
	const content = `{"summary":"draft openspec change from the intaken issue"}`

	h := New(Fixture{Marker: marker, Content: content})
	if err := h.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Stop()

	for i := 1; i <= 3; i++ {
		got := chatComplete(t, h, userReq(marker))
		if len(got.Choices) != 1 {
			t.Fatalf("call %d: choices = %d, want 1", i, len(got.Choices))
		}
		if got.Choices[0].Message.Content != content {
			t.Fatalf("call %d: content = %q, want %q", i, got.Choices[0].Message.Content, content)
		}
		if h.RequestCount() != i {
			t.Fatalf("call %d: RequestCount = %d, want %d", i, h.RequestCount(), i)
		}
	}
}

// An unmatched completion turn must return the loud sentinel, not the framework
// mock's plausible-looking default content — so a mistyped or unscripted marker
// fails greppably in the journey instead of passing green with alien output.
func TestUnmatchedCompletionHitsSentinel(t *testing.T) {
	h := New(Fixture{Marker: "PLAN THE CHANGE", Content: `{"summary":"ok"}`})
	if err := h.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Stop()

	got := chatComplete(t, h, userReq("A MARKER THAT MATCHES NOTHING"))
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(got.Choices))
	}
	if got.Choices[0].Message.Content != UnmatchedSentinel {
		t.Fatalf("content = %q, want sentinel %q", got.Choices[0].Message.Content, UnmatchedSentinel)
	}
}

// A tool fixture must emit a scripted tool call when the request advertises the
// tool, so the spine can drive structured turns (e.g. create_change) mock-only.
func TestToolFixtureEmitsScriptedCall(t *testing.T) {
	const marker = "AUTHOR THE CHANGE"
	tool := ssmock.Tool{Type: "function", Function: ssmock.FunctionDef{Name: "create_change"}}

	h := New(Fixture{
		Marker: marker,
		Tool:   &ToolCall{Name: "create_change", Args: map[string]any{"slug": "fix-null-deref"}},
	})
	if err := h.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Stop()

	got := chatComplete(t, h, userReq(marker, tool))
	if len(got.Choices) != 1 || len(got.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected exactly one scripted tool call, got %+v", got.Choices)
	}
	call := got.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "create_change" {
		t.Fatalf("tool name = %q, want create_change", call.Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		t.Fatalf("tool args not valid JSON: %v", err)
	}
	if args["slug"] != "fix-null-deref" {
		t.Fatalf("tool args = %v, want slug=fix-null-deref", args)
	}
}

// The zero-token guarantee is structural: the endpoint must be loopback. A
// loopback in-process server reaches no paid provider and needs no API key, so
// the whole mock ladder spends zero tokens by construction (G6).
func TestEndpointIsLoopbackZeroToken(t *testing.T) {
	h := New(Fixture{Marker: "x", Content: "y"})
	if err := h.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Stop()

	u, err := url.Parse(h.Endpoint())
	if err != nil {
		t.Fatalf("parse endpoint %q: %v", h.Endpoint(), err)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host %q: %v", u.Host, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Fatalf("endpoint host %q is not loopback — a non-loopback mock could reach a paid provider", host)
	}
}
