package measuretask

import "github.com/c360studio/semstreams/agentic"

// ListTools returns the measure_task tool's LLM-facing schema. The ONLY input is
// the task selector (task_index) — NO outcome field (pass/exit_code/…) and NO
// command. The command is read from the immutable task.spec on the graph, and the
// outcome is derived by the harness from the real exit code (G3): the model may say
// WHICH task to measure, it may not supply the command it runs or the verdict it
// records.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_index": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "The zero-based index of the projected task (task.spec.<i>) whose test_command to run and measure.",
			},
		},
		"required": []string{"task_index"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Effect:      agentic.ToolEffectMutating,
		Description: "Run a projected task's test_command and record the measured outcome. Reads the immutable task.spec.<task_index>.test_command from the run (you do not supply the command), runs it in the run's workspace, and stamps measurement.result.<task_index> from the real exit code — the pass/fail is measured, never asserted. Emit only the task index.",
		Parameters:  params,
	}}
}
