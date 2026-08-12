package ledger

import "testing"

// The schema must reject a status outside the fixed vocabulary (G7).
func TestValidateRejectsOutOfVocabularyStatus(t *testing.T) {
	e := Entry{RunID: "r1", Kind: KindMock, Status: Status("green"), Reason: "x"}
	if err := e.Validate(); err == nil {
		t.Fatal("Validate accepted an out-of-vocabulary status; G7 schema does not fire")
	}
}

// G7: a run whose artifact failed (or never ran) independent verification is
// never recorded as pass.
func TestValidateRejectsUnverifiedPass(t *testing.T) {
	e := Entry{RunID: "r1", Kind: KindRealLLM, Status: StatusPass, Verified: false, EvidenceRef: "artifact://x"}
	if err := e.Validate(); err == nil {
		t.Fatal("Validate accepted an unverified pass; G7 pin does not fire")
	}
}

// A pass with no backing evidence reference is not a recordable pass.
func TestValidateRejectsPassWithoutEvidence(t *testing.T) {
	e := Entry{RunID: "r1", Kind: KindRealLLM, Status: StatusPass, Verified: true}
	if err := e.Validate(); err == nil {
		t.Fatal("Validate accepted a pass with no evidence_ref")
	}
}

// Every non-pass status must carry a reason, so the ledger cannot hold an
// unexplained failure.
func TestValidateRequiresReasonForNonPass(t *testing.T) {
	for _, s := range []Status{StatusExploratory, StatusDiagnostic, StatusBlocked, StatusSkipped} {
		e := Entry{RunID: "r1", Kind: KindMock, Status: s}
		if err := e.Validate(); err == nil {
			t.Errorf("Validate accepted status %q with no reason", s)
		}
	}
}

// A well-formed verified real-LLM pass validates and counts as real-LLM evidence.
func TestValidVerifiedRealLLMPass(t *testing.T) {
	e := Entry{RunID: "r1", Kind: KindRealLLM, Status: StatusPass, Verified: true, EvidenceRef: "trajectory://r1"}
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate rejected a well-formed verified pass: %v", err)
	}
	if !e.CountsAsPass() {
		t.Error("verified pass with evidence does not CountsAsPass")
	}
	if !e.CountsAsRealLLMEvidence() {
		t.Error("verified real-LLM pass does not count as real-LLM evidence")
	}
}

// A mock run is bridge proof: even a verified pass never counts as real-LLM
// evidence (G7, T8).
func TestMockRunIsBridgeProofNotRealEvidence(t *testing.T) {
	e := Entry{RunID: "r1", Kind: KindMock, Status: StatusPass, Verified: true, EvidenceRef: "trajectory://r1"}
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate rejected a valid mock pass: %v", err)
	}
	if !e.Kind.IsBridgeProof() {
		t.Error("mock run is not labeled bridge proof")
	}
	if e.CountsAsRealLLMEvidence() {
		t.Error("mock run counted as real-LLM evidence; bridge-proof discipline (G7) does not hold")
	}
	if !e.CountsAsPass() {
		t.Error("a verified mock pass should still CountsAsPass as bridge evidence")
	}
}

// An unknown evidence kind is rejected.
func TestValidateRejectsUnknownKind(t *testing.T) {
	e := Entry{RunID: "r1", Kind: Kind("gpt"), Status: StatusBlocked, Reason: "x"}
	if err := e.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown evidence kind")
	}
}
