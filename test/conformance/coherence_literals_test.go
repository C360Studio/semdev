package conformance

import (
	"testing"

	"github.com/c360studio/semdev/internal/tools/checkcoherence"
	"github.com/c360studio/semdev/internal/tools/submitreview"
	"github.com/c360studio/semdev/internal/tools/validatechange"
	"github.com/c360studio/semdev/internal/tools/verifyartifact"
)

// check_coherence's Decide duplicates a few literals from the sibling tools it rolls up
// (kept dependency-free so the pure decision is offline-pinnable). This cross-check closes
// the drift the decide.go comment flags: an upstream rename (e.g. VerdictApproved) would
// otherwise silently start BLOCKING every coherent run (fail-closed, but a real regression).
func TestCoherenceLiteralsMatchSources(t *testing.T) {
	if checkcoherence.VerifyResultPred != verifyartifact.ResultPredicate {
		t.Errorf("check_coherence reads %q for the verify result but verify_artifact owns %q — drift", checkcoherence.VerifyResultPred, verifyartifact.ResultPredicate)
	}
	if checkcoherence.ValidatedPred != validatechange.ValidatedPredicate {
		t.Errorf("check_coherence reads %q for openspec.validated but validate_change owns %q — drift", checkcoherence.ValidatedPred, validatechange.ValidatedPredicate)
	}
	// The approved-verdict literal the roll-up requires must equal submit_review's.
	if got := checkcoherence.RequiredVerdict(); got != submitreview.VerdictApproved {
		t.Errorf("check_coherence requires verdict %q but submit_review stamps approval as %q — drift (a rename would block every coherent run)", got, submitreview.VerdictApproved)
	}
}
