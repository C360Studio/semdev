// Package brownfield is the deterministic ingest side of the openspec-io seam: it
// reads an existing target repository's openspec/specs/ workspace and projects
// each living capability spec to ONE canonical openspec.spec.document graph fact,
// under a single owner, with NO model in the loop (G3). It is the mirror of the
// hydrate path — hydrate renders a spec to markdown, brownfield parses markdown to
// a spec document — and it exists so onboarding a repo seeds the graph with its
// current spec surface without an LLM interpreting the artifacts.
//
// CANONICAL SHAPE (semstreams beta.150): a spec is stored as ONE JSON blob under the
// 3-part predicate openspec.spec.document (internal/specfacts), the living-spec twin of
// the beta.147 D3 change blob openspec.change.document — NOT the old
// openspec.spec.<cap>.<field> triple tree, whose 4+ segments the beta.150 fail-closed
// graph-write gate would reject. One document per capability, on that capability's spec
// entity, so the single predicate never collides across capabilities (G5).
//
// M0 scope is the LIVING SPECS only (openspec/specs/*/spec.md → openspec.spec.document).
// It deliberately does NOT project a brownfield repo's in-flight openspec/changes/ to
// openspec.change.* facts: that namespace has a single G5 writer (the create_change author
// tool), and a second writer would break the sole-writer invariant. In-flight changes in a
// target repo are the human's WIP, not state semdev owns; if a real need to ingest them
// appears, it earns its own owner and namespace then (G9 — no speculative vocabulary).
//
// This is a library projector (like internal/openspec, the ported engine): the pure
// parse-and-project core lands here with its red-first pins; the registered ingest
// component that wires it onto the raw lane (raw-lane→projector→graph-ingest, design D1)
// and stamps each capability's document on its spec entity lands with the runtime boot
// path (group 9/11). The single owner is the Source const, cross-checked against the
// vocab writer.
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
	"github.com/c360studio/semdev/internal/specfacts"
	"github.com/c360studio/semstreams/message"
)

// Source is the single owner stamped on every document triple this projector produces. It
// MUST equal the writer declared for openspec.spec.document in internal/vocab (G5) — a
// brownfield test cross-checks it, so the sole-owner claim is verifiable, not a comment.
const Source = "brownfield-spec-projector"

// SourceArtifact is one ingested spec.md's retained raw bytes and the content-hash
// reference (Ref, also carried inside the document blob as source_ref) — "store once,
// reference anywhere." The ingest wiring persists the bytes under Ref; the graph fact
// carries only the reference.
type SourceArtifact struct {
	Capability string
	Path       string
	Ref        string // sha256 hex of Bytes
	Bytes      []byte
}

// CapabilityDoc is one living capability projected to its canonical scalar: the marshaled
// openspec.spec.document object (specfacts.SpecDocument = the parsed spec + its source_ref)
// plus the retained source artifact. The ingest wiring stamps each on its own capability
// spec entity (Triples' subject), so the single openspec.spec.document predicate never
// collides across capabilities.
type CapabilityDoc struct {
	Capability string
	Document   string // the marshaled specfacts.SpecDocument scalar
	Source     SourceArtifact
}

// Projection is the deterministic result of parsing a repo's openspec/specs/: one canonical
// document per capability (in sorted-capability order) plus the lenient-parse warnings.
// Subjects and write metadata are applied by Triples — this core stays identity-agnostic
// and NATS-free.
type Projection struct {
	Docs     []CapabilityDoc
	Warnings []string
}

// ProjectSpecs reads specsDir (a repo's openspec/specs/ directory) and projects each
// capability's spec.md to ONE canonical openspec.spec.document blob (the parsed spec plus
// its provenance source_ref), returning one CapabilityDoc per capability (in sorted order)
// alongside the retained raw bytes. Parsing is lenient by design: bullet and heading-case
// variance produce warnings (surfaced, not fatal), and a missing/absent specs dir yields an
// empty projection, not an error — only a real filesystem read error (or a marshal fault)
// stops it. No model is invoked (G3).
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
		warnings := spec.Warnings // diagnostics are surfaced in the Projection, not the content blob
		spec.Warnings = nil

		ref := hashRef(raw)
		obj, err := specfacts.MarshalDocument(specfacts.SpecDocument{Spec: spec, SourceRef: ref})
		if err != nil {
			// A marshal failure is a real projector fault (not lenient-parse variance) — stop.
			return nil, fmt.Errorf("marshal spec document for %q: %w", capName, err)
		}
		p.Docs = append(p.Docs, CapabilityDoc{
			Capability: capName,
			Document:   obj,
			Source:     SourceArtifact{Capability: capName, Path: path, Ref: ref, Bytes: raw},
		})

		for _, w := range warnings {
			p.Warnings = append(p.Warnings, capName+": "+w)
		}
	}
	return p, nil
}

// Triples stamps ONE capability's document as a single owned openspec.spec.document triple
// on subject (that capability's spec entity). Source is the single owner (== the vocab
// writer), so the ingest is attributable to the projector. The wiring calls this once per
// capability with that capability's entity id, so the single predicate lands on distinct
// entities and never collides (G5).
func Triples(subject string, d CapabilityDoc, now time.Time) []message.Triple {
	return []message.Triple{{
		Subject:    subject,
		Predicate:  specfacts.DocumentPredicate,
		Object:     d.Document,
		Source:     Source,
		Timestamp:  now,
		Confidence: 1.0,
	}}
}

// hashRef is the content-hash reference for a source artifact's raw bytes.
func hashRef(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
