package harness

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
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
	// CacheHomeEnvs names the package-cache env vars each proof freshens (the G4
	// control), overriding the convention's. A non-Go repo needs the commands above too;
	// what made this one special is that it was the ONLY proof-required field with no
	// declaration surface at all, so a profile shipping no convention could not name it
	// by any means (semdev #28).
	CacheHomeEnvs []string `json:"cacheHomeEnvs"`
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
// keeps the convention default.
//
// It fails CLOSED four ways, each toward the operator rather than into a hollow manifest:
// no declared image (SB2); a profile with neither convention nor customizations; a
// malformed cache-home env name; and any missing required run field — the last reported
// as the COMPLETE list (MissingRunFields), never one field per round-trip. Two of those
// carry the graves they exist for: a manifest with no test command measures nothing (the
// semspec "verified over zero executions" grave, SB5), and one with no cache home has
// nothing to freshen, so its "cold" proof would prove nothing (G4).
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
		if len(custom.CacheHomeEnvs) > 0 {
			m.CacheHomeEnvs = slices.Clone(custom.CacheHomeEnvs)
		}
	}

	if !haveConvention && !found {
		return Manifest{}, fmt.Errorf("harness: no convention for profile %q and no customizations.semdev block — declare the run commands (SB2)", profile)
	}
	// Fail closed at the DECLARATION boundary, reporting EVERY missing field at once. A
	// guard per field would drip them one round-trip at a time — the operator fixes what
	// the message names, re-runs, and is told about the next one. That drip is the shape
	// of the bug this function is being fixed for (semdev #28), so the fix must not
	// reintroduce it one layer up.
	//
	// Two properties this enforces, both fail-closed:
	//   - a manifest with no test command measures nothing — the semspec "verified over
	//     zero executions" grave (SB5);
	//   - a manifest naming no cache home has nothing to freshen, so its "cold" proof
	//     would prove nothing — exactly the masking a fresh cache home exists to prevent (G4).
	// The cold provers re-check via the same function, which is genuinely redundant for
	// anything resolved here and is the ONLY guard for a hand-built manifest.
	// Validate the cache-home names BEFORE the completeness guard, so a list that is
	// entirely malformed reads as ABSENT and is reported as the missing declaration it
	// effectively is, rather than travelling on as a length-1 list of garbage.
	if bad := invalidCacheHomeEnvs(m.CacheHomeEnvs); len(bad) > 0 {
		return Manifest{}, fmt.Errorf("harness: manifest for profile %q declares invalid cache-home env names: %s — each must be a plain environment variable name, e.g. GRADLE_USER_HOME (SB2 fail-closed)", profile, strings.Join(bad, ", "))
	}
	m.CacheHomeEnvs = dedupe(m.CacheHomeEnvs)
	if missing := MissingRunFields(m); len(missing) > 0 {
		return Manifest{}, fmt.Errorf("harness: manifest for profile %q is missing required run fields: %s — declare them in the repo's customizations.semdev block (SB2 fail-closed)", profile, strings.Join(missing, ", "))
	}
	return m, nil
}

// MissingRunFields names the run fields the cold proofs require but this manifest does
// not carry, reported with the `customizations.semdev` keys an operator actually types.
// It returns the NAMES, not a bool, because semdev #28 was an operator being handed a
// list that restated fields they had already declared alongside one they had no surface
// to declare at all — a complete, specific list is the remedy for both halves.
//
// Both cold proofs require all four: the baseline proves resolve+BUILD, the final verify
// proves resolve+TEST, and every proof freshens the cache homes. A manifest missing any
// of them cannot complete the arc, so resolution reports them together rather than
// letting each proof discover its own.
func MissingRunFields(m Manifest) []string {
	var missing []string
	if len(m.ResolveCmd) == 0 {
		missing = append(missing, "resolveCommand")
	}
	if len(m.BuildCmd) == 0 {
		missing = append(missing, "buildCommand")
	}
	if len(m.TestCmd) == 0 {
		missing = append(missing, "testCommand")
	}
	if len(m.CacheHomeEnvs) == 0 {
		missing = append(missing, "cacheHomeEnvs")
	}
	return missing
}

// invalidCacheHomeEnvs names the declared cache-home entries that are not plain
// environment variable names. These strings come from a TARGET repo's committed
// devcontainer.json — untrusted input, the same class the spec already makes fail closed
// for a traversal path in the image declaration — and they become both an env var name
// and a container-internal mount path in the clean-room Runner. Rejecting them here
// costs one pass and catches the honest typo `["GRADLE_USER_HOME=/tmp/x"]`, which would
// otherwise set a nonsense variable and leave the REAL cache home unfreshened: a cold
// proof that quietly is not cold (G4).
func invalidCacheHomeEnvs(names []string) []string {
	var bad []string
	for _, n := range names {
		if !validEnvName(n) {
			bad = append(bad, strconv.Quote(n))
		}
	}
	return bad
}

// dedupe drops repeated cache-home names, preserving declaration order. A repeat states
// the same intent twice and is harmless to collapse — but passed through, it makes the
// clean-room Runner request two mounts at one container path, which docker REJECTS. That
// surfaces as a transport fault and is retried as infra, so a duplicated name would park
// a run on what is really a harmless typo. Collapsing preserves the operator's meaning
// exactly; rejecting would not.
func dedupe(names []string) []string {
	if len(names) < 2 {
		return names
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// validEnvName reports whether s is a POSIX-shaped environment variable name:
// [A-Za-z_][A-Za-z0-9_]*. Deliberately stricter than what a shell would tolerate — a
// cache-home name has no reason to carry a separator, a path, or whitespace.
func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
