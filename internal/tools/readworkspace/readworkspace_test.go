package readworkspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeWorkspace resolves a scripted checkout root — a real temp dir a test seeds with
// files — mirroring runspace.Checkouts.Root without a live git checkout. An err stands in
// for "no checkout materialized" (the fail-closed Root posture).
type fakeWorkspace struct {
	root string
	err  error
}

func (w *fakeWorkspace) Root(_ context.Context, _ string) (string, error) {
	if w.err != nil {
		return "", w.err
	}
	return w.root, nil
}

func call(path string, offset int, withOffset bool) agentic.ToolCall {
	args := map[string]any{"path": path}
	if withOffset {
		args["offset"] = offset
	}
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Arguments: args,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
	}
}

type resultPayload struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Bytes      int    `json:"bytes"`
	IsDir      bool   `json:"is_dir"`
	Truncated  bool   `json:"truncated"`
	NextOffset *int   `json:"next_offset"`
}

func decodeResult(t *testing.T, res agentic.ToolResult) resultPayload {
	t.Helper()
	var p resultPayload
	if err := json.Unmarshal([]byte(res.Content), &p); err != nil {
		t.Fatalf("decode result content %q: %v", res.Content, err)
	}
	return p
}

// A plain in-checkout file reads back verbatim, path-guarded through the SAME containment
// SafeJoin enforces on the write side.
func TestReadWorkspaceReadsAFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "health.go"), []byte("package health\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	res, err := New(&fakeWorkspace{root: root}, nil).Execute(context.Background(), call("health.go", 0, false))
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	p := decodeResult(t, res)
	if p.Content != "package health\n" {
		t.Errorf("content = %q, want the file's bytes", p.Content)
	}
	if p.IsDir {
		t.Error("a regular file must not report is_dir")
	}
	if p.Truncated {
		t.Error("a small file must not report truncated")
	}
	if res.StopLoop {
		t.Error("read_workspace must NOT StopLoop — it runs inside the multi-turn auto loop (R2/R7)")
	}
}

// A file larger than the 32KB cap is paginated: the first call returns up to
// maxContentBytes (less if the marshaled envelope needs shrinking, M1) with a next_offset,
// and resuming at that offset returns the tail. Plain digits need no JSON escaping, so this
// exercises the ordinary path/offset bookkeeping; it does NOT exercise M1's escape-driven
// envelope overflow (see TestReadWorkspacePaginatesHeavilyEscapedContent for that).
func TestReadWorkspacePaginatesALargeFile(t *testing.T) {
	root := t.TempDir()
	full := strings.Repeat("0123456789", 4000) // 40000 bytes, distinct from the 32000 cap
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(full), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	ws := &fakeWorkspace{root: root}

	first, err := New(ws, nil).Execute(context.Background(), call("big.txt", 0, false))
	if err != nil || first.Error != "" {
		t.Fatalf("execute (first): err=%v toolErr=%s", err, first.Error)
	}
	if len(first.Content) > 32768 {
		t.Fatalf("marshaled result is %d bytes, want <= 32768 (the framework's ToolResultMaxBytes)", len(first.Content))
	}
	p1 := decodeResult(t, first)
	if p1.Bytes == 0 || p1.Bytes > maxContentBytes {
		t.Fatalf("first call bytes = %d, want (0, %d]", p1.Bytes, maxContentBytes)
	}
	if !p1.Truncated {
		t.Fatal("first call must report truncated (the file is larger than the cap)")
	}
	if p1.NextOffset == nil || *p1.NextOffset != p1.Bytes {
		t.Fatalf("first call next_offset = %v, want %d", p1.NextOffset, p1.Bytes)
	}
	if p1.Content != full[:p1.Bytes] {
		t.Error("first call content must be the file's leading p1.Bytes bytes")
	}

	got, pages := paginateAll(t, ws, "big.txt", first)
	if got != full {
		t.Error("paging with the returned next_offset must eventually reconstruct the original file exactly")
	}
	if pages < 2 {
		t.Fatalf("expected at least 2 pages for a %d-byte file under a %d-byte cap, got %d", len(full), maxContentBytes, pages)
	}
}

