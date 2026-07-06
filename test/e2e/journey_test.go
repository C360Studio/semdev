//go:build e2e

package e2e

import "testing"

// TestSpineJourneyPlaceholder reserves the mock-LLM e2e journey. The full
// issue→PR journey and its pin assertions (G2/G3/G4/G7) land in group 11
// (tasks 11.1–11.4); until then this keeps `task e2e` green rather than failing
// on "no packages to test".
func TestSpineJourneyPlaceholder(t *testing.T) {
	t.Skip("mock-LLM spine journey lands in group 11 (tasks 11.1–11.4)")
}
