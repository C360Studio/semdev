package intake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/forge/githubwebhook"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
)

// --- fakes ---

type publishedMsg struct {
	subject string
	data    []byte
	msgID   string
}

type fakePublisher struct {
	published []publishedMsg
	err       error
}

func (f *fakePublisher) PublishToStream(_ context.Context, subject string, data []byte) error {
	f.published = append(f.published, publishedMsg{subject: subject, data: data})
	return f.err
}

func (f *fakePublisher) PublishToStreamWithMsgID(_ context.Context, subject string, data []byte, msgID string) error {
	f.published = append(f.published, publishedMsg{subject: subject, data: data, msgID: msgID})
	return f.err
}

type createdEntity struct {
	entityID string
	msgType  message.Type
	triples  []message.Triple
}

type fakeCreator struct {
	created []createdEntity
	exists  bool
	err     error
}

func (f *fakeCreator) CreateEntityWithTriples(_ context.Context, entityID string, msgType message.Type, triples []message.Triple) error {
	if f.exists {
		return &errs.ClassifiedError{
			Err: errs.ErrInvalidConfig, Message: "exists",
			Code: graph.ErrorCodeEntityExists,
		}
	}
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, createdEntity{entityID: entityID, msgType: msgType, triples: triples})
	return nil
}

// newTestComponent builds a direct-constructed Component with fakes — the
// booted shape minus NATS (handleIssueEvent never touches the client).
// existingRun seeds the redelivery discriminator's resolver ("" = no run yet).
func newTestIntake(pub *fakePublisher, creator EntityCreator, checker admission.PermissionChecker) *Component {
	return newTestIntakeWithRun(pub, creator, checker, "")
}

func newTestIntakeWithRun(pub *fakePublisher, creator EntityCreator, checker admission.PermissionChecker, existingRun string) *Component {
	cfg := ComponentConfig{
		Ports:     DefaultPorts(),
		Repo:      "c360studio/semdev-fixture",
		Allowlist: []string{"cglusky"},
	}
	applyConfigDefaults(&cfg)
	return &Component{
		config:   cfg,
		pub:      pub,
		creator:  creator,
		checker:  checker,
		resolver: &fakeResolver{runID: existingRun},
		platform: component.PlatformMeta{Org: "c360", Platform: "semdev-001"},
		logger:   slog.Default(),
	}
}

