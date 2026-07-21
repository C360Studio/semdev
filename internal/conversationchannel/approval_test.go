package conversationchannel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/forge/githubwebhook"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/message"
)

// --- fakes ---

type fakeResolver struct {
	runID    string
	decision string
	phase    string
	err      error
}

// ResolveRunByRef reports a gate opened an hour ago for a gated run, so the D12a
// watermark admits a message sent NOW. A fixture wanting the watermark to BITE uses
// fakeGraph with an explicit gateOpenedAt.
func (f *fakeResolver) ResolveRunByRef(context.Context, string) (admission.RunState, error) {
	st := admission.RunState{EntityID: f.runID, Decision: f.decision, Phase: f.phase}
	if st.Phase == admission.PhaseAwaitingApproval {
		st.GateOpenedAt = time.Now().Add(-time.Hour)
	}
	return st, f.err
}

// fakeIntentReader scripts the classified-ledger read handleMessage's NL bridge
// dedups against (conversation.intent.classified). It filters by prefix exactly as
// the production changefacts reader does.
type fakeIntentReader struct {
	facts []message.Triple
	err   error
	reads int
	// readsByPrefix separates WHICH fact a read was for. The exact-command
	// fast-path legitimately reads the DECISION fact (stampDecision's
	// first-decision-wins guard, D13) while still doing zero NL work, so a pin
	// that counted all reads together would either break or go vacuous.
	readsByPrefix map[string]int
}

func (f *fakeIntentReader) ReadFacts(_ context.Context, _ string, prefix string) ([]message.Triple, error) {
	f.reads++
	if f.readsByPrefix == nil {
		f.readsByPrefix = map[string]int{}
	}
	f.readsByPrefix[prefix]++
	if f.err != nil {
		return nil, f.err
	}
	var out []message.Triple
	for _, tr := range f.facts {
		if strings.HasPrefix(tr.Predicate, prefix) {
			out = append(out, tr)
		}
	}
	return out, nil
}

type replacedFacts struct {
	entityID string
	add      []message.Triple
}

type fakeWriter struct {
	calls []replacedFacts
	// err makes every write fail. Used by the B1 pin to drive an exhaustion
	// through the REAL writer path, so the "wrote nothing" assertions stay
	// reachable instead of vacuous.
	err error
}

func (f *fakeWriter) ReplaceTriples(_ context.Context, entityID string, add []message.Triple, _ []string) error {
	f.calls = append(f.calls, replacedFacts{entityID: entityID, add: add})
	return f.err
}

func (f *fakeWriter) ReadOwnedPredicates(context.Context, string, string) ([]string, error) {
	return nil, nil
}

// fakeGraph is a STATEFUL resolver+reader+writer triple: what the adapter writes is
// what a subsequent resolve and read observe. The stateless fakes above cannot express
// the group-8 defects at all — the wedge (8.2) and the watermark (8.1) are both about a
// SECOND message seeing what the FIRST one left behind, so a fake that forgets is a
// vacuous green (the grp5 lesson: assert reachability, never trust a fake that cannot
// fail).
type fakeGraph struct {
	runID string
	phase string
	// gateOpenedAt is the D12a watermark. Zero would mean "watermark unavailable",
	// which fails CLOSED — so fixtures that want a normal gated run must set it.
	gateOpenedAt time.Time
	facts        []message.Triple
	writes       int
}

func (f *fakeGraph) ResolveRunByRef(context.Context, string) (admission.RunState, error) {
	return admission.RunState{
		EntityID:     f.runID,
		Decision:     f.objectOf(admission.DecisionPredicate),
		Phase:        f.phase,
		GateOpenedAt: f.gateOpenedAt,
	}, nil
}

func (f *fakeGraph) ReadFacts(_ context.Context, _ string, prefix string) ([]message.Triple, error) {
	var out []message.Triple
	for _, tr := range f.facts {
		if strings.HasPrefix(tr.Predicate, prefix) {
			out = append(out, tr)
		}
	}
	return out, nil
}

// ReplaceTriples mirrors the graph's replace-by-predicate semantics: a written
// predicate REPLACES any prior triple of that predicate on the entity.
func (f *fakeGraph) ReplaceTriples(_ context.Context, _ string, add []message.Triple, _ []string) error {
	f.writes++
	for _, tr := range add {
		kept := f.facts[:0]
		for _, existing := range f.facts {
			if existing.Predicate != tr.Predicate {
				kept = append(kept, existing)
			}
		}
		f.facts = append(kept, tr)
	}
	return nil
}

