// Package secrets is semdev's governed creds-ref store and leak guard (design SB2c,
// G7). A secret required to resolve a real dependency (e.g. a GitHub Packages PAT that a
// cold `go mod download` needs) is a NAMED ref the operator registers in a governed
// store — a git-ignored `.env` at M0 — referenced by NAME everywhere (manifest,
// Dockerfile, run), never by value. semdev resolves the name to a value only at
// injection time and injects it through docker's real channels, so the value is present
// for cold resolution but NEVER baked into an image layer, committed to the artifact, or
// written to a log, tool result, or attestation fact. A missing required ref fails CLOSED
// toward the operator (register it) — never a degraded fallback that masks the missing
// secret (semspec's credential fallback was a masking path).
//
// M0 implements the RUN-TIME env channel (ExecEnvFlags/ExecEnvKV — a `-e NAME`
// pass-through with the value in the docker process's own environment, off its argv). A
// BUILD-time channel (BuildKit `--mount=type=secret`, so a build-time credential is never
// in an image layer or the build log) is a follow-up; when it lands, the build-log tail
// BuildImage surfaces on failure must be scrubbed too (it is not today — no secret
// reaches the image build at M0).
//
// The package holds three pieces: the Store (resolve a name → value), the Scrubber
// (redact values from any string before it is logged or stamped — the G7 guard), and
// the injection-arg builders (the docker channels). It writes no facts and fires no
// transition.
package secrets

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Store resolves a named creds-ref to its value. Implementations must never expose the
// full name→value map (only a by-name lookup), so a caller cannot enumerate secrets.
type Store interface {
	// Resolve returns the value for a ref name, and whether it is registered.
	Resolve(ref string) (value string, found bool)
}

// DotEnvStore is the M0 governed store: named entries in a git-ignored `.env`. The file
// is loaded once; values live only in memory.
type DotEnvStore struct {
	entries map[string]string
}

// LoadDotEnv loads a governed `.env` store from path. It fails closed: a missing file or
// a malformed line (no `=`) is an error, never an empty store that would then treat every
// required ref as absent-but-benign. Blank lines and `#` comments are skipped; a leading
// `export ` is tolerated; a value may be wrapped in matching single or double quotes.
func LoadDotEnv(path string) (*DotEnvStore, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("secrets: open governed store %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	entries := make(map[string]string)
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		raw = strings.TrimPrefix(raw, "export ")
		key, val, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("secrets: %s:%d: malformed entry (no '='): %q", path, line, sc.Text())
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("secrets: %s:%d: empty key", path, line)
		}
		entries[key] = unquote(strings.TrimSpace(val))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("secrets: read governed store %s: %w", path, err)
	}
	return &DotEnvStore{entries: entries}, nil
}

// Resolve returns the value for a ref name and whether it is registered.
func (s *DotEnvStore) Resolve(ref string) (string, bool) {
	v, ok := s.entries[ref]
	return v, ok
}

// unquote strips one layer of matching single or double quotes from a value.
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// MissingRefsError names the required creds-refs that are not registered in the store —
// the fail-closed park signal toward the operator (register these), never a silent
// fallback (SB2c).
type MissingRefsError struct {
	Refs []string
}

func (e *MissingRefsError) Error() string {
	return fmt.Sprintf("secrets: required creds-ref(s) not registered in the governed store: %s — register them (SB2c)", strings.Join(e.Refs, ", "))
}

// ResolveAll resolves every required ref to a name→value map, failing CLOSED with a
// *MissingRefsError if any is absent (park toward the operator). An empty ref list
// returns an empty map (the common M0 case: no secrets needed).
func ResolveAll(store Store, refs []string) (map[string]string, error) {
	out := make(map[string]string, len(refs))
	var missing []string
	for _, ref := range refs {
		if store == nil {
			missing = append(missing, ref)
			continue
		}
		v, ok := store.Resolve(ref)
		// An absent ref OR a registered-but-EMPTY value both fail closed: a blank secret
		// resolves nothing, so it is "not registered" for park purposes — a precise
		// "register a value for X" park beats a downstream "resolution failed" (SB2c).
		if !ok || strings.TrimSpace(v) == "" {
			missing = append(missing, ref)
			continue
		}
		out[ref] = v
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, &MissingRefsError{Refs: missing}
	}
	return out, nil
}
