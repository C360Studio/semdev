package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	parkPostSubject       = "semdev.park-post.request"
	parkPostInterfaceType = "semdev.park_post_request"
	parkPostVersion       = "v1"
)

type parkPortConfig struct {
	Kind       string   `json:"kind"`
	StreamName string   `json:"stream_name"`
	Subjects   []string `json:"subjects"`
	Interface  struct {
		Type    string `json:"type"`
		Version string `json:"version"`
	} `json:"interface"`
}

type parkPort struct {
	Name     string         `json:"name"`
	Required bool           `json:"required"`
	Config   parkPortConfig `json:"config"`
}

type parkConfig struct {
	Streams    map[string]streamDecl `json:"streams"`
	Components map[string]struct {
		Config struct {
			Ports struct {
				Inputs  []parkPort `json:"inputs"`
				Outputs []parkPort `json:"outputs"`
			} `json:"ports"`
		} `json:"config"`
	} `json:"components"`
}

func loadParkConfig(t *testing.T, name string) parkConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "configs", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var cfg parkConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return cfg
}

func findParkPort(ports []parkPort, name string) (parkPort, bool) {
	for _, port := range ports {
		if port.Name == name {
			return port, true
		}
	}
	return parkPort{}, false
}

func assertParkPort(t *testing.T, file, owner, direction string, ports []parkPort) {
	t.Helper()
	port, ok := findParkPort(ports, "park_post_requests")
	if !ok {
		t.Fatalf("%s %s has no park_post_requests %s port", file, owner, direction)
	}
	if !port.Required {
		t.Errorf("%s %s park_post_requests port must be required", file, owner)
	}
	if port.Config.Kind != "jetstream" || port.Config.StreamName != "USER" ||
		!slices.Equal(port.Config.Subjects, []string{parkPostSubject}) {
		t.Errorf("%s %s park port = kind %q stream %q subjects %v, want exact JetStream USER/%s",
			file, owner, port.Config.Kind, port.Config.StreamName, port.Config.Subjects, parkPostSubject)
	}
	if port.Config.Interface.Type != parkPostInterfaceType || port.Config.Interface.Version != parkPostVersion {
		t.Errorf("%s %s park interface = %s/%s, want %s/%s", file, owner,
			port.Config.Interface.Type, port.Config.Interface.Version, parkPostInterfaceType, parkPostVersion)
	}
}

// TestParkPostPortsAreExactAndTyped pins both ends of the product-owned request
// lane. A park post is a queued side effect, so the rule produces and the
// conversation channel consumes one exact JetStream subject under one named raw
// wire contract. user.response.> remains reserved for framework-owned typed
// UserResponse messages and is never a semdev park alias.
func TestParkPostPortsAreExactAndTyped(t *testing.T) {
	for _, file := range []string{"semdev-bootstrap.json", "semdev-live-gemini.json"} {
		cfg := loadParkConfig(t, file)
		user, ok := cfg.Streams["USER"]
		if !ok || !slices.Contains(user.Subjects, parkPostSubject) {
			t.Errorf("%s USER stream subjects = %v, want explicit %q capture", file, user.Subjects, parkPostSubject)
		}
		rule := cfg.Components["rule"].Config.Ports
		assertParkPort(t, file, "rule", "output", rule.Outputs)
		conversation := cfg.Components["conversation-channel"].Config.Ports
		assertParkPort(t, file, "conversation-channel", "input", conversation.Inputs)
	}
}

func authorsPark(action ruleAction) bool {
	if action.Predicate != "run.awaiting.human" {
		return false
	}
	switch action.Type {
	case "add_triple", "update_triple":
		return true
	case "reconcile_predicates":
		// An empty desired object clears the selected predicate; it does not
		// author park state. A non-empty reconcile is an alternate park writer.
		return action.Object != ""
	default:
		return false
	}
}

// TestEveryParkRuleUsesTheOwnedExactRequestSubject discovers park producers by
// behavior rather than a fixed ID allowlist. Each action phase is an independent
// execution path: whenever on_enter, on_exit, while_true, or on_recovery authors
// run.awaiting.human through add/update/non-empty reconcile, that same phase must
// emit exactly one request on semdev.park-post.request. A publish in another phase
// cannot satisfy the contract because that phase may never execute.
func TestEveryParkRuleUsesTheOwnedExactRequestSubject(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	parkPhases := 0
	for _, r := range rules {
		phases := []struct {
			name    string
			actions []ruleAction
		}{
			{name: "on_enter", actions: r.OnEnter},
			{name: "on_exit", actions: r.OnExit},
			{name: "while_true", actions: r.WhileTrue},
			{name: "on_recovery", actions: r.OnRecovery},
		}
		for _, phase := range phases {
			writesPark := false
			for _, action := range phase.actions {
				if authorsPark(action) {
					writesPark = true
					break
				}
			}
			if !writesPark {
				continue
			}
			parkPhases++

			publishes := 0
			for _, action := range phase.actions {
				if action.Type != "publish" {
					continue
				}
				publishes++
				if action.Subject != parkPostSubject {
					t.Errorf("park rule %s phase %s (%s) publishes %q, want exact %q",
						r.ID, phase.name, r.path, action.Subject, parkPostSubject)
				}
			}
			if publishes != 1 {
				t.Errorf("park rule %s phase %s (%s) has %d publish actions, want exactly 1 (no omitted, cross-phase, or dual publish)",
					r.ID, phase.name, r.path, publishes)
			}
		}
	}
	if parkPhases == 0 {
		t.Fatal("found no rule phase that authors run.awaiting.human; park census is vacuous")
	}
}
