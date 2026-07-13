// Package readworkspace is the read_workspace tool (design simplify-m0-execution-rail,
// R7/task 4.2): the read sibling of apply_patch's write guard. The developer (Amelia) and
// the reviewer (Quinn) both run inside a multi-turn loop and need to see what is currently in
// the run's isolated CHECKOUT — the file apply_patch just wrote, a neighboring file to ground
// a fix, the shape of a directory — without shelling raw file access the harness cannot
// contain. No existing primitive can put checkout bytes into a loop (G1): a rule only
// routes/aggregates facts, and a persona has no read surface of its own into a run's
// checkout; prompt templating carries triples, not file contents (R7).
//
// It is READ-ONLY: it stamps no fact (there is nothing to measure — G3 does not apply to a
// read) and fires no lifecycle transition (G2). Its schema takes only `path` and an optional
// pagination `offset`, never an outcome. Every path is guarded through the SAME containment
// check the write side enforces (runspace.SafeJoin, the exported form of the patcher's
// safeJoin) — a path that escapes the checkout is rejected exactly as apply_patch rejects an
// escaping diff target, never silently clamped to the checkout root.
//
// It runs INSIDE the multi-turn auto dev/review loop (R2/R7): unlike the forced
// single-turn stations it does NOT set StopLoop on success — the model keeps its turn and can
// read again, or move on to apply_patch/measure_task/submit_review, in the same turn
// sequence. A file larger than the 32KB tool-result cap is paginated: each call returns up to
// 32000 bytes from the requested offset plus a next_offset for continuation when there is
// more (R7 — "read_workspace paginates").
//
// Two adversarial-review findings (M1/M2) shape this file beyond the happy path:
//
//   - M1: a raw-byte content cap alone does not bound the MARSHALED result. JSON string-
//     escaping (quotes, backslashes, tabs, newlines, control bytes needing \u00XX) can expand
//     content by up to 6x, so a page built from a fixed raw cap can marshal past the
//     framework's ToolResultMaxBytes (32768) — silently truncating the JSON tail, which is
//     exactly where next_offset/truncated sit (Go marshals map keys alphabetically after
//     content). fitEnvelope fixes this by measuring the ACTUAL marshaled size and shrinking
//     the content field (recomputing bytes/next_offset/truncated for the shrunk length) until
//     it fits — correct regardless of the page's escape ratio.
//   - M2: runspace.SafeJoin's containment check is LEXICAL (filepath.Join + filepath.Rel) —
//     it does not follow symlinks. A committed symlink inside the checkout (an absolute
//     target, or a `../`-traversal target) passes that lexical guard but os.Stat/os.Open/
//     os.ReadDir all FOLLOW it, so without a second check a model-chosen path could read an
//     arbitrary host file. After confirming the (lexically-guarded) path exists, Execute
//     resolves both the checkout root and the target through filepath.EvalSymlinks and
//     re-checks containment on the REAL paths — root is resolved too so a symlinked temp base
//     (macOS's /var -> /private/var) does not false-reject a legitimate path.
package readworkspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/c360studio/semdev/internal/runspace"
	"github.com/c360studio/semstreams/agentic"
)

// ToolName is the registered tool name.
const ToolName = "read_workspace"

// maxContentBytes bounds how much of a file one call attempts to read/return BEFORE the
// envelope-fit check (fitEnvelope) — the common case (little/no escaping) returns exactly
// this much; fitEnvelope shrinks further only when JSON escaping would otherwise blow the
// marshaled envelope past resultBudget (M1).
const maxContentBytes = 32000

// resultBudget bounds the FINAL MARSHALED JSON tool result, not raw content. The framework
// enforces ToolResultMaxBytes=32768 on the marshaled envelope; JSON string-escaping can
// expand raw content by up to 6x, so a fixed raw-content cap alone cannot guarantee the
// envelope fits — only measuring the actual marshaled size and shrinking to it (fitEnvelope)
// does (M1). 32000 leaves ~768 bytes of margin under the framework's hard cap for whatever
// the envelope's own keys, braces, and the (variable-length, model-supplied) path value need.
const resultBudget = 32000

// Workspace resolves the run's materialized checkout root — the read surface
// read_workspace needs. It mirrors measure_task's Sandboxes seam pattern: a narrow
// interface declared in the tool package, satisfied by *runspace.Checkouts (its Root
// method — the SAME seam measure_task/verify_artifact resolve their checkout root
// through). nil at schema-only registration (the censuses scan ListTools without a live
// checkout); Execute fails loudly if it is nil. Root FAILS CLOSED when no checkout has
// been materialized for the run — the tool surfaces the error and the run parks, never a
// silent host-path guess (SB5).
type Workspace interface {
	Root(ctx context.Context, runEntityID string) (string, error)
}

// Executor is the read_workspace tool.
type Executor struct {
	workspace Workspace
	logger    *slog.Logger
}

// New builds the read_workspace executor. workspace is nil for schema-only registration
// (the censuses scan ListTools without a live checkout); Execute fails loudly if it is
// missing — a read the harness cannot resolve is a park, never a silent skip.
func New(workspace Workspace, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{workspace: workspace, logger: logger}
}

