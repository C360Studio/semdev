package openspec

import (
	"fmt"
	"strings"
)

// scenarioStepKeywords are the recognised Given/When/Then bullet leads.
var scenarioStepKeywords = map[string]bool{
	"GIVEN": true,
	"WHEN":  true,
	"THEN":  true,
	"AND":   true,
}

// bulletMarkers are the CommonMark unordered-list markers a scenario step may
// use. OpenSpec's canonical form is "- ", but real brownfield specs (and LLM
// output) also use "* " and "+ "; all three are recognised on ingest and
// normalised back to "- " on render.
var bulletMarkers = []string{"- ", "* ", "+ "}

// ParseSpec parses a capability spec.md body into a Spec. It is a line-oriented
// state machine over OpenSpec's regular heading structure:
//
//	# <Title>
//	## Purpose
//	## Requirements
//	### Requirement: <Name>
//	<RFC-2119 statement>
//	#### Scenario: <Name>
//	- GIVEN/WHEN/THEN/AND <text>
//
// Ingest is lenient by design: ParseSpec never fails, returning a best-effort Spec.
// Content that cannot be placed is dropped; the structural cases are recorded
// in Spec.Warnings rather than lost silently:
//
//   - an unknown "## " section — its prose is skipped (forward-compat);
//   - a "#### Scenario:" before any requirement — dropped, and warned;
//   - prose after a scenario has opened — dropped (it is past the statement slot);
//   - a "### Requirement:" with an empty name — kept as-is.
//
// Strict structural validation (every requirement has a SHALL statement and
// ≥1 scenario) is deliberately NOT done here — it belongs to create_change's
// create_change author tool, which gates authored output, not ingest.
func ParseSpec(md string) *Spec {
	s := &Spec{}

	var (
		section      string // "purpose" | "requirements" | "" (other/none)
		purposeLines []string
		stmtLines    []string
		curReq       *Requirement
		curScen      *Scenario
	)

	flushScenario := func() {
		if curReq != nil && curScen != nil {
			curReq.Scenarios = append(curReq.Scenarios, *curScen)
		}
		curScen = nil
	}
	flushRequirement := func() {
		flushScenario()
		if curReq != nil {
			if len(stmtLines) > 0 {
				// Newline-join (matching Purpose) preserves the authored line
				// structure of a multi-line statement; render writes it back
				// verbatim, so re-parse is stable.
				curReq.Statement = strings.TrimSpace(strings.Join(stmtLines, "\n"))
			}
			s.Requirements = append(s.Requirements, *curReq)
		}
		curReq = nil
		stmtLines = nil
	}

	for raw := range strings.SplitSeq(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "#### Scenario:"):
			flushScenario()
			name := strings.TrimSpace(strings.TrimPrefix(line, "#### Scenario:"))
			if curReq == nil {
				s.Warnings = append(s.Warnings,
					fmt.Sprintf("scenario %q before any requirement; dropped", name))
				break
			}
			curScen = &Scenario{Name: name}

		case strings.HasPrefix(line, "### Requirement:"):
			flushRequirement()
			curReq = &Requirement{Name: strings.TrimSpace(strings.TrimPrefix(line, "### Requirement:"))}

		case strings.HasPrefix(line, "## "):
			flushRequirement()
			switch strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## "))) {
			case "purpose":
				section = "purpose"
			case "requirements":
				section = "requirements"
			default:
				section = ""
			}

		case strings.HasPrefix(line, "# "):
			s.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))

		case trimmed == "":
			// blank line — boundaries are heading-driven; ignore

		default:
			if body, ok := bulletBody(trimmed); ok && curScen != nil {
				curScen.Steps = append(curScen.Steps, parseStep(body))
				break
			}
			switch {
			case section == "purpose":
				purposeLines = append(purposeLines, trimmed)
			case curReq != nil && curScen == nil:
				// Prose between a requirement header and its first scenario is
				// the RFC-2119 statement (bullets here are kept as statement
				// body, not steps, since no scenario is open).
				stmtLines = append(stmtLines, trimmed)
			}
		}
	}
	flushRequirement()

	s.Purpose = strings.TrimSpace(strings.Join(purposeLines, "\n"))
	return s
}