// flattenedIssue builds a flattened admitted-shape issue event.
func flattenedIssue(t *testing.T, action, sender, author, body string, labels []string) []byte {
	t.Helper()
	ev := githubwebhook.IssueEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issues",
			Action:     action,
			Repository: githubwebhook.Repository{Owner: "c360studio", Name: "semdev-fixture", FullName: "c360studio/semdev-fixture"},
			Sender:     sender,
			DeliveryID: "delivery-1",
			ReceivedAt: time.Now().UTC(),
		},
		Issue: githubwebhook.IssuePayload{
			Number: 7, Title: "boundary bug", Body: body, State: "open",
			Labels: labels, Author: author,
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// --- the intake-lane pins (task 2.1) ---

// TestAdmittedIssuePublishesExactlyOneWake pins the whole admitted path: the
// admission record births FIRST (Source admission-check, the spec's
// intake.actor.admitted), then EXACTLY one coordinator wake in the
// journey-proven byte shape (BaseMessage envelope, TaskMessage payload whose
// TaskID is the BARE ref and whose prompt carries ref + authored content).
func TestAdmittedIssuePublishesExactlyOneWake(t *testing.T) {
	pub := &fakePublisher{}
	creator := &fakeCreator{}
	c := newTestIntake(pub, creator, nil)

	payload := flattenedIssue(t, "opened", "cglusky", "cglusky", "/semdev fix the warning threshold", []string{"semdev"})
	if err := c.handleIssueEvent(context.Background(), payload); err != nil {
		t.Fatalf("handleIssueEvent: %v", err)
	}

	if len(creator.created) != 1 {
		t.Fatalf("admission records created = %d, want 1", len(creator.created))
	}
	rec := creator.created[0]
	if !strings.HasPrefix(rec.entityID, "c360.semdev-001.forge.intake.event.") {
		t.Errorf("record entity = %q, want the forge.intake.event grammar", rec.entityID)
	}
	wantTriples := map[string]string{
		ActorLoginPredicate:    "cglusky",
		ActorAdmittedPredicate: "true",
		EventRefPredicate:      "c360studio/semdev-fixture#7",
	}
	for _, tr := range rec.triples {
		if tr.Source != RecordSource {
			t.Errorf("record triple %s Source = %q, want %q (G5)", tr.Predicate, tr.Source, RecordSource)
		}
		want, ok := wantTriples[tr.Predicate]
		if !ok {
			t.Errorf("unexpected record predicate %q", tr.Predicate)
			continue
		}
		if got, _ := tr.Object.(string); got != want {
			t.Errorf("record %s = %q, want %q", tr.Predicate, got, want)
		}
		delete(wantTriples, tr.Predicate)
	}
	for missing := range wantTriples {
		t.Errorf("record is missing predicate %q", missing)
	}

	if len(pub.published) != 1 {
		t.Fatalf("publishes = %d, want exactly 1 wake", len(pub.published))
	}
	wake := pub.published[0]
	if wake.subject != FrontDoorSubject {
		t.Errorf("wake subject = %q, want %q", wake.subject, FrontDoorSubject)
	}
	if wake.msgID != rec.entityID {
		t.Errorf("wake msg-id = %q, want the admission record id (JetStream dedup layer)", wake.msgID)
	}
	var base struct {
		Payload agentic.TaskMessage `json:"payload"`
	}
	if err := json.Unmarshal(wake.data, &base); err != nil {
		t.Fatalf("wake is not a BaseMessage envelope: %v", err)
	}
	task := base.Payload
	if task.TaskID != "c360studio/semdev-fixture#7" {
		t.Errorf("TaskID = %q, want the BARE ref (D2: it becomes agent.loop.task, which the issue-ref rule stamps onto the run)", task.TaskID)
	}
	if !strings.Contains(task.Prompt, "c360studio/semdev-fixture#7") || !strings.Contains(task.Prompt, "warning threshold") {
		t.Errorf("wake prompt must carry the ref + the authored content (the M1-proven lane), got %q", task.Prompt)
	}
	if task.Tools != nil {
		t.Errorf("wake Tools must be nil (global discovery), got %v", task.Tools)
	}
}

// TestRejectedActorPublishesNothing pins the zero-token reject: an actor who is
// neither allowlisted nor (in allowlist-only mode) a collaborator produces NO
// record, NO wake, and a definitive ack (nil error — no retry can change it).
func TestRejectedActorPublishesNothing(t *testing.T) {
	pub := &fakePublisher{}
	creator := &fakeCreator{}
	c := newTestIntake(pub, creator, admission.AllowlistOnlyChecker{})

	payload := flattenedIssue(t, "opened", "mallory", "mallory", "/semdev do things", []string{"semdev"})
	if err := c.handleIssueEvent(context.Background(), payload); err != nil {
		t.Fatalf("a definitive reject must ACK (nil), got %v", err)
	}
	if len(pub.published) != 0 || len(creator.created) != 0 {
		t.Errorf("reject published %d, created %d — want 0/0 (zero tokens, zero writes)", len(pub.published), len(creator.created))
	}
	if n := atomic.LoadInt64(&c.rejected); n != 1 {
		t.Errorf("rejected counter = %d, want 1 (the admission metric)", n)
	}
}

// TestAuthorizedButNotOptedInIsRejected — authorization alone does not spend
// budget; the opt-in half is required (the spec's two-key gate).
func TestAuthorizedButNotOptedInIsRejected(t *testing.T) {
	pub := &fakePublisher{}
	creator := &fakeCreator{}
	c := newTestIntake(pub, creator, nil)

	payload := flattenedIssue(t, "opened", "cglusky", "cglusky", "just a normal issue", nil)
	if err := c.handleIssueEvent(context.Background(), payload); err != nil {
		t.Fatalf("not-opted-in is definitive: %v", err)
	}
	if len(pub.published) != 0 || len(creator.created) != 0 {
		t.Errorf("not-opted-in published %d, created %d — want 0/0", len(pub.published), len(creator.created))
	}
}

// TestMalformedPayloadSkipsWithoutCrash — garbage is logged and ACKED (a
// poison payload must not redeliver forever), never a panic.
func TestMalformedPayloadSkipsWithoutCrash(t *testing.T) {
	pub := &fakePublisher{}
	c := newTestIntake(pub, &fakeCreator{}, nil)
	if err := c.handleIssueEvent(context.Background(), []byte(`{not json`)); err != nil {
		t.Fatalf("malformed payload must ack (nil), got %v", err)
	}
	if len(pub.published) != 0 {
		t.Errorf("malformed payload published %d messages, want 0", len(pub.published))
	}
}

// TestRedeliveredAdmissionWithRunDoesNotDoubleWake pins the idempotency
// backstop's genuine-duplicate leg: the record exists AND a run already
// carries the ref ⇒ the wake is SKIPPED (never a second run), delivery acks.
func TestRedeliveredAdmissionWithRunDoesNotDoubleWake(t *testing.T) {
	pub := &fakePublisher{}
	creator := &fakeCreator{exists: true}
	c := newTestIntakeWithRun(pub, creator, nil, "c360.semdev-001.agent.chain.execution.r1")

	payload := flattenedIssue(t, "opened", "cglusky", "cglusky", "/semdev fix it", []string{"semdev"})
	if err := c.handleIssueEvent(context.Background(), payload); err != nil {
		t.Fatalf("redelivery must ack, got %v", err)
	}
	if len(pub.published) != 0 {
		t.Errorf("redelivery published %d wakes, want 0 (a duplicate wake double-mints a run)", len(pub.published))
	}
}

// TestRecordedButRunlessRedeliveryRepublishesTheWake pins the RECOVERY leg
// (review finding — without it the wake-publish retry was a dead end): the
// record exists but NO run carries the ref (the crash window, or a failed wake
// publish being redelivered) ⇒ the wake IS published, msg-id-deduped by the
// record ID.
func TestRecordedButRunlessRedeliveryRepublishesTheWake(t *testing.T) {
	pub := &fakePublisher{}
	creator := &fakeCreator{exists: true}
	c := newTestIntakeWithRun(pub, creator, nil, "") // no run

	payload := flattenedIssue(t, "opened", "cglusky", "cglusky", "/semdev fix it", []string{"semdev"})
	if err := c.handleIssueEvent(context.Background(), payload); err != nil {
		t.Fatalf("recovery redelivery must ack after re-publishing, got %v", err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("recovery redelivery published %d wakes, want 1 (the wake never took — republish)", len(pub.published))
	}
	if pub.published[0].msgID == "" {
		t.Error("the recovery wake must carry the record-ID msg-id (dedup absorbs an in-window double)")
	}
}

// TestUnboundRepoIsSkipped — the repo binding is defense in depth ahead of the
// gate.
func TestUnboundRepoIsSkipped(t *testing.T) {
	pub := &fakePublisher{}
	c := newTestIntake(pub, &fakeCreator{}, nil)
	ev := githubwebhook.IssueEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType: "issues", Action: "opened",
			Repository: githubwebhook.Repository{Owner: "evil", Name: "other", FullName: "evil/other"},
			Sender:     "cglusky",
		},
		Issue: githubwebhook.IssuePayload{Number: 1, Body: "/semdev", Author: "cglusky"},
	}
	data, _ := json.Marshal(ev)
	if err := c.handleIssueEvent(context.Background(), data); err != nil {
		t.Fatalf("unbound repo is definitive: %v", err)
	}
	if len(pub.published) != 0 {
		t.Errorf("unbound repo published %d, want 0", len(pub.published))
	}
}

