package standards

// Production construction for the provision-time standards sync (D4 boot wiring).

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/natsclient"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/graphown"
)

// Wiring is the set of live surfaces the production sync binds over.
type Wiring struct {
	NATS     *natsclient.Client
	Clients  *graphown.Clients
	Reader   changefacts.Reader
	Org      string
	Platform string
	// Snapshots is the SHARED provision-time capture store the floors-time checks lane
	// reads. The same instance must reach both stations, or the lane faults every run.
	Snapshots *Snapshots
	Logger    *slog.Logger
}

// NewProvisionSync builds the production standards sync, rejecting a partially-wired
// surface set at CONSTRUCTION.
//
// The loudness is not defensive habit; both framework constructors below fail late and
// quietly on their own. agentictools.NewLessonCurator takes its writer and reader
// without checking either, so a nil surface panics at the first Promote — inside a
// provision handler, on an otherwise healthy run. agentictools.NewNATSLessonStore
// discards its client-construction error entirely (filed upstream as U4), so a nil
// client yields a store that fails once per call forever. Neither failure names the
// wiring bug that caused it, so boot is the last place the truth is still cheap.
func NewProvisionSync(w Wiring) (*ProvisionSync, error) {
	if w.NATS == nil {
		return nil, fmt.Errorf("standards: a live NATS client is required (the lesson store and the record listing both ride it)")
	}
	if w.Clients == nil {
		return nil, fmt.Errorf("standards: the graph mutation clients are required (the curator and the source-entity creator bind through them)")
	}
	if w.Reader == nil {
		return nil, fmt.Errorf("standards: a fact reader is required (the run's target coordinate and evidence resolution both read through it)")
	}
	if w.Org == "" || w.Platform == "" {
		return nil, fmt.Errorf("standards: platform identity is required (org=%q platform=%q) — it forms the entity namespace every record and source is born into", w.Org, w.Platform)
	}
	// Non-empty is not enough: an org or platform carrying a dot (or any character outside
	// the entity-ID alphabet) makes agentic.AgentLessonEntityID PANIC — and it would do so
	// inside the provision station handler, on the first run of an otherwise clean boot.
	// NewRecordLister below runs the framework's own validation on these exact parts, so
	// constructing the lister here converts that into a boot-time verdict on the operator's
	// config, which is where a bad platform identity is still cheap to see.
	if err := validateIdentityParts(w.Org, w.Platform); err != nil {
		return nil, err
	}

	lister, err := newRecordListerChecked(w.NATS, w.Org, w.Platform)
	if err != nil {
		return nil, err
	}

	reconciler, authority := w.Clients.LessonSurfaces()
	if reconciler == nil || authority == nil {
		return nil, fmt.Errorf("standards: the lesson surfaces are unavailable (reconciler=%t authority=%t) — the curator would accept them and panic at the first promotion",
			reconciler != nil, authority != nil)
	}
	creator := w.Clients.Creator(Source)
	if creator == nil {
		return nil, fmt.Errorf("standards: no strict-create surface for owner %q — the source entity every record cites as evidence could not be born", Source)
	}

	logger := w.Logger
	if logger == nil {
		logger = slog.Default()
	}

	syncer := &Syncer{
		Store:      agentictools.NewNATSLessonStore(w.NATS),
		Curator:    agentictools.NewLessonCurator(reconciler, authority, logger),
		Creator:    creator,
		IsConflict: graphown.IsConflict,
		Lister:     lister,
		Resolver:   NewEvidenceResolver(w.Reader),
		Org:        w.Org,
		Platform:   w.Platform,
		Logger:     logger,
		Now:        time.Now,
	}
	if w.Snapshots == nil {
		return nil, fmt.Errorf("standards: the shared provision-time snapshot store is required — without it the " +
			"floors-time checks lane has no captured law and faults every run")
	}
	return &ProvisionSync{Syncer: syncer, Repos: NewRepoResolver(w.Reader), Snapshots: w.Snapshots, Logger: logger}, nil
}

// validateIdentityParts rejects an org/platform that cannot form an entity ID, by asking
// the framework rather than re-deriving its alphabet. AgentLessonRecordPrefix panics on a
// bad part, so this recovers it into an error the caller can report at boot.
func validateIdentityParts(org, platform string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("standards: platform identity %q/%q cannot form a lesson-record entity ID: %v", org, platform, r)
		}
	}()
	_ = agentic.AgentLessonRecordPrefix(org, platform)
	return nil
}

// newRecordListerChecked builds the lister after the identity is known good, so the
// panic-on-bad-identity path is unreachable from here.
func newRecordListerChecked(nats *natsclient.Client, org, platform string) (RecordLister, error) {
	if err := validateIdentityParts(org, platform); err != nil {
		return nil, err
	}
	return NewRecordLister(nats, org, platform), nil
}