// bulletBody returns the text of an unordered-list item (after its
// "- " / "* " / "+ " marker, trimmed) and true, or "", false if the line is
// not a bullet.
func bulletBody(trimmed string) (string, bool) {
	for _, m := range bulletMarkers {
		if rest, ok := strings.CutPrefix(trimmed, m); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// parseStep splits a scenario bullet (without its leading marker) into a Step.
// A recognised leading keyword (GIVEN/WHEN/THEN/AND, case-insensitive) is
// uppercased into Keyword; otherwise Keyword is empty and the bullet is kept
// verbatim in Text so render reproduces it.
func parseStep(bullet string) Step {
	bullet = strings.TrimSpace(bullet)
	head, rest, _ := strings.Cut(bullet, " ")
	if kw := strings.ToUpper(head); scenarioStepKeywords[kw] {
		// rest is "" for a keyword-only bullet (e.g. "GIVEN"), which renders
		// back as "- GIVEN" — stable.
		return Step{Keyword: kw, Text: strings.TrimSpace(rest)}
	}
	return Step{Text: bullet}
}

// deltaSection is the current "## ADDED/MODIFIED/REMOVED Requirements" section
// while parsing a delta.
type deltaSection int

const (
	sectionNone deltaSection = iota
	sectionAdded
	sectionModified
	sectionRemoved
)

// reqAccum accumulates one "### Requirement:" block during delta parsing,
// before it is dispatched into Delta.Added/Modified/Removed on flush.
type reqAccum struct {
	name       string
	scenarios  []Scenario
	previously string
	rationale  string
}

// ParseDelta parses a change delta spec.md body into a Delta. It reuses the
// requirement/scenario grammar of ParseSpec, adding the three delta sections
// and the "(Previously: ...)" / "(Rationale: ...)" annotations. Like ParseSpec
// it is lenient and infallible: a "### Requirement:" outside
// any ADDED/MODIFIED/REMOVED section, or a scenario before any requirement, is
// dropped with a warning.
func ParseDelta(md string) *Delta {
	d := &Delta{}

	var (
		section deltaSection
		cur     *reqAccum
		curScen *Scenario
		stmt    []string
	)

	flushScenario := func() {
		if cur != nil && curScen != nil {
			cur.scenarios = append(cur.scenarios, *curScen)
		}
		curScen = nil
	}
	flushRequirement := func() {
		flushScenario()
		if cur != nil {
			statement := ""
			if len(stmt) > 0 {
				statement = strings.TrimSpace(strings.Join(stmt, "\n"))
			}
			appendDeltaRequirement(d, section, cur, statement)
		}
		cur = nil
		stmt = nil
	}

	for raw := range strings.SplitSeq(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "#### Scenario:"):
			flushScenario()
			name := strings.TrimSpace(strings.TrimPrefix(line, "#### Scenario:"))
			if cur == nil {
				d.Warnings = append(d.Warnings,
					fmt.Sprintf("scenario %q before any requirement; dropped", name))
				break
			}
			curScen = &Scenario{Name: name}

		case strings.HasPrefix(line, "### Requirement:"):
			flushRequirement()
			cur = &reqAccum{name: strings.TrimSpace(strings.TrimPrefix(line, "### Requirement:"))}

		case strings.HasPrefix(line, "## "):
			flushRequirement()
			section = parseDeltaSection(strings.TrimPrefix(line, "## "))

		case strings.HasPrefix(line, "# "):
			d.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))

		case trimmed == "":
			// blank line — ignore

		default:
			if cur != nil {
				if v, ok := parenAnnotation(trimmed, "Previously"); ok {
					cur.previously = v
					break
				}
				if v, ok := parenAnnotation(trimmed, "Rationale"); ok {
					cur.rationale = v
					break
				}
			}
			if body, ok := bulletBody(trimmed); ok && curScen != nil {
				curScen.Steps = append(curScen.Steps, parseStep(body))
				break
			}
			if cur != nil && curScen == nil {
				stmt = append(stmt, trimmed)
			}
		}
	}
	flushRequirement()

	return d
}

// appendDeltaRequirement dispatches a completed requirement accumulator into
// the matching Delta section, recording a warning for the lossy REMOVED case
// (content beyond name+rationale) and the orphan (sectionNone) case.
func appendDeltaRequirement(d *Delta, section deltaSection, cur *reqAccum, statement string) {
	req := Requirement{Name: cur.name, Statement: statement, Scenarios: cur.scenarios}
	switch section {
	case sectionAdded:
		d.Added = append(d.Added, req)
	case sectionModified:
		d.Modified = append(d.Modified, ModifiedRequirement{Requirement: req, Previously: cur.previously})
	case sectionRemoved:
		if statement != "" || len(cur.scenarios) > 0 {
			d.Warnings = append(d.Warnings,
				fmt.Sprintf("removed requirement %q carries a statement or scenarios; only name + rationale are kept", cur.name))
		}
		d.Removed = append(d.Removed, RemovedRequirement{Name: cur.name, Rationale: cur.rationale})
	default: // sectionNone
		d.Warnings = append(d.Warnings,
			fmt.Sprintf("requirement %q outside any ADDED/MODIFIED/REMOVED section; dropped", cur.name))
	}
}

