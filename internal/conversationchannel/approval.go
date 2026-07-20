package conversationchannel

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// The APPROVAL ADAPTER (conversation-channel-seam D8, re-homed from issue-intake):
// the change-approval human gate, operable from the conversation thread. v1 signal
// = the comment command `/semdev approve` by an AUTHORIZED actor. Re-homed behind
// the channel-neutral Channel port: it reads a neutral Message (via the GitHub
// impl's contained normalize) — NOT a githubwebhook.CommentEvent — so a second
// channel would drive the SAME approval logic. The adapter writes EXACTLY the fact
// the journeys' stand-in wrote — run.change.approved="true", Source approval-adapter,
// on the RUN — and the untouched resume rule (run-lifecycle/02) does the rest (G2:
// the transition stays rule-owned).
//
// Event invariant: the command counts ONLY when the message is attributable to its
// author (the Channel's normalize enforces sender==author before emitting a
// Message), and the author must be authorized (allowlist or push-capable
// collaborator — the same admission.Authorize the intake gate runs). An
// unauthorized command is ignored with a log line and zero writes.

// ApprovalCommandVerb is the second token of the approval command (the first
// is the configured opt-in command, default "/semdev").
const ApprovalCommandVerb = "approve"

// ApprovedSource is the vocab-declared writer of run.change.approved — the exact
// stand-in Source the journeys proved. The PREDICATE lives in the shared admission
// core (the resolver reads it too).
const ApprovedSource = "approval-adapter"

// approvalAdapter consumes github.event.comment payloads (via the Channel's neutral
// normalize) and releases the change-approval gate.
type approvalAdapter struct {
	cfg      ComponentConfig
	checker  admission.PermissionChecker
	resolver admission.RunResolver
	writer   agentictools.OwnedFactWriter
	logger   *slog.Logger

	ignored int64
	granted int64
}

func newApprovalAdapter(client *natsclient.Client, cfg ComponentConfig, checker admission.PermissionChecker, platform component.PlatformMeta, logger *slog.Logger) *approvalAdapter {
	a := &approvalAdapter{cfg: cfg, checker: checker, logger: logger}
	if client != nil {
		a.resolver = admission.NewRunResolver(client, platform.Org, platform.Platform)
		a.writer = agentictools.NewNATSOwnedFactWriter(client)
	}
	return a
}

// handleCommentEvent processes one flattened comment event through the Channel's
// neutral normalize. nil = definitive (acked); error = transient (redelivered): an
// authorization lookup fault, a graph read/write blip, or the run not existing YET
// (the wake→mint race — the approval may beat the mint by seconds; redelivery
// retries it). A DECODE failure is definitive (acked) — the receiver only publishes
// shapes it flattened itself (grp2-review carry-forward b).
func (a *approvalAdapter) handleCommentEvent(ctx context.Context, payload []byte) error {
	msg, thread, ok, err := conversation.NormalizeInboundComment(payload)
	if err != nil {
		a.logger.Error("approval: malformed comment event; skipping", slog.Any("error", err))
		return nil
	}
	// ok folds the old Relevant && Attributable: a created, issue-bound comment
	// whose author is its sender. A non-signal comment is skipped without a write.
	if !ok {
		return nil
	}
	if a.cfg.Repo != "" && !strings.EqualFold(strings.TrimSpace(a.cfg.Repo), refRepo(string(thread))) {
		return nil
	}
	if !hasApprovalCommand(a.cfg.OptInCommand, msg.Body) {
		return nil
	}

	// Build the admission Event from the neutral Message (Author = the attributed
	// author == the sender, since ok requires sender==author) + the thread's
	// resolved code-host scope (owner/repo from SplitRef(thread) == the payload's
	// Repository.Owner/Name). This keeps Authorize byte-identical to the pre-carve
	// path (grp2-review carry-forward a).
	owner, repo, _, err := admission.SplitRef(string(thread))
	if err != nil {
		// A real flattened comment always carries Repository.FullName (the flattener
		// reconstructs owner/name), so an unparseable thread is unreachable in
		// practice. If it ever occurred (e.g. an empty FullName), fail CLOSED here —
		// strictly safer than the pre-carve path, which read owner/repo from the raw
		// fields and would have proceeded (review L2: a deliberate, safe divergence).
		a.logger.Error("approval: unparseable thread from a normalized comment; skipping",
			slog.String("thread", string(thread)), slog.Any("error", err))
		return nil
	}
	ev := admission.Event{Actor: msg.Author, Owner: owner, Repo: repo, AuthoredText: msg.Body}

	// Authorization — the SAME gate admission runs, fail-closed with retry.
	authorized, err := admission.Authorize(ctx, a.admissionConfig(), ev, a.checker)
	if err != nil {
		a.logger.Warn("approval: authorization undetermined; redelivering",
			slog.String("ref", string(thread)), slog.String("actor", ev.Actor), slog.Any("error", err))
		return err
	}
	if !authorized {
		atomic.AddInt64(&a.ignored, 1)
		a.logger.Info("approval: unauthorized actor's command ignored (Event invariant)",
			slog.String("ref", string(thread)), slog.String("actor", ev.Actor))
		return nil
	}

	runEntityID, alreadyApproved, err := a.resolver.ResolveRunByRef(ctx, string(thread))
	if err != nil {
		return fmt.Errorf("approval: resolve run for %s: %w", string(thread), err)
	}
	if runEntityID == "" {
		// The run may not be minted yet (the approval raced the wake) — or the
		// ref never admitted. Redeliver a bounded number of times; MaxDeliver
		// exhausts loud in the consumer log.
		return fmt.Errorf("approval: no run carries run.issue.ref=%s yet; redelivering", string(thread))
	}
	if alreadyApproved {
		a.logger.Info("approval: run already approved; replay is a no-op",
			slog.String("ref", string(thread)), slog.String("run", runEntityID))
		return nil
	}

	tr := message.Triple{
		Subject:    runEntityID,
		Predicate:  admission.ApprovedPredicate,
		Object:     "true",
		Source:     ApprovedSource,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := a.writer.ReplaceTriples(ctx, runEntityID, []message.Triple{tr}, nil); err != nil {
		return fmt.Errorf("approval: stamp %s on %s: %w", admission.ApprovedPredicate, runEntityID, err)
	}
	atomic.AddInt64(&a.granted, 1)
	a.logger.Info("approval: authorized command released the change gate",
		slog.String("ref", string(thread)), slog.String("actor", ev.Actor), slog.String("run", runEntityID))
	return nil
}

func (a *approvalAdapter) admissionConfig() admission.Config {
	return admission.Config{Allowlist: a.cfg.Allowlist, OptInLabel: a.cfg.OptInLabel, OptInCommand: a.cfg.OptInCommand}
}

// hasApprovalCommand reports whether text contains the two-token approval
// command (`<command> approve`, default "/semdev approve") as consecutive
// whole whitespace-delimited tokens — same tokenizing discipline as the opt-in
// match, so "/semdevil approve" and "prefix/semdev approve" do not match.
func hasApprovalCommand(command, text string) bool {
	if command == "" {
		return false
	}
	tokens := strings.Fields(text)
	for i := 0; i+1 < len(tokens); i++ {
		if strings.EqualFold(tokens[i], command) && strings.EqualFold(tokens[i+1], ApprovalCommandVerb) {
			return true
		}
	}
	return false
}

// refRepo returns the "owner/repo" half of an "owner/repo#number" thread.
func refRepo(ref string) string {
	if i := strings.LastIndex(ref, "#"); i > 0 {
		return ref[:i]
	}
	return ref
}
