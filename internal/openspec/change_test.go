package openspec

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func changeFixtureDir() string {
	return filepath.Join("testdata", "openspec", "changes", "add-mfa")
}

// TestReadChange_Fixture pins the assembled Change read from a real folder.
func TestReadChange_Fixture(t *testing.T) {
	c, err := ReadChange(changeFixtureDir())
	if err != nil {
		t.Fatalf("ReadChange: %v", err)
	}

	if c.Slug != "add-mfa" {
		t.Errorf("Slug = %q, want add-mfa", c.Slug)
	}
	if c.Proposal == nil || c.Design == nil || c.Tasks == nil {
		t.Fatalf("missing artifact: proposal=%v design=%v tasks=%v",
			c.Proposal != nil, c.Design != nil, c.Tasks != nil)
	}
	if got, want := c.Proposal.Title, "Proposal: Add Multi-Factor Authentication"; got != want {
		t.Errorf("proposal title = %q, want %q", got, want)
	}
	if len(c.Deltas) != 1 || c.Deltas[0].Capability != "auth" {
		t.Fatalf("deltas = %+v, want exactly one for capability auth", c.Deltas)
	}
	d := c.Deltas[0]
	if len(d.Added) != 1 || len(d.Modified) != 1 || len(d.Removed) != 1 {
		t.Errorf("auth delta shape = +%d ~%d -%d, want 1/1/1", len(d.Added), len(d.Modified), len(d.Removed))
	}
}

// TestChangeRoundTrip_Folder is the P1 acceptance gate (ADR-057 §D2): read a
// real change folder, write it to a fresh directory, read it back, and assert
// semantic stability across the whole assembled Change.
func TestChangeRoundTrip_Folder(t *testing.T) {
	first, err := ReadChange(changeFixtureDir())
	if err != nil {
		t.Fatalf("ReadChange: %v", err)
	}

	dst := filepath.Join(t.TempDir(), first.Slug)
	if err := WriteChange(dst, first); err != nil {
		t.Fatalf("WriteChange: %v", err)
	}
	second, err := ReadChange(dst)
	if err != nil {
		t.Fatalf("re-ReadChange: %v", err)
	}

	if diff := cmp.Diff(first, second, ignoreDeltaWarnings, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("change folder round-trip not stable (-first +second):\n%s", diff)
	}
}

// TestSpecsRoundTrip_Dir round-trips the living specs/ directory.
func TestSpecsRoundTrip_Dir(t *testing.T) {
	first, err := ReadSpecs(filepath.Join("testdata", "openspec", "specs"))
	if err != nil {
		t.Fatalf("ReadSpecs: %v", err)
	}
	if len(first) != 1 || first[0].Capability != "auth" {
		t.Fatalf("specs = %+v, want exactly one for capability auth", first)
	}

	dst := filepath.Join(t.TempDir(), "specs")
	if err := WriteSpecs(dst, first); err != nil {
		t.Fatalf("WriteSpecs: %v", err)
	}
	second, err := ReadSpecs(dst)
	if err != nil {
		t.Fatalf("re-ReadSpecs: %v", err)
	}

	if diff := cmp.Diff(first, second, ignoreWarnings); diff != "" {
		t.Errorf("specs dir round-trip not stable (-first +second):\n%s", diff)
	}
}

// TestReadChange_MissingDirIsEmpty: reading a nonexistent change folder yields
// an empty Change (lenient ingest), not an error.
func TestReadChange_MissingDirIsEmpty(t *testing.T) {
	c, err := ReadChange(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("ReadChange on missing dir: %v", err)
	}
	if c.Proposal != nil || c.Design != nil || c.Tasks != nil || len(c.Deltas) != 0 {
		t.Errorf("expected empty Change, got %+v", c)
	}
	if c.Slug != "nope" {
		t.Errorf("Slug = %q, want nope", c.Slug)
	}
}

