package openspec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644
)

// Change is a complete OpenSpec change folder (changes/<slug>/), aggregating the
// proposal, optional design, tasks, and per-capability delta specs. It is the
// unit that ReadChange/WriteChange round-trip through the filesystem.
type Change struct {
	// Slug is the changes/<slug>/ folder name.
	Slug string
	// Proposal is proposal.md; nil if absent.
	Proposal *Proposal
	// Design is design.md; nil if absent (design.md is optional in OpenSpec).
	Design *Design
	// Tasks is tasks.md; nil if absent.
	Tasks *Tasks
	// Deltas are the specs/<capability>/spec.md delta specs, one per capability,
	// sorted by capability.
	Deltas []Delta
}

// ReadChange reads an OpenSpec change folder from disk into a Change. The slug
// is the folder's base name. Missing files yield nil/empty fields — ingest is
// lenient, so reading a directory with no recognised files yields
// an empty Change, not an error; only filesystem errors are returned.
//
// A nonexistent directory is not distinguished from an empty one: both yield an
// all-nil Change with Slug set from the path. A caller that must know whether
// the change exists (e.g. create_change) should os.Stat the dir first.
func ReadChange(dir string) (*Change, error) {
	c := &Change{Slug: filepath.Base(dir)}

	md, ok, err := readFileIfExists(filepath.Join(dir, "proposal.md"))
	if err != nil {
		return nil, err
	}
	if ok {
		c.Proposal = ParseProposal(md)
	}

	md, ok, err = readFileIfExists(filepath.Join(dir, "design.md"))
	if err != nil {
		return nil, err
	}
	if ok {
		c.Design = ParseDesign(md)
	}

	md, ok, err = readFileIfExists(filepath.Join(dir, "tasks.md"))
	if err != nil {
		return nil, err
	}
	if ok {
		c.Tasks = ParseTasks(md)
	}

	c.Deltas, err = readDeltas(filepath.Join(dir, "specs"))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// WriteChange writes a Change to an OpenSpec change folder at dir, creating it
// and the specs/<capability>/ subtree as needed. The render is canonical
// , so WriteChange∘ReadChange is semantically stable, not
// byte-identical to the source.
//
// WriteChange is **authoritative** over the files it manages: it rewrites the
// managed specs/ subtree from scratch and removes any optional file the Change
// omits, so the target reflects exactly this Change even when it is non-empty
// (a stale capability delta or a dropped design.md is pruned). Un-managed
// sibling files (e.g. a README) are left untouched.
func WriteChange(dir string, c *Change) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create change dir %q: %w", dir, err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "specs")); err != nil {
		return fmt.Errorf("clear delta specs: %w", err)
	}

	if err := writeOrRemove(filepath.Join(dir, "proposal.md"), c.Proposal != nil,
		func() string { return RenderProposal(c.Proposal) }); err != nil {
		return err
	}
	if err := writeOrRemove(filepath.Join(dir, "design.md"), c.Design != nil,
		func() string { return RenderDesign(c.Design) }); err != nil {
		return err
	}
	if err := writeOrRemove(filepath.Join(dir, "tasks.md"), c.Tasks != nil,
		func() string { return RenderTasks(c.Tasks) }); err != nil {
		return err
	}

	for i := range c.Deltas {
		d := &c.Deltas[i]
		if err := writeCapabilitySpec(filepath.Join(dir, "specs"), d.Capability, RenderDelta(d)); err != nil {
			return err
		}
	}
	return nil
}

// ReadSpecs reads an OpenSpec specs/ directory (one subdir per capability) into
// living Specs, sorted by capability. A missing dir yields nil, no error.
func ReadSpecs(specsDir string) ([]Spec, error) {
	names, err := capabilityNames(specsDir)
	if err != nil {
		return nil, err
	}
	var specs []Spec
	for _, name := range names {
		md, ok, err := readFileIfExists(filepath.Join(specsDir, name, "spec.md"))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		s := ParseSpec(md)
		s.Capability = name
		specs = append(specs, *s)
	}
	return specs, nil
}

// WriteSpecs writes living specs to a specs/ directory, one spec.md per
// capability.
func WriteSpecs(specsDir string, specs []Spec) error {
	for i := range specs {
		s := &specs[i]
		if err := writeCapabilitySpec(specsDir, s.Capability, RenderSpec(s)); err != nil {
			return err
		}
	}
	return nil
}

// readDeltas reads each capability subdir of a change's specs/ directory into a
// Delta, sorted by capability.
func readDeltas(specsDir string) ([]Delta, error) {
	names, err := capabilityNames(specsDir)
	if err != nil {
		return nil, err
	}
	var deltas []Delta
	for _, name := range names {
		md, ok, err := readFileIfExists(filepath.Join(specsDir, name, "spec.md"))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		d := ParseDelta(md)
		d.Capability = name
		deltas = append(deltas, *d)
	}
	return deltas, nil
}

// capabilityNames returns the sorted subdirectory names of a specs/ directory
// (one per capability). A missing directory yields nil, no error.
func capabilityNames(specsDir string) ([]string, error) {
	entries, err := os.ReadDir(specsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read specs dir %q: %w", specsDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

// writeOrRemove writes render() to path when present, else removes any existing
// file there (pruning an optional artifact the Change omits). render is only
// invoked when present, so it may dereference the then-non-nil source.
func writeOrRemove(path string, present bool, render func() string) error {
	if !present {
		return removeIfExists(path)
	}
	if err := writeFile(path, render()); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

// writeCapabilitySpec writes content to specsDir/<capability>/spec.md, creating
// the directory. The capability is validated as a single safe path segment so a
// programmatically-built Change (e.g. from create_change) cannot write outside
// the specs tree.
func writeCapabilitySpec(specsDir, capability, content string) error {
	if err := validateCapability(capability); err != nil {
		return err
	}
	p := filepath.Join(specsDir, capability, "spec.md")
	if err := os.MkdirAll(filepath.Dir(p), dirPerm); err != nil {
		return fmt.Errorf("create capability dir for %q: %w", capability, err)
	}
	if err := writeFile(p, content); err != nil {
		return fmt.Errorf("write capability %q spec: %w", capability, err)
	}
	return nil
}

// validateCapability rejects a capability name that is not a single safe path
// segment (empty, "." / "..", or containing a path separator or ".."), which
// would let a write escape the specs tree.
func validateCapability(capability string) error {
	if capability == "" || capability == "." || capability == ".." ||
		strings.ContainsAny(capability, `/\`) || strings.Contains(capability, "..") {
		return fmt.Errorf("invalid capability name %q", capability)
	}
	return nil
}

// readFileIfExists reads a file, returning ("", false, nil) if it does not
// exist so callers can treat optional files uniformly.
func readFileIfExists(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %q: %w", path, err)
	}
	return string(b), true, nil
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), filePerm)
}

// removeIfExists deletes path, tolerating its absence.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %q: %w", path, err)
	}
	return nil
}
