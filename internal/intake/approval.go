package intake

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// The APPROVAL ADAPTER (forge-io-real-lanes D3 as-built): the change-approval
// human gate, operable from the issue. v1 signal = the comment command
// `/semdev approve` by an AUTHORIZED actor — possible because semdev's own
// flattener carries the comment's parent issue number (the gap that used to
// defer every comment flow). The adapter writes EXACTLY the fact the journeys'
// stand-in wrote — run.change.approved="true", Source approval-adapter, on the
// RUN — and the untouched resume rule (run-lifecycle/02) does the rest (G2:
// the transition stays rule-owned).
//
// Event invariant: the command counts ONLY when the comment is attributable to
// the event's sender (sender == comment author), and the sender must be
// authorized (allowlist or push-capable collaborator — the same authorize()
// the admission gate runs). An unauthorized or unattributable command is
// ignored with a log line and zero writes.

// ApprovalCommandVerb is the second token of the approval command (the first
// is the configured opt-in command, default "/semdev").
const ApprovalCommandVerb = "approve"

// ApprovedPredicate / ApprovedSource mirror the resume rule's condition and
// the vocab's declared writer — the exact stand-in shape the journeys proved.
const (
	ApprovedPredicate = "run.change.approved"
	ApprovedSource    = "approval-adapter"
)

// RunResolver finds the run entity bound to a host-neutral issue ref via its
// rule-stamped run.issue.ref fact (D2). Implemented over the graph's prefix
// query; faked in unit pins.
type RunResolver interface {
	// ResolveRunByRef returns the run entity ID carrying run.issue.ref == ref,
	// "" when none exists (yet), or an error on a transport fault.
	ResolveRunByRef(ctx context.Context, ref string) (runEntityID string, approved bool, err error)
}

// approvalAdapter consumes github.event.comment payloads from the component's
// durable consumer and releases the change-approval gate.
type approvalAdapter struct {
	cfg      ComponentConfig
	checker  PermissionChecker
	resolver RunResolver
	writer   agentictools.OwnedFactWriter
	logger   *slog.Logger

	ignored int64
	granted int64
}

func newApprovalAdapter(client *natsclient.Client, cfg ComponentConfig, checker PermissionChecker, platform component.PlatformMeta, logger *slog.Logger) *approvalAdapter {
	a := &approvalAdapter{cfg: cfg, checker: checker, logger: logger}
	if client != nil {
		a.resolver = &natsRunResolver{
			client: client,
			prefix: fmt.Sprintf("%s.%s.agent.chain.execution", platform.Org, platform.Platform),
		}
		a.writer = agentictools.NewNATSOwnedFactWriter(client)
	}
	return a
}

