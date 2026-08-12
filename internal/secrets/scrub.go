package secrets

import (
	"sort"
	"strings"
)

// redaction replaces a secret value wherever it appears. It is a fixed marker, not
// derived from the value, so nothing about the secret leaks.
const redaction = "***"

// Scrubber redacts secret VALUES from any string before it is logged, returned in a tool
// result, or stamped into a fact — the G7 guard that a secret value cannot be derived
// from any recorded evidence (SB2c). It is built from resolved values (not names): a
// build/resolve step whose output happens to echo an injected token has that token
// replaced with a fixed marker.
//
// Known limits (the safe direction — over-redact, never under-redact): it matches the
// value LITERALLY, so a token re-encoded (base64/URL/hex) in output is not caught (a
// value-scrubber floor, not total coverage); and a very short value over-redacts every
// occurrence across the text. Both fail toward MORE redaction, never a leak.
type Scrubber struct {
	replacer *strings.Replacer
}

// NewScrubber builds a Scrubber that redacts every value in the given name→value map.
// BLANK / whitespace-only values are dropped: redacting "" would replace the entire
// string (a catastrophic false redaction). Longer values are redacted first so a value
// that contains another is fully covered.
func NewScrubber(secretEnv map[string]string) *Scrubber {
	values := make([]string, 0, len(secretEnv))
	for _, v := range secretEnv {
		if strings.TrimSpace(v) == "" {
			continue
		}
		values = append(values, v)
	}
	if len(values) == 0 {
		return &Scrubber{} // no-op
	}
	// Longest first, so a value containing a shorter one is replaced whole.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := make([]string, 0, len(values)*2)
	for _, v := range values {
		pairs = append(pairs, v, redaction)
	}
	return &Scrubber{replacer: strings.NewReplacer(pairs...)}
}

// Scrub returns s with every known secret value replaced by the redaction marker. A
// nil/no-op scrubber returns s unchanged.
func (s *Scrubber) Scrub(str string) string {
	if s == nil || s.replacer == nil {
		return str
	}
	return s.replacer.Replace(str)
}
