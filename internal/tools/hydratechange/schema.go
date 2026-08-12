package hydratechange

import "github.com/c360studio/semstreams/agentic"

// ListTools returns the render_openspec tool's LLM-facing schema. The only input
// is the change slug — which change on the run to render. It is a read-only
// projection: no outcome/validated/pass field (G3, trivially — nothing is
// stamped), and the run entity is resolved for the tool from call metadata, not
// supplied by the model.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"slug": map[string]any{"type": "string", "description": "The change slug (changes/<slug>/) to render from the run's facts."},
		},
		"required": []string{"slug"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Effect:      agentic.ToolEffectReadOnly,
		Description: "Render an OpenSpec change back to markdown from the run's openspec.change.* facts (the graph is authoritative; the artifact is a projection). Returns proposal/specs/tasks as one document. Read-only — stamps nothing.",
		Parameters:  params,
	}}
}
