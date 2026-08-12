package cleanroom

import "strings"

// ResolveClass is how a dependency-resolution step's outcome maps onto the verify
// evidence: it is the one distinction verify.Decide cannot make for itself, because
// a resolve command that ran and exited non-zero looks identical whether the
// registry was unreachable (an infra hiccup) or a coordinate was missing/fabricated
// (a genuine artifact failure). Getting this wrong is the make-or-break error in
// both directions: a transport fault read as genuine terminal-rejects a GOOD
// artifact; a genuine fabrication read as transport lets a BAD artifact spin instead
// of failing. Neither can ever produce a false-green (a failed resolve is never a
// Pass), so this only decides Retry-vs-Fail.
type ResolveClass int

const (
	// ResolveOK — the resolve step exited zero; dependencies resolved cold.
	ResolveOK ResolveClass = iota
	// ResolveFailed — the resolve step failed and the failure is a GENUINE artifact
	// fault (a missing or fabricated coordinate the fresh cache could not mask). Maps
	// to verify Resolved=false → terminal Fail.
	ResolveFailed
	// ResolveTransport — the resolve step failed for a transport/infrastructure reason
	// (registry unreachable, TLS/DNS/proxy fault, timeout). Maps to verify
	// Completed=false → Retry, never a terminal reject of the artifact.
	ResolveTransport
)

// transportSignatures are substrings that positively identify a resolve failure as
// a transport/infrastructure fault rather than a genuine missing coordinate. The
// list targets the Go toolchain's network-failure phrasing (M0's profile) plus the
// ecosystem-agnostic TCP/TLS/DNS/proxy vocabulary; other profiles extend it. Matched
// case-insensitively against the combined stdout+stderr.
var transportSignatures = []string{
	"dial tcp",
	"dial udp",
	"i/o timeout",
	"timeout",
	"connection refused",
	"connection reset",
	"network is unreachable",
	"no such host",
	"server misbehaving",
	"tls handshake",
	"tls: ",
	"x509",
	"certificate",
	"proxyconnect",
	"proxy error",
	"too many requests", // HTTP 429 — proxy rate-limiting, transient
	"500 internal server error",
	"502 bad gateway",
	"503 service unavailable",
	"504 gateway timeout",
	"eof",
	"temporary failure in name resolution",
}

// ClassifyResolve maps a resolve step's Result to a ResolveClass, and returns a short
// human detail. A zero exit is ResolveOK. A non-zero exit whose output carries a
// known transport signature is ResolveTransport (Retry). Any OTHER non-zero exit is
// ResolveFailed (genuine) — the fail-closed default: for a known ecosystem, a resolve
// failure that is not a recognized transport fault is treated as a genuine artifact
// failure, matching the spec's "fail closed on a genuine artifact failure; retry WHEN
// a transport error." The transport signature list is the explicit retry carve-out;
// keep it broad so an unrecognized infra fault is not misread as a fabrication.
func ClassifyResolve(r Result) (ResolveClass, string) {
	if r.ExitCode == 0 {
		return ResolveOK, "dependencies resolved cold from the artifact's own declarations"
	}
	haystack := strings.ToLower(r.Stdout + "\n" + r.Stderr)
	if sig, ok := firstMatch(haystack, transportSignatures); ok {
		return ResolveTransport, "resolve step hit a transport/infrastructure fault (" + sig + ") — retry, not a verdict"
	}
	return ResolveFailed, "resolve step failed to resolve a coordinate cold (missing or fabricated dependency)"
}

// firstMatch returns the first signature contained in haystack (already lowered).
func firstMatch(haystack string, signatures []string) (string, bool) {
	for _, s := range signatures {
		if strings.Contains(haystack, s) {
			return s, true
		}
	}
	return "", false
}
