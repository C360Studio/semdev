// Package semsourceproxy is the four read-only proxy tools over semsource's
// public HTTP read surface (integrate-semsource-ab-harness, D1): code_context,
// code_impact, code_search, doc_context — named exactly as semsource's product
// surface names them, so semsource's own docs and prompts transfer verbatim.
//
// ALWAYS registered, CONDITIONALLY advertised (the D1/D2 asymmetry): boot
// registers this executor unconditionally — schema-only with a nil querier
// when no semsource endpoint is configured, failing loudly if executed (the
// github_list_comments precedent) — so the G3 schema census sees every schema.
// The CONDITION controls advertisement via the variant dispatch pack: a
// baseline loop never advertises these tools and (post-#551) cannot call
// them; only the semsource-condition variant pack appends them to the
// developer's allowlist.
//
// Read-only by construction: no facts are stamped (no G5 writer), the schemas
// take a single query parameter (no outcome fields — G3), and results return
// to the loop as tool content. A mid-run semsource fault surfaces as a loud
// tool errResult in the trajectory (D4) — never an empty success, never a
// silent fallback (the loop's tool set cannot mutate mid-run).
package semsourceproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/forge/semsource"
	"github.com/c360studio/semstreams/agentic"
)

// The four tool names — semsource's product-surface names, verbatim.
const (
	ToolCodeContext = "code_context"
	ToolCodeImpact  = "code_impact"
	ToolCodeSearch  = "code_search"
	ToolDocContext  = "doc_context"
)

// ToolNames is the complete advertised set the semsource-condition variant
// pack appends to the developer allowlist — the parity pin's tools delta.
var ToolNames = []string{ToolCodeContext, ToolCodeImpact, ToolCodeSearch, ToolDocContext}

// routes maps each tool to its semsource HTTP verb route.
var routes = map[string]string{
	ToolCodeContext: semsource.RouteCodeContext,
	ToolCodeImpact:  semsource.RouteCodeImpact,
	ToolCodeSearch:  semsource.RouteCodeSearch,
	ToolDocContext:  semsource.RouteDocContext,
}

// querier is the slice of the semsource client the proxies need.
type querier interface {
	Query(ctx context.Context, route, query string) ([]byte, error)
}

// Executor proxies the four semsource read tools.
type Executor struct {
	q      querier
	logger *slog.Logger
}

// New builds the proxy executor. q may be nil for schema-only registration
// (baseline boots and the censuses — pass a LITERAL nil, never a typed-nil
// *semsource.Client); Execute fails loudly if it is nil.
func New(q querier, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{q: q, logger: logger}
}

// payload is the shared input: one free-form query string (G3 — a query,
// never an outcome).
type payload struct {
	Query string `json:"query"`
}

// ListTools returns the four schemas. Descriptions mirror semsource's own
// MCP tool prose so its docs transfer.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	param := func(desc string) map[string]any {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": desc},
			},
			"required": []string{"query"},
		}
	}
	return []agentic.ToolDefinition{
		{
			Name:        ToolCodeContext,
			Effect:      agentic.ToolEffectReadOnly,
			Description: "Fused code answer from the semsource knowledge graph: the resolved symbol, its verbatim body, and its callers/callees — 'show me this code and how it connects'. Read-only.",
			Parameters:  param("The query — a symbol name (e.g. RunFloors)."),
		},
		{
			Name:        ToolCodeImpact,
			Effect:      agentic.ToolEffectReadOnly,
			Description: "Reverse-dependency closure of a symbol from the semsource knowledge graph: what depends on it — what would break if you change it. A query grep cannot answer. Read-only.",
			Parameters:  param("The query — a symbol name whose dependents to find."),
		},
		{
			Name:        ToolCodeSearch,
			Effect:      agentic.ToolEffectReadOnly,
			Description: "Semantic natural-language discovery over the semsource-indexed code — 'where is the retry-with-backoff logic' — returning matching symbols and bodies. Read-only.",
			Parameters:  param("The query — a natural-language phrase describing the code to find."),
		},
		{
			Name:        ToolDocContext,
			Effect:      agentic.ToolEffectReadOnly,
			Description: "Fused documentation context (READMEs/ADRs/design prose) from the semsource knowledge graph for a query — the intended design, not just the code. Read-only.",
			Parameters:  param("The query — a natural-language phrase describing the design topic."),
		},
	}
}

// Execute proxies one call to its semsource route. No StopLoop: these run
// INSIDE the multi-turn developer loop — the model keeps its turn and reads
// the result as feedback.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	route, ok := routes[call.Name]
	if !ok {
		return errResult(call, agentic.ToolErrorInvalidArgs, "%s: not a semsource proxy tool", call.Name)
	}
	if e.q == nil {
		return errResult(call, agentic.ToolErrorInternal, "%s: no live semsource client — the boot did not declare the semsource condition (or no endpoint is configured); this tool should not have been advertised", call.Name)
	}
	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "%s: encode arguments: %v", call.Name, err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "%s: decode arguments: %v", call.Name, err)
	}
	if p.Query == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs, "%s: query is required", call.Name)
	}
	body, err := e.q.Query(ctx, route, p.Query)
	if err != nil {
		// D4: a mid-run semsource fault is LOUD in the trajectory — an explicit
		// tool error, never an empty success, never a fallback.
		return errResult(call, agentic.ToolErrorNetwork, "%s: %v", call.Name, err)
	}
	return agentic.ToolResult{CallID: call.ID, Name: call.Name, Content: string(body)}, nil
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{CallID: call.ID, Name: call.Name, Error: fmt.Sprintf(format, args...), ErrorKind: kind}, nil
}