// handleCommentEvent processes one flattened comment event. nil = definitive
// (acked); error = transient (redelivered): an authorization lookup fault, a
// graph read/write blip, or the run not existing YET (the wake→mint race — the
// approval may beat the mint by seconds; redelivery retries it).
func (a *approvalAdapter) handleCommentEvent(ctx context.Context, payload []byte) error {
	sig, err := NormalizeComment(payload)
	if err != nil {
		a.logger.Error("approval: malformed comment event; skipping", slog.Any("error", err))
		return nil
	}
	if !sig.Relevant || !sig.Attributable {
		return nil
	}
	if a.cfg.Repo != "" && !strings.EqualFold(strings.TrimSpace(a.cfg.Repo), refRepo(sig.IssueRef)) {
		return nil
	}
	if !hasApprovalCommand(a.cfg.OptInCommand, sig.Event.AuthoredText) {
		return nil
	}

	// Authorization — the SAME gate admission runs, fail-closed with retry.
	authorized, err := Authorize(ctx, a.admissionConfig(), sig.Event, a.checker)
	if err != nil {
		a.logger.Warn("approval: authorization undetermined; redelivering",
			slog.String("ref", sig.IssueRef), slog.String("actor", sig.Event.Actor), slog.Any("error", err))
		return err
	}
	if !authorized {
		atomic.AddInt64(&a.ignored, 1)
		a.logger.Info("approval: unauthorized actor's command ignored (Event invariant)",
			slog.String("ref", sig.IssueRef), slog.String("actor", sig.Event.Actor))
		return nil
	}

	runEntityID, alreadyApproved, err := a.resolver.ResolveRunByRef(ctx, sig.IssueRef)
	if err != nil {
		return fmt.Errorf("approval: resolve run for %s: %w", sig.IssueRef, err)
	}
	if runEntityID == "" {
		// The run may not be minted yet (the approval raced the wake) — or the
		// ref never admitted. Redeliver a bounded number of times; MaxDeliver
		// exhausts loud in the consumer log.
		return fmt.Errorf("approval: no run carries run.issue.ref=%s yet; redelivering", sig.IssueRef)
	}
	if alreadyApproved {
		a.logger.Info("approval: run already approved; replay is a no-op",
			slog.String("ref", sig.IssueRef), slog.String("run", runEntityID))
		return nil
	}

	tr := message.Triple{
		Subject:    runEntityID,
		Predicate:  ApprovedPredicate,
		Object:     "true",
		Source:     ApprovedSource,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := a.writer.ReplaceTriples(ctx, runEntityID, []message.Triple{tr}, nil); err != nil {
		return fmt.Errorf("approval: stamp %s on %s: %w", ApprovedPredicate, runEntityID, err)
	}
	atomic.AddInt64(&a.granted, 1)
	a.logger.Info("approval: authorized command released the change gate",
		slog.String("ref", sig.IssueRef), slog.String("actor", sig.Event.Actor), slog.String("run", runEntityID))
	return nil
}

func (a *approvalAdapter) admissionConfig() Config {
	return Config{Allowlist: a.cfg.Allowlist, OptInLabel: a.cfg.OptInLabel, OptInCommand: a.cfg.OptInCommand}
}

// Authorize is the exported authorization half of the admission gate (the
// approval lane runs authorization WITHOUT the opt-in half — approving is a
// signal on already-admitted work, not an opt-in). Same fail-closed contract
// as Decide: a lookup error returns (false, err) and the caller retries.
func Authorize(ctx context.Context, cfg Config, ev Event, checker PermissionChecker) (bool, error) {
	ev.Actor = strings.TrimSpace(ev.Actor)
	if ev.Actor == "" {
		return false, nil
	}
	return authorize(ctx, cfg, ev, checker)
}

// hasApprovalCommand reports whether text contains the two-token approval
// command (`<command> approve`, default "/semdev approve") as consecutive
// whole whitespace-delimited tokens — same tokenizing discipline as optedIn,
// so "/semdevil approve" and "prefix/semdev approve" do not match.
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

// natsRunResolver resolves ref→run over the graph's paginated prefix query
// (the framework's lesson-reader pattern): list this platform's
// chain-execution entities, match run.issue.ref.
type natsRunResolver struct {
	client *natsclient.Client
	// prefix is the 5-part chain namespace {org}.{platform}.agent.chain.execution.
	prefix string
}

// maxRunPages bounds the pagination defensively (a page is 1000 entities).
const maxRunPages = 16

func (r *natsRunResolver) ResolveRunByRef(ctx context.Context, ref string) (string, bool, error) {
	cursor := ""
	for page := 0; page < maxRunPages; page++ {
		req := graph.PrefixQueryRequest{Prefix: r.prefix, Cursor: cursor}
		data, err := json.Marshal(req)
		if err != nil {
			return "", false, err
		}
		respData, err := r.client.RequestClassified(ctx, "graph.ingest.query.prefix", data, 5*time.Second)
		if err != nil {
			return "", false, fmt.Errorf("prefix query: %w", err)
		}
		var resp graph.PrefixQueryResponse
		if err := json.Unmarshal(respData, &resp); err != nil {
			return "", false, fmt.Errorf("decode prefix response: %w", err)
		}
		for i := range resp.Entities {
			e := &resp.Entities[i]
			var refMatch, approved bool
			for _, tr := range e.Triples {
				if tr.Predicate == "run.issue.ref" {
					if s, ok := tr.Object.(string); ok && s == ref {
						refMatch = true
					}
				}
				// approved = the VALUE the resume rule matches ("true"), not
				// mere presence (review finding).
				if tr.Predicate == ApprovedPredicate {
					if s, ok := tr.Object.(string); ok && s == "true" {
						approved = true
					}
				}
			}
			if refMatch {
				// First match in page order. Two runs sharing one ref is
				// reachable only via a re-triggered issue after a terminal
				// park; when the resume lane lands, prefer-newest (or an
				// explicit disambiguation) replaces this — noted in the
				// design's resume carry-forward.
				return e.ID, approved, nil
			}
		}
		if resp.NextCursor == "" {
			return "", false, nil
		}
		cursor = resp.NextCursor
	}
	return "", false, fmt.Errorf("run resolution exceeded %d pages", maxRunPages)
}
