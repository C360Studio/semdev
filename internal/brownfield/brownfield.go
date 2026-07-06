// Package brownfield is the deterministic ingest side of the openspec-io seam: it
// reads an existing target repository's openspec/specs/ workspace and projects
// the living capability specs to openspec.spec.* graph facts, under a single
// owner, with NO model in the loop (G3). It is the mirror of the hydrate path —
// hydrate renders facts to markdown, brownfield parses markdown to facts — and it
// exists so onboarding a repo seeds the graph with its current spec surface
// without an LLM interpreting the artifacts.
//
// M0 scope is the LIVING SPECS only (openspec/specs/*/spec.md → openspec.spec.*).
// It deliberately does NOT project a brownfield repo's in-flight
// openspec/changes/ to openspec.change.* facts: that namespace has a single G5
// writer (the create_change author tool), and a second writer would break the
// sole-writer invariant. In-flight changes in a target repo are the human's WIP,
// not state semdev owns; if a real need to ingest them appears, it earns its own
// owner and namespace then (G9 — no speculative vocabulary).
//
// This is a library projector (like internal/openspec, the ported engine): the
// pure parse-and-project core lands here at M0 with its red-first pins; the
// registered ingest component that wires it onto the raw lane
// (raw-lane→projector→graph-ingest, design D1) and stamps the facts on their spec
// entities lands with the runtime boot path (group 11). The single owner is the
// Source const, cross-checked against the vocab writer.
package brownfield

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/message"
)

// Source is the single owner stamped on every fact this projector produces. It
// MUST equal the writer declared for openspec.spec.* in internal/vocab (G5) — a
// brownfield test cross-checks it, so the sole-owner claim is verifiable, not a
// comment.
const Source = "brownfield-spec-projector"

// provenanceKey is the per-capability predicate (under the capability's
// openspec.spec.<cap>. subtree) that carries the source artifact's content-hash
// reference, so every fact of a capability traces to the retained raw bytes of
// its source spec.md.
const provenanceKey = "source_ref"

// SourceArtifact is one ingested spec.md's retained raw bytes and the content-hash
// reference (Ref) the provenance fact points at — "store once, reference
// anywhere." The ingest wiring persists the bytes under Ref; the graph facts
// carry only the reference.
type SourceArtifact struct {
	Capability string
	Path       string
	Ref        string // sha256 hex of Bytes
	Bytes      []byte
}

// Projection is the deterministic result of parsing a repo's openspec/specs/: the
// subject-less facts (openspec.Fact, as the format engine emits), the retained
// source artifacts, and the lenient-parse warnings. Subjects and write metadata
// are applied by Triples — this core stays identity-agnostic and NATS-free.
type Projection struct {
	Facts    []openspec.Fact
	Sources  []SourceArtifact
	Warnings []string
}

// ProjectSpecs reads specsDir (a repo's openspec/specs/ directory), parses each
// capability's spec.md, and projects it to openspec.spec.<cap>.* facts plus a
// provenance source_ref and the retained raw bytes. Parsing is lenient by design:
// bullet and heading-case variance produce warnings (surfaced, not fatal), and a
// missing/absent specs dir yields an empty projection, not an error — only a real
// filesystem read error stops it. No model is invoked (G3).
func ProjectSpecs(specsDir string) (*Projection, error) {
	entries, err := os.ReadDir(specsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return &Projection{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read specs dir %q: %w", specsDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // deterministic order regardless of directory iteration

	p := &Projection{}
	for _, capName := range names {
		path := filepath.Join(specsDir, capName, "spec.md")
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue // a capability dir with no spec.md is skipped, not an error
		}
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", path, err)
		}

		spec := openspec.ParseSpec(string(raw))
		spec.Capability = capName

		p.Facts = append(p.Facts, spec.Facts()...)

		ref := hashRef(raw)
		p.Facts = append(p.Facts, openspec.Fact{
			Predicate: openspec.SpecEntityPrefix(capName) + provenanceKey,
			Object:    ref,
		})
		p.Sources = append(p.Sources, SourceArtifact{Capability: capName, Path: path, Ref: ref, Bytes: raw})

		for _, w := range spec.Warnings {
			p.Warnings = append(p.Warnings, capName+": "+w)
		}
	}
	return p, nil
}

// Triples stamps a projection's subject-less facts onto subject as owned triples:
// Source is the single owner (== the vocab writer), so the whole ingest is
// attributable to the projector. The ingest wiring calls this with the target
// spec entity's id and the ingest time.
func Triples(subject string, p *Projection, now time.Time) []message.Triple {
	out := make([]message.Triple, 0, len(p.Facts))
	for _, f := range p.Facts {
		out = append(out, message.Triple{
			Subject:    subject,
			Predicate:  f.Predicate,
			Object:     f.Object,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		})
	}
	return out
}

// hashRef is the content-hash reference for a source artifact's raw bytes.
func hashRef(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