func (f *fakeGraph) ReadOwnedPredicates(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func (f *fakeGraph) objectOf(pred string) string {
	for _, tr := range f.facts {
		if tr.Predicate == pred {
			if s, ok := tr.Object.(string); ok {
				return s
			}
		}
	}
	return ""
}

// gateFacts returns every gate-decision triple the run carries — the assertion
// surface for "the gate holds exactly one decision".
func (f *fakeGraph) gateFacts() []message.Triple {
	var out []message.Triple
	for _, tr := range f.facts {
		if strings.HasPrefix(tr.Predicate, "run.change.") {
			out = append(out, tr)
		}
	}
	return out
}

func newTestApprovalOnGraph(g *fakeGraph, checker admission.PermissionChecker) *approvalAdapter {
	cfg := ComponentConfig{Repo: "c360studio/semdev-fixture", Allowlist: []string{"cglusky"}}
	applyConfigDefaults(&cfg)
	return &approvalAdapter{cfg: cfg, checker: checker, resolver: g, writer: g, reader: g, logger: slog.Default()}
}

// TestOppositeCommandsCannotWedgeTheGate is the group-8 8.2 pin (external review #2,
// CONFIRMED). BEFORE the fix, releaseGate consulted ONLY alreadyApproved (which reads
// run.change.approved) and NEVER run.change.rejected, so an authorized `/semdev reject`
// followed by `/semdev approve` on a still-gated run left BOTH gate facts present — after
// which the resume rule (which requires rejected absent) and the cancel rule (which
// requires approved absent) BOTH refuse to fire and the run is wedged at the gate
// FOREVER, with no park, no post, and no operator surface. That cell was previously
// DOCUMENTED as an accepted "unsurfaced stall"; design D13 reverses that call and makes
// the contradictory state unrepresentable via ONE single-valued run.change.decision.
//
// The assertion is deliberately on the CELL SPACE, not on a predicate name: the run must
// carry EXACTLY ONE gate-decision fact no matter which verbs arrive in which order.
func TestOppositeCommandsCannotWedgeTheGate(t *testing.T) {
	g := &fakeGraph{runID: "c360.semdev-001.agent.chain.execution.r1", phase: admission.PhaseAwaitingApproval, gateOpenedAt: time.Now().Add(-time.Hour)}
	a := newTestApprovalOnGraph(g, nil)
	ctx := context.Background()

	if err := a.handleCommentEvent(ctx, flattenedComment(t, "cglusky", "cglusky", "/semdev reject")); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := a.handleCommentEvent(ctx, flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err != nil {
		t.Fatalf("approve: %v", err)
	}

	got := g.gateFacts()
	// THE LOAD-BEARING ASSERTION is the VALUE: first decision wins (D13 layer 1 — H1's
	// irreversible-approval generalized symmetrically, so a cancelled run is never
	// resurrected either). Without the refusal, the approve OVERWRITES the reject here
	// and this is the check that catches it.
	if len(got) == 0 {
		t.Fatal("no decision recorded at all — the reject should have decided the gate")
	}
	if obj, _ := got[0].Object.(string); obj != admission.DecisionReject {
		t.Fatalf("decision = %q, want %q — the FIRST authorized decision must stand; an "+
			"opposite command on a decided run is refused, never applied", obj, admission.DecisionReject)
	}
	// The cell-space check is a REGRESSION GUARD, not the proof: with one single-valued
	// predicate and replace-by-predicate semantics len() cannot exceed 1 by construction,
	// so this fires only if someone reintroduces a second run.change.* gate fact.
	if len(got) != 1 {
		t.Errorf("run carries %d gate-decision facts %v, want EXACTLY 1 — a run holding two "+
			"contradictory gate facts fires NEITHER lifecycle rule and is wedged at the gate forever", len(got), got)
	}
}

// TestExactCommandOnUngatedRunIsIgnored is the BLOCKING pin from the group-8 review.
//
// releaseGate had NO phase guard, so an exact command decided whatever run the ref
// resolved to, in ANY phase. Combined with run-lifecycle/01 (the ONLY rule that moves a
// run INTO awaiting_approval) now requiring the gate UNDECIDED, that made a PRE-GATE
// `/semdev reject` an unrecoverable wedge: the decision lands while the run is still
// executing → the gate is never offered → the cancel rule is phase-guarded to a phase the
// run can never reach → and a follow-up `/semdev approve` is refused by first-decision-wins.
// No park, no post, no operator surface — the exact class D13 exists to remove.
//
// It is also the D12 rule stated once: a command before the gate opens does not decide it.
func TestExactCommandOnUngatedRunIsIgnored(t *testing.T) {
	for _, phase := range []string{"executing", "completed", ""} {
		t.Run("phase="+phase, func(t *testing.T) {
			g := &fakeGraph{runID: "c360.semdev-001.agent.chain.execution.r1", phase: phase}
			a := newTestApprovalOnGraph(g, nil)

			for _, body := range []string{"/semdev reject", "/semdev approve"} {
				if err := a.handleCommentEvent(context.Background(),
					flattenedComment(t, "cglusky", "cglusky", body)); err != nil {
					t.Fatalf("%s must be DEFINITIVE (acked), got %v — redelivery cannot make a run gated, "+
						"and a retry that happened to span the gate opening would make pre-approval "+
						"nondeterministically work", body, err)
				}
			}
			if n := len(g.gateFacts()); n != 0 {
				t.Fatalf("an exact command on a run at phase %q recorded %d decision(s) %v, want 0 — "+
					"a pre-gate decision makes run-lifecycle/01 unable to OPEN the gate, wedging the run "+
					"in a phase from which nothing can recover it", phase, n, g.gateFacts())
			}
		})
	}
}

// TestGatedButDecidedRunSpendsNoClassifier pins the NL bridge's Decided() guard. Between a
// decision landing and the lifecycle rule firing the transition, the run is STILL
// awaiting_approval — so the phase check alone lets a message through, and the classifier
// it spawns produces an intent the routing rules (which require the gate undecided) can
// never route. That is a real model turn spent on a guaranteed-dead result.
func TestGatedButDecidedRunSpendsNoClassifier(t *testing.T) {
	g := &fakeGraph{runID: "c360.semdev-001.agent.chain.execution.r1", phase: admission.PhaseAwaitingApproval, gateOpenedAt: time.Now().Add(-time.Hour)}
	g.facts = append(g.facts, message.Triple{Predicate: admission.DecisionPredicate, Object: admission.DecisionApprove})
	a := newTestApprovalOnGraph(g, nil)
	before := g.writes

	if err := a.handleCommentEvent(context.Background(),
		flattenedComment(t, "cglusky", "cglusky", "yes please ship it")); err != nil {
		t.Fatalf("handleCommentEvent: %v", err)
	}
	if g.writes != before {
		t.Errorf("an NL message on a gated-but-DECIDED run performed %d writes, want 0 — "+
			"stamping conversation.pending.* here spawns a classifier whose intent cannot route", g.writes-before)
	}
}

func newTestApproval(resolver admission.RunResolver, writer *fakeWriter, checker admission.PermissionChecker) *approvalAdapter {
	cfg := ComponentConfig{Repo: "c360studio/semdev-fixture", Allowlist: []string{"cglusky"}}
	applyConfigDefaults(&cfg)
	return &approvalAdapter{cfg: cfg, checker: checker, resolver: resolver, writer: writer, reader: &fakeIntentReader{}, logger: slog.Default()}
}

func flattenedComment(t *testing.T, sender, author, body string) []byte {
	t.Helper()
	ev := githubwebhook.CommentEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issue_comment",
			Action:     "created",
			Repository: githubwebhook.Repository{Owner: "c360studio", Name: "semdev-fixture", FullName: "c360studio/semdev-fixture"},
			Sender:     sender,
			ReceivedAt: time.Now().UTC(),
		},
		IssueNumber: 7,
		Comment:     githubwebhook.CommentPayload{Body: body, Author: author},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// --- the approval pins (conversation-channel-seam 4.3) ---

// TestApprovalReadsNeutralMessage pins the carve: the approval adapter authorizes
// and releases the gate from a NEUTRAL Message (via conversation.NormalizeInboundComment)
// — NOT a githubwebhook.CommentEvent/CommentSignal — and lands run.change.decision="approve"
// (Source approval-adapter) on the resolved run, byte-for-byte the stand-in fact the
// resume rule consumes. The admission Event it authorizes is rebuilt from Message.Author
// + SplitRef(thread), so Authorize is unchanged.
func TestApprovalReadsNeutralMessage(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "c360.semdev-001.agent.chain.execution.r1", phase: admission.PhaseAwaitingApproval}, writer, nil)

	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err != nil {
		t.Fatalf("handleCommentEvent: %v", err)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("ReplaceTriples calls = %d, want 1", len(writer.calls))
	}
	call := writer.calls[0]
	if call.entityID != "c360.semdev-001.agent.chain.execution.r1" {
		t.Errorf("stamped entity = %q, want the resolved run", call.entityID)
	}
	if len(call.add) != 1 {
		t.Fatalf("add = %d triples, want 1", len(call.add))
	}
	tr := call.add[0]
	if tr.Predicate != admission.DecisionPredicate || tr.Object != admission.DecisionApprove || tr.Source != ApprovedSource {
		t.Errorf("triple = %s=%v (Source %s), want %s=true (Source %s) — the exact resume-rule condition",
			tr.Predicate, tr.Object, tr.Source, admission.DecisionApprove, ApprovedSource)
	}
}

