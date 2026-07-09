package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hostNames are code-host identifiers that must never appear in an arc rule's
// predicate positions. The forge-io seam keeps host-awareness in the Go adapter
// (T7); the arc reacts only to NORMALIZED facts, so a host name in a condition
// field or an action subject/predicate is host coupling that breaks the
// swap-the-adapter contract.
var hostNames = []string{"github", "gitlab", "bitbucket", "gitea"}

// containsHostName returns the host identifier a string embeds (case-insensitive),
// or "" if none.
func containsHostName(s string) string {
	low := strings.ToLower(s)
	for _, h := range hostNames {
		if strings.Contains(low, h) {
			return h
		}
	}
	return ""
}

// hostSpecificRuleRefs returns a violation message for each PREDICATE POSITION in
// which an arc rule names a code host — a condition `field` or an action
// `subject`/`predicate` (the structural positions the host-neutrality requirement
// covers; action `object` values are data, not predicates, and are not scanned).
// Empty when every rule is host-neutral. Pure over the parsed rules so the pin can
// be exercised with synthetic input.
func hostSpecificRuleRefs(rules []ruleFile) []string {
	var out []string
	for _, r := range rules {
		for _, c := range r.Conditions {
			if h := containsHostName(c.Field); h != "" {
				out = append(out, fmt.Sprintf("rule %q condition field %q names host %q", r.ID, c.Field, h))
			}
		}
		actions := append(append([]ruleAction{}, r.OnEnter...), r.OnExit...)
		for _, a := range actions {
			if h := containsHostName(a.Subject); h != "" {
				out = append(out, fmt.Sprintf("rule %q action subject %q names host %q", r.ID, a.Subject, h))
			}
			if h := containsHostName(a.Predicate); h != "" {
				out = append(out, fmt.Sprintf("rule %q action predicate %q names host %q", r.ID, a.Predicate, h))
			}
			// A rule that dispatches a host-named tool (e.g. github_list_comments)
			// names a code host in its arc logic — the exact swap-the-adapter
			// coupling this pin prevents. (Distinct from the bootstrap allowed_tools
			// allowlist, which is the registration surface, not arc logic.)
			for _, tool := range a.Tools {
				if h := containsHostName(tool); h != "" {
					out = append(out, fmt.Sprintf("rule %q action dispatches host-named tool %q (host %q)", r.ID, tool, h))
				}
			}
		}
	}
	return out
}

// This file holds the shared loaders for semdev's rule packs and bootstrap
// config, used by the run-lifecycle contract pins (taxonomy closure, gate
// ordering, tool allowlist). Rules are validated offline by parsing the JSON —
// no NATS or LLM — the semteams test/contract discipline.

// ruleCondition is one condition in a rule's `conditions` array.
type ruleCondition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
	Required bool   `json:"required"`
}

// ruleAction is one action in a rule's on_enter/on_exit (union of the fields the
// pins inspect).
type ruleAction struct {
	Type       string   `json:"type"`
	Workflow   string   `json:"workflow"`
	Phase      string   `json:"phase"`
	Subject    string   `json:"subject"`
	Predicate  string   `json:"predicate"`
	Object     string   `json:"object"`
	Tools      []string `json:"tools"`
	RunScope   string   `json:"run_scope"`
	ToolChoice struct {
		Mode         string `json:"mode"`
		FunctionName string `json:"function_name"`
	} `json:"tool_choice"`
}

// ruleFile is a single expression rule.
type ruleFile struct {
	path       string
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Enabled    bool            `json:"enabled"`
	Conditions []ruleCondition `json:"conditions"`
	Logic      string          `json:"logic"`
	OnEnter    []ruleAction    `json:"on_enter"`
	OnExit     []ruleAction    `json:"on_exit"`
}

// loadRules parses every *.json rule under configs/rules/ (recursively) at the
// repo root.
func loadRules(root string) ([]ruleFile, error) {
	var out []ruleFile
	rulesDir := filepath.Join(root, "configs", "rules")
	err := filepath.WalkDir(rulesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var r ruleFile
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		r.path = path
		out = append(out, r)
		return nil
	})
	return out, err
}

// nextActionValues returns the coordinator.decision.next_action values a rule
// routes on — handling both `eq` (a string) and `in` (an array).
func (r ruleFile) nextActionValues() []string {
	var out []string
	for _, c := range r.Conditions {
		if c.Field != "coordinator.decision.next_action" {
			continue
		}
		switch v := c.Value.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// agenticComponent is the minimal shape a pin reads off an agentic-execution
// component declaration: the factory `name` (which selects the framework
// factory — distinct from the arbitrary map key) and whether it is enabled.
type agenticComponent struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// streamDecl is the minimal shape a pin reads off a JetStream stream declaration.
type streamDecl struct {
	Subjects []string `json:"subjects"`
}

// bootstrapConfig is the minimal shape of configs/semdev-bootstrap.json the pins
// read.
type bootstrapConfig struct {
	Version    string                `json:"version"`
	Streams    map[string]streamDecl `json:"streams"`
	Components struct {
		Rule struct {
			Config struct {
				RulesFiles []string `json:"rules_files"`
			} `json:"config"`
		} `json:"rule"`
		AgenticTools struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			Config  struct {
				AllowedTools []string `json:"allowed_tools"`
			} `json:"config"`
		} `json:"agentic-tools"`
		AgenticModel    agenticComponent `json:"agentic-model"`
		AgenticLoop     agenticComponent `json:"agentic-loop"`
		AgenticDispatch agenticComponent `json:"agentic-dispatch"`
	} `json:"components"`
}

func loadBootstrap(root string) (bootstrapConfig, error) {
	var cfg bootstrapConfig
	raw, err := os.ReadFile(filepath.Join(root, "configs", "semdev-bootstrap.json"))
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(raw, &cfg)
	return cfg, err
}
