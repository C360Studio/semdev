package boot

import (
	"strings"
	"testing"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semdev/internal/graphown"
)

// TestGraphClientConstructionRequiresDeclaredVocabulary is the behavioral half of the
// launch-lane fix: it pins the framework coupling that makes declaredGraphClients'
// ordering load-bearing, so a future reader cannot mistake the helper for style.
//
// On a cleared registry the raw construction MUST fail (contract validation calls
// vocabulary.RequireDeclaredPredicate on every contract predicate) and the helper MUST
// succeed, because it declares first. If semstreams ever relaxes contract validation,
// this test goes red and the helper's rationale needs re-reading — that is the point.
func TestGraphClientConstructionRequiresDeclaredVocabulary(t *testing.T) {
	t.Cleanup(vocabulary.SnapshotRegistry())
	vocabulary.ClearRegistry()

	// A non-nil client is all construction needs: contract validation is purely local
	// (ADR-091 — contracts validate local intent; nothing registers or leases), so this
	// pin needs no NATS connection.
	nc := &natsclient.Client{}

	if _, err := graphown.NewClients(nc); err == nil {
		t.Fatal("graphown.NewClients succeeded on a cleared vocabulary registry — the coupling " +
			"declaredGraphClients exists to order has disappeared; re-read the helper's rationale")
	} else if !strings.Contains(err.Error(), "not declared in the vocabulary registry") {
		t.Fatalf("construction failed for an unexpected reason, so this pin is no longer watching "+
			"the undeclared-predicate class: %v", err)
	}

	if _, err := declaredGraphClients(nc); err != nil {
		t.Fatalf("declaredGraphClients failed on a cleared registry — it must declare the "+
			"vocabulary before building the client: %v", err)
	}
}