// parseDeltaSection maps a "## " heading body to its delta section. The match
// is a case-insensitive prefix (not exact): OpenSpec's canonical heading is
// "## ADDED Requirements", but brownfield/LLM output also writes "## Added" or
// "## ADDED". An unrelated "## Added Notes" would false-positive into the
// section; that is an accepted leniency tradeoff (strict validation is
// the create_change author tool's job, ). An unrecognised heading yields sectionNone.
func parseDeltaSection(heading string) deltaSection {
	switch h := strings.ToUpper(strings.TrimSpace(heading)); {
	case strings.HasPrefix(h, "ADDED"):
		return sectionAdded
	case strings.HasPrefix(h, "MODIFIED"):
		return sectionModified
	case strings.HasPrefix(h, "REMOVED"):
		return sectionRemoved
	default:
		return sectionNone
	}
}

// parenAnnotation parses a single-line "(Label: value)" annotation, matching
// the label case-insensitively for lenient ingest (OpenSpec emits the
// canonical capitalized form, but brownfield/LLM output may not). It returns
// the trimmed value and true on a match, else "", false. The closing ")" is
// optional — a value with no terminator is taken to end-of-line — and an
// interior ")" is preserved, since only the final one is trimmed.
func parenAnnotation(trimmed, label string) (string, bool) {
	prefix := "(" + label + ":"
	if len(trimmed) < len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimSpace(trimmed[len(prefix):]), ")")
	return strings.TrimSpace(inner), true
}

// ParseProposal parses a change proposal.md body into a Proposal. Sections are
// matched case-insensitively by prefix; "## Scope" bullets are split by the
// "In scope:" / "Out of scope:" sub-labels, defaulting to in-scope when a flat
// bullet list is used. Lenient and infallible.
func ParseProposal(md string) *Proposal {
	p := &Proposal{}

	var (
		section  string // "intent" | "scope" | "approach" | ""
		scopeOut bool   // within scope: are we under "Out of scope:"?
		intent   []string
		approach []string
		extra    *MarkdownSection
		extraRaw []string
	)
	flushExtra := func() {
		if extra != nil {
			extra.Body = strings.TrimSpace(strings.Join(extraRaw, "\n"))
			p.ExtraSections = append(p.ExtraSections, *extra)
		}
		extra = nil
		extraRaw = nil
	}

	for raw := range strings.SplitSeq(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "## "):
			flushExtra()
			heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			section, scopeOut = proposalSection(heading), false
			if section == "" {
				extra = &MarkdownSection{Heading: heading}
			}
		case strings.HasPrefix(line, "# "):
			p.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		case trimmed == "":
			// ignore
		default:
			switch section {
			case "intent":
				intent = append(intent, trimmed)
			case "approach":
				approach = append(approach, trimmed)
			case "scope":
				switch low := strings.ToLower(trimmed); {
				case strings.HasPrefix(low, "in scope"):
					scopeOut = false
				case strings.HasPrefix(low, "out of scope"), strings.HasPrefix(low, "out-of-scope"):
					scopeOut = true
				default:
					if body, ok := bulletBody(trimmed); ok {
						if scopeOut {
							p.ScopeOut = append(p.ScopeOut, body)
						} else {
							p.ScopeIn = append(p.ScopeIn, body)
						}
					}
				}
			default:
				if extra != nil {
					extraRaw = append(extraRaw, trimmed)
				}
			}
		}
	}
	flushExtra()

	p.Intent = strings.TrimSpace(strings.Join(intent, "\n"))
	p.Approach = strings.TrimSpace(strings.Join(approach, "\n"))
	return p
}

// proposalSection classifies a "## " heading body for a proposal.
func proposalSection(heading string) string {
	switch h := strings.ToLower(strings.TrimSpace(heading)); {
	case strings.HasPrefix(h, "intent"):
		return "intent"
	case strings.HasPrefix(h, "scope"):
		return "scope"
	case strings.HasPrefix(h, "approach"):
		return "approach"
	default:
		return ""
	}
}

