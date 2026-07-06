package openspec

// Change <-> openspec.change.<slug>.* fact mapping. The companion to
// facts.go's living-spec mapping; the requirement/scenario sub-mappings are
// shared. See the facts.go header for the Fact contract and the scope boundary
// (writer-only graph-state — status, acceptance_command, the execution-rich
// task fields — is NOT emitted here).
//
// Heading text (the model's Title fields) is NOT modeled: carries no title
// predicate, and a "# heading" is projection cosmetics render synthesizes. The round-trip ignores Title fields, like Warnings.

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

const (
	segProposal = "proposal"
	segDesign   = "design"
	segTask     = "task"
	segDelta    = "delta"

	keyIntent       = "intent"
	keyScopeIn      = "scope_in"
	keyScopeOut     = "scope_out"
	keyApproach     = "approach"
	keyTechApproach = "technical_approach"
	keyDecisions    = "decisions"
	keyDataFlow     = "data_flow"
	keyFileChanges  = "file_changes"
	keyNumber       = "number"
	keyText         = "text"
	keyDone         = "done"
	keySection      = "section"
	keyOp           = "op"
	keyPreviously   = "previously"
	keyRationale    = "rationale"

	opAdded    = "added"
	opModified = "modified"
	opRemoved  = "removed"
)

// Facts maps a Change to its openspec.change.<slug>.* facts. The slug is
// taken from c.Slug. Absent artifacts (nil Proposal/Design/Tasks, empty Deltas)
// emit nothing, so a sparse change yields sparse facts.
func (c *Change) Facts() []Fact {
	fl := &factList{}
	base := changePrefix + c.Slug + "."
	c.proposalFacts(fl, base+segProposal+".")
	c.designFacts(fl, base+segDesign+".")
	c.taskFacts(fl, base+segTask+".")
	for i := range c.Deltas {
		deltaFacts(fl, base+segDelta+".", &c.Deltas[i])
	}
	return fl.facts
}

func (c *Change) proposalFacts(fl *factList, base string) {
	if c.Proposal == nil {
		return
	}
	p := c.Proposal
	fl.add(base+keyIntent, p.Intent)
	fl.add(base+keyScopeIn, encodeStrings(p.ScopeIn))
	fl.add(base+keyScopeOut, encodeStrings(p.ScopeOut))
	fl.add(base+keyApproach, p.Approach)
}

func (c *Change) designFacts(fl *factList, base string) {
	if c.Design == nil {
		return
	}
	d := c.Design
	fl.add(base+keyTechApproach, d.TechnicalApproach)
	fl.add(base+keyDecisions, encodeDecisions(d.Decisions))
	fl.add(base+keyDataFlow, d.DataFlow)
	fl.add(base+keyFileChanges, encodeFileChanges(d.FileChanges))
}

// taskFacts flattens the section-grouped Tasks model into per-task facts keyed
// by a global zero-based index (the key encodes order, so no separate position
// fact is needed — unlike requirements, whose rid key carries no order). Each
// task records its section name so reconstruction can re-group; the OpenSpec
// dotted "1.1" label is preserved as .number, not used as the key (it may be
// absent or duplicated — the thin-ingest case).
func (c *Change) taskFacts(fl *factList, base string) {
	if c.Tasks == nil {
		return
	}
	i := 0
	for _, sec := range c.Tasks.Sections {
		for _, t := range sec.Tasks {
			p := base + strconv.Itoa(i) + "."
			fl.add(p+keyNumber, t.Number)
			fl.add(p+keyText, t.Text)
			fl.addRaw(p+keyDone, strconv.FormatBool(t.Done))
			fl.add(p+keySection, sec.Name)
			i++
		}
	}
}

