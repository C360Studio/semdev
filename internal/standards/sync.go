package standards

// The sync core (standards-via-lessons D3/D4/D5): parse → source-entity birth →
// lesson-record birth → Lane-1 promotion → removed-standard retirement. Pure
// orchestration over narrow seams; the real implementations bind at boot
// (group 4): the framework LessonStore/LessonCurator and graphown's Creator.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/processor/agentic-loop/lessonmatch"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// Curator is the Lane-1 lifecycle surface — *agentictools.LessonCurator
// satisfies it; tests fake it.
type Curator interface {
	Promote(ctx context.Context, lessonEntityID string) error
	Retire(ctx context.Context, lessonEntityID string) error
}

// SourceCreator births the standards-file source entity on the strict Create
// lane — *graphown.Creator satisfies it; tests fake it.
type SourceCreator interface {
	Create(ctx context.Context, entityID string, msgType message.Type, triples []message.Triple) error
}

// ConflictClassifier reports whether a Create error is the strict-create
// duplicate signal (graphown.IsConflict in production; injected so this
// package does not import graphown).
type ConflictClassifier func(error) bool

// CandidateRecord is a lesson record's retirement-relevant projection,
// returned by the Lister (a graph prefix query in production).
type CandidateRecord struct {
	EntityID string
	Category string
	Status   string
	Evidence []string
}

// RecordLister lists every lesson record under the platform's record prefix.
type RecordLister interface {
	ListLessonRecords(ctx context.Context) ([]CandidateRecord, error)
}

// EvidenceResolver resolves a cited source entity's declared repo
// (repo.standards.repo), with found=false for an absent entity.
type EvidenceResolver interface {
	SourceRepo(ctx context.Context, sourceEntityID string) (repo string, found bool, err error)
}

// DeclarationError marks a fault in what the TARGET REPO declared — a malformed
// standards file, an over-bound injection form — as opposed to a fault in
// semdev's own substrate (a graph write, a transport timeout). Provisioning
// needs the distinction and cannot make it for itself: a declaration fault
// parks toward the human with the exact defect, because no amount of retrying
// fixes a typo, while a substrate fault must retry or a NATS hiccup parks the
// run permanently.
type DeclarationError struct{ Err error }

func (e *DeclarationError) Error() string {
	if e == nil || e.Err == nil {
		// Exported type with an exported field: &DeclarationError{} compiles, and a nil
		// deref here would panic inside a provision handler while REPORTING another fault.
		return "standards: unspecified declaration fault"
	}
	return e.Err.Error()
}
func (e *DeclarationError) Unwrap() error { return e.Err }

// promotionRefusalMarker is how the framework's curator words a REFUSAL as opposed to a
// fault (lesson_promotion.go:82,99). Matched on the substring because the framework
// returns a plain error with no sentinel — filed upstream as part of the consumer-surface
// hardening ask. A miss here is safe in the conservative direction: the fault falls
// through to the retry path, which is where it sits today.
const promotionRefusalMarker = "refused —"

// isPromotionRefusal reports whether the curator declined to activate on the record's own
// state (unresolvable or absent evidence) rather than failing to reach the graph.
func isPromotionRefusal(err error) bool {
	return err != nil && strings.Contains(err.Error(), promotionRefusalMarker)
}

// IsDeclaration reports whether err is a repo-declaration fault.
func IsDeclaration(err error) bool {
	var d *DeclarationError
	return errors.As(err, &d)
}

// Result summarizes one sync pass. Promoted counts promotion CALLS issued this
// pass (proposed/retired → active), not net state transitions; the steady state
// (all records already active) issues none.
type Result struct {
	SourceEntityID string
	Born           int
	Promoted       int
	Retired        int
}

// Syncer executes the provision-time standards sync.
type Syncer struct {
	Store      agentictools.LessonStore
	Curator    Curator
	Creator    SourceCreator
	IsConflict ConflictClassifier
	Lister     RecordLister
	Resolver   EvidenceResolver
	Org        string
	Platform   string
	Logger     *slog.Logger
	Now        func() time.Time
}

// Source is the triple Source every standards-sync fact carries (the G5 writer).
const Source = "standards-sync"

// standardCategory is the open-taxonomy category every file-derived standard
// record carries — the retirement filter's first key (D5).
const standardCategory = "repo-standard"

// standardsNamespaceUUID namespaces the content-derived record identity —
// UUIDv5 over the SAME four fields the framework store's conflict check reads
// (category, sorted applies-to, summary, sorted evidence), so its EntityExists
// verification holds and identical content re-syncs are no-ops.
var standardsNamespaceUUID = uuid.MustParse("6f9c2a41-90de-4b6c-b1de-3e01a5c7e9d2")

