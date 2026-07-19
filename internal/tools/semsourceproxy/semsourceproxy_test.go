package semsourceproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/forge/semsource"
	"github.com/c360studio/semstreams/agentic"
)

// A live proxy round-trips the query to its semsource route and returns the
// response body as tool content — the request shape semsource's own MCP
// gateway sends ({"query": ...} POSTed to the verb route).
func TestProxyRoundTripsPerRoute(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"answer":"fused"}`))
	}))
	defer srv.Close()

	e := New(semsource.NewClient(srv.URL), nil)
	wantRoutes := map[string]string{
		ToolCodeContext: "/code-context/context",
		ToolCodeImpact:  "/code-context/impact",
		ToolCodeSearch:  "/code-context/search",
		ToolDocContext:  "/doc-context/context",
	}
	for name, wantPath := range wantRoutes {
		res, err := e.Execute(context.Background(), agentic.ToolCall{ID: "c1", Name: name, Arguments: map[string]any{"query": "RunFloors"}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Error != "" {
			t.Fatalf("%s: unexpected tool error %q", name, res.Error)
		}
		if res.Content != `{"answer":"fused"}` {
			t.Fatalf("%s: content = %q, want the response body verbatim", name, res.Content)
		}
		if gotPath != wantPath {
			t.Fatalf("%s hit %q, want %q", name, gotPath, wantPath)
		}
		if !strings.Contains(gotBody, `"query":"RunFloors"`) {
			t.Fatalf("%s body = %q, want the {\"query\":...} shape semsource's MCP gateway sends", name, gotBody)
		}
	}
}

// An upstream fault (non-200) is a LOUD tool errResult carrying the body —
// never an empty success (D4: mid-run faults are trajectory-visible).
func TestProxyUpstreamFaultIsLoud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "index rebuilding", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	e := New(semsource.NewClient(srv.URL), nil)
	res, err := e.Execute(context.Background(), agentic.ToolCall{ID: "c1", Name: ToolCodeSearch, Arguments: map[string]any{"query": "retry logic"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "503") {
		t.Fatalf("an upstream 503 must be a loud errResult naming the status, got %+v", res)
	}
	if res.Content != "" {
		t.Fatalf("a failed proxy must not return content, got %q", res.Content)
	}
}

// A schema-only (nil-querier) executor fails loudly if executed — the
// baseline boot registers it so the census sees the schemas, but no baseline
// loop should ever reach Execute (nothing advertises the tools).
func TestProxyNilClientFailsLoudly(t *testing.T) {
	e := New(nil, nil)
	res, err := e.Execute(context.Background(), agentic.ToolCall{ID: "c1", Name: ToolCodeContext, Arguments: map[string]any{"query": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "no live semsource client") {
		t.Fatalf("nil-client execution must fail loudly, got %+v", res)
	}
}

// The schemas: all four present, each taking ONLY the query parameter (G3 —
// no outcome fields), all required.
func TestProxySchemasAreQueryOnly(t *testing.T) {
	defs := New(nil, nil).ListTools()
	if len(defs) != 4 {
		t.Fatalf("want 4 tool schemas, got %d", len(defs))
	}
	want := map[string]bool{ToolCodeContext: true, ToolCodeImpact: true, ToolCodeSearch: true, ToolDocContext: true}
	for _, d := range defs {
		if !want[d.Name] {
			t.Fatalf("unexpected tool %q", d.Name)
		}
		delete(want, d.Name)
		props, ok := d.Parameters["properties"].(map[string]any)
		if !ok || len(props) != 1 {
			t.Fatalf("%s: schema must take exactly the query parameter, got %v", d.Name, d.Parameters["properties"])
		}
		if _, ok := props["query"]; !ok {
			t.Fatalf("%s: schema must take query, got %v", d.Name, props)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}

// Missing/empty query is invalid-args, loudly.
func TestProxyRequiresQuery(t *testing.T) {
	// The nil-client check fires FIRST by design (a wiring fault outranks an
	// argument fault) — asserted, so the documented ordering cannot silently
	// invert. Then a live-but-unreachable client exposes the args path.
	e := New(nil, nil)
	res, err := e.Execute(context.Background(), agentic.ToolCall{ID: "c1", Name: ToolCodeSearch, Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "no live semsource client") {
		t.Fatalf("the nil-client fault must outrank args validation, got %+v", res)
	}
	e = New(semsource.NewClient("http://127.0.0.1:1"), nil)
	res, err = e.Execute(context.Background(), agentic.ToolCall{ID: "c1", Name: ToolCodeSearch, Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "query is required") {
		t.Fatalf("empty query must be invalid-args, got %+v", res)
	}
}
