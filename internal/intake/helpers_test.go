package intake

import (
	"context"

	"github.com/c360studio/semdev/internal/intake/admission"
)

// Shared fakes for the package-intake tests (normalize / assess / component).

// fakeChecker scripts a permission level (and records whether it was called, to
// prove the allowlist path skips the network).
type fakeChecker struct {
	level string
	err   error
	calls int
}

func (f *fakeChecker) Permission(_ context.Context, _, _, _ string) (string, error) {
	f.calls++
	return f.level, f.err
}

// cfg is the standard admission policy the intake tests decide under.
func cfg() admission.Config {
	return admission.Config{Allowlist: []string{"trusted-bot"}, OptInLabel: "semdev", OptInCommand: "/semdev"}
}

// fakeResolver seeds the redelivery discriminator's run lookup ("" = no run yet).
type fakeResolver struct {
	runID    string
	decision string
	phase    string
	err      error
}

func (f *fakeResolver) ResolveRunByRef(context.Context, string) (admission.RunState, error) {
	return admission.RunState{EntityID: f.runID, Decision: f.decision, Phase: f.phase}, f.err
}