// TestReadChange_OptionalDesignAbsent: a change without design.md yields a nil
// Design and still round-trips (design.md is optional in OpenSpec).
func TestReadChange_OptionalDesignAbsent(t *testing.T) {
	src, err := ReadChange(changeFixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	src.Design = nil

	cdir := filepath.Join(t.TempDir(), src.Slug)
	if err := WriteChange(cdir, src); err != nil {
		t.Fatal(err)
	}
	got, err := ReadChange(cdir)
	if err != nil {
		t.Fatal(err)
	}

	if got.Design != nil {
		t.Errorf("Design = %+v, want nil", got.Design)
	}
	if diff := cmp.Diff(src, got, ignoreDeltaWarnings, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("round-trip with absent design not stable (-src +got):\n%s", diff)
	}
}

// TestWriteChange_RejectsTraversalCapability: a programmatically-built Change
// with a traversal capability must not write outside the change dir (the
// create_change path-safety guard, ADR-057 §D2 §Risks).
func TestWriteChange_RejectsTraversalCapability(t *testing.T) {
	root := t.TempDir()
	c := &Change{
		Slug:   "x",
		Deltas: []Delta{{Capability: "../escaped", Added: []Requirement{{Name: "R", Statement: "The system SHALL x."}}}},
	}

	if err := WriteChange(filepath.Join(root, "x"), c); err == nil {
		t.Fatal("WriteChange accepted a traversal capability; want error")
	}
	if _, err := os.Stat(filepath.Join(root, "escaped")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("traversal wrote outside the change dir (stat err = %v)", err)
	}
}

// TestWriteChange_AuthoritativeOverStaleDir: re-emitting a Change over an
// existing folder prunes files the Change no longer carries (here, design.md),
// so the round-trip is stable on a non-empty target, not just a fresh one.
func TestWriteChange_AuthoritativeOverStaleDir(t *testing.T) {
	src, err := ReadChange(changeFixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), src.Slug)
	if err := WriteChange(dst, src); err != nil { // first write: full change incl. design
		t.Fatal(err)
	}

	src.Design = nil // re-emit without design; authoritative write must prune it
	if err := WriteChange(dst, src); err != nil {
		t.Fatal(err)
	}

	got, err := ReadChange(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got.Design != nil {
		t.Errorf("stale design.md not pruned: %+v", got.Design)
	}
	if diff := cmp.Diff(src, got, ignoreDeltaWarnings, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("authoritative round-trip not stable (-src +got):\n%s", diff)
	}
}

// TestChange_MultiCapabilityOrdering: a Change with multiple capabilities reads
// back sorted by capability, and re-emits as a fixed point.
func TestChange_MultiCapabilityOrdering(t *testing.T) {
	c := &Change{
		Slug: "multi",
		Deltas: []Delta{
			{Capability: "billing", Added: []Requirement{{Name: "Invoice", Statement: "The system SHALL issue an invoice."}}},
			{Capability: "auth", Added: []Requirement{{Name: "Login", Statement: "The system SHALL log a user in."}}},
		},
	}
	dst := filepath.Join(t.TempDir(), c.Slug)
	if err := WriteChange(dst, c); err != nil {
		t.Fatal(err)
	}
	got, err := ReadChange(dst)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Deltas) != 2 {
		t.Fatalf("got %d deltas, want 2", len(got.Deltas))
	}
	if got.Deltas[0].Capability != "auth" || got.Deltas[1].Capability != "billing" {
		t.Errorf("deltas not sorted by capability: [%s, %s]", got.Deltas[0].Capability, got.Deltas[1].Capability)
	}

	// re-emit the sorted Change and confirm it is a fixed point (same slug dir
	// so Slug, which ReadChange derives from the path, is preserved)
	dst2 := filepath.Join(t.TempDir(), got.Slug)
	if err := WriteChange(dst2, got); err != nil {
		t.Fatal(err)
	}
	got2, err := ReadChange(dst2)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(got, got2, ignoreDeltaWarnings, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("multi-capability round-trip not stable (-got +got2):\n%s", diff)
	}
}
