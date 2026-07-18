package specfacts

import (
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/openspec"
)

// The empty string (a spec entity that never had a document projected) decodes to the zero
// document — NOT an error — so a reader can tell "absent" from "malformed" (the caller
// decides whether absent is a failure for its step).
func TestUnmarshalEmptyIsZeroNotError(t *testing.T) {
	doc, err := UnmarshalDocument("")
	if err != nil {
		t.Fatalf("empty string must decode without error, got %v", err)
	}
	if doc.Spec != nil || doc.SourceRef != "" {
		t.Errorf("empty string must decode to the zero document, got %+v", doc)
	}
}

// Marshal∘Unmarshal is an identity on the modeled fields, so a projected spec survives the
// scalar round-trip the graph stores it as.
func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	in := SpecDocument{
		Spec: &openspec.Spec{
			Capability: "auth",
			Title:      "Auth Specification",
			Purpose:    "Guard the front door.",
			Requirements: []openspec.Requirement{
				{Name: "Token check", Statement: "The system SHALL reject an expired token."},
			},
		},
		SourceRef: "deadbeef",
	}
	s, err := MarshalDocument(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalDocument(s)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.SourceRef != in.SourceRef {
		t.Errorf("source_ref = %q, want %q", out.SourceRef, in.SourceRef)
	}
	if out.Spec == nil || out.Spec.Title != in.Spec.Title || out.Spec.Purpose != in.Spec.Purpose {
		t.Fatalf("spec did not round-trip: got %+v", out.Spec)
	}
	if len(out.Spec.Requirements) != 1 || out.Spec.Requirements[0].Statement != in.Spec.Requirements[0].Statement {
		t.Errorf("requirement did not round-trip: got %+v", out.Spec.Requirements)
	}
}

// Determinism: the same document marshals to a byte-identical scalar (the projector's
// reproducibility contract depends on this).
func TestMarshalIsDeterministic(t *testing.T) {
	doc := SpecDocument{Spec: &openspec.Spec{Capability: "auth", Title: "T", Purpose: "P"}, SourceRef: "ref"}
	a, err := MarshalDocument(doc)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	b, err := MarshalDocument(doc)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	if a != b {
		t.Errorf("marshal is non-deterministic:\n a=%s\n b=%s", a, b)
	}
}

// Malformed JSON is a wrapped error the caller can surface, not a silent zero document.
func TestUnmarshalMalformedIsWrappedError(t *testing.T) {
	_, err := UnmarshalDocument("{not json")
	if err == nil {
		t.Fatal("malformed JSON must error")
	}
	if !strings.Contains(err.Error(), "unmarshal spec document") {
		t.Errorf("error not wrapped with context: %v", err)
	}
}
