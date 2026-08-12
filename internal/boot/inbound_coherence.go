package boot

import (
	"encoding/json"

	"github.com/c360studio/semdev/internal/conversationchannel"
	"github.com/c360studio/semdev/internal/intake"
	"github.com/c360studio/semstreams/config"
)

// inboundApprovalReachable reports whether the assembled bootstrap leaves the
// change-approval comment lane reachable (pull-first-transport H1): EITHER
// issue-intake's webhook receiver is on (http_port > 0, a webhook can deliver
// comment events onto the GITHUB stream) OR conversation-channel's poll transport
// is enabled (the poller reads the thread). Both off is the SILENT
// dead-approval-lane combo — every run parks at awaiting_approval and no /semdev
// approve can ever land, because nothing feeds the inbound comment lane.
//
// The two knobs live in DIFFERENT component blocks (issue-intake owns the receiver,
// conversation-channel owns the poll), so neither component can detect the combo
// alone — only boot, which assembles both, can. boot LOUD-WARNS rather than failing
// closed: a test harness or an external publisher can still put comment events on
// the GITHUB stream directly (the shipped bootstrap + every e2e journey do exactly
// that — http_port 0, poll off, flattened events published straight to the stream),
// so a hard fail would break the legitimate direct-publish path. reachable drives
// whether boot warns; detail is the warning text.
func inboundApprovalReachable(cfg *config.Config) (reachable bool, detail string) {
	receiverOn := false
	pollOn := false
	for _, comp := range cfg.Components {
		switch comp.Name {
		case intake.ComponentName:
			var ic struct {
				HTTPPort int `json:"http_port"`
			}
			_ = json.Unmarshal(comp.Config, &ic)
			if ic.HTTPPort > 0 {
				receiverOn = true
			}
		case conversationchannel.ComponentName:
			var cc struct {
				Poll *struct {
					Enabled bool `json:"enabled"`
				} `json:"poll"`
			}
			_ = json.Unmarshal(comp.Config, &cc)
			if cc.Poll != nil && cc.Poll.Enabled {
				pollOn = true
			}
		}
	}
	if receiverOn || pollOn {
		return true, ""
	}
	return false, "issue-intake http_port==0 (no webhook receiver) AND conversation-channel poll.enabled==false: " +
		"the /semdev approve gate is unreachable — a run parks at awaiting_approval and never resumes unless comment " +
		"events are published to the GITHUB stream directly. Set issue-intake http_port>0 (webhook deploy) or " +
		"conversation-channel poll.enabled=true (pull-first deploy)."
}
