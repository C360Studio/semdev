package harness

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Command is a run command authored in a customizations.semdev block. It normalizes
// to argv (the shell-free form the clean-room Runner's Exec takes). It unmarshals from
// EITHER a JSON array of strings (literal argv, run with no shell) OR a single JSON
// string, which becomes `sh -c "<cmd>"` — matching how measure_task runs a task's
// test command, so an operator may write the ergonomic `"go test ./..."`.
//
// SB2 keeps this to RUN commands only; it is not an environment DSL — the Dockerfile
// owns the toolchain and environment.
type Command []string

// UnmarshalJSON accepts an argv array as-is, or wraps a bare string as `sh -c "<cmd>"`.
// An empty string / empty array / null normalizes to a nil Command (absent). Any other
// JSON type is a malformed declaration and errors (never silently dropped).
func (c *Command) UnmarshalJSON(data []byte) error {
	// Try an argv array first.
	var argv []string
	if err := json.Unmarshal(data, &argv); err == nil {
		if len(argv) == 0 {
			*c = nil
			return nil
		}
		*c = argv
		return nil
	}
	// Then a bare string convenience → sh -c. A blank/whitespace-only string
	// normalizes to absent (nil), NOT `sh -c "   "` — the latter runs, exits 0, and
	// reads as a false pass (the SB5 "measures-nothing reads green" grave). Left absent,
	// the ResolveManifest fail-closed guard catches it.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			*c = nil
			return nil
		}
		*c = Command{"sh", "-c", s}
		return nil
	}
	// null unmarshals into neither above with a nil error only for the array attempt
	// when data is `null` (json leaves argv nil) — handled above. Anything else is a
	// type error.
	return fmt.Errorf("harness: command must be a string or an array of strings, got %s", string(data))
}

// Customizations is the semdev-specific run block read from a devcontainer.json's
// customizations.semdev key. It carries ONLY run fields — never image/toolchain/env
// modeling (SB2: the Dockerfile owns the environment). Unset fields fall back to the
// profile convention in ResolveManifest.
type Customizations struct {
	// ResolveCommand resolves base dependencies (overrides the convention resolve cmd).
	ResolveCommand Command `json:"resolveCommand"`
	// BuildCommand builds the artifact cold (overrides the convention build cmd).
	BuildCommand Command `json:"buildCommand"`
	// TestCommand runs the artifact's own tests (overrides the convention test cmd).
	TestCommand Command `json:"testCommand"`
	// Tiers overrides the convention sandbox/operator-ci tier split when the operator
	// declares one (e.g. an OSH repo excluding its SITL tier from the sandbox).
	Tiers []Tier `json:"tiers"`
	// SecretRefs names the governed creds-refs resolution needs (SB2c) — names only.
	SecretRefs []string `json:"secretRefs"`
}

// devcontainer is the minimal slice of a devcontainer.json this package reads: only
// the customizations.semdev block. Every other devcontainer key (image, build,
// features, extensions) is intentionally ignored — the Dockerfile owns the
// environment (SB2).
type devcontainer struct {
	Customizations struct {
		Semdev *Customizations `json:"semdev"`
	} `json:"customizations"`
}

// ReadCustomizations extracts the customizations.semdev run block from a
// devcontainer.json's bytes. found is false (with a nil error) when the file carries
// no such block — the caller falls back to the profile convention. A malformed JSON
// document errors (fail loud). M0 requires strict JSON; JSONC comment tolerance is a
// follow-up.
func ReadCustomizations(devcontainerJSON []byte) (c Customizations, found bool, err error) {
	if len(devcontainerJSON) == 0 {
		return Customizations{}, false, nil
	}
	var dc devcontainer
	if err := json.Unmarshal(devcontainerJSON, &dc); err != nil {
		return Customizations{}, false, fmt.Errorf("harness: parse devcontainer.json: %w", err)
	}
	if dc.Customizations.Semdev == nil {
		return Customizations{}, false, nil
	}
	return *dc.Customizations.Semdev, true, nil
}

// ResolveManifest assembles the run Manifest for a checkout: the profile convention
// defaults (Convention), overlaid by any customizations.semdev block read from
// devcontainerJSON (pass nil when the repo declares a bare Dockerfile with no
// devcontainer). image is the located operator declaration (SB2).
//
// Overlay rule: a field the operator set in customizations.semdev wins; an unset field
// keeps the convention default. It fails CLOSED (SB5): if neither the convention nor
// the customizations supplies a test command, there is nothing to measure — a manifest
// that measured nothing is the semspec "verified over zero executions" grave — so it
// errors rather than return a hollow manifest.
func ResolveManifest(profile string, image ImageDecl, devcontainerJSON []byte) (Manifest, error) {
	m, haveConvention := Convention(profile)
	m.Profile = profile
	m.Image = image

	// SB2 fail-closed: an operator MUST declare an image; semdev never guesses one, so a
	// manifest with no declared image cannot be built or proven cold — error here so the
	// provisioning rule parks toward the operator (never a silent skip).
	if !image.Declared() {
		return Manifest{}, fmt.Errorf("harness: no image declared for profile %q — the operator must commit a Dockerfile/devcontainer (SB2 fail-closed)", profile)
	}

	custom, found, err := ReadCustomizations(devcontainerJSON)
	if err != nil {
		return Manifest{}, err
	}
	if found {
		if len(custom.ResolveCommand) > 0 {
			m.ResolveCmd = custom.ResolveCommand
		}
		if len(custom.BuildCommand) > 0 {
			m.BuildCmd = custom.BuildCommand
		}
		if len(custom.TestCommand) > 0 {
			m.TestCmd = custom.TestCommand
		}
		if len(custom.Tiers) > 0 {
			m.Tiers = slices.Clone(custom.Tiers)
		}
		if len(custom.SecretRefs) > 0 {
			m.SecretRefs = slices.Clone(custom.SecretRefs)
		}
	}

	if !haveConvention && !found {
		return Manifest{}, fmt.Errorf("harness: no convention for profile %q and no customizations.semdev block — declare the run commands (SB2)", profile)
	}
	if len(m.TestCmd) == 0 {
		return Manifest{}, fmt.Errorf("harness: manifest for profile %q has no test command — nothing to measure (SB5 fail-closed)", profile)
	}
	return m, nil
}