// TestApprovalCoreSharedByWebhookAndPoll pins the pull-first-transport D3 split:
// ONE approval core (handleMessage), TWO transports. The WEBHOOK path
// (handleCommentEvent → NormalizeInboundComment → handleMessage) and the POLL path
// (a neutral Message fed to handleMessage directly) both authorize Message.Author
// and land the IDENTICAL run.change.decision="approve" (Source approval-adapter) on the
// resolved run. The webhook path stays byte-identical to today (the migrated
// approval pins are its regression guard); this adds the direct-Message entry point.
func TestApprovalCoreSharedByWebhookAndPoll(t *testing.T) {
	ctx := context.Background()
	const runID = "c360.semdev-001.agent.chain.execution.r1"
	const thread = "c360studio/semdev-fixture#7"

	// Webhook transport: a flattened created comment through the full path.
	wWriter := &fakeWriter{}
	aw := newTestApproval(&fakeResolver{runID: runID, phase: admission.PhaseAwaitingApproval}, wWriter, nil)
	if err := aw.handleCommentEvent(ctx, flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err != nil {
		t.Fatalf("webhook handleCommentEvent: %v", err)
	}

	// Poll transport: a neutral Message (the poll transport's unit — a real comment
	// id, one author) fed to the SAME core directly, no webhook payload.
	pWriter := &fakeWriter{}
	ap := newTestApproval(&fakeResolver{runID: runID, phase: admission.PhaseAwaitingApproval}, pWriter, nil)
	msg := conversation.Message{ID: "9876543210", Author: "cglusky", Body: "/semdev approve", At: time.Now().UTC()}
	if err := ap.handleMessage(ctx, msg, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("poll handleMessage: %v", err)
	}

	// Both transports produced the byte-identical write on the same run.
	for name, w := range map[string]*fakeWriter{"webhook": wWriter, "poll": pWriter} {
		if len(w.calls) != 1 {
			t.Fatalf("%s transport: ReplaceTriples calls = %d, want 1", name, len(w.calls))
		}
		call := w.calls[0]
		if call.entityID != runID || len(call.add) != 1 {
			t.Fatalf("%s transport: stamped entity=%q with %d triples, want %q with 1", name, call.entityID, len(call.add), runID)
		}
		tr := call.add[0]
		if tr.Predicate != admission.DecisionPredicate || tr.Object != admission.DecisionApprove || tr.Source != ApprovedSource {
			t.Errorf("%s transport: triple = %s=%v (Source %s), want %s=true (Source %s)",
				name, tr.Predicate, tr.Object, tr.Source, admission.DecisionApprove, ApprovedSource)
		}
	}
}

// TestPollAuthorizesMessageAuthorNotAllowlisted — the poll path gates on
// Message.Author exactly as the webhook path gates on the (sender==)author: a
// non-allowlisted author's command is ignored, zero writes (H-1: one identity).
func TestPollAuthorizesMessageAuthorNotAllowlisted(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x", phase: admission.PhaseAwaitingApproval}, writer, admission.AllowlistOnlyChecker{})
	msg := conversation.Message{ID: "1", Author: "mallory", Body: "/semdev approve", At: time.Now()}
	if err := a.handleMessage(context.Background(), msg, conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("unauthorized poll message is definitive: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("unauthorized poll author wrote %d facts, want 0", len(writer.calls))
	}
}

// TestPollHonorsEditedApproveWebhookDoesNot pins the ACCEPTED transport divergence
// (review M3): the poll path reads a comment's CURRENT body, so an approve EDITED
// INTO a comment is honored (attributed to its author, gated by Authorize); the
// webhook path fires only on action=="created" and never sees an edit. Not a
// security hole (Authorize gates either way) and arguably more correct (the thread's
// current state is the truth) — DOCUMENTED, not claimed away.
func TestPollHonorsEditedApproveWebhookDoesNot(t *testing.T) {
	ctx := context.Background()
	const runID = "run-1"

	// Webhook: an EDITED comment carrying "/semdev approve" — normalizeComment
	// rejects a non-created action, so no write ever reaches the core.
	wWriter := &fakeWriter{}
	aw := newTestApproval(&fakeResolver{runID: runID, phase: admission.PhaseAwaitingApproval}, wWriter, nil)
	editedEvent := githubwebhook.CommentEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issue_comment",
			Action:     "edited",
			Repository: githubwebhook.Repository{Owner: "c360studio", Name: "semdev-fixture", FullName: "c360studio/semdev-fixture"},
			Sender:     "cglusky",
			ReceivedAt: time.Now().UTC(),
		},
		IssueNumber: 7,
		Comment:     githubwebhook.CommentPayload{Body: "/semdev approve", Author: "cglusky"},
	}
	editedPayload, err := json.Marshal(editedEvent)
	if err != nil {
		t.Fatalf("marshal edited event: %v", err)
	}
	if err := aw.handleCommentEvent(ctx, editedPayload); err != nil {
		t.Fatalf("webhook edited comment: %v", err)
	}
	if len(wWriter.calls) != 0 {
		t.Errorf("webhook honored an EDITED approve (%d writes); it must fire only on created", len(wWriter.calls))
	}

	// Poll: the SAME edited-in approve, read as a current-body Message, IS honored.
	pWriter := &fakeWriter{}
	ap := newTestApproval(&fakeResolver{runID: runID, phase: admission.PhaseAwaitingApproval}, pWriter, nil)
	msg := conversation.Message{ID: "5", Author: "cglusky", Body: "/semdev approve", At: time.Now().UTC()}
	if err := ap.handleMessage(ctx, msg, conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("poll edited-in approve: %v", err)
	}
	if len(pWriter.calls) != 1 {
		t.Errorf("poll did NOT honor an edited-in approve (%d writes); the poll path reads the current body", len(pWriter.calls))
	}
}

