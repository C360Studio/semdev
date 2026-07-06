// Package listcomments is the github_list_comments tool: the thin comment-read the
// framework's github tools lack. The framework ships github_get_issue (reads the
// issue) and github_add_comment (posts one), but no way to READ an issue/PR's
// comment thread — which a coordinator or reviewer needs to ground a decision in
// the conversation so far. semdev adds it (task 5.5, inventory of thin comment
// tools), reusing the semdev GitHub client.
//
// Read-only: it writes no facts (no G5 writer) and its schema takes only the
// coordinates of the thread to read (owner/repo/number) — content, never an
// outcome (G3). It is host-specific by construction (a GitHub read), like the
// framework's github_* tools; the arc consumes the result, not the API.
package listcomments

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semstreams/agentic"
)

// ToolName is the registered tool name.
const ToolName = "github_list_comments"

// commentLister is the read surface the tool needs — satisfied by *github.Client.
type commentLister interface {
	ListComments(ctx context.Context, owner, repo string, number int) ([]github.Comment, error)
}

// Executor lists an issue/PR's comments.
type Executor struct {
	lister commentLister
	logger *slog.Logger
}

// New builds the github_list_comments executor. lister may be nil for schema-only
// registration (the censuses, and the no-GITHUB_TOKEN boot path); Execute fails
// loudly if it is nil.
func New(lister commentLister, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{lister: lister, logger: logger}
}

// Execute reads the comment thread named by the arguments and returns it as JSON.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.lister == nil {
		return errResult(call, agentic.ToolErrorInternal, "github_list_comments: no GitHub client wired (GITHUB_TOKEN unset?)")
	}
	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "github_list_comments: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "github_list_comments: decode arguments: %v", err)
	}
	if p.Owner == "" || p.Repo == "" || p.Number == 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "github_list_comments: owner, repo, and number are required")
	}

	comments, err := e.lister.ListComments(ctx, p.Owner, p.Repo, p.Number)
	if err != nil {
		return errResult(call, agentic.ToolErrorNetwork, "github_list_comments: %v", err)
	}
	out, _ := json.Marshal(map[string]any{"count": len(comments), "comments": comments})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(out), StopLoop: true}, nil
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Error: fmt.Sprintf(format, args...), ErrorKind: kind}, nil
}