// paginateAll follows first's next_offset (and every subsequent page's) until truncated is
// false, returning the concatenated content across ALL pages (first included) and the page
// count. It caps iterations defensively so a pagination bug fails the test instead of
// hanging it.
func paginateAll(t *testing.T, ws Workspace, path string, first agentic.ToolResult) (string, int) {
	t.Helper()
	p := decodeResult(t, first)
	content := p.Content
	pages := 1
	for p.Truncated {
		if pages > 1000 {
			t.Fatalf("pagination did not terminate within 1000 pages")
		}
		if p.NextOffset == nil {
			t.Fatalf("page %d: truncated=true but next_offset is missing", pages)
		}
		res, err := New(ws, nil).Execute(context.Background(), call(path, *p.NextOffset, true))
		if err != nil || res.Error != "" {
			t.Fatalf("execute (page %d): err=%v toolErr=%s", pages+1, err, res.Error)
		}
		if len(res.Content) > 32768 {
			t.Fatalf("page %d: marshaled result is %d bytes, want <= 32768", pages+1, len(res.Content))
		}
		p = decodeResult(t, res)
		content += p.Content
		pages++
	}
	if p.NextOffset != nil {
		t.Error("the final page must not carry a next_offset once EOF is reached")
	}
	return content, pages
}

// M1 (adversarial review): a page whose content is HEAVY on JSON-escaping characters
// (quotes, tabs, backslashes, newlines) expands well past its raw byte count once
// marshaled. A fixed raw-content cap alone would push the envelope past the framework's
// ToolResultMaxBytes (32768) and silently truncate the JSON tail — exactly where
// next_offset/truncated sit (Go marshals map keys alphabetically after content). The
// digit-only pagination test above cannot catch this (digits never escape). fitEnvelope
// must keep the marshaled envelope under the hard cap AND keep next_offset/truncated intact
// so pagination still converges on the whole file.
func TestReadWorkspacePaginatesHeavilyEscapedContent(t *testing.T) {
	root := t.TempDir()
	line := "\"a\tb\\c\"\n" // quote, tab, backslash, quote, newline — all JSON-escaped
	full := strings.Repeat(line, 6000)
	if len(full) < 40000 {
		t.Fatalf("test fixture too small: %d bytes", len(full))
	}
	if err := os.WriteFile(filepath.Join(root, "escaped.txt"), []byte(full), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	ws := &fakeWorkspace{root: root}

	first, err := New(ws, nil).Execute(context.Background(), call("escaped.txt", 0, false))
	if err != nil || first.Error != "" {
		t.Fatalf("execute (first): err=%v toolErr=%s", err, first.Error)
	}
	if len(first.Content) > 32768 {
		t.Fatalf("marshaled result is %d bytes, want <= 32768 (the framework's ToolResultMaxBytes)", len(first.Content))
	}
	p1 := decodeResult(t, first)
	if !p1.Truncated {
		t.Fatal("a heavily-escaped page over the cap must still report truncated=true")
	}
	if p1.NextOffset == nil {
		t.Fatal("truncated=true must carry a next_offset — escaping must not push this field out of the envelope")
	}
	if p1.Content != full[:p1.Bytes] {
		t.Error("first page content must be a valid leading prefix of the file")
	}

	got, _ := paginateAll(t, ws, "escaped.txt", first)
	if got != full {
		t.Error("paging with the returned next_offset must eventually reconstruct the original (heavily-escaped) file exactly")
	}
}

// A path that escapes the checkout — either a `../` traversal or an absolute path — is
// rejected as invalid model input (ToolErrorInvalidArgs), never silently clamped.
func TestReadWorkspaceRejectsEscapingPaths(t *testing.T) {
	root := t.TempDir()
	ws := &fakeWorkspace{root: root}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"relative traversal", "../etc/passwd"},
		{"absolute path", "/etc/passwd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := New(ws, nil).Execute(context.Background(), call(tc.path, 0, false))
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if res.Error == "" {
				t.Fatal("an escaping path must surface as a tool error")
			}
			if res.ErrorKind != agentic.ToolErrorInvalidArgs {
				t.Errorf("ErrorKind = %q, want %q (the model re-reads a valid path)", res.ErrorKind, agentic.ToolErrorInvalidArgs)
			}
		})
	}
}

