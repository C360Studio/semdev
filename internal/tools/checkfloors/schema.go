package checkfloors

import "github.com/c360studio/semstreams/agentic"

// ListTools returns check_floors' schema. The ONLY input is the task selector: the
// floors are DETERMINISTIC and read the attempt's authored source, so the model may
// only say WHICH task to evaluate — it cannot supply a floor verdict or a
// passed/rejected outcome (G3). The findings are computed from the source, not
// asserted.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_index": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "The zero-based index of the projected task (task.spec.<i>) whose current attempt to evaluate against the deterministic floors.",
			},
		},
		"required": []string{"task_index"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Run the deterministic floor checks over your current attempt at a task (source-build integrity, tests-must-exist, vacuous-test, stub, anti-mock) and record the findings. The verdicts are computed from your authored source — you cannot supply them. A rejecting finding blocks advance to review until you fix it. Emit only the task index.",
		Parameters:  params,
	}}
}
