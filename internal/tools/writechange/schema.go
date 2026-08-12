package writechange

import "github.com/c360studio/semstreams/agentic"

// ListTools returns the write_change tool's LLM-facing schema. The only input is
// the change slug — which change on the run to materialize. It writes files, not
// facts: no outcome/validated/pass field (G3), and neither the run entity nor the
// workspace path is model-supplied (the run is resolved from call metadata, the
// path from the injected workspace resolver).
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"slug": map[string]any{"type": "string", "description": "The change slug (changes/<slug>/) to write into the workspace from the run's facts."},
		},
		"required": []string{"slug"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Effect:      agentic.ToolEffectMutating,
		Description: "Materialize an OpenSpec change as an on-disk folder in the run's target-repo workspace, rendered from the run's openspec.change.* facts, so it can be committed for the PR. Writes files only — stamps no facts.",
		Parameters:  params,
	}}
}