// M2 (adversarial review): SafeJoin's containment check is LEXICAL (filepath.Join +
// filepath.Rel) and does not follow symlinks. A committed symlink inside the checkout can
// point outside it — via an absolute target, or a `../`-traversal target — and still pass
// that lexical guard; os.Stat/os.Open all FOLLOW it. Both shapes must be rejected as invalid
// model input (ToolErrorInvalidArgs) with NO leaked host-file content.
func TestReadWorkspaceRejectsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // a SEPARATE temp dir — never inside root
	secretPath := filepath.Join(outside, "secret.txt")
	const secret = "TOP SECRET HOST CONTENT"
	if err := os.WriteFile(secretPath, []byte(secret), 0o644); err != nil {
		t.Fatalf("seed outside secret: %v", err)
	}

	if err := os.Symlink(secretPath, filepath.Join(root, "link_abs")); err != nil {
		t.Fatalf("seed absolute-target symlink: %v", err)
	}
	relTarget, err := filepath.Rel(root, secretPath)
	if err != nil {
		t.Fatalf("compute relative target: %v", err)
	}
	if err := os.Symlink(relTarget, filepath.Join(root, "link_rel")); err != nil {
		t.Fatalf("seed ../-traversal symlink: %v", err)
	}

	ws := &fakeWorkspace{root: root}
	for _, tc := range []struct {
		name string
		path string
	}{
		{"absolute-target symlink", "link_abs"},
		{"../-traversal symlink", "link_rel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := New(ws, nil).Execute(context.Background(), call(tc.path, 0, false))
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if res.Error == "" {
				t.Fatal("a symlink escaping the checkout must surface as a tool error")
			}
			if res.ErrorKind != agentic.ToolErrorInvalidArgs {
				t.Errorf("ErrorKind = %q, want %q (the model re-reads a valid path)", res.ErrorKind, agentic.ToolErrorInvalidArgs)
			}
			if strings.Contains(res.Content, secret) || strings.Contains(res.Error, secret) {
				t.Fatal("the outside secret content must NEVER be leaked, in Content or Error")
			}
		})
	}
}

// A directory path returns a sorted, newline-joined listing with a trailing "/" on
// subdirectories.
func TestReadWorkspaceListsADirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "sub"), 0o755); err != nil {
		t.Fatalf("seed dirs: %v", err)
	}
	for _, f := range []string{"pkg/health.go", "pkg/health_test.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed file %s: %v", f, err)
		}
	}
	res, err := New(&fakeWorkspace{root: root}, nil).Execute(context.Background(), call("pkg", 0, false))
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	p := decodeResult(t, res)
	if !p.IsDir {
		t.Error("a directory path must report is_dir=true")
	}
	want := "health.go\nhealth_test.go\nsub/"
	if p.Content != want {
		t.Errorf("directory listing = %q, want %q", p.Content, want)
	}
}

// Schema-only registration (nil workspace) fails loudly — never a silent skip.
func TestReadWorkspaceFailsLoudlyWithoutWorkspace(t *testing.T) {
	res, err := New(nil, nil).Execute(context.Background(), call("health.go", 0, false))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-workspace read_workspace must fail loudly")
	}
	if res.ErrorKind != agentic.ToolErrorInternal {
		t.Errorf("ErrorKind = %q, want %q", res.ErrorKind, agentic.ToolErrorInternal)
	}
}

// G3: the schema exposes only path/offset, no outcome field.
func TestReadWorkspaceSchemaTakesNoOutcome(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	for _, banned := range []string{"passed", "outcome", "result", "success", "pass"} {
		if _, ok := props[banned]; ok {
			t.Errorf("schema exposes an outcome-like field %q — read_workspace is read-only (G3)", banned)
		}
	}
	if _, ok := props["path"]; !ok {
		t.Errorf("schema must expose a `path` property, got %v", props)
	}
}
