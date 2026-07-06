package conformance

import (
	"fmt"
	"sort"
	"strings"

	"github.com/c360studio/semdev/internal/registry"
	"github.com/c360studio/semdev/internal/vocab"
)

// forbiddenOutcomeFields are the outcome-shaped input-schema property names a
// tool must never accept from the caller (G3): the model may claim, only the
// harness records. From the harness-measurement spec + the constitution's
// "test-count fact" (G3). The set is extensible; matching is by property name.
var forbiddenOutcomeFields = map[string]bool{
	"pass":         true,
	"passed":       true,
	"exit_code":    true,
	"success":      true,
	"resolved":     true,
	"outcome":      true,
	"test_count":   true,
	"tests_passed": true,
	"tests_failed": true,
	"failures":     true,
}

// outcomeFieldViolations returns one message per forbidden outcome-shaped
// property found anywhere in a tool's input schema (G3). params is the tool's
// JSON-schema object (agentic.ToolDefinition.Parameters); the walk descends
// nested object properties, array items, the composition keywords
// (anyOf/allOf/oneOf), and named-schema maps ($defs/definitions/
// patternProperties), so an outcome field cannot hide in a composite. Matching
// is by property name — an outcome expressed as an enum value under an
// innocently-named property is out of scope for a name census.
func outcomeFieldViolations(toolName string, params map[string]any) []string {
	var out []string
	var walk func(schema map[string]any)
	descend := func(v any) {
		switch t := v.(type) {
		case map[string]any:
			walk(t)
		case []any:
			for _, e := range t {
				if m, ok := e.(map[string]any); ok {
					walk(m)
				}
			}
		}
	}
	walk = func(schema map[string]any) {
		if props, ok := schema["properties"].(map[string]any); ok {
			for name, sub := range props {
				if forbiddenOutcomeFields[strings.ToLower(name)] {
					out = append(out, fmt.Sprintf("tool %q input schema accepts caller-supplied outcome field %q (G3): the harness stamps outcomes, the model may not", toolName, name))
				}
				descend(sub)
			}
		}
		// single nested schemas
		for _, key := range []string{"items", "additionalProperties"} {
			descend(schema[key])
		}
		// arrays of schemas
		for _, key := range []string{"anyOf", "allOf", "oneOf"} {
			descend(schema[key])
		}
		// maps of named schemas: descend the values, not the (arbitrary) names
		for _, key := range []string{"$defs", "definitions", "patternProperties"} {
			if m, ok := schema[key].(map[string]any); ok {
				for _, v := range m {
					descend(v)
				}
			}
		}
	}
	walk(params)
	return out
}

// This file holds the pure census cores so each pin is provably fireable: the
// real pin runs a core over the checked-in table and asserts no violations,
// while a red-first test runs the same core over a known-bad fixture and asserts
// it catches the violation. A pin that cannot go red is theater.

// singleWriterViolations returns one message per G5 violation in preds: a
// predicate with no name, a predicate with no writer, or — the semspec wedge
// shape — a predicate stamped by more than one writer.
func singleWriterViolations(preds []vocab.Predicate) []string {
	var out []string
	writers := make(map[string]map[string]bool)
	for _, p := range preds {
		if p.Name == "" {
			out = append(out, fmt.Sprintf("predicate with empty Name (writer %q); every fact must be named", p.Writer))
			continue
		}
		if p.Writer == "" {
			out = append(out, fmt.Sprintf("predicate %q has no declared writer; G5 requires exactly one", p.Name))
			continue
		}
		if writers[p.Name] == nil {
			writers[p.Name] = make(map[string]bool)
		}
		writers[p.Name][p.Writer] = true
	}
	for name, w := range writers {
		if len(w) != 1 {
			out = append(out, fmt.Sprintf("predicate %q has %d writers %v; G5 (B4) requires exactly one", name, len(w), sortedKeys(w)))
		}
	}
	return out
}

// duplicateNameViolations returns one message per predicate declared more than
// once — a silent duplicate would let two changes each claim the same fact.
func duplicateNameViolations(preds []vocab.Predicate) []string {
	count := make(map[string]int)
	for _, p := range preds {
		count[p.Name]++
	}
	var out []string
	for name, n := range count {
		if n != 1 {
			out = append(out, fmt.Sprintf("predicate %q is declared %d times; each fact appears exactly once", name, n))
		}
	}
	return out
}

// provenanceViolations returns one message per G9/G10 violation: an empty
// IntroducedBy, an IntroducedBy that resolves to no known change, or a
// Capability that is not one of the declared capability names.
func provenanceViolations(preds []vocab.Predicate, validChanges, validCaps map[string]bool) []string {
	var out []string
	for _, p := range preds {
		switch {
		case p.IntroducedBy == "":
			out = append(out, fmt.Sprintf("predicate %q declares no introducing change (G9)", p.Name))
		case !validChanges[p.IntroducedBy]:
			out = append(out, fmt.Sprintf("predicate %q names introducing change %q, which resolves to no change directory", p.Name, p.IntroducedBy))
		}
		if !validCaps[p.Capability] {
			out = append(out, fmt.Sprintf("predicate %q claims capability %q, not a declared capability", p.Name, p.Capability))
		}
	}
	return out
}

// undeclaredComponents returns one message per component in added that has no
// entry in declared — the G1 "registered without a registry entry + alignment
// note" shape.
func undeclaredComponents(added, declared map[string]bool) []string {
	var out []string
	for name := range added {
		if !declared[name] {
			out = append(out, fmt.Sprintf("component %q is registered but has no registry.Entry + alignment note (G1)", name))
		}
	}
	return out
}

// alignmentNoteViolations returns one message per G1 alignment-note violation:
// an entry with an empty AlignmentNote, or a note that resolves to no heading in
// the alignment-notes doc (anchors).
func alignmentNoteViolations(entries []registry.Entry, anchors map[string]bool) []string {
	var out []string
	for _, e := range entries {
		if e.AlignmentNote == "" {
			out = append(out, fmt.Sprintf("registry entry %q has no framework-alignment note (G1)", e.Name))
			continue
		}
		if !anchors[e.AlignmentNote] {
			out = append(out, fmt.Sprintf("registry entry %q references alignment note %q, which is not a heading in docs/alignment-notes.md", e.Name, e.AlignmentNote))
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
