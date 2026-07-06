package openspec

import "strings"

// RenderChangeFolder renders a whole Change as a single OpenSpec-markdown
// document — the proposal, optional design, per-capability delta specs, and
// tasks, each under an HTML-comment file marker naming its
// openspec/changes/<slug>/ path. It is the on-demand projection the
// render_openspec tool returns and any caller that needs the change as one
// readable doc (vs. WriteChange's multi-file folder on disk).
//
// Pure + dep-free (composes the per-artifact renderers); stays in this package
// alongside them so the format layer remains extraction-ready. Cosmetic "# Title" headings are NOT modeled by the
// graph (facts_change.go), so an artifact reconstructed via ChangeFromFacts has
// an empty Title; this fills a sensible default on a LOCAL copy (never mutates
// the caller's Change).
func RenderChangeFolder(c *Change) string {
	var b strings.Builder
	b.WriteString("# OpenSpec change: ")
	b.WriteString(c.Slug)
	b.WriteString("\n")

	base := "openspec/changes/" + c.Slug + "/"
	if c.Proposal != nil {
		p := *c.Proposal
		if p.Title == "" {
			p.Title = "Proposal"
		}
		writeChangeArtifact(&b, base+"proposal.md", RenderProposal(&p))
	}
	if c.Design != nil {
		d := *c.Design
		if d.Title == "" {
			d.Title = "Design"
		}
		writeChangeArtifact(&b, base+"design.md", RenderDesign(&d))
	}
	for i := range c.Deltas {
		// Value copy for the Title write; the inner slices (Added/Modified/Removed)
		// are shared with the caller, which is safe ONLY because RenderDelta reads
		// them and never mutates. Keep that invariant if the render path changes.
		d := c.Deltas[i]
		if d.Title == "" {
			d.Title = d.Capability
		}
		writeChangeArtifact(&b, base+"specs/"+d.Capability+"/spec.md", RenderDelta(&d))
	}
	if c.Tasks != nil {
		t := *c.Tasks
		if t.Title == "" {
			t.Title = "Tasks"
		}
		writeChangeArtifact(&b, base+"tasks.md", RenderTasks(&t))
	}
	return b.String()
}

