package readworkspace

import "github.com/c360studio/semstreams/agentic"

// ListTools returns read_workspace's schema. It takes `path` (required) and an optional
// pagination `offset` — NO outcome field (G3): this is a read, so there is nothing for the
// model to assert.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Repo-relative file or directory path inside the run's checkout to read. Path-guarded: a path that escapes the checkout is rejected.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Byte offset to resume reading a file from (for pagination across calls). Defaults to 0. Ignored for a directory path.",
			},
		},
		"required": []string{"path"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Read a file's contents from the run's isolated checkout — read-only, path-guarded to inside the checkout. Returns up to ~32KB per call starting at offset, with a next_offset for continuation when the file is larger. A directory path returns a listing of its entries instead. You supply no outcome; this is a read.",
		Parameters:  params,
	}}
}
