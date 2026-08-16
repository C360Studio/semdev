// Package standards parses a target repo's committed `.semdev/standards.yaml` —
// the repo-declared standards + checks file (standards-via-lessons D1). The
// parser is PURE and STRICT: unknown fields, duplicate ids, invalid severities
// or roles, empty text, or a bad version all reject naming the exact defect —
// a malformed file must park toward the operator, never half-parse (the
// repo-standards spec's fail-closed requirement).
package standards

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Path is the fixed conventional location of the standards file in a target repo.
const Path = ".semdev/standards.yaml"

// fileVersion is the only accepted schema version.
const fileVersion = 1

// maxStandardsPerFile bounds how many lesson records one repo revision can mint
// (semstreams-review M4): each standard is a graph entity, and the injection
// reader's page cap silently truncates candidate coverage platform-wide past
// ~16k records. 100 is generous against the K=10-per-role injection ceiling.
const maxStandardsPerFile = 100

// maxFileBytes bounds the raw input before any parse work (M4) — a standards
// file is a page of human-authored law, never megabytes.
const maxFileBytes = 256 * 1024

// maxInjectionFormBytes mirrors the lesson substrate's injection-form bound
// (semstreams processor/agentic-tools/emit_lesson.go, unexported upstream). The
// store rejects an over-bound form at birth anyway; enforcing it HERE names the
// standard id at parse/render time instead of a mid-sync store rejection.
const maxInjectionFormBytes = 320

// Severity is the RFC-2119 weight of a standard.
type Severity string

// The three declared severities. must gates review; should surfaces; may informs.
const (
	SeverityMust   Severity = "must"
	SeverityShould Severity = "should"
	SeverityMay    Severity = "may"
)

// injectableRoles are the agent roles a standard may scope to — the two roles
// whose briefs receive lesson injection (the loop scope is tag:<role>, D3).
var injectableRoles = []string{"developer", "reviewer"}

// Standard is one declared repo standard.
type Standard struct {
	ID       string
	Text     string
	Severity Severity
	// Roles the standard scopes to (subset of injectableRoles). Defaulted to
	// ALL injectable roles when omitted in the file.
	Roles []string
}

// Check is one deterministic repo-declared command (the floors lane, D7).
type Check struct {
	Name     string
	Command  string
	Required bool
}

// File is a parsed standards file.
type File struct {
	Standards []Standard
	Checks    []Check
}

// yamlFile is the strict on-disk schema. KnownFields(true) makes any unknown
// field at any level a named rejection.
type yamlFile struct {
	Version   int            `yaml:"version"`
	Standards []yamlStandard `yaml:"standards"`
	Checks    []yamlCheck    `yaml:"checks"`
}

type yamlStandard struct {
	ID       string   `yaml:"id"`
	Text     string   `yaml:"text"`
	Severity string   `yaml:"severity"`
	Roles    []string `yaml:"roles"`
}

type yamlCheck struct {
	Name     string `yaml:"name"`
	Command  string `yaml:"command"`
	Required bool   `yaml:"required"`
}

// kebabToken validates ids and check names: lower-kebab, no leading/trailing
// or doubled hyphen.
var kebabToken = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Parse strictly parses standards-file bytes. Every rejection names its defect.
func Parse(data []byte) (File, error) {
	if len(data) > maxFileBytes {
		return File{}, fmt.Errorf("standards: the file is %d bytes, over the %d-byte bound", len(data), maxFileBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var raw yamlFile
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return File{}, fmt.Errorf("standards: the file is empty — declare `version: %d`", fileVersion)
		}
		return File{}, fmt.Errorf("standards: yaml parse: %w", err)
	}
	// A second YAML document would silently drop content under a single Decode.
	if err := dec.Decode(new(yamlFile)); !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("standards: the file contains more than one YAML document")
	}
	// A null sequence entry (`- ~`, a bare `-`) is SKIPPED by the struct decode
	// above — a silent partial parse the spec forbids (go-review M1). A parallel
	// node-shape pass rejects them.
	if err := rejectNullEntries(data); err != nil {
		return File{}, err
	}
	if raw.Version != fileVersion {
		return File{}, fmt.Errorf("standards: version = %d, want %d", raw.Version, fileVersion)
	}

	if n := len(raw.Standards); n > maxStandardsPerFile {
		return File{}, fmt.Errorf("standards: the file declares %d standards, over the %d bound", n, maxStandardsPerFile)
	}

	out := File{}
	seenIDs := map[string]bool{}
	// Two ids with identical (text, roles) would alias to ONE lesson record —
	// id is not a record-identity input — so the second id's [std:] line would
	// silently never exist (semstreams-review M5). Reject the pair, naming both.
	seenIdentity := map[string]string{}
	for i, s := range raw.Standards {
		std, err := validateStandard(i, s, seenIDs)
		if err != nil {
			return File{}, err
		}
		sortedRoles := slices.Clone(std.Roles)
		slices.Sort(sortedRoles) // record identity sorts applies-to, so role ORDER must not differentiate
		identityKey := std.Text + "\x1f" + strings.Join(sortedRoles, "\x1e")
		if prior, dup := seenIdentity[identityKey]; dup {
			return File{}, fmt.Errorf(
				"standards: %q and %q declare identical text and roles — they would alias to one record; merge them or differentiate the text",
				prior, std.ID)
		}
		seenIdentity[identityKey] = std.ID
		out.Standards = append(out.Standards, std)
	}
	seenChecks := map[string]bool{}
	for i, c := range raw.Checks {
		chk, err := validateCheck(i, c, seenChecks)
		if err != nil {
			return File{}, err
		}
		out.Checks = append(out.Checks, chk)
	}
	return out, nil
}

