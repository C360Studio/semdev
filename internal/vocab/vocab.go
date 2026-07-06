// Package vocab is semdev's fact vocabulary — the checked-in source of truth the
// G5 (single-writer) and G9 (minimal-vocabulary) conformance pins compare
// against, and that docs project (G10). semspec accreted 224 predicates with
// "sole writer" as a false comment; semdev starts from zero and every predicate
// carries, in code, its single writer and the OpenSpec change that introduced it.
//
// Adding a predicate here is the deliberate act G9 requires: a new fact must be
// named by the change that needs it. Product code and rules reference these
// entries; the census tests in test/conformance enforce the invariants.
package vocab

import "strings"

// Predicate is one fact predicate: its name, the single writer allowed to stamp
// it (G5), the capability that owns it, and the OpenSpec change slug that
// introduced it (G9). A trailing ".*" in Name denotes a writer-owned namespace
// (every predicate under that prefix has the same single writer).
type Predicate struct {
	Name         string
	Writer       string
	Capability   string
	IntroducedBy string
}

// m0 is the change slug that introduces semdev's founding vocabulary.
const m0 = "m0-walking-skeleton-spine"

// Predicates is the complete fact vocabulary. See design.md D12 — this table is
// that decision made executable. Order is presentational only; the pins treat it
// as a set keyed by Name.
var Predicates = []Predicate{
	{"intake.actor", "admission-check", "forge-io", m0},
	{"intake.admitted", "admission-check", "forge-io", m0},
	{"run.issue_ref", "issue-intake-adapter", "forge-io", m0},
	{"run.change_approved", "approval-adapter", "forge-io", m0},
	{"human.signal", "comment-adapter", "forge-io", m0},
	{"run.awaiting_human", "park-rule", "run-lifecycle", m0},
	{"pr.ref", "pr-delivery-adapter", "forge-io", m0},
	{"openspec.change.*", "create-change-author-tool", "openspec-io", m0},
	{"openspec.spec.*", "brownfield-spec-projector", "openspec-io", m0},
	{"openspec.validated", "openspec-validate-harness", "openspec-io", m0},
	{"openspec.archived", "openspec-archive-harness", "openspec-io", m0},
	{"task.spec", "task-projector", "dev-from-task", m0},
	{"task.attempt", "dev-loop-harness", "dev-from-task", m0},
	{"floor.finding", "floor-tools", "dev-from-task", m0},
	{"measurement.result", "measurement-harness", "harness-measurement", m0},
	{"review.verdict", "reviewer-quinn", "harness-measurement", m0},
	{"verify.result", "verify-harness", "clean-room-verify", m0},
	{"evidence.run", "evidence-ledger", "evidence-ledger", m0},
}

// Names returns every predicate name in declaration order.
func Names() []string {
	names := make([]string, len(Predicates))
	for i, p := range Predicates {
		names[i] = p.Name
	}
	return names
}

// WriterOf returns the single writer declared for a predicate name. It matches
// exactly first, then falls back to a declared namespace: a concrete name like
// "openspec.change.proposal" resolves to the writer of the "openspec.change.*"
// entry. ok is false when the name is in neither.
func WriterOf(name string) (writer string, ok bool) {
	for _, p := range Predicates {
		if p.Name == name {
			return p.Writer, true
		}
	}
	for _, p := range Predicates {
		if prefix, isNS := strings.CutSuffix(p.Name, ".*"); isNS && strings.HasPrefix(name, prefix+".") {
			return p.Writer, true
		}
	}
	return "", false
}