// sourceMessageType types the standards-file source entity (the admission-record
// idiom: a semdev-domain typed envelope).
func sourceMessageType() message.Type {
	return message.Type{Domain: "semdev", Category: "standards_source", Version: "v1"}
}

// injectionKBound is the substrate's default per-brief record ceiling, imported
// rather than mirrored (it is exported, unlike the 320B bound — go-review L2).
const injectionKBound = lessonmatch.DefaultK

// Sync parses raw standards-file bytes and projects them onto the lesson
// substrate for the given owner/repo: source-entity birth → record births →
// Lane-1 promotion (only the records this pass ensured — the named
// auto-promotion policy) → retirement of this repo's no-longer-declared
// records. Fail-closed on any parse or write error; the strict-create conflict
// on the source entity is the idempotent duplicate signal, not a failure.
func (s *Syncer) Sync(ctx context.Context, raw []byte, repo string) (Result, error) {
	if err := s.validate(); err != nil {
		return Result{}, err
	}
	file, err := Parse(raw)
	if err != nil {
		return Result{}, &DeclarationError{Err: err}
	}

	// The digest covers repo + content (semstreams-review M2): byte-identical
	// template files across two repos must NOT alias to one source entity —
	// the source's repo.standards.repo is the retirement scope key, and a
	// shared source would make it first-writer-wins (cross-repo retirement
	// flap). Per-repo sources also give per-repo record identities (evidence
	// is an identity input), at the cost of duplicate brief lines in a
	// multi-target deployment (recorded in D2/D5).
	digest := sha256.Sum256(append([]byte(repo+"\x1f"), raw...))
	digestHex := hex.EncodeToString(digest[:])
	sourceID := fmt.Sprintf("%s.%s.repo.standards.source.%s", s.Org, s.Platform, digestHex[:12])

	res := Result{SourceEntityID: sourceID}
	now := s.Now().UTC()

	// The source entity FIRST: every record cites it as evidence, and Promote
	// resolves evidence existence before activating (D4).
	sourceTriples := []message.Triple{
		s.triple(sourceID, "repo.standards.digest", digestHex, now),
		s.triple(sourceID, "repo.standards.path", Path, now),
		s.triple(sourceID, "repo.standards.repo", repo, now),
	}
	if err := s.Creator.Create(ctx, sourceID, sourceMessageType(), sourceTriples); err != nil {
		if s.IsConflict == nil || !s.IsConflict(err) {
			return Result{}, fmt.Errorf("standards: create source entity %s: %w", sourceID, err)
		}
		// Identical file bytes re-provisioned — the idempotent duplicate signal.
	}

	// Birth + promote, scoped strictly to the file's declared set.
	expected := map[string]bool{}
	for _, std := range file.Standards {
		entityID, triples, err := s.recordFor(std, sourceID, now)
		if err != nil {
			// recordFor's only fault is the injection-form bound — repo-authored text
			// that is too long to inject. The repo's mistake, so it parks like a parse
			// defect rather than retrying forever.
			return Result{}, &DeclarationError{Err: err}
		}
		expected[entityID] = true
		created, err := s.Store.CreateLesson(ctx, entityID, agentic.AgentLessonMessageType(), triples)
		if err != nil {
			return Result{}, fmt.Errorf("standards: birth record for %q: %w", std.ID, err)
		}
		if created {
			res.Born++
		}
		// The named policy: a record derived from the committed file
		// auto-promotes — the git commit/PR review of the file IS the human
		// gate. Status-aware (semstreams-review M1): an already-active record
		// is left untouched (no per-sync lifecycle writes on the steady
		// state), a proposed one activates, and a RETIRED one is deliberately
		// resurrected — re-declaring previously-removed content in the
		// committed file is the same human gate, and this is also why an
		// operator's manual retirement of a still-declared standard reverses
		// on the next provision (the file is the law; retire by editing it).
		status, found, err := s.Store.ReadLessonStatus(ctx, entityID)
		if err != nil {
			return Result{}, fmt.Errorf("standards: read status of %q (%s): %w", std.ID, entityID, err)
		}
		if found && status == "active" {
			continue
		}
		if err := s.Curator.Promote(ctx, entityID); err != nil {
			// The curator REFUSES (rather than faults) when a record's cited evidence does
			// not resolve. That is a durable state defect, not a transient one: retrying it
			// burns the station's whole budget and then parks with a message naming the
			// station instead of the problem. Classify it as a declaration fault so the run
			// parks immediately, carrying the curator's own words.
			//
			// The reachable path is a source-entity ID collision: our strict Create conflicts
			// with a pre-existing entity that is NOT our source, IsConflict reads that as the
			// idempotent duplicate signal, and Promote then cannot resolve the evidence.
			if isPromotionRefusal(err) {
				return Result{}, &DeclarationError{Err: fmt.Errorf("standards: cannot activate %q (%s): %w", std.ID, entityID, err)}
			}
			return Result{}, fmt.Errorf("standards: promote %q (%s): %w", std.ID, entityID, err)
		}
		res.Promoted++
	}

	if err := s.retireRemoved(ctx, repo, expected, &res); err != nil {
		return Result{}, err
	}

	s.warnOnKBound(file)
	return res, nil
}

