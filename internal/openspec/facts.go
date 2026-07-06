package openspec

// This file is the pure mapping between the OpenSpec Go model and its graph
// predicate shape. It stays in the format layer — it has NO graph/NATS
// dependency — by emitting subject-less Facts (see the Fact type) that the
// graph-writing caller turns into a triple. That keeps the mapping independent of
// entity identity: the caller supplies the subject and the write metadata.
//
// Scope boundary. The mapping covers exactly what is representable in OpenSpec
// markdown — the proposal/design/tasks/delta/spec *content* — so it is precisely
// the render/ingest round-trip surface: Facts∘FromFacts is stable on the modeled
// fields, mirroring the markdown round-trip in roundtrip_test.go. Writer-only
// graph-state (a requirement's or change's status, archive date, acceptance
// command, and execution-rich task fields such as target_files/test_command) is
// deliberately NOT here — none of it derives from the OpenSpec model or renders
// to markdown; the tools that own those facts stamp them separately. Keeping them
// out makes the round-trip honest and the boundary clean.

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// nonAlnum matches runs of non-[a-z0-9] characters for slugification. slugify is
// inlined from semteams' slug package to keep this format layer fully dep-free.
var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify lowercases s and collapses non-alphanumeric runs to single hyphens,
// trimming leading/trailing hyphens — the deterministic rid basis.
func slugify(s string) string {
	out := nonAlnum.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(out, "-")
}

// ChangeEntityPrefix returns the "openspec.change.<slug>." predicate prefix that
// Change.Facts() stamps under. A writer (e.g. the create_change author tool) that adds
// writer-only graph-state predicates — status, acceptance_command, the
// execution-rich task fields — uses this so its facts share the change's subtree
// with the pure content facts. (The living-spec analogue is added when the
// living-spec writer needs it.)
func ChangeEntityPrefix(slug string) string { return changePrefix + slug + "." }

// Fact is a subject-less graph assertion: the predicate key and object that the
// OpenSpec model determines — a message.Triple minus the fields the writer owns
// (Subject/Source/Timestamp/Confidence). The graph-writing caller (the
// create_change author tool or a render/ingest subscriber) supplies the subject
// (entity ID) and write metadata when it builds the message.Triple. Subject-less
// keeps this format layer dependency-free and the mapping identity-agnostic.
//
// Object is always a string: prose verbatim, scalars formatted (strconv), and
// arrays / scenario lists JSON-encoded. The string form is what the rule engine
// substitutes via $entity.triple.<predicate>, so it is also the faithful
// round-trip currency.
type Fact struct {
	Predicate string
	Object    string
}

// predicate keys and key fragments.
const (
	// Re-vocabularied to semdev's openspec.* namespace (G9): change facts land
	// under openspec.change.* (vocab writer create-change-author-tool), living-spec
	// facts under openspec.spec.*. This is the one edit site for the port.
	specPrefix     = "openspec.spec."
	changePrefix   = "openspec.change."
	keyTitle       = "title"
	keyPurpose     = "purpose"
	keyName        = "name"
	keyStatement   = "statement"
	keyScenarios   = "scenarios"
	keyPosition    = "position"
	segRequirement = "requirement"
)

// factList accumulates Facts in document order; emit-order is the natural order
// of the model, which keeps a pure Facts→FromFacts round-trip order-stable even
// before the explicit position fields are consulted.
type factList struct {
	facts []Fact
}

// add appends a fact with a non-empty object. Empty-object facts are skipped:
// an absent fact and an empty value are treated identically on the way back
// (FromFacts leaves the field zero), so emitting "" would only add noise.
func (fl *factList) add(predicate, object string) {
	if object == "" {
		return
	}
	fl.facts = append(fl.facts, Fact{Predicate: predicate, Object: object})
}

// addRaw appends a fact unconditionally, including a value that add would treat
// as skippable. Used for the position index and the done bool, which must always
// be stamped: a fully-zero task (Task{} — blank number/text, done=false) would
// otherwise emit NO facts, and intChildIndices could not discover it. The always-
// present "<i>.done=false" is what keeps such a task on the round-trip
// (TestChangeFacts_RoundTrip_EdgeCases "fully-zero task" pins this).
func (fl *factList) addRaw(predicate, object string) {
	fl.facts = append(fl.facts, Fact{Predicate: predicate, Object: object})
}

// factIndex is a predicate→object lookup over a fact set, built once per
// FromFacts so the parse is a series of map reads rather than repeated scans.
type factIndex map[string]string

func indexFacts(facts []Fact) factIndex {
	idx := make(factIndex, len(facts))
	for _, f := range facts {
		// First write wins: a duplicated predicate keeps the earliest
		// (document-order) value, matching the rule engine's first-match
		// $entity.triple read semantics.
		if _, ok := idx[f.Predicate]; !ok {
			idx[f.Predicate] = f.Object
		}
	}
	return idx
}

