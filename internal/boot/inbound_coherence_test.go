package boot

import (
	"encoding/json"
	"testing"

	"github.com/c360studio/semdev/internal/conversationchannel"
	"github.com/c360studio/semdev/internal/intake"
	"github.com/c360studio/semstreams/config"
	"github.com/c360studio/semstreams/types"
)

// TestInboundApprovalReachable pins the boot coherence guard (pull-first-transport
// H1): the /semdev approve lane is reachable iff issue-intake's webhook receiver is
// on (http_port>0) OR conversation-channel's poll is enabled. Both off is the
// silent dead-approval-lane combo the guard must flag (it loud-warns, not
// fail-closed — a harness can still publish comment events to the stream directly).
func TestInboundApprovalReachable(t *testing.T) {
	comp := func(name string, cfg map[string]any) types.ComponentConfig {
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("marshal %s config: %v", name, err)
		}
		return types.ComponentConfig{Name: name, Enabled: true, Config: raw}
	}
	build := func(httpPort int, pollEnabled bool) *config.Config {
		return &config.Config{Components: config.ComponentConfigs{
			"issue-intake":         comp(intake.ComponentName, map[string]any{"http_port": httpPort}),
			"conversation-channel": comp(conversationchannel.ComponentName, map[string]any{"poll": map[string]any{"enabled": pollEnabled}}),
		}}
	}

	cases := []struct {
		name          string
		httpPort      int
		pollEnabled   bool
		wantReachable bool
	}{
		{"receiver on, poll off (webhook deploy)", 8080, false, true},
		{"receiver off, poll on (pull-first deploy)", 0, true, true},
		{"receiver on, poll on (both — belt and suspenders)", 8080, true, true},
		{"receiver off, poll off (the dead-lane combo)", 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reachable, detail := inboundApprovalReachable(build(c.httpPort, c.pollEnabled))
			if reachable != c.wantReachable {
				t.Errorf("inboundApprovalReachable = %v, want %v", reachable, c.wantReachable)
			}
			if !reachable && detail == "" {
				t.Error("an unreachable lane must carry a non-empty warning detail")
			}
			if reachable && detail != "" {
				t.Errorf("a reachable lane must carry no detail, got %q", detail)
			}
		})
	}
}
