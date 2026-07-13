package checkgate

import "github.com/c360studio/semstreams/agentic"

// ListTools returns check_gate's schema. The ONLY input is the task selector: the
// gate READS harness-computed facts (measurement.result.<i>.passed, floor.finding.
// <i>.rejected, the distinct attempt count, task.spec.<i>.budget) and DERIVES the
// route in Go — so the model may only say WHICH task to gate, never supply a
// decision, a pass/fail, or an attempt count (G3). The routing decision is computed
// from the recorded evidence, not asserted.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_index": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "The zero-based index of the projected task (task.spec.<i>) whose dev-loop attempt to gate: advance if it measured green and no floor rejected, retry if the budget remains, else escalate.",
			},
		},
		"required": []string{"task_index"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Gate a task's dev-loop attempt: the harness reads the recorded measurement and floor findings and the attempt count against the task's iteration budget, then derives advance / retry / escalate. You supply no verdict, count, or decision — only the task index. Calling it completes your turn.",
		Parameters:  params,
	}}
}
