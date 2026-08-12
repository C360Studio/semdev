package readdiff

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeDiffer returns a scripted diff string (or an error, standing in for "no checkout
// materialized" / a git failure) — a test double for runspace.Checkouts.Diff, never a real
// git shell-out.
type fakeDiffer struct {
	diff     string
	err      error
	gotRunID string
}

func (f *fakeDiffer) Diff(_ context.Context, runEntityID string) (string, error) {
	f.gotRunID = runEntityID
	if f.err != nil {
		return "", f.err
	}
	return f.diff, nil
}

func call() agentic.ToolCall {
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Arguments: map[string]any{},
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
	}
}

type resultPayload struct {
	Diff      string `json:"diff"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

func decodeResult(t *testing.T, res agentic.ToolResult) resultPayload {
	t.Helper()
	var p resultPayload
	if err := json.Unmarshal([]byte(res.Content), &p); err != nil {
		t.Fatalf("decode result content %q: %v", res.Content, err)
	}
	return p
}

// read_diff returns the differ's diff verbatim, targeting THIS run's checkout.
func TestReadDiffReturnsTheDiff(t *testing.T) {
	d := &fakeDiffer{diff: "--- a/health.go\n+++ b/health.go\n@@ -1 +1 @@\n-old\n+new\n"}
	res, err := New(d, nil).Execute(context.Background(), call())
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if d.gotRunID != runEntity {
		t.Errorf("differ got run %q, want %q", d.gotRunID, runEntity)
	}
	p := decodeResult(t, res)
	if p.Diff != d.diff {
		t.Errorf("diff = %q, want %q", p.Diff, d.diff)
	}
	if p.Truncated {
		t.Error("a small diff must not report truncated")
	}
	if p.Bytes != len(d.diff) {
		t.Errorf("bytes = %d, want %d", p.Bytes, len(d.diff))
	}
	if res.StopLoop {
		t.Error("read_diff must NOT StopLoop — it runs inside Quinn's multi-turn auto loop (R2/R7)")
	}
}

// A diff larger than the 32KB cap is truncated, not rejected — the reviewer still sees as
// much as fits, with truncated:true telling her there's more (she can read_workspace the
// individual files for the rest). The marshaled envelope must stay under the framework's
// real ToolResultMaxBytes (32768) — not just the raw diff under some fixed byte count (M1).
func TestReadDiffTruncatesALargeDiff(t *testing.T) {
	big := strings.Repeat("+", resultBudget+5000)
	d := &fakeDiffer{diff: big}
	res, err := New(d, nil).Execute(context.Background(), call())
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if len(res.Content) > 32768 {
		t.Fatalf("marshaled result is %d bytes, want <= 32768 (the framework's ToolResultMaxBytes)", len(res.Content))
	}
	p := decodeResult(t, res)
	if p.Bytes == 0 || p.Bytes >= len(big) {
		t.Fatalf("bytes = %d, want a shrunk page strictly between 0 and %d", p.Bytes, len(big))
	}
	if !p.Truncated {
		t.Error("a diff over the cap must report truncated")
	}
	if p.Diff != big[:p.Bytes] {
		t.Error("the returned diff must be the leading p.Bytes bytes of the original — a valid prefix, not garbled")
	}
}

// A diff whose bytes are HEAVY on JSON-escaping characters (quotes, backslashes, tabs,
// newlines) expands well past its raw byte count once marshaled — a fixed raw-byte cap
// alone would overflow the framework's ToolResultMaxBytes and silently drop `truncated`
// (M1, exactly the class a digit/plus-only test cannot catch). fitEnvelope must still land
// the marshaled envelope under the hard cap and keep `truncated` correct.
func TestReadDiffCapsHeavilyEscapedContent(t *testing.T) {
	line := "\"a\tb\\c\"\n" // quote, tab, backslash, quote, newline — all JSON-escaped
	big := strings.Repeat(line, 6000)
	if len(big) < 40000 {
		t.Fatalf("test fixture too small: %d bytes", len(big))
	}
	d := &fakeDiffer{diff: big}
	res, err := New(d, nil).Execute(context.Background(), call())
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if len(res.Content) > 32768 {
		t.Fatalf("marshaled result is %d bytes, want <= 32768 (the framework's ToolResultMaxBytes)", len(res.Content))
	}
	p := decodeResult(t, res)
	if !p.Truncated {
		t.Error("a heavily-escaped diff over the cap must still report truncated=true — the escaping must not silently push this field out of the envelope")
	}
	if p.Diff != big[:p.Bytes] {
		t.Error("the returned diff must be the leading p.Bytes bytes of the original")
	}
}

// A differ error (no checkout, no committed attempt, git failure) fails loud — the
// reviewer has no arguments to retry with, so this is not invalid-args.
func TestReadDiffFailsLoudlyOnDifferError(t *testing.T) {
	d := &fakeDiffer{err: errors.New("runspace: no checkout materialized for run — cannot resolve the workspace")}
	res, err := New(d, nil).Execute(context.Background(), call())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a differ error must surface as a tool error")
	}
	if res.ErrorKind != agentic.ToolErrorInternal {
		t.Errorf("ErrorKind = %q, want %q", res.ErrorKind, agentic.ToolErrorInternal)
	}
}

// Schema-only registration (nil differ) fails loudly — never a silent skip.
func TestReadDiffFailsLoudlyWithoutDiffer(t *testing.T) {
	res, err := New(nil, nil).Execute(context.Background(), call())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-differ read_diff must fail loudly")
	}
	if res.ErrorKind != agentic.ToolErrorInternal {
		t.Errorf("ErrorKind = %q, want %q", res.ErrorKind, agentic.ToolErrorInternal)
	}
}

// A call with no run-entity id fails loudly — the tool cannot target a checkout.
func TestReadDiffMissingRunEntityFailsLoudly(t *testing.T) {
	res, err := New(&fakeDiffer{}, nil).Execute(context.Background(), agentic.ToolCall{ID: "c1", Name: ToolName, Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a call with no run entity id must fail loudly")
	}
}

// G3: the schema takes NO arguments — a review is grounded by the run's own metadata,
// never a model-supplied path or ref.
func TestReadDiffSchemaTakesNoArguments(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("schema exposes %d properties, want 0 (read_diff takes no arguments): %v", len(props), props)
	}
}
