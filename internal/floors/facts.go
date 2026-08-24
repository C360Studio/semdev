package floors

// This file names the GRAPH PROJECTION of a Finding — the owned fact package the
// check_floors tool (the floor-tools wrapper) stamps on the run entity, and that the
// loop gate reads to route on. The pure floors compute the Finding; these consts fix
// how it lands on the graph, shared between the writer and any future reader so they
// cannot drift.
//
// beta.147 D1/D4: the findings FLATTEN. The old per-(task, floor) tree
// floor.finding.<i>.<floor>.<field> is not canonicalizable (>3 segments, a digit
// segment), so it collapses to three flat predicates on the run: the aggregate the
// route reads, one human-legible detail scalar (the per-floor prose concatenated), and
// the attempt binding. Single-task at M0 (the index is out of the predicate); the
// route only ever read the aggregate, so nothing loses a matched condition.

import (
	"fmt"
	"strings"
)

// FindingPrefix is the shared prefix of the flattened finding predicates — the
// read/clear scope for the whole finding package on the run entity (writer
// floor-tools, G5).
const FindingPrefix = "floor.finding."

// The flattened finding predicates (writer floor-tools, G5). These are WHOLE
// predicates (not suffixes concatenated onto FindingPrefix) — the *Predicate naming
// signals that, distinct from measurement's Fact* suffixes which ARE concatenated.
const (
	// RejectedPredicate is the AGGREGATE verdict the dev-loop route reads: "true" iff
	// ANY floor rejected (floors.AnyRejected). One literal read, not a re-derivation
	// from per-floor sub-keys. The route stays a simple literal read.
	RejectedPredicate = "floor.finding.rejected"
	// DetailPredicate is the per-floor prose, concatenated into one human-legible
	// scalar — kept so a park-toward-human on a rejecting floor is legible (G7). It was
	// never matched in a rule condition, so folding the per-floor sub-keys into one
	// scalar loses no routing surface.
	DetailPredicate = "floor.finding.detail"
	// AttemptPredicate binds the whole finding set to the AttemptID it evaluated, so a
	// gate can require the findings belong to the run's CURRENT attempt before reading
	// them as a pass — without it, a stale earlier pass is indistinguishable from a current one.
	AttemptPredicate = "floor.finding.attempt"
)

// FormatDetail renders the per-floor findings into the one human-legible detail
// scalar (D4). Each line names the floor, its pass/reject, and its reason — so a
// park-toward-human keeps the full floor breakdown the per-floor triples used to
// carry. Order is CheckAll's fixed floor order followed by the repo's declared check
// order (standards-via-lessons D7), so the output stays stable run to run.
func FormatDetail(findings []Finding) string {
	var b strings.Builder
	for i, f := range findings {
		if i > 0 {
			b.WriteByte('\n')
		}
		status := "passed"
		switch {
		case f.Passed:
		case f.Advisory:
			// Reported, not gating — and still rendered as a failure, because a reader
			// who sees "passed" for a check that failed has been misled by the evidence.
			status = "FAILED (advisory — reported, does not gate)"
		default:
			status = "REJECTED"
		}
		fmt.Fprintf(&b, "%s: %s", f.Floor, status)
		if f.Detail != "" {
			fmt.Fprintf(&b, " — %s", f.Detail)
		}
	}
	return b.String()
}
