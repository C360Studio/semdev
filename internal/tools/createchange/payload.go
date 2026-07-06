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
	Done   bool   `json:"done"`
}

// toChange maps the authored payload onto the format engine's Change model, whose
// Facts() then produces the openspec.change.* predicates.
func (p *payload) toChange() *openspec.Change {
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

	if len(p.Tasks) > 0 {
		tasks := &openspec.Tasks{}
		for _, s := range p.Tasks {
			section := openspec.TaskSection{Name: s.Name}
			for _, it := range s.Items {
				section.Tasks = append(section.Tasks, openspec.Task{Number: it.Number, Text: it.Text, Done: it.Done})
			}
			tasks.Sections = append(tasks.Sections, section)
		}
		c.Tasks = tasks
	}

	return c
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
