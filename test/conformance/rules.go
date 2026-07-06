package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

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
	Type      string   `json:"type"`
	Workflow  string   `json:"workflow"`
	Phase     string   `json:"phase"`
	Subject   string   `json:"subject"`
	Predicate string   `json:"predicate"`
	Object    string   `json:"object"`
	Tools     []string `json:"tools"`
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

// bootstrapConfig is the minimal shape of configs/semdev-bootstrap.json the pins
// read.
type bootstrapConfig struct {
	Components struct {
		Rule struct {
			Config struct {
				RulesFiles []string `json:"rules_files"`
			} `json:"config"`
		} `json:"rule"`
		AgenticTools struct {
			Config struct {
				AllowedTools []string `json:"allowed_tools"`
			} `json:"config"`
		} `json:"agentic-tools"`
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
