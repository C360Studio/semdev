// Package hydratechange is the render_openspec tool (openspec-io): it renders a
// run's OpenSpec change back to markdown FROM the run's openspec.change.* facts,
// so the artifact is a projection of the graph and never a second hand-authored
// source of truth (G10). It is the read mirror of the create_change author tool —
// that tool stamps the facts, this renders them.
//
// Read-only: it writes NO facts (it takes a changefacts.Reader, not a writer), so
// it has no G5 vocab writer and no outcome to stamp. Its schema takes only the
// change slug (content), never a validation/outcome field (G3). D15: the facts
// live on the run entity, resolved from call metadata, so hydrate reads the same
// subject the author wrote.
//
// Rendering is the format engine's RenderChangeFolder — one readable document
// with each artifact under its openspec/changes/<slug>/ path marker — not a
// multi-file write (that is the separate WriteChange-to-workspace tool). A run
// with no authored change facts hydrates to an empty Change; hydrate fails loudly
// on that rather than returning a hollow header, so an un-authored change cannot
// masquerade as a rendered one.
package hydratechange

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
)

// ToolName is the registered tool name and the coordinator's hydrate action
// handler.
const ToolName = "render_openspec"

// Executor renders a run's change facts back to OpenSpec markdown.
type Executor struct {
	reader changefacts.Reader
	logger *slog.Logger
}

// New builds the render_openspec executor. reader may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if it is nil.
func New(reader changefacts.Reader, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, logger: logger}
}

// Execute hydrates the change named by the call's slug from the run entity's
// facts and returns it rendered as an OpenSpec-markdown document. It is read-only
// and terminal for the loop turn (StopLoop) — a render is the answer, not a step
// to iterate.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil {
		return errResult(call, agentic.ToolErrorInternal, "render_openspec: no fact reader wired")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "render_openspec: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "render_openspec: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "render_openspec: decode arguments: %v", err)
	}
	if p.Slug == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs, "render_openspec: slug is required")
	}

	change, err := changefacts.Hydrate(ctx, e.reader, runEntityID, p.Slug)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "render_openspec: %v", err)
	}
	if isEmpty(change) {
		return errResult(call, agentic.ToolErrorInvalidArgs, "render_openspec: no openspec.change.%s.* facts on %s — nothing to render (was the change authored?)", p.Slug, runEntityID)
	}

	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: openspec.RenderChangeFolder(change), StopLoop: true}, nil
}

// isEmpty reports whether a hydrated Change carries no artifact facts — the shape
// a never-authored or misnamed slug produces (ChangeFromFacts's absent-is-nil
// contract). Rendering it would emit only a bare header, which reads as a
// successful render of nothing; hydrate rejects it instead.
func isEmpty(c *openspec.Change) bool {
	return c.Proposal == nil && c.Design == nil && c.Tasks == nil && len(c.Deltas) == 0
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
