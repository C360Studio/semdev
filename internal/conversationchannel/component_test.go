package conversationchannel

import (
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/intake/admission"
)

// TestPollConfigXORsTheWebhookConsumer pins B-1: in poll mode the component wires
// the POLLER for inbound comments and SKIPS the webhook comment_events consumer (a
// comment is never double-processed); in webhook mode the comment consumer runs. The
// park-post consumer (user.response) runs in BOTH modes.
func TestPollConfigXORsTheWebhookConsumer(t *testing.T) {
	hasSubject := func(ports []struct {
		name, subject string
	}, subject string) bool {
		for _, p := range ports {
			if p.subject == subject {
				return true
			}
		}
		return false
	}
	subjectsOf := func(c *Component) []struct{ name, subject string } {
		var out []struct{ name, subject string }
		for _, p := range c.activeConsumerPorts() {
			out = append(out, struct{ name, subject string }{p.Name, p.Subject})
		}
		return out
	}

	// Webhook mode (poll absent): the comment consumer + the park consumer both wire.
	webhook := &Component{config: ComponentConfig{Ports: DefaultPorts()}}
	wPorts := subjectsOf(webhook)
	if !hasSubject(wPorts, admission.SubjectComment) {
		t.Errorf("webhook mode must wire the %q comment consumer, got %v", admission.SubjectComment, wPorts)
	}
	if !hasSubject(wPorts, UserResponseSubject) {
		t.Errorf("webhook mode must wire the park-post %q consumer, got %v", UserResponseSubject, wPorts)
	}

	// Poll mode: the comment consumer is SKIPPED (the poller owns it), the park
	// consumer still runs.
	poll := &Component{config: ComponentConfig{Ports: DefaultPorts(), Poll: &PollConfig{Enabled: true}}}
	pPorts := subjectsOf(poll)
	if hasSubject(pPorts, admission.SubjectComment) {
		t.Errorf("poll mode must SKIP the webhook %q consumer (XOR); got %v", admission.SubjectComment, pPorts)
	}
	if !hasSubject(pPorts, UserResponseSubject) {
		t.Errorf("poll mode must still wire the park-post %q consumer, got %v", UserResponseSubject, pPorts)
	}
}

// TestPollIntervalDefaultsAndFloor pins the cadence contract (M4): an unset interval
// defaults to 15s; a positive interval below the 5s floor is clamped up (a mis-set
// tight loop would hammer ListComments into rate-limit exhaustion).
func TestPollIntervalDefaultsAndFloor(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 15 * time.Second},   // unset → default
		{"3s", 5 * time.Second},  // sub-floor → clamped
		{"5s", 5 * time.Second},  // at floor
		{"30s", 30 * time.Second}, // above floor → as-authored
	}
	for _, c := range cases {
		cfg := ComponentConfig{Ports: DefaultPorts(), Poll: &PollConfig{Enabled: true, Interval: c.in}}
		applyConfigDefaults(&cfg)
		if got := cfg.pollInterval(); got != c.want {
			t.Errorf("interval %q → %v, want %v", c.in, got, c.want)
		}
	}
}

// TestPollRejectsNonPositiveInterval pins M4's reject half: a non-positive or
// unparseable interval fails Validate (loud), never silently becomes a tight loop.
func TestPollRejectsNonPositiveInterval(t *testing.T) {
	for _, bad := range []string{"0s", "-5s", "garbage"} {
		cfg := ComponentConfig{Ports: DefaultPorts(), Poll: &PollConfig{Enabled: true, Interval: bad}}
		applyConfigDefaults(&cfg) // a non-empty bad value survives defaulting
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate accepted poll.interval %q; want a loud rejection", bad)
		}
	}
	// A valid interval passes.
	ok := ComponentConfig{Ports: DefaultPorts(), Poll: &PollConfig{Enabled: true, Interval: "15s"}}
	applyConfigDefaults(&ok)
	if err := ok.Validate(); err != nil {
		t.Errorf("Validate rejected a valid poll config: %v", err)
	}
}

// TestStopCancelsPoller pins M5: Stop cancels the poll goroutine's context (today's
// Stop only flipped `started`, leaking the goroutine).
func TestStopCancelsPoller(t *testing.T) {
	cancelled := false
	c := &Component{}
	c.started = true
	c.pollCancel = func() { cancelled = true }
	if err := c.Stop(time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !cancelled {
		t.Error("Stop did not cancel the poll goroutine's context (M5 — it would keep polling after shutdown)")
	}
}

// Compile guard: the shared resolver satisfies the poller's enumeration interface.
var _ awaitingRunLister = (*admission.NATSRunResolver)(nil)
