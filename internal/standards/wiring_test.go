package standards

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/vocab"
)

// fullWiring is a Wiring whose every field is populated well enough that construction
// SUCCEEDS, so each case can mutate exactly one field and reach the guard it targets.
//
// No connection is involved: contract validation is purely local (ADR-091), so a bare
// non-nil *natsclient.Client builds a real *graphown.Clients with real lesson surfaces.
func fullWiring(t *testing.T) Wiring {
	t.Helper()
	vocab.Register() // contract validation requires the declared vocabulary; idempotent
	clients, err := graphown.NewClients(&natsclient.Client{})
	if err != nil {
		t.Fatalf("build graph clients for the wiring fixture: %v", err)
	}
	return Wiring{
		NATS:      &natsclient.Client{},
		Clients:   clients,
		Reader:    stubReader{},
		Org:       "org",
		Platform:  "plat",
		Snapshots: NewSnapshots(),
		Logger:    slog.Default(),
	}
}

// TestNewProvisionSyncAcceptsAFullyWiredSurfaceSet is the positive control the guard table
// needs. Without it a guard that fired unconditionally would satisfy every negative case,
// and the table would report health while construction was simply broken.
func TestNewProvisionSyncAcceptsAFullyWiredSurfaceSet(t *testing.T) {
	got, err := NewProvisionSync(fullWiring(t))
	if err != nil {
		t.Fatalf("a fully-wired surface set must construct: %v", err)
	}
	if got == nil || got.Syncer == nil || got.Repos == nil {
		t.Fatalf("construction returned an incomplete sync: %+v", got)
	}
}

// TestNewProvisionSyncRejectsNilSurfacesLoudly is the R2 requirement. The framework's
// NewLessonCurator accepts nil surfaces WITHOUT checking and would panic at the first
// Promote — deep inside a provision handler, on a run that was otherwise healthy. And
// NewNATSLessonStore swallows its own client-construction error (upstream ask U4), so a
// nil client yields a store that fails per-call rather than at build. Both make boot the
// only honest place to fail.
//
// Each case mutates ONE field of a fully-populated Wiring and asserts the message that
// guard produces. An earlier version of this test left NATS unset in every case, so every
// case tripped the FIRST guard and passed on its message — five of the six guards had no
// coverage at all and could have been deleted with the test still green. Asserting the
// guard-specific substring is what makes each case bite.
func TestNewProvisionSyncRejectsNilSurfacesLoudly(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Wiring)
		wantMsg string
	}{
		{"nil NATS client", func(w *Wiring) { w.NATS = nil }, "live NATS client"},
		{"nil graph clients", func(w *Wiring) { w.Clients = nil }, "graph mutation clients"},
		{"nil reader", func(w *Wiring) { w.Reader = nil }, "fact reader"},
		{"empty org", func(w *Wiring) { w.Org = "" }, "platform identity"},
		{"empty platform", func(w *Wiring) { w.Platform = "" }, "platform identity"},
		{"dotted org breaks the entity ID", func(w *Wiring) { w.Org = "acme.corp" }, "cannot form a lesson-record entity ID"},
		// Without the shared capture store the floors-time checks lane has nothing to gate
		// on and faults every run — a boot-time error, not a per-run surprise.
		{"nil snapshot store", func(w *Wiring) { w.Snapshots = nil }, "snapshot store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := fullWiring(t)
			tc.mutate(&w)
			got, err := NewProvisionSync(w)
			if err == nil {
				t.Fatalf("a partially-wired standards sync must fail at construction, got %+v", got)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not name the guard this case is about (%q) — the case is passing on\n"+
					"some OTHER guard's message, which means the guard it targets is untested", err, tc.wantMsg)
			}
		})
	}
}

// TestNewProvisionSyncRejectsAWiringMissingLessonSurfaces covers the guard that exists
// because the framework's curator does NOT check: a Clients whose lesson surfaces are
// unavailable would be accepted by NewLessonCurator and panic at the first Promote.
func TestNewProvisionSyncRejectsAWiringMissingLessonSurfaces(t *testing.T) {
	w := fullWiring(t)
	// A zero-value *graphown.Clients has no underlying mutation client, so LessonSurfaces
	// returns the nil pair on its census path — exactly the shape that must not reach the
	// curator.
	w.Clients = &graphown.Clients{}
	_, err := NewProvisionSync(w)
	if err == nil {
		t.Fatal("a Clients with no lesson surfaces was accepted — the curator would take the nil pair " +
			"without checking and panic at the first promotion, mid-provision")
	}
	if !strings.Contains(err.Error(), "lesson surfaces") && !strings.Contains(err.Error(), "strict-create") {
		t.Errorf("the error must name the unavailable surface; got %v", err)
	}
}
