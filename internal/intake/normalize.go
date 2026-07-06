package intake

import (
	"encoding/json"
	"fmt"
	"strings"

	githubwebhook "github.com/c360studio/semstreams/input/github-webhook"
)

// Subjects the framework github_webhook input publishes to the GITHUB stream.
const (
	SubjectIssue   = "github.event.issue"
	SubjectPR      = "github.event.pr"
	SubjectComment = "github.event.comment"
	SubjectReview  = "github.event.review"
)

// Intake is the normalized, host-neutral result of decoding one webhook event —
// the admission Event plus the run's issue reference. It is what the intake
// adapter feeds to Decide and (on admission) stamps + spawns from; the arc never
// sees the host payload.
type Intake struct {
	// Event is the host-neutral admission input (actor, opt-in signals bound to
	// that actor per the Event invariant).
	Event Event
	// IssueRef is the host-neutral reference to the work item, "owner/repo#number".
	// The arc treats it as an OPAQUE handle — only forge-io adapters (PR delivery,
	// github_add_comment) parse owner/repo/number back out; a neutral rule/lifecycle
	// that split it would leak the host shape (host-neutrality, 5.8).
	IssueRef string
	// Relevant is false for an event that is not an M0 intake trigger (see
	// Normalize); the adapter skips it without decision or stamp.
	Relevant bool
}

// Normalize decodes a github.event.* payload (the bare, FLATTENED shape the
// framework input publishes) into a host-neutral Intake. It honors the Event
// invariant — it extracts an opt-in signal ONLY when that signal is safely
// attributable to the event's actor.
//
// M0 SCOPE: intake triggers on issue `opened` only, the one flow the flattened
// payload fully supports (actor == opener, initial labels + body are the opener's,
// issue number present). Two flattened-payload gaps push the other flows to a
// later increment and are recorded as upstream asks (design D13):
//   - a `labeled` action's SPECIFIC added label is dropped (only aggregate Labels
//     remain), so a "labeled after open" opt-in cannot be attributed to the
//     labeler — the aggregate is deliberately NOT treated as the actor's signal.
//   - a comment event drops the issue number/URL, so a `/semdev` comment or a human
//     reply cannot be tied to its run — the respond flow waits on that field.
//
// Until those land, a non-`opened` issue action carries no opt-in signal (creates
// no run), and comment/PR/review events are not intake triggers.
func Normalize(subject string, payload []byte) (*Intake, error) {
	if subject != SubjectIssue {
		return &Intake{Relevant: false}, nil
	}
	var e githubwebhook.IssueEvent
	if err := json.Unmarshal(payload, &e); err != nil {
		return nil, fmt.Errorf("intake: decode issue event: %w", err)
	}
	return normalizeIssue(e), nil
}

func normalizeIssue(e githubwebhook.IssueEvent) *Intake {
	in := &Intake{
		Relevant: true,
		IssueRef: fmt.Sprintf("%s#%d", e.Repository.FullName, e.Issue.Number),
		Event: Event{
			Actor: e.Sender,
			Owner: e.Repository.Owner,
			Repo:  e.Repository.Name,
		},
	}
	// Extract the opt-in signal ONLY for `opened` AND only when the event's sender
	// is the issue's author. On a normal issues.opened the sender IS the opener,
	// who applied the initial labels and authored the body — the sole issue action
	// whose opt-in is safely attributable to the actor from the flattened payload.
	// The sender==author guard makes "authored by THIS actor" robust rather than
	// assumed: any host/edge where an opened event's sender differs from the author
	// yields no attributable signal (defense in depth at the security door). A
	// `labeled` action's aggregate Labels are likewise NOT the labeler's specific
	// signal (the added label is dropped upstream), so they are ignored — otherwise
	// an authorized actor's unrelated event would inherit a foreign actor's `semdev`
	// label and be wrongly admitted (the Event invariant).
	if e.Action == "opened" && strings.EqualFold(strings.TrimSpace(e.Sender), strings.TrimSpace(e.Issue.Author)) {
		in.Event.AppliedLabels = e.Issue.Labels
		in.Event.AuthoredText = e.Issue.Body
	}
	return in
}
