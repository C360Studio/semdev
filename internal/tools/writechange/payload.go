package writechange

// payload is the write_change input: which change on the run to materialize. It
// carries only the slug (content) — no outcome or status field (G3), and no
// caller-supplied path (the workspace dir is resolved, not model-supplied).
type payload struct {
	Slug string `json:"slug"`
}
