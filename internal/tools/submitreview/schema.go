package submitreview

import "github.com/c360studio/semstreams/agentic"

// ListTools returns submit_review's LLM-facing schema. Review is PER TASK: the
// inputs are the task selector (task_index) and the findings you raise reviewing
// THAT task. There is NO approve/verdict/outcome field (G3): the per-task verdict
// is DERIVED here from that task's measured fact (measurement.result.<i>) and your
// findings, so the reviewer can require more but can never approve past a failing
// measurement. Approval is not Quinn's to assert; it is the harness's to compute.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_index": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "The index of the task you are reviewing (its task.spec.<task_index>). Review one unit of work at a time.",
			},
			"findings": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "The required changes you found reviewing THIS task's work against its task.spec. Review adversarially — try to refute the attempt. Each finding is an additive constraint that must be addressed before this task can be approved; pass an empty list when you require nothing further. You cannot approve — approval is derived from this task's measured fact plus the absence of findings, and a failing measurement can never be approved however the work describes itself.",
			},
		},
		"required": []string{"task_index"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Effect:      agentic.ToolEffectMutating,
		Description: "Record your adversarial review of one task's developed work. Provide the task_index and the required changes you found (findings); the per-task verdict is computed from that task's harness measurement plus your findings — approved only when the task has a passing measurement AND you raised no finding, otherwise changes_requested. You read the stamped measurement facts, not the work's claims about itself.",
		Parameters:  params,
	}}
}
