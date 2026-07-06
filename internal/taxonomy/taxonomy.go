// Package taxonomy is semdev's closed action taxonomy (T1) — the canonical list
// of actions the rule layer consumes. It mirrors the proven OpenSpec lifecycle
// (create_change=new, dev_from_task=apply, verify=clean-room outcome,
// archive_change=archive) wrapped with semdev's front-door, delivery, and HITL
// actions. This Go list is the source of truth; the coordinator persona's
// decision-contract fragment and every rule's action routing are pinned to it,
// and a conformance census fails on drift. No phase enum is introduced — the
// run's position is derived from milestone facts, not a phase field.
package taxonomy

import "slices"

// Action is one member of the closed taxonomy.
type Action string

// The eight actions. issue_intake / open_pr / ask_human / respond are semdev's
// wrapper; the other four are the OpenSpec lifecycle checkpoints.
const (
	IssueIntake   Action = "issue_intake"
	CreateChange  Action = "create_change"
	DevFromTask   Action = "dev_from_task"
	Verify        Action = "verify"
	OpenPR        Action = "open_pr"
	AskHuman      Action = "ask_human"
	Respond       Action = "respond"
	ArchiveChange Action = "archive_change"
)

// Actions is the complete closed taxonomy in arc order. Order is presentational;
// the census treats it as a set.
var Actions = []Action{
	IssueIntake,
	CreateChange,
	DevFromTask,
	Verify,
	OpenPR,
	AskHuman,
	Respond,
	ArchiveChange,
}

// Valid reports whether s is a member of the closed taxonomy — the routability
// check the "out-of-taxonomy action is not routable" pin rests on.
func Valid(s string) bool {
	return slices.Contains(Actions, Action(s))
}

// Names returns the taxonomy as plain strings, in arc order.
func Names() []string {
	out := make([]string, len(Actions))
	for i, a := range Actions {
		out[i] = string(a)
	}
	return out
}
