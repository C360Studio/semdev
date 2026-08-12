package applypatch

import "github.com/c360studio/semstreams/agentic"

// ListTools returns apply_patch's schema. It takes ONLY the diff — the change the
// developer authored — and NO outcome (G3): the model supplies the intelligence (the
// fix), the harness supplies whether the task then passes (a separate measurement).
// The diff writes to the run's isolated checkout only (path-guarded), never the host.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"diff": map[string]any{
				"type":        "string",
				"description": "A unified diff (git format with a/ and b/ prefixes) authoring the change. It is applied to the run's checkout, path-guarded to inside the checkout; every touched file must be repo-relative and inside the tree.",
			},
		},
		"required": []any{"diff"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Effect:      agentic.ToolEffectMutating,
		Description: "Author a code change by emitting a unified diff. The harness applies it to the run's isolated checkout (path-guarded — a diff that escapes the checkout is rejected) and reports which files it touched. It does NOT run tests or record pass/fail — you cannot supply the outcome; the harness measures the real result separately.",
		Parameters:  params,
	}}
}