// recordFor maps one declared standard to its content-derived record identity
// and birth triples (design D3).
func (s *Syncer) recordFor(std Standard, sourceID string, now time.Time) (string, []message.Triple, error) {
	form, err := InjectionForm(std)
	if err != nil {
		return "", nil, err // parse already enforced this; belt for direct callers
	}
	appliesTo := make([]string, 0, len(std.Roles))
	for _, role := range std.Roles {
		appliesTo = append(appliesTo, "tag:"+role)
	}
	evidence := []string{sourceID}
	entityID := agentic.AgentLessonEntityID(s.Org, s.Platform, lessonIdentity(standardCategory, appliesTo, std.Text, evidence))
	detail := fmt.Sprintf("%s — declared in %s of the target repo (source %s)", std.ID, Path, sourceID)

	triples := []message.Triple{
		s.triple(entityID, "agent.lesson.category", standardCategory, now),
		s.triple(entityID, "agent.lesson.polarity", "best_practice", now),
		s.triple(entityID, "agent.lesson.severity", severityFor(std.Severity), now),
		s.triple(entityID, "agent.lesson.status", "proposed", now),
		s.triple(entityID, "agent.lesson.created-at", now.Format(time.RFC3339), now),
		s.triple(entityID, "agent.lesson.summary", std.Text, now),
		s.triple(entityID, "agent.lesson.detail", detail, now),
		s.triple(entityID, "agent.lesson.injection-form", form, now),
	}
	for _, ev := range evidence {
		triples = append(triples, s.triple(entityID, "agent.lesson.evidence", ev, now))
	}
	for _, key := range appliesTo {
		triples = append(triples, s.triple(entityID, "agent.lesson.applies-to", key, now))
	}
	return entityID, triples, nil
}

// SyncAbsent handles a repo whose standards file is ABSENT at provision:
// retirement-only (the file is the law; no file, no law — the spec's
// deleted-file scenario). No source entity is minted for a nonexistent file,
// nothing births, and a repo that never declared standards is a no-op (no
// candidate resolves to it).
func (s *Syncer) SyncAbsent(ctx context.Context, repo string) (Result, error) {
	if err := s.validate(); err != nil {
		return Result{}, err
	}
	res := Result{}
	if err := s.retireRemoved(ctx, repo, map[string]bool{}, &res); err != nil {
		return Result{}, err
	}
	return res, nil
}

