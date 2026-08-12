package openspec

// The proposal/design/tasks types in this file intentionally carry no Warnings
// field (unlike Spec/Delta in model.go/model_delta.go). Their lenient-ingest
// drops are minor and round-trip stably — stray "## Scope" prose, a misplaced
// "### Decision:" body, trailing text after a file-change "(kind)", a
// non-checkbox task bullet — and authored output is gated by create_change's
// create_change author tool, not by ingest.

// Proposal is a change's proposal.md:
// the "why" and shape of a change, before specs/design/tasks.
type Proposal struct {
	// Title is the "# <Title>" heading (e.g. "Proposal: Add Dark Mode").
	Title string
	// Intent is the "## Intent" prose — why the change matters.
	Intent string
	// ScopeIn / ScopeOut are the "## Scope" bullets. OpenSpec writes them under
	// "In scope:" / "Out of scope:" labels; a flat bullet list (no labels) is
	// taken as in-scope.
	ScopeIn  []string
	ScopeOut []string
	// Approach is the "## Approach" prose — high-level direction.
	Approach string
	// ExtraSections preserves non-normative "## ..." sections that are not part
	// of the modeled OpenSpec proposal contract, such as Future Roadmap notes.
	ExtraSections []MarkdownSection
}

// Design is a change's design.md: the
// technical approach, architecture decisions, data flow, and file changes.
type Design struct {
	// Title is the "# <Title>" heading (e.g. "Design: Add Dark Mode").
	Title string
	// TechnicalApproach is the "## Technical Approach" prose.
	TechnicalApproach string
	// Decisions are the "## Architecture Decisions" → "### Decision: <Name>"
	// blocks, in document order.
	Decisions []DesignDecision
	// DataFlow is the "## Data Flow" prose.
	DataFlow string
	// FileChanges are the "## File Changes" bullets (`path` + kind).
	FileChanges []FileChange
	// ExtraSections preserves non-normative "## ..." sections that are not part
	// of the modeled OpenSpec design contract, such as Future Roadmap notes.
	ExtraSections []MarkdownSection
}

// MarkdownSection is an unmodeled top-level "## <Heading>" section preserved
// for filesystem round-trip/export fidelity. The graph fact model intentionally
// ignores it; it is prose source material, not governed execution state.
type MarkdownSection struct {
	Heading string
	Body    string
}

// DesignDecision is one "### Decision: <Name>" block under Architecture
// Decisions: a name and its rationale prose.
type DesignDecision struct {
	Name string
	Body string
}

// FileChange is one "## File Changes" bullet — a backtick-quoted path with an
// optional "(new|modified|deleted)" kind.
type FileChange struct {
	Path string
	Kind string
}

// Tasks is a change's tasks.md. OpenSpec
// tasks.md is THIN — checkboxes grouped by section. The execution-rich fields
// (target_files, test_command, …) that dev-from-task needs are graph-only,
// authored by create_change, and are NOT represented here.
type Tasks struct {
	// Title is the "# <Title>" heading (typically "Tasks").
	Title string
	// Sections are the "## <heading>" groups, in document order.
	Sections []TaskSection
}

// TaskSection is a "## <Name>" group of tasks. Name is the full heading text
// (OpenSpec numbers them, e.g. "1. Theme Infrastructure"); an empty Name is an
// implicit section holding tasks that appeared before any heading. An empty
// Name is only meaningful at index 0 — render emits no "## " heading for it, so
// a mid-list empty-Name section would merge into its predecessor on re-parse.
type TaskSection struct {
	Name  string
	Tasks []Task
}

// Task is one "- [ ] 1.1 <text>" checkbox. Number is OpenSpec's dotted number
// ("1.1"), empty if absent; Done reflects "[x]" vs "[ ]".
type Task struct {
	Number string
	Text   string
	Done   bool
}
