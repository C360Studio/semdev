package validatechange

// payload is the validate_change input: which change on the run to validate. It
// carries only the slug (content) — no outcome/valid/pass field (G3); the verdict
// comes from the CLI's exit code, not the model.
type payload struct {
	Slug string `json:"slug"`
}