// --- the receiver pins ---

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookReceiverFlattensAndPublishes pins the receiver lane: a raw GitHub
// issues POST is flattened (semdev owns the mapping now) and published to the
// GITHUB stream keyed by the delivery GUID.
func TestWebhookReceiverFlattensAndPublishes(t *testing.T) {
	pub := &fakePublisher{}
	c := newTestIntake(pub, &fakeCreator{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(rawIssuesPayloadForReceiver))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "guid-42")
	w := httptest.NewRecorder()
	c.handleWebhook(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d, want 1", len(pub.published))
	}
	got := pub.published[0]
	if got.subject != admission.SubjectIssue {
		t.Errorf("subject = %q, want %q", got.subject, admission.SubjectIssue)
	}
	if got.msgID != "guid-42" {
		t.Errorf("msg-id = %q, want the delivery GUID (stream-layer redelivery dedup)", got.msgID)
	}
	var ev githubwebhook.IssueEvent
	if err := json.Unmarshal(got.data, &ev); err != nil {
		t.Fatalf("published payload is not the flattened IssueEvent: %v", err)
	}
	if ev.Issue.Number != 7 || ev.Sender != "cglusky" || ev.DeliveryID != "guid-42" {
		t.Errorf("flattened event = number %d sender %q delivery %q — the raw→flat mapping drifted", ev.Issue.Number, ev.Sender, ev.DeliveryID)
	}
}

