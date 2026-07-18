// Package specfacts is the living-spec analogue of internal/changefacts: it holds the
// one canonical scalar a brownfield-projected capability spec lands under. The old
// openspec.spec.<cap>.<field> triple tree is 4+ segments — NOT canonicalizable under the
// 3-part predicate contract semstreams beta.150 enforces FAIL-CLOSED at the graph-write
// boundary — so the brownfield projector serializes a WHOLE capability spec to one JSON
// document here (mirroring changefacts.DocumentPredicate / the beta.147 D3 change blob).
// A capability spec is one artifact, not dozens of independent facts; the blob is simpler,
// canonical, and shrinks the vocabulary (G9). Single writer brownfield-spec-projector (G5).
package specfacts

import (
	"encoding/json"
	"fmt"

	"github.com/c360studio/semdev/internal/openspec"
)

// DocumentPredicate is the single canonical (3-part) predicate a living capability spec
// lands under, on that capability's spec entity. Latest-wins per entity (a re-ingest
// upserts it). It is the living-spec twin of changefacts.DocumentPredicate.
const DocumentPredicate = "openspec.spec.document"

// SpecDocument is one living capability spec in one scalar: the parsed openspec.Spec model
// plus the content-hash reference to its retained source bytes (provenance travels WITH the
// content — there is no separate gate that reads it, unlike the change side's revision). The
// Spec's own Warnings are projection diagnostics, not spec content, so the projector clears
// them before marshaling; they stay in the Projection for surfacing.
type SpecDocument struct {
	Spec      *openspec.Spec `json:"spec"`
	SourceRef string         `json:"source_ref"`
}

// MarshalDocument encodes the document to the scalar object stored under DocumentPredicate.
// Go's json.Marshal is deterministic for structs (fixed field order), so a re-ingest of an
// unchanged spec produces an identical object (the determinism the projector's tests pin).
func MarshalDocument(doc SpecDocument) (string, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal spec document: %w", err)
	}
	return string(b), nil
}

// UnmarshalDocument decodes the scalar stored under DocumentPredicate. An empty string
// (never projected) yields the zero document (nil Spec, empty SourceRef), NOT an error —
// the caller decides whether an empty spec is a failure for its step.
func UnmarshalDocument(s string) (SpecDocument, error) {
	if s == "" {
		return SpecDocument{}, nil
	}
	var doc SpecDocument
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return SpecDocument{}, fmt.Errorf("unmarshal spec document: %w", err)
	}
	return doc, nil
}
