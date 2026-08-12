// Package openspec parses and renders the OpenSpec spec-driven-development
// markdown format (https://github.com/Fission-AI/OpenSpec) to and from a Go
// model. It is the FORMAT ENGINE of semdev's openspec-io capability, ported
// from semteams' dep-free engine. The graph seam lives elsewhere: the whole
// change travels as ONE canonical document blob (openspec.change.document /
// openspec.spec.document — internal/changefacts and internal/specfacts), which
// JSON-serializes this package's model verbatim; the earlier fine-grained
// per-field Facts projection was retired with that flatten.
//
// OpenSpec requirements are RFC-2119 "The system SHALL ..." statements paired
// with Given/When/Then scenarios. The round-trip contract is *semantic*
// stability, not byte stability: parse(render(parse(x))) equals parse(x) on the
// modeled fields.
//
// The model is split across files by artifact: this file holds the capability
// spec types; model_delta.go the change-delta types; model_change.go the
// proposal/design/tasks types.
//
// The whole package is dependency-free (stdlib only) by design: the format layer
// (model*.go, parse.go, render.go, change.go) implements the external OpenSpec
// standard independent of NATS and of entity identity.
package openspec

// Spec is a capability's living specification — the content of one
// openspec/specs/<capability>/spec.md file (stored on the graph as the
// openspec.spec.document blob, internal/specfacts).
type Spec struct {
	// Capability is the specs/<capability>/ folder name. It is not part of
	// the markdown body; callers parsing a file set it from the path.
	Capability string
	// Title is the text of the "# <Title>" heading (e.g. "Auth Specification").
	Title string
	// Purpose is the "## Purpose" prose.
	Purpose string
	// Requirements are the "### Requirement: <Name>" blocks under
	// "## Requirements", in document order.
	Requirements []Requirement
	// Warnings records non-fatal structural problems found while parsing
	// (e.g. a scenario before any requirement, whose Given/When/Then content
	// is dropped). Ingest is best-effort and lenient by design;
	// Warnings makes the lossy cases visible instead of silent. It is
	// diagnostic metadata, not round-trippable content — render ignores it.
	// Empty on a clean parse.
	Warnings []string
}

// Requirement is one "### Requirement: <Name>" block: an RFC-2119 statement
// plus its Given/When/Then scenarios.
type Requirement struct {
	// Name is the text after "### Requirement:". The graph layer stores the
	// whole change as one canonical document blob (internal/changefacts), so
	// the original Name always survives the round-trip verbatim — nothing
	// derives or reverses a slug from it.
	Name string
	// Statement is the RFC-2119 body, e.g. "The system SHALL ...".
	Statement string
	// Scenarios are the "#### Scenario: <Name>" blocks, in document order.
	Scenarios []Scenario
}

// Scenario is a "#### Scenario: <Name>" Given/When/Then block.
type Scenario struct {
	Name  string
	Steps []Step
}

// Step is one scenario bullet. Keyword is the leading GIVEN/WHEN/THEN/AND
// (uppercased); for a bullet with no recognised keyword Keyword is empty and
// the whole bullet text is preserved in Text so it still round-trips.
type Step struct {
	Keyword string
	Text    string
}
