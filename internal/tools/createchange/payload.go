package createchange

import (
	"strings"

	"github.com/c360studio/semdev/internal/openspec"
)

// payload is the authored change content the model supplies. It mirrors the
// content half of the OpenSpec model (proposal / spec deltas / tasks) so it maps
// cleanly onto openspec.Change; it carries NO outcome or status field (G3).
type payload struct {
	Slug     string          `json:"slug"`
	Proposal proposalPayload `json:"proposal"`
	Deltas   []deltaPayload  `json:"deltas"`
	Tasks    []taskSection   `json:"tasks"`
}

type proposalPayload struct {
	Intent   string   `json:"intent"`
	ScopeIn  []string `json:"scope_in"`
	ScopeOut []string `json:"scope_out"`
	Approach string   `json:"approach"`
}

type deltaPayload struct {
	Capability string       `json:"capability"`
	Added      []reqPayload `json:"added"`
	Modified   []modPayload `json:"modified"`
	Removed    []remPayload `json:"removed"`
}

type reqPayload struct {
	Name      string            `json:"name"`
	Statement string            `json:"statement"`
	Scenarios []scenarioPayload `json:"scenarios"`
}

type modPayload struct {
	reqPayload
	Previously string `json:"previously"`
}

type remPayload struct {
	Name      string `json:"name"`
	Rationale string `json:"rationale"`
}

type scenarioPayload struct {
	Name  string        `json:"name"`
	Steps []stepPayload `json:"steps"`
}

type stepPayload struct {
	Keyword string `json:"kw"`
	Text    string `json:"text"`
}

type taskSection struct {
	Name  string        `json:"section"`
	Items []taskPayload `json:"items"`
}

type taskPayload struct {
	Number string `json:"number"`
	Text   string `json:"text"`
	// Execution-rich, GRAPH-ONLY fields the dev-from-task projector reads (they are
	// not represented in tasks.md; see internal/openspec.model_change). They mirror
	// devtask.RawTask INCLUDING its presence semantics: a nil slice or nil Budget
	// means the field was NOT authored — the projector reads it as a gap and fails
	// toward the human — whereas an authored-empty assumptions/non_goals list is a
	// real value the projector accepts. encoding/json preserves this: an absent key
	// decodes to nil, an explicit [] to a non-nil empty slice. create_change records
	// what was authored; it does NOT validate the Karpathy schema (the projector
	// owns gap detection — division of labor, design D14).
	TargetFiles []string `json:"target_files"`
	TestCommand string   `json:"test_command"`
	Assumptions []string `json:"assumptions"`
	NonGoals    []string `json:"non_goals"`
	Budget      *int     `json:"budget"`
	// No Done / outcome field: an authored change's task completion is never
	// model-supplied (dev-from-task derives status from execution markers). A `done`
	// or any outcome-shaped field in the raw arguments is silently ignored (G3), and
	// toChange stamps every task not-done.
}

// richTask is one task's execution-rich, graph-only fields paired with the flat
// index it was assigned AS IT ENTERED change.Tasks — so the rich facts key by the
// exact same <i> the format engine derives when it walks change.Tasks, with no
// separate flatten that could drift.
type richTask struct {
	Index       int
	TargetFiles []string
	TestCommand string
	Assumptions []string
	NonGoals    []string
	Budget      *int
}

// toChange maps the authored payload onto the format engine's Change model (whose
// Facts() produces the openspec.change.* predicates) AND returns the rich per-task
// fields indexed by the flat position each task takes IN change.Tasks. Both are
// produced by this one walk: the flat index is assigned as each task is appended to
// change.Tasks, so it equals the format engine's per-task index by construction —
// any future filter/dedup/reorder here moves the thin append and the rich index
// together, and the two can never drift. There is no order-preserving invariant to
// remember.
func (p *payload) toChange() (*openspec.Change, []richTask) {
	c := &openspec.Change{Slug: p.Slug}

	if pr := p.Proposal; pr.Intent != "" || len(pr.ScopeIn) > 0 || len(pr.ScopeOut) > 0 || pr.Approach != "" {
		c.Proposal = &openspec.Proposal{
			Intent:   pr.Intent,
			ScopeIn:  pr.ScopeIn,
			ScopeOut: pr.ScopeOut,
			Approach: pr.Approach,
		}
	}

	for _, d := range p.Deltas {
		delta := openspec.Delta{Capability: d.Capability}
		for _, r := range d.Added {
			delta.Added = append(delta.Added, r.toRequirement())
		}
		for _, m := range d.Modified {
			delta.Modified = append(delta.Modified, openspec.ModifiedRequirement{
				Requirement: m.reqPayload.toRequirement(),
				Previously:  m.Previously,
			})
		}
		for _, rm := range d.Removed {
			delta.Removed = append(delta.Removed, openspec.RemovedRequirement{Name: rm.Name, Rationale: rm.Rationale})
		}
		c.Deltas = append(c.Deltas, delta)
	}

	var rich []richTask
	if len(p.Tasks) > 0 {
		tasks := &openspec.Tasks{}
		i := 0
		for _, s := range p.Tasks {
			section := openspec.TaskSection{Name: s.Name}
			for _, it := range s.Items {
				// Done is deliberately not carried from the payload: an authored
				// task is always not-done (status is derived, not authored).
				section.Tasks = append(section.Tasks, openspec.Task{Number: it.Number, Text: it.Text})
				// Capture the rich fields at the SAME point (and index) the thin task
				// enters change.Tasks — i counts appended tasks, so it is the format
				// engine's per-task index by construction.
				rich = append(rich, richTask{
					Index:       i,
					TargetFiles: it.TargetFiles,
					TestCommand: it.TestCommand,
					Assumptions: it.Assumptions,
					NonGoals:    it.NonGoals,
					Budget:      it.Budget,
				})
				i++
			}
			tasks.Sections = append(tasks.Sections, section)
		}
		c.Tasks = tasks
	}

	return c, rich
}

func (r reqPayload) toRequirement() openspec.Requirement {
	req := openspec.Requirement{Name: r.Name, Statement: r.Statement}
	for _, s := range r.Scenarios {
		sc := openspec.Scenario{Name: s.Name}
		for _, st := range s.Steps {
			sc.Steps = append(sc.Steps, openspec.Step{Keyword: strings.ToUpper(strings.TrimSpace(st.Keyword)), Text: st.Text})
		}
		req.Scenarios = append(req.Scenarios, sc)
	}
	return req
}
