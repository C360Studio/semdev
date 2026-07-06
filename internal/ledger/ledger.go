// Package ledger is semdev's evidence ledger schema (G7) — the honest record of
// runs, present from M0. It ports semspec's status-vocabulary discipline (S10)
// without semspec's UI/hard-scenario specifics: a fixed status vocabulary, a
// fail-closed Validate that rejects an out-of-vocabulary status, a CountsAsPass
// rule that never blesses a run whose artifact failed independent verification,
// and a bridge-proof label so mock and fixture-seeded runs are never presented
// as real-LLM product evidence.
//
// The single writer of ledger entries is the evidence-ledger (vocab predicate
// evidence.run, G5). This package defines the schema; the writer and the
// journey-honesty rule land with the evidence-ledger capability.
package ledger

import (
	"fmt"
	"slices"
)

// Status is a run's honest outcome. Only pass is green evidence; exploratory,
// diagnostic, blocked, and skipped are evidence but never green.
type Status string

const (
	StatusPass        Status = "pass"
	StatusExploratory Status = "exploratory"
	StatusDiagnostic  Status = "diagnostic"
	StatusBlocked     Status = "blocked"
	StatusSkipped     Status = "skipped"
)

// Statuses is the fixed status vocabulary. An entry whose status is outside this
// set is rejected by Validate.
var Statuses = []Status{StatusPass, StatusExploratory, StatusDiagnostic, StatusBlocked, StatusSkipped}

// Valid reports whether s is in the fixed vocabulary.
func (s Status) Valid() bool {
	return slices.Contains(Statuses, s)
}

// Kind records how a run was driven. Only real-llm runs can be real-LLM product
// evidence; mock and fixture-seeded runs are bridge proof (G7, T8).
type Kind string

const (
	KindRealLLM       Kind = "real-llm"
	KindMock          Kind = "mock"
	KindFixtureSeeded Kind = "fixture-seeded"
)

// Kinds is the fixed evidence-kind vocabulary.
var Kinds = []Kind{KindRealLLM, KindMock, KindFixtureSeeded}

// Valid reports whether k is a known evidence kind.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds, k)
}

// IsBridgeProof reports whether a run of this kind is bridge proof — a mock or
// fixture-seeded run that must never be counted as real-LLM evidence.
func (k Kind) IsBridgeProof() bool {
	return k == KindMock || k == KindFixtureSeeded
}

// Entry is one recorded run in the evidence ledger.
type Entry struct {
	RunID       string `json:"run_id"`
	Kind        Kind   `json:"kind"`
	Status      Status `json:"status"`
	Verified    bool   `json:"verified"`               // did independent clean-room verification pass (G4)?
	Reason      string `json:"reason,omitempty"`       // required for every non-pass status
	EvidenceRef string `json:"evidence_ref,omitempty"` // artifact/trajectory reference backing a pass
}

// Validate checks an entry against the ledger schema, fail-closed. It rejects an
// out-of-vocabulary status or kind, refuses to record pass for a run that failed
// (or never ran) independent verification (G7), and requires a reason for every
// non-pass status so the ledger cannot carry an unexplained failure.
func (e Entry) Validate() error {
	if e.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if !e.Kind.Valid() {
		return fmt.Errorf("unknown evidence kind %q; must be one of %v", e.Kind, Kinds)
	}
	if !e.Status.Valid() {
		return fmt.Errorf("unknown status %q; must be one of %v", e.Status, Statuses)
	}
	if e.Status == StatusPass {
		if !e.Verified {
			return fmt.Errorf("status %q requires independent verification to have passed (G7): a run whose artifact fails verification is never pass", StatusPass)
		}
		if e.EvidenceRef == "" {
			return fmt.Errorf("status %q requires an evidence_ref", StatusPass)
		}
		return nil
	}
	if e.Reason == "" {
		return fmt.Errorf("status %q requires a reason", e.Status)
	}
	return nil
}

// CountsAsPass reports whether an entry is green evidence: a verified pass with a
// backing reference. Callers should still run Validate and handle its error.
func (e Entry) CountsAsPass() bool {
	return e.Status == StatusPass && e.Verified && e.EvidenceRef != ""
}

// CountsAsRealLLMEvidence reports whether an entry is real-LLM product evidence.
// A mock or fixture-seeded run — bridge proof — never qualifies, even when it is
// a verified pass (G7).
func (e Entry) CountsAsRealLLMEvidence() bool {
	return e.Kind == KindRealLLM && e.CountsAsPass()
}