// validate rejects a partially-wired Syncer loudly at entry (semstreams-review
// M3): a nil Lister would silently disable retirement (fail-open) while a nil
// Resolver would panic mid-retirement — both are wiring bugs that must fail at
// the first sync, named, not degrade.
func (s *Syncer) validate() error {
	var missing []string
	for name, absent := range map[string]bool{
		"Store": s.Store == nil, "Curator": s.Curator == nil, "Creator": s.Creator == nil,
		"IsConflict": s.IsConflict == nil, "Lister": s.Lister == nil, "Resolver": s.Resolver == nil,
		"Now": s.Now == nil,
	} {
		if absent {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("standards: Syncer is missing %v — a partially-wired sync would fail open", missing)
	}
	if s.Org == "" || s.Platform == "" {
		return fmt.Errorf("standards: Syncer needs the platform identity (org=%q platform=%q)", s.Org, s.Platform)
	}
	return nil
}

// retireRemoved retires THIS repo's repo-standard records that the current file
// no longer declares (D5). Cross-repo isolation: a candidate retires only when
// its evidence resolves to a source entity whose declared repo matches; a
// candidate whose evidence genuinely does not exist is skipped loudly, never
// retired.
//
// A READ FAULT is not a skip. Collapsing "this evidence entity is absent" into
// "this read failed" would make a NATS blip on one evidence read leave a
// withdrawn standard active while the sync returned success — fail-OPEN on the
// one outcome retirement exists to produce, visible only as a WARN. So a real
// fault propagates as a substrate error: the station retries and, exhausted,
// parks toward the human (the same posture as maxRecordPages refusing to
// truncate a listing).
func (s *Syncer) retireRemoved(ctx context.Context, repo string, expected map[string]bool, res *Result) error {
	candidates, err := s.Lister.ListLessonRecords(ctx)
	if err != nil {
		return fmt.Errorf("standards: list records for retirement: %w", err)
	}
	// One read per DISTINCT source entity, not per candidate. Every record from one file
	// cites the same source, and foreign repos' records re-cite theirs on every pass, so
	// without this a deployment with R repos × N standards does R×N serial 5s-bounded
	// reads on the critical path of every provision to learn R answers.
	seen := map[string]sourceLookup{}
	for _, c := range candidates {
		if c.Category != standardCategory || expected[c.EntityID] {
			continue
		}
		if c.Status != "active" && c.Status != "proposed" {
			continue
		}
		matches, err := s.evidenceMatchesRepo(ctx, c, repo, seen)
		if err != nil {
			return fmt.Errorf("standards: resolve evidence of candidate %s: %w", c.EntityID, err)
		}
		if !matches {
			continue
		}
		if err := s.Curator.Retire(ctx, c.EntityID); err != nil {
			return fmt.Errorf("standards: retire removed record %s: %w", c.EntityID, err)
		}
		res.Retired++
	}
	return nil
}

// sourceLookup is one memoized evidence resolution. Faults are deliberately NOT cached:
// they abort the pass, so there is never a second question to answer.
type sourceLookup struct {
	repo  string
	found bool
}

// evidenceMatchesRepo reports whether any of the candidate's cited evidence resolves to
// a source entity declaring repo, resolving each distinct evidence entity at most once
// per pass via seen. An error means the resolution could not be COMPLETED — the caller
// must not read that as "not ours" (see retireRemoved).
func (s *Syncer) evidenceMatchesRepo(ctx context.Context, c CandidateRecord, repo string, seen map[string]sourceLookup) (bool, error) {
	for _, ev := range c.Evidence {
		hit, cached := seen[ev]
		if !cached {
			r, found, err := s.Resolver.SourceRepo(ctx, ev)
			if err != nil {
				return false, fmt.Errorf("read source repo of evidence %s: %w", ev, err)
			}
			hit = sourceLookup{repo: r, found: found}
			seen[ev] = hit
		}
		r, found := hit.repo, hit.found
		if !found {
			// Genuinely absent (or carrying no repo): out of scope, and the resolver has
			// already distinguished this from a fault. Skipping is right here — a foreign
			// writer's record must not enter this repo's retirement set.
			if s.Logger != nil {
				s.Logger.Warn("standards: candidate record's evidence names no source repo — treating it as out of scope",
					slog.String("record", c.EntityID), slog.String("evidence", ev))
			}
			continue
		}
		if r == repo {
			return true, nil
		}
	}
	return false, nil
}

// warnOnKBound warns when a role's declared set exceeds the substrate's
// per-brief record ceiling — the lowest-severity tail would drop silently at
// injection, so be loud at authoring time instead. NOTE (go-review L3): this
// counts only THIS file's declared set; the real ceiling is shared with every
// active in-scope lesson platform-wide (other repos' standards, any future
// lesson adoption), so the warning UNDERCOUNTS — absence of the warning is not
// proof of headroom.
func (s *Syncer) warnOnKBound(file File) {
	if s.Logger == nil {
		return
	}
	perRole := map[string]int{}
	for _, std := range file.Standards {
		for _, role := range std.Roles {
			perRole[role]++
		}
	}
	for role, n := range perRole {
		if n > injectionKBound {
			s.Logger.Warn("standards: a role's declared standards exceed the injection ceiling — the lowest-severity tail will not reach briefs",
				slog.String("role", role), slog.Int("declared", n), slog.Int("ceiling", injectionKBound))
		}
	}
}

func (s *Syncer) triple(subject, predicate string, object any, now time.Time) message.Triple {
	return message.Triple{
		Subject:    subject,
		Predicate:  predicate,
		Object:     object,
		Source:     Source,
		Timestamp:  now,
		Confidence: 1.0,
	}
}

// severityFor maps the RFC-2119 weight onto the substrate's closed enum:
// musts always outrank shoulds outrank mays at injection (severity-first
// ordering), so must→critical, should→warning, may→info.
func severityFor(sev Severity) string {
	switch sev {
	case SeverityMust:
		return "critical"
	case SeverityShould:
		return "warning"
	default:
		return "info"
	}
}

// lessonIdentity mirrors the framework store's canonical identity content
// (category, sorted applies-to, summary, sorted evidence — \x1f-delimited,
// sets \x1e-joined) under the semdev-standards namespace, so the store's
// EntityExists identity verification holds for our records.
func lessonIdentity(category string, appliesTo []string, summary string, evidence []string) string {
	a := slices.Clone(appliesTo)
	e := slices.Clone(evidence)
	slices.Sort(a)
	slices.Sort(e)
	content := "category=" + category + "\x1f" +
		"applies_to=" + strings.Join(a, "\x1e") + "\x1f" +
		"summary=" + summary + "\x1f" +
		"evidence=" + strings.Join(e, "\x1e")
	return uuid.NewSHA1(standardsNamespaceUUID, []byte(content)).String()
}
