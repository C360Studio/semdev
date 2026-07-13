package openpr

import "github.com/c360studio/semstreams/agentic"

// ListTools returns open_pr's schema. It takes NO arguments (G3): the run has already been
// cleared by the coherence gate, and the harness forms the delivery reference — the model
// may only trigger delivery, never supply the ref or any outcome.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Open the pull request for the delivered, verified change. The run has passed the coherence gate (clean-room verify, OpenSpec validation, and every task approved); the harness records the delivery reference. You supply nothing. Calling it completes your turn.",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{},
		},
	}}
}
