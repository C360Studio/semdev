package hydratechange

// payload is the render_openspec input: which change on the run to render. It
// carries only the slug (content) — no outcome or status field (G3).
type payload struct {
	Slug string `json:"slug"`
}
