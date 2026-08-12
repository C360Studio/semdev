package runspace

import (
	"context"
	"fmt"
	"os"
)

// StaticSource resolves EVERY run to one configured source directory — the M0
// posture where semdev develops a single known target (the in-repo Go fixture).
// It is the concrete provision-time Sources seam: provision_sandbox calls Resolve
// to learn WHERE to materialize the run's checkout from (design SB2, group 5).
//
// It ignores runEntityID: at M0 there is one target for every run. The run's
// portable coordinate already lives in the graph as run.issue_ref; the HOST PATH a
// source resolves to is infra state and stays OUT of the graph (like the checkout
// dir — see this package's doc). forge-io's per-run `--recursive` PR clone —
// resolved from that coordinate — lands behind this same seam at M2, at which
// point Resolve reads the run's coordinate and its return contract may flip from a
// host dir to a clone coordinate (the group-5 source-resolution carry-forward).
//
// It fails CLOSED: an unset or non-existent directory errors toward the operator
// (the tool parks), never a CWD guess — a guessed source is the sandbox==nil
// disease both predecessors died on (SB5).
type StaticSource struct {
	// Dir is the absolute path to the run's target source (the fixture at M0).
	Dir string
}

// Resolve returns the configured source directory, verifying it exists. runEntityID
// is ignored at M0 (one target for every run) but kept in the signature so the M2
// forge-io implementation resolves per-run without a provision_sandbox change.
func (s StaticSource) Resolve(_ context.Context, _ string) (string, error) {
	if s.Dir == "" {
		return "", fmt.Errorf("runspace: no sandbox source configured — declare the run's target (park toward the operator)")
	}
	info, err := os.Stat(s.Dir)
	if err != nil {
		return "", fmt.Errorf("runspace: sandbox source %s: %w", s.Dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("runspace: sandbox source %s is not a directory", s.Dir)
	}
	return s.Dir, nil
}
