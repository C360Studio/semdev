package graphown

import (
	"errors"
	"testing"
)

// TestInProcessClaimLedger pins the bind-guard mechanics directly — the offline
// pin the docker suite otherwise exercises only implicitly (sequential boots per
// journey binary going green). BindOwners cannot drive the live-claim rejection
// without a real NATS connection (its nil-client check runs BEFORE the claim is
// taken), so the ledger is pinned at the claim/release layer it lives in. If
// claimInProcess is ever no-op'd or the release moves to the wrong place, this
// goes RED offline — instead of the destructive-second-bind hazard (ADR-056:
// silent token invalidation) returning invisibly.
func TestInProcessClaimLedger(t *testing.T) {
	ResetInProcessBindingsForTest()
	t.Cleanup(ResetInProcessBindingsForTest)

	if err := claimInProcess([]string{"owner-a", "owner-b"}); err != nil {
		t.Fatalf("first claim must succeed: %v", err)
	}

	// A claim overlapping a LIVE claim fails with the sentinel — the
	// destructive-second-bind hazard the guard exists for.
	if err := claimInProcess([]string{"owner-b"}); !errors.Is(err, ErrOwnersAlreadyBoundInProcess) {
		t.Fatalf("overlapping claim must fail with ErrOwnersAlreadyBoundInProcess, got: %v", err)
	}

	// A failed claim must be ATOMIC: the fresh owner in a mixed claim must not be
	// left claimed, or one conflict would poison unrelated owners for the process.
	if err := claimInProcess([]string{"owner-b", "owner-c"}); !errors.Is(err, ErrOwnersAlreadyBoundInProcess) {
		t.Fatalf("mixed claim must fail on the duplicate, got: %v", err)
	}
	if err := claimInProcess([]string{"owner-c"}); err != nil {
		t.Fatalf("a failed mixed claim leaked a partial claim on the fresh owner: %v", err)
	}

	// Release enables the legitimate sequential case — one runtime stops, the next
	// boots in the same process. Releasing something never claimed is a no-op.
	ReleaseOwners("owner-a", "owner-b", "owner-c", "never-claimed")
	if err := claimInProcess([]string{"owner-a", "owner-b"}); err != nil {
		t.Fatalf("re-claim after release must succeed: %v", err)
	}
}