// writeChangeArtifact appends one file's rendered content under an
// HTML-comment marker naming its in-folder path, normalising a trailing
// newline so artifacts don't run together.
func writeChangeArtifact(b *strings.Builder, path, content string) {
	b.WriteString("\n<!-- ")
	b.WriteString(path)
	b.WriteString(" -->\n\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
}

// RenderSpec renders a Spec back to OpenSpec capability spec.md markdown. The
// output is canonical (one blank line between blocks, "- " bullets); it is not
// guaranteed to be byte-identical to arbitrary input, because the round-trip
// contract is semantic stability — parse(RenderSpec(parse(x))) equals parse(x)
// — not byte stability. Spec.Warnings is diagnostic-only and is
// not rendered. RenderSpec panics on a nil Spec.
func RenderSpec(s *Spec) string {
	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(s.Title)
	b.WriteString("\n")

	if s.Purpose != "" {
		b.WriteString("\n## Purpose\n")
		b.WriteString(s.Purpose)
		b.WriteString("\n")
	}

	b.WriteString("\n## Requirements\n")
	for _, r := range s.Requirements {
		renderRequirement(&b, r, "", "")
	}

	return b.String()
}

// RenderDelta renders a Delta back to OpenSpec change delta spec.md markdown, in
// canonical section order (ADDED, MODIFIED, REMOVED); empty sections are
// omitted. Same semantic-stability contract as RenderSpec. Panics on a nil
// Delta.
func RenderDelta(d *Delta) string {
	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(d.Title)
	b.WriteString("\n")

	if len(d.Added) > 0 {
		b.WriteString("\n## ADDED Requirements\n")
		for _, r := range d.Added {
			renderRequirement(&b, r, "", "")
		}
	}
	if len(d.Modified) > 0 {
		b.WriteString("\n## MODIFIED Requirements\n")
		for _, m := range d.Modified {
			renderRequirement(&b, m.Requirement, m.Previously, "")
		}
	}
	if len(d.Removed) > 0 {
		b.WriteString("\n## REMOVED Requirements\n")
		for _, r := range d.Removed {
			renderRequirement(&b, Requirement{Name: r.Name}, "", r.Rationale)
		}
	}

	return b.String()
}

// renderRequirement writes one "### Requirement:" block: name, optional
// statement, optional "(Previously:)" / "(Rationale:)" annotations (delta
// MODIFIED/REMOVED only — empty otherwise), then scenarios.
func renderRequirement(b *strings.Builder, r Requirement, previously, rationale string) {
	b.WriteString("\n### Requirement: ")
	b.WriteString(r.Name)
	b.WriteString("\n")
	if r.Statement != "" {
		b.WriteString(r.Statement)
		b.WriteString("\n")
	}
	if previously != "" {
		b.WriteString("(Previously: ")
		b.WriteString(previously)
		b.WriteString(")\n")
	}
	if rationale != "" {
		b.WriteString("(Rationale: ")
		b.WriteString(rationale)
		b.WriteString(")\n")
	}
	for _, sc := range r.Scenarios {
		renderScenario(b, sc)
	}
}

// renderScenario writes one "#### Scenario:" block. Steps are normalised to
// "- " bullets; a step with an empty Keyword is written verbatim from Text.
func renderScenario(b *strings.Builder, sc Scenario) {
	b.WriteString("\n#### Scenario: ")
	b.WriteString(sc.Name)
	b.WriteString("\n")
	for _, st := range sc.Steps {
		b.WriteString("- ")
		if st.Keyword != "" {
			b.WriteString(st.Keyword)
			if st.Text != "" {
				b.WriteString(" ")
				b.WriteString(st.Text)
			}
		} else {
			b.WriteString(st.Text)
		}
		b.WriteString("\n")
	}
}

// RenderProposal renders a Proposal back to OpenSpec proposal.md markdown.
// Scope is written under canonical "In scope:" / "Out of scope:" labels (a
// flat-bullet input thus normalises to labelled in-scope). Same
// semantic-stability contract as RenderSpec. Panics on a nil Proposal.
func RenderProposal(p *Proposal) string {
	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(p.Title)
	b.WriteString("\n")

	if p.Intent != "" {
		b.WriteString("\n## Intent\n")
		b.WriteString(p.Intent)
		b.WriteString("\n")
	}

	if len(p.ScopeIn) > 0 || len(p.ScopeOut) > 0 {
		b.WriteString("\n## Scope\n")
		if len(p.ScopeIn) > 0 {
			b.WriteString("In scope:\n")
			renderBullets(&b, p.ScopeIn)
		}
		if len(p.ScopeOut) > 0 {
			if len(p.ScopeIn) > 0 {
				b.WriteString("\n")
			}
			b.WriteString("Out of scope:\n")
			renderBullets(&b, p.ScopeOut)
		}
	}

	if p.Approach != "" {
		b.WriteString("\n## Approach\n")
		b.WriteString(p.Approach)
		b.WriteString("\n")
	}

	renderExtraSections(&b, p.ExtraSections)

	return b.String()
}

// RenderDesign renders a Design back to OpenSpec design.md markdown. Same
// semantic-stability contract as RenderSpec. Panics on a nil Design.
func RenderDesign(d *Design) string {
	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(d.Title)
	b.WriteString("\n")

	if d.TechnicalApproach != "" {
		b.WriteString("\n## Technical Approach\n")
		b.WriteString(d.TechnicalApproach)
		b.WriteString("\n")
	}

	if len(d.Decisions) > 0 {
		b.WriteString("\n## Architecture Decisions\n")
		for _, dec := range d.Decisions {
			b.WriteString("\n### Decision: ")
			b.WriteString(dec.Name)
			b.WriteString("\n")
			if dec.Body != "" {
				b.WriteString(dec.Body)
				b.WriteString("\n")
			}
		}
	}

	if d.DataFlow != "" {
		b.WriteString("\n## Data Flow\n")
		b.WriteString(d.DataFlow)
		b.WriteString("\n")
	}

	if len(d.FileChanges) > 0 {
		b.WriteString("\n## File Changes\n")
		for _, fc := range d.FileChanges {
			renderFileChange(&b, fc)
		}
	}

	renderExtraSections(&b, d.ExtraSections)

	return b.String()
}

func renderExtraSections(b *strings.Builder, sections []MarkdownSection) {
	for _, sec := range sections {
		if sec.Heading == "" {
			continue
		}
		b.WriteString("\n## ")
		b.WriteString(sec.Heading)
		b.WriteString("\n")
		if sec.Body != "" {
			b.WriteString(sec.Body)
			b.WriteString("\n")
		}
	}
}

// renderFileChange writes one "## File Changes" bullet — the inverse of
// parseFileChange: "- `<path>`" plus " (<kind>)" when Kind is set.
func renderFileChange(b *strings.Builder, fc FileChange) {
	b.WriteString("- `")
	b.WriteString(fc.Path)
	b.WriteString("`")
	if fc.Kind != "" {
		b.WriteString(" (")
		b.WriteString(fc.Kind)
		b.WriteString(")")
	}
	b.WriteString("\n")
}

// RenderTasks renders a Tasks back to OpenSpec tasks.md markdown. A section
// with an empty Name renders its tasks with no "## " heading (the implicit
// section). Same semantic-stability contract as RenderSpec. Panics on nil.
func RenderTasks(t *Tasks) string {
	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(t.Title)
	b.WriteString("\n")

	for _, sec := range t.Sections {
		if sec.Name != "" {
			b.WriteString("\n## ")
			b.WriteString(sec.Name)
			b.WriteString("\n")
		}
		for _, task := range sec.Tasks {
			b.WriteString("- [")
			if task.Done {
				b.WriteByte('x')
			} else {
				b.WriteByte(' ')
			}
			b.WriteString("]")
			if content := taskContent(task); content != "" {
				b.WriteByte(' ')
				b.WriteString(content)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// taskContent joins a task's number and text into the bullet body — the
// inverse of splitTaskNumber.
func taskContent(t Task) string {
	switch {
	case t.Number != "" && t.Text != "":
		return t.Number + " " + t.Text
	case t.Number != "":
		return t.Number
	default:
		return t.Text
	}
}

// renderBullets writes each item as a "- <item>" line.
func renderBullets(b *strings.Builder, items []string) {
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it)
		b.WriteString("\n")
	}
}
