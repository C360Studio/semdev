// Package semsource is semdev's thin HTTP adapter for semsource's public read
// surface (integrate-semsource-ab-harness, D1). semsource exposes its product
// query verbs over plain HTTP (`POST /code-context/<verb>`, `POST
// /doc-context/context`) and a readiness/status endpoint
// (`GET /source-manifest/status`) — this client speaks exactly that documented
// surface and nothing else. Deliberately NOT an MCP client (the framework has
// no MCP client seam and the same gateway serves these verbs over HTTP; design
// D1b) and NOT a NATS bridge (semdev's and semsource's NATS are separate
// clusters; D1c). Nothing is shared between the repos but HTTP, so the
// semstreams pin skew between them never matters.
//
// Read-only by construction: every call is a query; no semdev fact is written
// here (no G5 writer) and no semsource fact ever enters semdev's graph.
package semsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const requestTimeout = 30 * time.Second

// The four query routes of semsource's product read surface, verified against
// semsource's own workbench capability table (source-manifest
// workbench_capabilities.go): code_context/code_impact gate on the structural
// index, code_search on the semantic index, doc_context on the structural
// index of the docs lens.
const (
	RouteCodeContext = "/code-context/context"
	RouteCodeImpact  = "/code-context/impact"
	RouteCodeSearch  = "/code-context/search"
	RouteDocContext  = "/doc-context/context"
	statusRoute      = "/source-manifest/status"
)

// Client calls semsource's HTTP read surface.
type Client struct {
	base string
	http *http.Client
}

// NewClient builds a semsource client for the given base endpoint (e.g.
// "http://localhost:8080"). The endpoint comes from boot config; a client is
// only constructed when boot declares the semsource condition (D1 — baseline
// boots construct zero live semsource clients).
func NewClient(endpoint string) *Client {
	return &Client{base: strings.TrimRight(endpoint, "/"), http: &http.Client{Timeout: requestTimeout}}
}

// Signal is one per-signal readiness object from the status surface
// (source-manifest's canonical readiness shape): Available=false means the
// signal could not even be fetched; Ready is the gate consumers poll for.
type Signal struct {
	Available bool   `json:"available"`
	Ready     bool   `json:"ready"`
	State     string `json:"state"`
}

// Status is the slice of `GET /source-manifest/status` the A/B readiness gate
// reads: the aggregate phase plus the two PER-SIGNAL readiness objects. D4:
// the gate is index.ready AND embedding.ready — the aggregate phase alone
// means "all sources reported" and admits a cold-embeddings gateway whose
// code_search returns weak 200-OK results (silent degradation).
type Status struct {
	Phase     string `json:"phase"`
	Index     Signal `json:"index"`
	Embedding Signal `json:"embedding"`
}

// Status fetches the readiness/status surface.
func (c *Client) Status(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+statusRoute, nil)
	if err != nil {
		return Status{}, fmt.Errorf("semsource: build status request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("semsource: status request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Status{}, fmt.Errorf("semsource: read status response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("semsource: status returned HTTP %d: %s", resp.StatusCode, truncate(body))
	}
	var s Status
	if err := json.Unmarshal(body, &s); err != nil {
		return Status{}, fmt.Errorf("semsource: decode status response: %w", err)
	}
	return s, nil
}

// Query POSTs {"query": q} to one of the Route* verbs and returns the raw
// response body — the same request shape semsource's own MCP gateway sends to
// these routes, so semsource's docs and prompts transfer verbatim. A non-200
// is a loud error carrying the (truncated) body; the caller surfaces it as a
// tool errResult, never an empty success.
func (c *Client) Query(ctx context.Context, route, query string) ([]byte, error) {
	payload, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("semsource: encode query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+route, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("semsource: build query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("semsource: query %s: %w", route, err)
	}
	defer func() { _ = resp.Body.Close() }()
	const maxBody = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("semsource: read query response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("semsource: query %s returned HTTP %d: %s", route, resp.StatusCode, truncate(body))
	}
	// An oversized body is a LOUD error, never a silently mid-document-cut
	// blob handed to the model as if complete.
	if len(body) > maxBody {
		return nil, fmt.Errorf("semsource: query %s response exceeds %d bytes — refusing to return a truncated document as a complete answer", route, maxBody)
	}
	return body, nil
}

func truncate(b []byte) string {
	const max = 512
	if len(b) <= max {
		return string(b)
	}
	// Cut on a rune boundary so an error message never carries mojibake.
	return strings.ToValidUTF8(string(b[:max]), "") + "…"
}
