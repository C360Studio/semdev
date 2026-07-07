package projecttasks

import "github.com/c360studio/semstreams/agentic"

// ListTools returns the project_tasks tool's LLM-facing schema. The only input is
// the change slug to project (a content selector, resolved against the run entity
// the tool is given from call metadata) — NO outcome/status field (G3). The task
// definitions are read from the graph, not supplied by the model; the model only
// says WHICH approved change to freeze into task.spec.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"slug": map[string]any{"type": "string", "description": "The approved change to project into task.spec (changes/<slug>/)."},
		},
		"required": []string{"slug"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Project an approved change's tasks into the immutable task.spec facts the dev loop converges on. Reads the change's task facts from the run entity (resolved for you), enforces the task schema and clamps the iteration budget, and stamps task.spec — or fails toward the human if a task is missing a required field. Emit only the change slug.",
		Parameters:  params,
	}}
}