// TestWebhookReceiverEnforcesHMAC — with a secret configured, a bad or missing
// signature is rejected 401 and publishes nothing; the correct signature is
// accepted. The security door of the whole live lane.
func TestWebhookReceiverEnforcesHMAC(t *testing.T) {
	pub := &fakePublisher{}
	c := newTestIntake(pub, &fakeCreator{}, nil)
	c.webhookSecret = "s3cret"

	body := []byte(rawIssuesPayloadForReceiver)

	bad := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(body)))
	bad.Header.Set("X-GitHub-Event", "issues")
	bad.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	w := httptest.NewRecorder()
	c.handleWebhook(w, bad)
	if w.Code != http.StatusUnauthorized || len(pub.published) != 0 {
		t.Fatalf("bad signature: status %d, published %d — want 401 and 0", w.Code, len(pub.published))
	}

	good := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(body)))
	good.Header.Set("X-GitHub-Event", "issues")
	good.Header.Set("X-GitHub-Delivery", "guid-77")
	good.Header.Set("X-Hub-Signature-256", signBody("s3cret", body))
	w = httptest.NewRecorder()
	c.handleWebhook(w, good)
	if w.Code != http.StatusAccepted || len(pub.published) != 1 {
		t.Fatalf("good signature: status %d, published %d — want 202 and 1", w.Code, len(pub.published))
	}
}

// TestWebhookReceiverDropsIrrelevantEvents — a ping (or any event outside the
// consumed lanes) is accepted and dropped, never published.
func TestWebhookReceiverDropsIrrelevantEvents(t *testing.T) {
	pub := &fakePublisher{}
	c := newTestIntake(pub, &fakeCreator{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(`{"zen":"keep it logically awesome"}`))
	req.Header.Set("X-GitHub-Event", "ping")
	w := httptest.NewRecorder()
	c.handleWebhook(w, req)
	if w.Code != http.StatusAccepted || len(pub.published) != 0 {
		t.Errorf("ping: status %d, published %d — want 202 and 0", w.Code, len(pub.published))
	}
}

// rawIssuesPayloadForReceiver is a raw-shape issues payload for the receiver
// pins (nested objects — the receiver flattens it).
const rawIssuesPayloadForReceiver = `{
  "action": "opened",
  "issue": {"number": 7, "title": "t", "body": "/semdev fix", "state": "open",
            "labels": [{"name": "semdev"}], "user": {"login": "cglusky"},
            "html_url": "https://github.com/c360studio/semdev-fixture/issues/7"},
  "repository": {"name": "semdev-fixture", "full_name": "c360studio/semdev-fixture", "owner": {"login": "c360studio"}},
  "sender": {"login": "cglusky"}
}`