// TestUnauthorizedApprovalIsIgnored — the Event invariant: no authorization,
// no write, definitive ack.
func TestUnauthorizedApprovalIsIgnored(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x", phase: admission.PhaseAwaitingApproval}, writer, admission.AllowlistOnlyChecker{})
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "mallory", "mallory", "/semdev approve")); err != nil {
		t.Fatalf("unauthorized is definitive: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("unauthorized command wrote %d facts, want 0", len(writer.calls))
	}
}

// TestUnattributableCommandIsNoSignal — sender ≠ comment author means the body is
// NOT the sender's words; the neutral normalize drops it (ok == false), so the
// command never counts (privilege confusion otherwise).
func TestUnattributableCommandIsNoSignal(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x", phase: admission.PhaseAwaitingApproval}, writer, nil)
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "mallory", "/semdev approve")); err != nil {
		t.Fatalf("unattributable is definitive: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("unattributable command wrote %d facts, want 0", len(writer.calls))
	}
}

// TestApprovalReplayIsIdempotent — an already-approved run gets no second write
// (and no error: the replay acks). It runs on the STATEFUL fakeGraph because the
// refusal is a fresh read at STAMP time (D13), not the resolver's earlier snapshot:
// the resolve happens before an external Authorize round-trip, which is exactly the
// window a competing decision lands in, so a test that only seeds the resolver would
// prove the wrong guard.
func TestApprovalReplayIsIdempotent(t *testing.T) {
	g := &fakeGraph{runID: "run-x", phase: admission.PhaseAwaitingApproval, gateOpenedAt: time.Now().Add(-time.Hour)}
	g.facts = append(g.facts, message.Triple{Predicate: admission.DecisionPredicate, Object: admission.DecisionApprove})
	a := newTestApprovalOnGraph(g, nil)
	before := g.writes
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err != nil {
		t.Fatalf("replay must ack: %v", err)
	}
	if g.writes != before {
		t.Errorf("replay performed %d writes, want 0", g.writes-before)
	}
}