// Execute reads a repo-relative path out of the run's checkout, path-guarded, and returns
// its content (paginated) or — for a directory — a sorted listing of its entries. It
// stamps nothing (G3) and fires no transition (G2).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.workspace == nil {
		return errResult(call, agentic.ToolErrorInternal, "read_workspace: harness not fully wired (workspace)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "read_workspace: %s missing on the tool call — cannot target the run's checkout", agentic.MetadataKeyRunEntityID)
	}

	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "read_workspace: re-encode arguments: %v", err)
	}
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		// The model's arguments failed to decode — a schema/argument fault, not an
		// executor bug (ToolErrorInvalidArgs, like the sibling tools).
		return errResult(call, agentic.ToolErrorInvalidArgs, "read_workspace: decode arguments: %v", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs, "read_workspace: path is required")
	}
	if args.Offset < 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "read_workspace: offset must be non-negative, got %d", args.Offset)
	}

	root, err := e.workspace.Root(ctx, runEntityID)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "read_workspace: resolve run checkout: %v", err)
	}

	abs, err := runspace.SafeJoin(root, args.Path)
	if err != nil {
		// A path escape is invalid model input the developer/reviewer re-reads from —
		// ToolErrorInvalidArgs (matching apply_patch's rejected-diff posture), not an
		// executor bug.
		return errResult(call, agentic.ToolErrorInvalidArgs, "read_workspace: %v", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		// A missing path is invalid model input (the model asked for something that
		// isn't there) — it re-reads a valid path, same posture as the escape case.
		return errResult(call, agentic.ToolErrorInvalidArgs, "read_workspace: %q not found in the checkout: %v", args.Path, err)
	}

	// M2: SafeJoin's containment check above is LEXICAL and does not follow symlinks. A
	// committed symlink inside the checkout can point outside it (an absolute target, or a
	// `../`-traversal target) and still pass that lexical guard — os.Stat/os.Open/os.ReadDir
	// all follow it. Now that the path is confirmed to exist, resolve BOTH root and abs
	// through their real (symlink-free) forms and re-check containment on those. root is
	// resolved too so a symlinked temp base (macOS's /var -> /private/var) does not
	// false-reject an otherwise legitimate path.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "read_workspace: resolve checkout root: %v", err)
	}
	realAbs, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// abs was just confirmed to exist (os.Stat above); a failure here means a broken
		// or looping symlink — invalid model input the reviewer/developer re-reads from,
		// the same posture as a not-found path.
		return errResult(call, agentic.ToolErrorInvalidArgs, "read_workspace: %q could not be resolved (broken symlink?): %v", args.Path, err)
	}
	if rel, relErr := filepath.Rel(realRoot, realAbs); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errResult(call, agentic.ToolErrorInvalidArgs, "read_workspace: %q escapes the checkout via a symlink", args.Path)
	}

	if info.IsDir() {
		listing, err := listDir(abs)
		if err != nil {
			return errResult(call, agentic.ToolErrorInternal, "read_workspace: list %q: %v", args.Path, err)
		}
		build := func(c string) map[string]any {
			m := map[string]any{
				"path":    args.Path,
				"content": c,
				"bytes":   len(c),
				"is_dir":  true,
			}
			if len(c) < len(listing) {
				m["truncated"] = true
			}
			return m
		}
		out, ferr := fitEnvelope(resultBudget, listing, build)
		if ferr != nil {
			return errResult(call, agentic.ToolErrorInternal, "read_workspace: encode result: %v", ferr)
		}
		return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(out)}, nil
	}

	content, err := readWindow(abs, args.Offset)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "read_workspace: read %q: %v", args.Path, err)
	}

	size := info.Size()
	offset := args.Offset
	build := func(c string) map[string]any {
		next := offset + len(c)
		truncated := int64(next) < size
		m := map[string]any{
			"path":      args.Path,
			"content":   c,
			"bytes":     len(c),
			"is_dir":    false,
			"truncated": truncated,
		}
		if truncated {
			m["next_offset"] = next
		}
		return m
	}
	out, err := fitEnvelope(resultBudget, content, build)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "read_workspace: encode result: %v", err)
	}
	// No StopLoop: read_workspace runs INSIDE the multi-turn auto loop (R2/R7) — the
	// model reads and continues its own turn (patch, measure, or read again) rather
	// than the harness forcibly ending it.
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(out)}, nil
}

// readWindow reads up to maxContentBytes bytes of the file at path starting at offset. It is
// the pre-fitEnvelope read window, not the final page: fitEnvelope may shrink what it
// returns further if the marshaled envelope would otherwise exceed resultBudget (M1).
func readWindow(path string, offset int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, maxContentBytes)
	n, rerr := f.ReadAt(buf, int64(offset))
	if rerr != nil && rerr != io.EOF {
		return "", rerr
	}
	return string(buf[:n]), nil
}

// fitEnvelope marshals build(content) and, if the result exceeds budget bytes, shrinks
// content (dropping trailing bytes) and re-marshals — looping until the envelope fits or
// content is empty (M1). JSON string-escaping (quotes, backslashes, tabs, newlines, and
// other control bytes needing \u00XX) can expand raw bytes by up to 6x, so a FIXED raw-byte
// cap on content cannot guarantee the MARSHALED envelope stays under the framework's
// ToolResultMaxBytes — only measuring the actual marshaled size and shrinking to fit does.
// build receives the (possibly shrunk) content and must derive every length-dependent field
// (bytes, next_offset, truncated) from THAT content, not the original, so each iteration's
// envelope is internally consistent on its own.
func fitEnvelope(budget int, content string, build func(content string) map[string]any) ([]byte, error) {
	for {
		out, err := json.Marshal(build(content))
		if err != nil {
			return nil, err
		}
		if len(out) <= budget || content == "" {
			return out, nil
		}
		overshoot := len(out) - budget
		newLen := max(len(content)-overshoot, 0)
		content = content[:newLen]
	}
}

// listDir returns a sorted, newline-joined listing of dir's entries, with a trailing "/"
// on each subdirectory name.
func listDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
