package readdiff

import "github.com/c360studio/semstreams/agentic"

// ListTools returns read_diff's schema. It takes NO arguments (G3): the run — and hence
// which checkout's diff to read — is resolved from the tool call's own metadata, never a
// model-supplied path or ref.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Return the unified diff of everything this run has authored so far (base..HEAD over the run's committed checkout) — the cumulative change to review. Read-only; you supply nothing.",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{},
		},
	}}
}