// deltaFacts emits one capability's delta under base = openspec.change.<slug>.delta.<cap>.
// with a per-delta rid minter (rids unique within the capability) and a
// per-delta position counter spanning Added→Modified→Removed, so each op slice's
// internal order survives reconstruction.
func deltaFacts(fl *factList, deltaBase string, d *Delta) {
	base := deltaBase + d.Capability + "."
	minter := newRIDMinter()
	pos := 0
	for _, r := range d.Added {
		p := base + minter.mint(r.Name) + "."
		fl.add(p+keyOp, opAdded)
		requirementFacts(fl, p, r, pos)
		pos++
	}
	for _, m := range d.Modified {
		p := base + minter.mint(m.Name) + "."
		fl.add(p+keyOp, opModified)
		requirementFacts(fl, p, m.Requirement, pos)
		fl.add(p+keyPreviously, m.Previously)
		pos++
	}
	for _, rm := range d.Removed {
		p := base + minter.mint(rm.Name) + "."
		fl.add(p+keyOp, opRemoved)
		fl.add(p+keyName, rm.Name)
		fl.addRaw(p+keyPosition, strconv.Itoa(pos))
		fl.add(p+keyRationale, rm.Rationale)
		pos++
	}
}

// ChangeFromFacts rebuilds the Change for slug from a fact set, the inverse of
// Facts on the modeled fields. Facts for other slugs/specs are
// ignored. An artifact with no facts comes back nil (Proposal/Design/Tasks) or
// empty (Deltas), so an empty-but-present source artifact collapses to absent —
// an accepted lossy edge (an empty artifact carries no graph content).
func ChangeFromFacts(slug string, facts []Fact) *Change {
	idx := indexFacts(facts)
	base := changePrefix + slug + "."
	return &Change{
		Slug:     slug,
		Proposal: proposalFromIndex(idx, base+segProposal+"."),
		Design:   designFromIndex(idx, base+segDesign+"."),
		Tasks:    tasksFromIndex(idx, base+segTask+"."),
		Deltas:   deltasFromIndex(idx, base+segDelta+"."),
	}
}

func proposalFromIndex(idx factIndex, base string) *Proposal {
	intent, scopeIn, scopeOut, approach :=
		idx[base+keyIntent], idx[base+keyScopeIn], idx[base+keyScopeOut], idx[base+keyApproach]
	if intent == "" && scopeIn == "" && scopeOut == "" && approach == "" {
		return nil
	}
	return &Proposal{
		Intent:   intent,
		ScopeIn:  decodeStrings(scopeIn),
		ScopeOut: decodeStrings(scopeOut),
		Approach: approach,
	}
}

func designFromIndex(idx factIndex, base string) *Design {
	tech, decisions, dataFlow, fileChanges :=
		idx[base+keyTechApproach], idx[base+keyDecisions], idx[base+keyDataFlow], idx[base+keyFileChanges]
	if tech == "" && decisions == "" && dataFlow == "" && fileChanges == "" {
		return nil
	}
	return &Design{
		TechnicalApproach: tech,
		Decisions:         decodeDecisions(decisions),
		DataFlow:          dataFlow,
		FileChanges:       decodeFileChanges(fileChanges),
	}
}

// tasksFromIndex re-groups the flat per-index task facts back into sections,
// preserving task order (numeric index) and section order (first appearance).
func tasksFromIndex(idx factIndex, base string) *Tasks {
	indices := intChildIndices(idx, base)
	if len(indices) == 0 {
		return nil
	}
	t := &Tasks{}
	secAt := map[string]int{} // section name -> index in t.Sections
	for _, i := range indices {
		p := base + strconv.Itoa(i) + "."
		done, _ := strconv.ParseBool(idx[p+keyDone])
		task := Task{Number: idx[p+keyNumber], Text: idx[p+keyText], Done: done}
		name := idx[p+keySection]
		si, ok := secAt[name]
		if !ok {
			si = len(t.Sections)
			secAt[name] = si
			t.Sections = append(t.Sections, TaskSection{Name: name})
		}
		t.Sections[si].Tasks = append(t.Sections[si].Tasks, task)
	}
	return t
}