// childKeys returns, for a "<prefix>" like "spec.auth.requirement.", the set of
// distinct immediate child segments (here the <rid>s) present in idx, in
// position order. Position is read from "<prefix><child>.position"; children
// with no position fact sort after positioned ones, then lexically — a
// deterministic fallback for hand-authored facts that omit position.
func (idx factIndex) childKeys(prefix string) []string {
	const noPos = 1 << 30 // children with no position fact sort last, then lexically
	type child struct {
		name string
		pos  int
	}
	seen := map[string]*child{}
	order := []*child{}
	for key := range idx {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := key[len(prefix):]
		dot := strings.IndexByte(rest, '.')
		if dot <= 0 {
			continue
		}
		name := rest[:dot]
		c, ok := seen[name]
		if !ok {
			c = &child{name: name, pos: noPos}
			seen[name] = c
			order = append(order, c)
		}
		if rest[dot+1:] == keyPosition {
			if p, err := strconv.Atoi(idx[key]); err == nil {
				c.pos = p
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].pos != order[j].pos {
			return order[i].pos < order[j].pos
		}
		return order[i].name < order[j].name
	})
	out := make([]string, len(order))
	for i, c := range order {
		out[i] = c.name
	}
	return out
}

// --- shared JSON codecs for the structured triple objects ---

func encodeStrings(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

func decodeStrings(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

// scenarioJSON / stepJSON pin the wire shape of an encoded scenario list to a
// {name, steps:[{kw,text}]} shape, decoupled from the Go model field names so
// the model can evolve without breaking stored facts.
type scenarioJSON struct {
	Name  string     `json:"name"`
	Steps []stepJSON `json:"steps,omitempty"`
}

type stepJSON struct {
	Keyword string `json:"kw,omitempty"`
	Text    string `json:"text,omitempty"`
}

func encodeScenarios(ss []Scenario) string {
	if len(ss) == 0 {
		return ""
	}
	dto := make([]scenarioJSON, len(ss))
	for i, s := range ss {
		steps := make([]stepJSON, len(s.Steps))
		for j, st := range s.Steps {
			steps[j] = stepJSON{Keyword: st.Keyword, Text: st.Text}
		}
		dto[i] = scenarioJSON{Name: s.Name, Steps: steps}
	}
	b, _ := json.Marshal(dto)
	return string(b)
}

func decodeScenarios(s string) []Scenario {
	if s == "" {
		return nil
	}
	var dto []scenarioJSON
	if json.Unmarshal([]byte(s), &dto) != nil {
		return nil
	}
	out := make([]Scenario, len(dto))
	for i, d := range dto {
		var steps []Step
		for _, st := range d.Steps {
			steps = append(steps, Step{Keyword: st.Keyword, Text: st.Text})
		}
		out[i] = Scenario{Name: d.Name, Steps: steps}
	}
	return out
}

// ridMinter slugifies requirement names into unique <rid> predicate keys,
// disambiguating collisions deterministically. The original name
// is always stored in the ".name" predicate, so the inverse recovers it from the
// fact, never from the (possibly suffixed) rid.
type ridMinter struct{ seen map[string]int }

func newRIDMinter() *ridMinter { return &ridMinter{seen: map[string]int{}} }

func (m *ridMinter) mint(name string) string {
	base := slugify(name)
	if base == "" {
		base = "requirement"
	}
	n := m.seen[base]
	m.seen[base] = n + 1
	if n == 0 {
		return base
	}
	return base + "-" + strconv.Itoa(n+1)
}

// requirementFacts emits the shared requirement body (name/statement/scenarios)
// under "<prefix>" (which already ends in the rid + "."), plus its position.
// Used by both the living-spec and the change-delta mappings.
func requirementFacts(fl *factList, prefix string, r Requirement, pos int) {
	fl.add(prefix+keyName, r.Name)
	fl.add(prefix+keyStatement, r.Statement)
	fl.add(prefix+keyScenarios, encodeScenarios(r.Scenarios))
	fl.addRaw(prefix+keyPosition, strconv.Itoa(pos))
}

// requirementFromIndex rebuilds a Requirement from the facts under "<prefix>".
func requirementFromIndex(idx factIndex, prefix string) Requirement {
	return Requirement{
		Name:      idx[prefix+keyName],
		Statement: idx[prefix+keyStatement],
		Scenarios: decodeScenarios(idx[prefix+keyScenarios]),
	}
}

// --- living spec: Spec <-> openspec.spec.<cap>.* ---

// Facts maps a living capability Spec to its openspec.spec.<cap>.* facts.
// The capability is taken from s.Capability; an empty Capability yields facts
// under "spec..*", so callers parsing from a path must set it (ReadSpecs does).
// Requirement <rid> keys are slugified names, disambiguated on collision; the
// lifecycle status predicate is writer-stamped, not emitted here (see the file
// header).
func (s *Spec) Facts() []Fact {
	fl := &factList{}
	base := specPrefix + s.Capability + "."
	fl.add(base+keyTitle, s.Title)
	fl.add(base+keyPurpose, s.Purpose)
	minter := newRIDMinter()
	for i, r := range s.Requirements {
		rid := minter.mint(r.Name)
		requirementFacts(fl, base+segRequirement+"."+rid+".", r, i)
	}
	return fl.facts
}

// SpecFromFacts rebuilds the Spec for capability from a fact set (which may hold
// facts for other capabilities or changes; only spec.<capability>.* are read).
// It is the inverse of Facts on the modeled fields: SpecFromFacts(cap,
// s.Facts()) equals s for a Spec whose Capability == cap.
func SpecFromFacts(capability string, facts []Fact) *Spec {
	idx := indexFacts(facts)
	base := specPrefix + capability + "."
	s := &Spec{
		Capability: capability,
		Title:      idx[base+keyTitle],
		Purpose:    idx[base+keyPurpose],
	}
	reqPrefix := base + segRequirement + "."
	for _, rid := range idx.childKeys(reqPrefix) {
		s.Requirements = append(s.Requirements, requirementFromIndex(idx, reqPrefix+rid+"."))
	}
	return s
}
