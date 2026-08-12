// Package conformance holds semdev's constitution pins — the machine checks that
// enforce the ten guardrails (docs/constitution.md). The thesis, ported from
// semspec's test/plumbing discipline: every architectural failure semspec paid a
// real run to discover was observable from deterministic data-shape inspection
// alone. These tests move that inspection into `go test`, so a drift fails the
// build offline instead of waiting for a paid run to re-discover it.
//
// Each pin enumerates reality into a set, compares it bidirectionally against a
// checked-in declaration, and fails with a message naming the cost it prevents.
// A guardrail without a pin is a wish (constitution preamble); this package is
// where the wishes become law.
package conformance