// ParseDesign parses a change design.md body into a Design. Lenient and
// infallible.
func ParseDesign(md string) *Design {
	d := &Design{}

	var (
		section   string // "technical" | "decisions" | "dataflow" | "filechanges" | ""
		technical []string
		dataFlow  []string
		curDec    *DesignDecision
		decBody   []string
		extra     *MarkdownSection
		extraRaw  []string
	)

	flushDecision := func() {
		if curDec != nil {
			curDec.Body = strings.TrimSpace(strings.Join(decBody, "\n"))
			d.Decisions = append(d.Decisions, *curDec)
		}
		curDec = nil
		decBody = nil
	}
	flushExtra := func() {
		if extra != nil {
			extra.Body = strings.TrimSpace(strings.Join(extraRaw, "\n"))
			d.ExtraSections = append(d.ExtraSections, *extra)
		}
		extra = nil
		extraRaw = nil
	}

	for raw := range strings.SplitSeq(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case section == "decisions" && strings.HasPrefix(line, "### Decision:"):
			flushDecision()
			curDec = &DesignDecision{Name: strings.TrimSpace(strings.TrimPrefix(line, "### Decision:"))}
		case strings.HasPrefix(line, "## "):
			flushDecision()
			flushExtra()
			heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			section = designSection(heading)
			if section == "" {
				extra = &MarkdownSection{Heading: heading}
			}
		case strings.HasPrefix(line, "# "):
			d.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		case trimmed == "":
			// ignore
		default:
			switch section {
			case "technical":
				technical = append(technical, trimmed)
			case "dataflow":
				dataFlow = append(dataFlow, trimmed)
			case "decisions":
				if curDec != nil {
					decBody = append(decBody, trimmed)
				}
			case "filechanges":
				if fc, ok := parseFileChange(trimmed); ok {
					d.FileChanges = append(d.FileChanges, fc)
				}
			default:
				if extra != nil {
					extraRaw = append(extraRaw, trimmed)
				}
			}
		}
	}
	flushDecision()
	flushExtra()

	d.TechnicalApproach = strings.TrimSpace(strings.Join(technical, "\n"))
	d.DataFlow = strings.TrimSpace(strings.Join(dataFlow, "\n"))
	return d
}

// designSection classifies a "## " heading body for a design doc.
func designSection(heading string) string {
	switch h := strings.ToLower(strings.TrimSpace(heading)); {
	case strings.HasPrefix(h, "technical approach"):
		return "technical"
	case strings.HasPrefix(h, "architecture decision"):
		return "decisions"
	case strings.HasPrefix(h, "data flow"):
		return "dataflow"
	case strings.HasPrefix(h, "file change"):
		return "filechanges"
	default:
		return ""
	}
}

// parseFileChange parses a "## File Changes" bullet, e.g. "- `path/to/x` (new)",
// into a FileChange. Returns false if the bullet has no backtick-quoted path.
func parseFileChange(trimmed string) (FileChange, bool) {
	body, ok := bulletBody(trimmed)
	if !ok || !strings.HasPrefix(body, "`") {
		return FileChange{}, false
	}
	end := strings.Index(body[1:], "`")
	if end < 0 {
		return FileChange{}, false
	}
	fc := FileChange{Path: body[1 : 1+end]}
	if rest := strings.TrimSpace(body[1+end+1:]); strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
		fc.Kind = strings.TrimSpace(rest[1 : len(rest)-1])
	}
	return fc, true
}

// ParseTasks parses a change tasks.md body into a Tasks. Each "## " heading
// opens a section; "- [ ] / [x]" bullets are tasks (any bullet is accepted as
// a task — lenient). Tasks before any heading land in an implicit unnamed
// section. Lenient and infallible.
func ParseTasks(md string) *Tasks {
	t := &Tasks{}

	var cur *TaskSection
	flushSection := func() {
		if cur != nil {
			t.Sections = append(t.Sections, *cur)
		}
		cur = nil
	}

	for raw := range strings.SplitSeq(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "## "):
			flushSection()
			cur = &TaskSection{Name: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
		case strings.HasPrefix(line, "# "):
			t.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		case trimmed == "":
			// ignore
		default:
			if task, ok := parseTaskBullet(trimmed); ok {
				if cur == nil {
					cur = &TaskSection{} // implicit unnamed section
				}
				cur.Tasks = append(cur.Tasks, task)
			}
		}
	}
	flushSection()

	return t
}

// parseTaskBullet parses a task checkbox bullet into a Task. A leading "[x]" /
// "[X]" marks Done; "[ ]" or no checkbox marks not-done. The remainder is split
// into an optional dotted Number and the text. Returns false for non-bullets.
func parseTaskBullet(trimmed string) (Task, bool) {
	body, ok := bulletBody(trimmed)
	if !ok {
		return Task{}, false
	}
	done := false
	switch {
	case strings.HasPrefix(body, "[x]"), strings.HasPrefix(body, "[X]"):
		done, body = true, strings.TrimSpace(body[3:])
	case strings.HasPrefix(body, "[ ]"):
		body = strings.TrimSpace(body[3:])
	}
	number, text := splitTaskNumber(body)
	return Task{Number: number, Text: text, Done: done}, true
}

// splitTaskNumber separates a leading dotted number ("1.1") from the task text.
func splitTaskNumber(s string) (number, text string) {
	head, rest, found := strings.Cut(s, " ")
	if found && isTaskNumber(head) {
		return head, strings.TrimSpace(rest)
	}
	if isTaskNumber(s) {
		return s, ""
	}
	return "", s
}

// isTaskNumber reports whether s is a dotted task number (digits and dots, at
// least one digit), e.g. "1" or "1.2".
func isTaskNumber(s string) bool {
	if s == "" {
		return false
	}
	hasDigit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '.':
		default:
			return false
		}
	}
	return hasDigit
}