// TestApprovalBeforeRunExistsRedelivers — the approval racing the mint is a
// TRANSIENT state: the handler errors so the consumer redelivers (bounded).
func TestApprovalBeforeRunExistsRedelivers(t *testing.T) {
	a := newTestApproval(&fakeResolver{runID: ""}, &fakeWriter{}, nil)
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err == nil {
		t.Fatal("no-run-yet must return an error (redeliver) — dropping an authorized approval strands the gate")
	}
}

// TestApprovalResolverFaultRedelivers — a transport fault is transient.
func TestApprovalResolverFaultRedelivers(t *testing.T) {
	a := newTestApproval(&fakeResolver{err: errors.New("nats blip")}, &fakeWriter{}, nil)
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err == nil {
		t.Fatal("resolver fault must redeliver")
	}
}

// TestDecodeErrorIsDefinitiveAck — a malformed payload is logged + ACKED, never
// redelivered (grp2-review carry-forward b: the receiver only publishes shapes it
// flattened itself).
func TestDecodeErrorIsDefinitiveAck(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x", phase: admission.PhaseAwaitingApproval}, writer, nil)
	if err := a.handleCommentEvent(context.Background(), []byte(`{not json`)); err != nil {
		t.Fatalf("a decode error must ack (nil), got %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("a malformed payload wrote %d facts, want 0", len(writer.calls))
	}
}

// TestNonCommandCommentsAreIgnored — ordinary conversation never touches the graph.
func TestNonCommandCommentsAreIgnored(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x", phase: admission.PhaseAwaitingApproval}, writer, nil)
	for _, body := range []string{
		"looks good to me",
		"/semdevil approve",       // prefix-collision guard
		"about /semdev approvals", // "approvals" ≠ the verb token
		"approve /semdev",         // wrong token order
	} {
		if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", body)); err != nil {
			t.Fatalf("%q: %v", body, err)
		}
	}
	if len(writer.calls) != 0 {
		t.Errorf("non-command comments wrote %d facts, want 0", len(writer.calls))
	}
}