func deltasFromIndex(idx factIndex, base string) []Delta {
	// Collect, per capability, each rid's op + position via right-parsing the
	// "<cap>.<rid>.<field>" tail (robust to capability names with dots).
	type entry struct {
		rid string
		pos int
	}
	byCap := map[string][]entry{}
	seen := map[string]bool{} // capName\x00rid — dedup across the rid's several fields
	for key := range idx {
		if !strings.HasPrefix(key, base) {
			continue
		}
		capName, rid, _, ok := parseDeltaTail(key[len(base):])
		if !ok {
			continue
		}
		ck := capName + "\x00" + rid
		if seen[ck] {
			continue
		}
		seen[ck] = true
		p := base + capName + "." + rid + "."
		pos, _ := strconv.Atoi(idx[p+keyPosition])
		byCap[capName] = append(byCap[capName], entry{rid: rid, pos: pos})
	}
	if len(byCap) == 0 {
		return nil
	}
	caps := make([]string, 0, len(byCap))
	for capName := range byCap {
		caps = append(caps, capName)
	}
	sort.Strings(caps)

	var deltas []Delta
	for _, capName := range caps {
		entries := byCap[capName]
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].pos < entries[j].pos })
		d := Delta{Capability: capName}
		for _, e := range entries {
			p := base + capName + "." + e.rid + "."
			switch idx[p+keyOp] {
			case opAdded:
				d.Added = append(d.Added, requirementFromIndex(idx, p))
			case opModified:
				d.Modified = append(d.Modified, ModifiedRequirement{
					Requirement: requirementFromIndex(idx, p),
					Previously:  idx[p+keyPreviously],
				})
			case opRemoved:
				d.Removed = append(d.Removed, RemovedRequirement{
					Name:      idx[p+keyName],
					Rationale: idx[p+keyRationale],
				})
			}
		}
		deltas = append(deltas, d)
	}
	return deltas
}

// parseDeltaTail splits "<cap>.<rid>.<field>" from the right: field and rid are
// dot-free (a known suffix and a slug), so capability may itself contain dots.
func parseDeltaTail(tail string) (capability, rid, field string, ok bool) {
	i := strings.LastIndexByte(tail, '.')
	if i < 0 {
		return "", "", "", false
	}
	field = tail[i+1:]
	left := tail[:i]
	j := strings.LastIndexByte(left, '.')
	if j < 0 {
		return "", "", "", false
	}
	rid = left[j+1:]
	capability = left[:j]
	return capability, rid, field, capability != "" && rid != "" && field != ""
}

// intChildIndices returns the sorted distinct integer child indices present
// under base (the task.<i>.* keys), sorted numerically so >9 tasks order
// correctly (lexical "10" < "2" would not).
func intChildIndices(idx factIndex, base string) []int {
	seen := map[int]bool{}
	var out []int
	for key := range idx {
		if !strings.HasPrefix(key, base) {
			continue
		}
		rest := key[len(base):]
		dot := strings.IndexByte(rest, '.')
		if dot <= 0 {
			continue
		}
		n, err := strconv.Atoi(rest[:dot])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// --- design.md structured-array codecs ---

type decisionJSON struct {
	Name string `json:"name"`
	Body string `json:"body,omitempty"`
}

func encodeDecisions(ds []DesignDecision) string {
	if len(ds) == 0 {
		return ""
	}
	dto := make([]decisionJSON, len(ds))
	for i, d := range ds {
		dto[i] = decisionJSON{Name: d.Name, Body: d.Body}
	}
	b, _ := json.Marshal(dto)
	return string(b)
}

func decodeDecisions(s string) []DesignDecision {
	if s == "" {
		return nil
	}
	var dto []decisionJSON
	if json.Unmarshal([]byte(s), &dto) != nil {
		return nil
	}
	out := make([]DesignDecision, len(dto))
	for i, d := range dto {
		out[i] = DesignDecision{Name: d.Name, Body: d.Body}
	}
	return out
}

type fileChangeJSON struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
}

func encodeFileChanges(fs []FileChange) string {
	if len(fs) == 0 {
		return ""
	}
	dto := make([]fileChangeJSON, len(fs))
	for i, f := range fs {
		dto[i] = fileChangeJSON{Path: f.Path, Kind: f.Kind}
	}
	b, _ := json.Marshal(dto)
	return string(b)
}

func decodeFileChanges(s string) []FileChange {
	if s == "" {
		return nil
	}
	var dto []fileChangeJSON
	if json.Unmarshal([]byte(s), &dto) != nil {
		return nil
	}
	out := make([]FileChange, len(dto))
	for i, f := range dto {
		out[i] = FileChange{Path: f.Path, Kind: f.Kind}
	}
	return out
}
