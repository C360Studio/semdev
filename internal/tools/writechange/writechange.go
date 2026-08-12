// Package writechange is the write_change tool (openspec-io): it materializes a
// run's OpenSpec change as an on-disk folder in the run's target-repo workspace,
// so the change can be committed and delivered as the PR. It is the third face of
// the openspec-io seam — create_change stamps the facts, render_openspec renders
// them to a document, write_change writes them to the workspace filesystem.
//
// It is graph-READ-only: it hydrates the Change from the run's openspec.change.*
// facts (changefacts.Hydrate) and writes it with the format engine's WriteChange.
// It stamps NO graph facts (it produces files, not triples), so it has no G5 vocab
// writer; its schema takes only the change slug (G3). D15: the facts are read off
// the RUN entity (call metadata).
//
// WHERE it writes — the run's checked-out target repo — is runtime state the
// forge-io checkout (group 5) and the clean-room Runner (group 8) own; the tool
// resolves the change directory through an injected WorkspaceResolver seam rather
// than inventing its own workspace bookkeeping (B1). At M0 there is no production
// resolver yet: the tool registers with a nil resolver (schema-only, like the nil
// reader) and fails loudly if executed, and the group-11 journey injects a real
// (temp-dir) resolver. An un-authored/misnamed slug hydrates empty and is
// rejected — write_change never materializes a hollow change folder.
package writechange

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
)

// ToolName is the registered tool name and the coordinator's write-to-workspace
// action handler.
const ToolName = "write_change"

// WorkspaceResolver resolves the on-disk OpenSpec change directory
// (<checkout>/openspec/changes/<slug>/) for a run's target-repo workspace. It is
// the seam over the checkout: its production implementation (which locates the
// run's checked-out repo) lands with forge-io / the clean-room Runner; write_change
// depends only on this narrow surface so it holds no workspace state of its own.
//
// The caller validates slug as a single safe path segment (openspec.ValidateSlug)
// before calling, and an implementation MUST keep the returned directory within
// the run's checkout — belt-and-suspenders against a traversal slug so a
// resolver that naively joins the slug cannot escape the checkout.
type WorkspaceResolver interface {
	ChangeDir(ctx context.Context, runEntityID, slug string) (string, error)
}

// Executor materializes a run's change facts to its workspace folder.
type Executor struct {
	reader   changefacts.Reader
	resolver WorkspaceResolver
	logger   *slog.Logger
}

// New builds the write_change executor. reader and resolver may be nil for
// schema-only registration (the tool censuses inspect ListTools without a live
// NATS client or a checked-out workspace); Execute fails loudly if either is nil.
func New(reader changefacts.Reader, resolver WorkspaceResolver, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, resolver: resolver, logger: logger}
}

// Execute hydrates the change named by the call's slug from the run entity's facts
// and writes it as an OpenSpec change folder into the run's workspace. It is
// terminal for the turn (StopLoop) — the rule engine drives the next station
// (validate / open_pr).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil {
		return errResult(call, agentic.ToolErrorInternal, "write_change: no fact reader wired")
	}
	if e.resolver == nil {
		return errResult(call, agentic.ToolErrorInternal, "write_change: no workspace resolver wired")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "write_change: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "write_change: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "write_change: decode arguments: %v", err)
	}
	if p.Slug == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs, "write_change: slug is required")
	}
	// Defense in depth: the slug builds a changes/<slug>/ path in the workspace, so
	// reject a traversal slug here even though create_change already guards it at
	// authoring — this tool must never hand an unsafe segment to a resolver.
	if err := openspec.ValidateSlug(p.Slug); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "write_change: %v", err)
	}

	change, err := changefacts.Hydrate(ctx, e.reader, runEntityID, p.Slug)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "write_change: %v", err)
	}
	if isEmpty(change) {
		return errResult(call, agentic.ToolErrorInvalidArgs, "write_change: no openspec.change.%s.* facts on %s — nothing to write (was the change authored?)", p.Slug, runEntityID)
	}

	dir, err := e.resolver.ChangeDir(ctx, runEntityID, p.Slug)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "write_change: resolve workspace dir for %q: %v", p.Slug, err)
	}
	if err := openspec.WriteChange(dir, change); err != nil {
		return errResult(call, agentic.ToolErrorInternal, "write_change: write change folder %q: %v", dir, err)
	}

	summary, _ := json.Marshal(map[string]any{"slug": p.Slug, "dir": dir, "run_entity": runEntityID})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// isEmpty reports whether a hydrated Change carries no artifact facts — the shape
// a never-authored or misnamed slug produces. Writing it would create an empty
// change folder that reads as a real materialized change; write_change rejects it.
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