func validateStandard(i int, s yamlStandard, seen map[string]bool) (Standard, error) {
	if strings.TrimSpace(s.ID) == "" {
		return Standard{}, fmt.Errorf("standards: standards[%d] has no id", i)
	}
	if !kebabToken.MatchString(s.ID) {
		return Standard{}, fmt.Errorf("standards: id %q is not a lower-kebab token", s.ID)
	}
	if seen[s.ID] {
		return Standard{}, fmt.Errorf("standards: duplicate id %q", s.ID)
	}
	seen[s.ID] = true
	if strings.TrimSpace(s.Text) == "" {
		return Standard{}, fmt.Errorf("standards: standard %q has empty text", s.ID)
	}
	// Control bytes (C0 + DEL) are the prompt-scaffolding smuggling channel: the
	// injection form is rendered VERBATIM as one line of a downstream agent's
	// brief, so a newline or escape in repo-authored text could forge scaffolding
	// (`do X\n[SYSTEM] ...`). The upstream store rejects them at birth; rejecting
	// HERE names the standard id at parse time (go-review H1).
	for _, r := range s.Text {
		if r < 0x20 || r == 0x7F {
			return Standard{}, fmt.Errorf("standards: standard %q text contains a control byte (0x%02X) — one plain line only (the brief-injection surface)", s.ID, r)
		}
	}
	sev := Severity(s.Severity)
	switch sev {
	case SeverityMust, SeverityShould, SeverityMay:
	default:
		return Standard{}, fmt.Errorf("standards: standard %q severity %q is not one of must|should|may", s.ID, s.Severity)
	}
	roles := s.Roles
	if len(roles) == 0 {
		roles = append([]string(nil), injectableRoles...)
	}
	seenRoles := map[string]bool{}
	for _, r := range roles {
		if !validRole(r) {
			return Standard{}, fmt.Errorf("standards: standard %q role %q is not injectable (want a subset of %v)", s.ID, r, injectableRoles)
		}
		if seenRoles[r] {
			return Standard{}, fmt.Errorf("standards: standard %q lists role %q twice", s.ID, r)
		}
		seenRoles[r] = true
	}
	std := Standard{ID: s.ID, Text: strings.TrimSpace(s.Text), Severity: sev, Roles: roles}
	// The bound is a parse-time property of the declaration, not a sync-time
	// surprise: an over-bound standard rejects the FILE here, naming the id.
	if _, err := InjectionForm(std); err != nil {
		return Standard{}, err
	}
	return std, nil
}

func validateCheck(i int, c yamlCheck, seen map[string]bool) (Check, error) {
	if strings.TrimSpace(c.Name) == "" {
		return Check{}, fmt.Errorf("standards: checks[%d] has no name", i)
	}
	if !kebabToken.MatchString(c.Name) {
		return Check{}, fmt.Errorf("standards: check name %q is not a lower-kebab token", c.Name)
	}
	if seen[c.Name] {
		return Check{}, fmt.Errorf("standards: duplicate check name %q", c.Name)
	}
	seen[c.Name] = true
	if strings.TrimSpace(c.Command) == "" {
		return Check{}, fmt.Errorf("standards: check %q has no command", c.Name)
	}
	return Check{Name: c.Name, Command: c.Command, Required: c.Required}, nil
}

func validRole(r string) bool {
	return slices.Contains(injectableRoles, r)
}

// rejectNullEntries walks the document's node shape and rejects null elements
// in the standards/checks sequences — entries the strict struct decode would
// silently skip (yaml.v3 drops null elements when decoding into []struct).
func rejectNullEntries(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil // the strict decode already vetted the document shape
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		if key.Value != "standards" && key.Value != "checks" {
			continue
		}
		if val.Kind != yaml.SequenceNode {
			continue
		}
		for n, item := range val.Content {
			if item.Tag == "!!null" {
				return fmt.Errorf("standards: %s[%d] is a null entry (line %d) — remove it or declare the entry", key.Value, n, item.Line)
			}
		}
	}
	return nil
}

// InjectionForm renders a standard's brief line — `[std:<id>] MUST <text>` —
// the exact string injected into agent briefs. It errors (naming the id, never
// truncating) when the rendered form exceeds the substrate bound.
func InjectionForm(s Standard) (string, error) {
	form := fmt.Sprintf("[std:%s] %s %s", s.ID, strings.ToUpper(string(s.Severity)), s.Text)
	if n := len(form); n > maxInjectionFormBytes {
		return "", fmt.Errorf(
			"standards: standard %q renders to %d bytes, over the %d-byte injection bound — shorten the text (it is never truncated)",
			s.ID, n, maxInjectionFormBytes)
	}
	return form, nil
}