// TestHasApprovalCommandTokenizing pins the two-token whole-word match.
func TestHasApprovalCommandTokenizing(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"/semdev approve", true},
		{"please /semdev approve now", true},
		{"/SEMDEV APPROVE", true}, // case-insensitive like the opt-in
		{"/semdev  approve", true},
		{"/semdev\napprove", true},
		{"/semdevapprove", false},
		{"/semdev", false},
		{"approve", false},
		{"", false},
	}
	for _, c := range cases {
		if got := hasApprovalCommand("/semdev", c.text); got != c.want {
			t.Errorf("hasApprovalCommand(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// TestHasRejectCommandTokenizing pins the reject verb's two-token whole-word match
// (the deterministic counterpart to approve).
func TestHasRejectCommandTokenizing(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"/semdev reject", true},
		{"please /semdev reject now", true},
		{"/SEMDEV REJECT", true},
		{"/semdevreject", false},
		{"/semdev", false},
		{"reject", false},
		{"/semdev approve", false}, // the other verb is not a reject
		{"", false},
	}
	for _, c := range cases {
		if got := hasRejectCommand("/semdev", c.text); got != c.want {
			t.Errorf("hasRejectCommand(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// --- the grp3 pins: hybrid fast-path (exact approve/reject) + the NL bridge ---

// TestExactApproveCommandStillDeterministic — the whole-token /semdev approve
// fast-path stays BYTE-IDENTICAL under the hybrid dispatch: it stamps
// the approve decision (Source approval-adapter) and NEVER consults the classifier
// ledger (no NL path, zero model turns). The migrated approval pins above are the
// regression guard; this adds the "no classifier read on the fast-path" assertion.
func TestExactApproveCommandStillDeterministic(t *testing.T) {
	writer := &fakeWriter{}
	reader := &fakeIntentReader{}
	a := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, writer, nil)
	a.reader = reader

	if err := a.handleMessage(context.Background(),
		conversation.Message{ID: "1", Author: "cglusky", Body: "/semdev approve", At: time.Now()},
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if len(writer.calls) != 1 || len(writer.calls[0].add) != 1 {
		t.Fatalf("exact approve = %d calls, want one 1-triple write", len(writer.calls))
	}
	tr := writer.calls[0].add[0]
	if tr.Predicate != admission.DecisionPredicate || tr.Object != admission.DecisionApprove || tr.Source != ApprovedSource {
		t.Errorf("triple = %s=%v (Source %s), want %s=true (Source %s)",
			tr.Predicate, tr.Object, tr.Source, admission.DecisionApprove, ApprovedSource)
	}
	if n := reader.readsByPrefix[conversationintent.IntentClassifiedPredicate]; n != 0 {
		t.Errorf("exact approve consulted the classifier ledger %d times, want 0 (deterministic fast-path)", n)
	}
}

// TestExactRejectCommandStampsRejected — a whole-token /semdev reject stamps
// the reject decision (Source approval-adapter) deterministically, no model turn,
// no classifier read. (The awaiting_approval→cancelled transition is a rule,
// asserted in group 5.)
func TestExactRejectCommandStampsRejected(t *testing.T) {
	writer := &fakeWriter{}
	reader := &fakeIntentReader{}
	a := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, writer, nil)
	a.reader = reader

	if err := a.handleMessage(context.Background(),
		conversation.Message{ID: "2", Author: "cglusky", Body: "/semdev reject", At: time.Now()},
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if len(writer.calls) != 1 || len(writer.calls[0].add) != 1 {
		t.Fatalf("exact reject = %d calls, want one 1-triple write", len(writer.calls))
	}
	tr := writer.calls[0].add[0]
	if tr.Predicate != admission.DecisionPredicate || tr.Object != admission.DecisionReject || tr.Source != ApprovedSource {
		t.Errorf("triple = %s=%v (Source %s), want %s=true (Source %s)",
			tr.Predicate, tr.Object, tr.Source, admission.DecisionReject, ApprovedSource)
	}
	if n := reader.readsByPrefix[conversationintent.IntentClassifiedPredicate]; n != 0 {
		t.Errorf("exact reject consulted the classifier ledger %d times, want 0", n)
	}
}

// TestExactRejectRefusedOnApprovedRun — H1: an approval is irreversible once
// landed, so /semdev reject on an already-approved run is a no-op (no write).
func TestExactRejectRefusedOnApprovedRun(t *testing.T) {
	g := &fakeGraph{runID: "run-1", phase: "executing"}
	g.facts = append(g.facts, message.Triple{Predicate: admission.DecisionPredicate, Object: admission.DecisionApprove})
	a := newTestApprovalOnGraph(g, nil)
	before := g.writes
	if err := a.handleMessage(context.Background(),
		conversation.Message{ID: "3", Author: "cglusky", Body: "/semdev reject", At: time.Now()},
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if g.writes != before {
		t.Errorf("reject on an approved run performed %d writes, want 0 (H1 — approval irreversible)", g.writes-before)
	}
	if got := g.objectOf(admission.DecisionPredicate); got != admission.DecisionApprove {
		t.Errorf("decision = %q, want it UNCHANGED at %q", got, admission.DecisionApprove)
	}
}

// TestNonCommandAuthorizedGatedMessageStampsPending — a non-command message from an
// AUTHORIZED author on an awaiting_approval run stamps
// conversation.pending.{message-id,author,body} (Source conversation-adapter) and
// ACKs; an UNAUTHORIZED author and a NON-gated run each stamp NOTHING.
func TestNonCommandAuthorizedGatedMessageStampsPending(t *testing.T) {
	ctx := context.Background()
	const thread = "c360studio/semdev-fixture#7"

	// Authorized author, gated run, fresh id → three pending triples on the run.
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, writer, nil)
	msg := conversation.Message{ID: "msg-42", Author: "cglusky", Body: "yes, please ship this", At: time.Now()}
	if err := a.handleMessage(ctx, msg, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("authorized gated non-command: %v", err)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("pending stamp = %d writer calls, want 1", len(writer.calls))
	}
	got := map[string]string{}
	for _, tr := range writer.calls[0].add {
		if tr.Source != conversationintent.AdapterSource {
			t.Errorf("pending triple %s Source = %q, want %q", tr.Predicate, tr.Source, conversationintent.AdapterSource)
		}
		if s, ok := tr.Object.(string); ok {
			got[tr.Predicate] = s
		}
	}
	if got[conversationintent.PendingMessageIDPredicate] != "msg-42" ||
		got[conversationintent.PendingAuthorPredicate] != "cglusky" ||
		got[conversationintent.PendingBodyPredicate] != "yes, please ship this" {
		t.Errorf("pending triples = %v, want message-id=msg-42 author=cglusky body=<the message>", got)
	}

	// Unauthorized author → zero writes, zero model turns.
	uWriter := &fakeWriter{}
	ua := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, uWriter, admission.AllowlistOnlyChecker{})
	if err := ua.handleMessage(ctx, conversation.Message{ID: "m", Author: "mallory", Body: "approve it", At: time.Now()}, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("unauthorized non-command: %v", err)
	}
	if len(uWriter.calls) != 0 {
		t.Errorf("unauthorized non-command wrote %d facts, want 0", len(uWriter.calls))
	}

	// Non-gated run (phase != awaiting_approval) → zero writes.
	nWriter := &fakeWriter{}
	na := newTestApproval(&fakeResolver{runID: "run-1", phase: "executing"}, nWriter, nil)
	if err := na.handleMessage(ctx, conversation.Message{ID: "m2", Author: "cglusky", Body: "looks good", At: time.Now()}, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("non-gated non-command: %v", err)
	}
	if len(nWriter.calls) != 0 {
		t.Errorf("non-command on a non-gated run wrote %d facts, want 0", len(nWriter.calls))
	}
}

// TestPendingDedupByAppendSetLedger — dedup is against the MULTI-VALUED
// conversation.intent.classified ledger: a message id already in the ledger is NOT
// re-stamped (no re-spawn, MEDIUM-4); a genuinely new id stamps.
func TestPendingDedupByAppendSetLedger(t *testing.T) {
	ctx := context.Background()
	const thread = "c360studio/semdev-fixture#7"
	ledger := []message.Triple{
		{Predicate: conversationintent.IntentClassifiedPredicate, Object: "old-1"},
		{Predicate: conversationintent.IntentClassifiedPredicate, Object: "old-2"},
	}

	// An id already in the ledger → no re-stamp.
	dWriter := &fakeWriter{}
	da := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, dWriter, nil)
	da.reader = &fakeIntentReader{facts: ledger}
	if err := da.handleMessage(ctx, conversation.Message{ID: "old-2", Author: "cglusky", Body: "re-read", At: time.Now()}, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("already-classified message: %v", err)
	}
	if len(dWriter.calls) != 0 {
		t.Errorf("an already-classified id re-stamped pending (%d writes), want 0 (append-set dedup)", len(dWriter.calls))
	}

	// A genuinely new id → stamps.
	nWriter := &fakeWriter{}
	na := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, nWriter, nil)
	na.reader = &fakeIntentReader{facts: ledger}
	if err := na.handleMessage(ctx, conversation.Message{ID: "new-3", Author: "cglusky", Body: "ship it", At: time.Now()}, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("new message: %v", err)
	}
	if len(nWriter.calls) != 1 {
		t.Errorf("a new id did not stamp pending (%d writes), want 1", len(nWriter.calls))
	}
}

// TestNonCommandLedgerReadFaultRedelivers — a classified-ledger read fault is
// TRANSIENT (redeliver), never a silent drop of an authorized message.
func TestNonCommandLedgerReadFaultRedelivers(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, writer, nil)
	a.reader = &fakeIntentReader{err: errors.New("graph read blip")}
	if err := a.handleMessage(context.Background(),
		conversation.Message{ID: "x", Author: "cglusky", Body: "ship it", At: time.Now()},
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err == nil {
		t.Fatal("a ledger read fault must redeliver (return error), not silently drop the message")
	}
	if len(writer.calls) != 0 {
		t.Errorf("a read fault stamped %d facts, want 0", len(writer.calls))
	}
}

// TestHistoricalMessageCannotDecideANewGate is the D12a watermark pin (external review
// #1, the worst of the four in-scope blockers).
//
// THE DEFECT: the poll cursor is an in-memory map built EMPTY, so any restart re-reads a
// thread from the top. NL dedup reads a ledger ON THE RUN and the exact path's
// decided-check is per-run — so a SECOND run over a reused issue starts with an empty
// ledger and an undecided gate, and a months-old "ship it" or `/semdev approve` would
// decide a proposal the human never saw. D12b sharpened it: resolving deterministically to
// the ACTIVE gated run turned a probabilistic mis-target into a certain one.
//
// The rule is UNIFORM across both inbound shapes — that is the whole point of D12, and the
// reason this is table-driven over the NL message and the exact commands together.
func TestHistoricalMessageCannotDecideANewGate(t *testing.T) {
	gateOpened := time.Now().Add(-10 * time.Minute)

	for _, body := range []string{"/semdev approve", "/semdev reject", "yes, ship it"} {
		t.Run(body, func(t *testing.T) {
			g := &fakeGraph{
				runID:        "c360.semdev-001.agent.chain.execution.r2",
				phase:        admission.PhaseAwaitingApproval,
				gateOpenedAt: gateOpened,
			}
			a := newTestApprovalOnGraph(g, nil)

			// Authored for a PREVIOUS run's gate, hours before this one opened.
			stale := conversation.Message{
				ID: "issuecomment-1", Author: "cglusky", Body: body,
				At: gateOpened.Add(-3 * time.Hour),
			}
			if err := a.handleMessage(context.Background(), stale,
				conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
				t.Fatalf("a stale message must be DEFINITIVE (acked), got %v — redelivery cannot "+
					"make a message newer", err)
			}
			if g.writes != 0 {
				t.Fatalf("a message authored %v BEFORE the gate opened produced %d write(s) — it would "+
					"decide a proposal its author never saw", gateOpened.Sub(stale.At), g.writes)
			}
		})
	}

	// The same thread, same run, a message written AFTER the gate opened: honored.
	t.Run("a message written for THIS gate still decides it", func(t *testing.T) {
		g := &fakeGraph{
			runID:        "c360.semdev-001.agent.chain.execution.r2",
			phase:        admission.PhaseAwaitingApproval,
			gateOpenedAt: gateOpened,
		}
		a := newTestApprovalOnGraph(g, nil)
		fresh := conversation.Message{
			ID: "issuecomment-2", Author: "cglusky", Body: "/semdev approve",
			At: gateOpened.Add(30 * time.Second),
		}
		if err := a.handleMessage(context.Background(), fresh,
			conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
		if got := g.objectOf(admission.DecisionPredicate); got != admission.DecisionApprove {
			t.Fatalf("decision = %q, want %q — the watermark must not block a message written "+
				"for the gate that is actually open", got, admission.DecisionApprove)
		}
	})
}

// TestWatermarkFailsClosedWithoutAGateOpenTime — a gated run always carries the framework's
// last-transition-at, so a missing one means the run's audit facts are wrong. A missing
// LOWER BOUND must never read as "no lower bound": that would honor every historical
// comment on the thread, which is the exact defect the watermark exists to close.
func TestWatermarkFailsClosedWithoutAGateOpenTime(t *testing.T) {
	g := &fakeGraph{
		runID: "c360.semdev-001.agent.chain.execution.r1",
		phase: admission.PhaseAwaitingApproval,
		// gateOpenedAt deliberately ZERO.
	}
	a := newTestApprovalOnGraph(g, nil)

	if err := a.handleCommentEvent(context.Background(),
		flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err != nil {
		t.Fatalf("handleCommentEvent: %v", err)
	}
	if g.writes != 0 {
		t.Fatalf("a run with no readable gate-open time accepted a decision (%d writes) — "+
			"an unestablishable watermark must FAIL CLOSED, never fall open", g.writes)
	}
}

// TestWatermarkToleratesClockSkew pins the deliberate margin. The poll transport timestamps
// a message with the CODE HOST's clock while the gate-open moment is stamped with semdev's,
// so a strict comparison drops legitimate approvals whenever semdev runs slightly ahead —
// silently, since a dropped message gets no reply. The margin is far below the separation
// between two runs' gates (a full author→validate→gate arc) and far above real NTP skew.
func TestWatermarkToleratesClockSkew(t *testing.T) {
	gateOpened := time.Now()
	g := &fakeGraph{
		runID:        "c360.semdev-001.agent.chain.execution.r1",
		phase:        admission.PhaseAwaitingApproval,
		gateOpenedAt: gateOpened,
	}
	a := newTestApprovalOnGraph(g, nil)

	// Authored "before" the gate only because the two clocks disagree slightly.
	skewed := conversation.Message{
		ID: "issuecomment-9", Author: "cglusky", Body: "/semdev approve",
		At: gateOpened.Add(-gateWatermarkSkew / 2),
	}
	if err := a.handleMessage(context.Background(), skewed,
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if got := g.objectOf(admission.DecisionPredicate); got != admission.DecisionApprove {
		t.Errorf("decision = %q, want %q — a sub-skew timestamp difference is clock noise, "+
			"not a replayed message", got, admission.DecisionApprove)
	}

	// Well outside the margin is still a replay.
	g2 := &fakeGraph{runID: g.runID, phase: g.phase, gateOpenedAt: gateOpened}
	a2 := newTestApprovalOnGraph(g2, nil)
	old := skewed
	old.At = gateOpened.Add(-10 * gateWatermarkSkew)
	if err := a2.handleMessage(context.Background(), old,
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if g2.writes != 0 {
		t.Errorf("a message %v before the gate was honored — the tolerance is for clock noise, "+
			"not a replay window", 10*gateWatermarkSkew)
	}
}
