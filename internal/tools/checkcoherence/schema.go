package checkcoherence

import "github.com/c360studio/semstreams/agentic"

// ListTools returns check_coherence's schema. It takes NO arguments (G3): the gate READS
// the harness-stamped delivery signals (verify.result, openspec.validated, the projected
// task.spec set, each review.verdict) and DERIVES coherence in Go — the model may only
// trigger the roll-up, never supply a signal or a decision.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Gate the run for delivery: the harness rolls up the clean-room verify result, the OpenSpec validation, and every task's review verdict, then derives coherent (open the PR) or blocked (park toward the human). You supply nothing and claim no outcome. Calling it completes your turn.",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{},
		},
	}}
}
