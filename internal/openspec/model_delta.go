package openspec

// Delta is a change's delta spec for one capability — the content of
// changes/<slug>/specs/<capability>/spec.md. It groups requirement changes by
// operation. The three
// slices map to OpenSpec's "## ADDED / MODIFIED / REMOVED Requirements"
// sections; render always emits them in that canonical order, so the round-trip
// is stable regardless of the input's section ordering.
type Delta struct {
	// Capability is the specs/<capability>/ folder name; set from the path,
	// not the markdown body.
	Capability string
	// Title is the text of the "# <Title>" heading (e.g. "Delta for Auth").
	Title string
	// Added are "## ADDED Requirements" — brand-new requirements.
	Added []Requirement
	// Modified are "## MODIFIED Requirements" — each the full revised
	// requirement plus its prior statement.
	Modified []ModifiedRequirement
	// Removed are "## REMOVED Requirements" — name + rationale only.
	Removed []RemovedRequirement
	// Warnings records non-fatal structural problems found while parsing
	// (see Spec.Warnings); diagnostic, not round-trippable.
	Warnings []string
}

// ModifiedRequirement is a "## MODIFIED Requirements" entry: the full revised
// Requirement plus Previously, the prior statement OpenSpec records inline as
// "(Previously: ...)".
type ModifiedRequirement struct {
	Requirement
	Previously string
}

// RemovedRequirement is a "## REMOVED Requirements" entry: the requirement name
// plus Rationale, recorded as "(Rationale: ...)".
type RemovedRequirement struct {
	Name      string
	Rationale string
}
