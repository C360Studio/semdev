package listcomments

import "github.com/c360studio/semstreams/agentic"

// payload is the github_list_comments input: the coordinates of the thread to
// read. Content only — no outcome field (G3); it is a read.
type payload struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// ListTools returns the github_list_comments schema — owner/repo/number, all
// required, read-only.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"owner":  map[string]any{"type": "string", "description": "Repository owner (org or user)."},
			"repo":   map[string]any{"type": "string", "description": "Repository name."},
			"number": map[string]any{"type": "integer", "description": "The issue or pull-request number whose comments to read."},
		},
		"required": []string{"owner", "repo", "number"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "List the comments on a GitHub issue or pull request (oldest first) to ground a decision in the conversation so far. Read-only.",
		Parameters:  params,
	}}
}
