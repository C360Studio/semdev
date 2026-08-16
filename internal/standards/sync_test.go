package standards

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"
)

// ---- fakes ----

type fakeStore struct {
	created  map[string][]message.Triple // entityID -> triples of the CREATING call
	existed  map[string]bool             // entityIDs that pre-exist (created=false)
	statuses map[string]string           // explicit status overrides for existed ids
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		created:  map[string][]message.Triple{},
		existed:  map[string]bool{},
		statuses: map[string]string{},
	}
}

func (f *fakeStore) CreateLesson(_ context.Context, entityID string, _ message.Type, triples []message.Triple) (bool, error) {
	if f.existed[entityID] {
		return false, nil
	}
	if _, dup := f.created[entityID]; dup {
		return false, nil
	}
	f.created[entityID] = triples
	return true, nil
}

func (f *fakeStore) ReadLessonStatus(_ context.Context, entityID string) (string, bool, error) {
	if st, ok := f.statuses[entityID]; ok {
		return st, true, nil
	}
	if f.existed[entityID] {
		return "active", true, nil
	}
	if _, ok := f.created[entityID]; ok {
		return "proposed", true, nil
	}
	return "", false, nil
}

type fakeCurator struct {
	promoted []string
	retired  []string
}

func (f *fakeCurator) Promote(_ context.Context, id string) error {
	f.promoted = append(f.promoted, id)
	return nil
}

func (f *fakeCurator) Retire(_ context.Context, id string) error {
	f.retired = append(f.retired, id)
	return nil
}

type fakeCreator struct {
	created  []string
	conflict map[string]bool // entityIDs whose Create reports the duplicate signal
}

var errDuplicate = errors.New("strict-create conflict (duplicate)")

func (f *fakeCreator) Create(_ context.Context, entityID string, _ message.Type, _ []message.Triple) error {
	if f.conflict[entityID] {
		return errDuplicate
	}
	f.created = append(f.created, entityID)
	return nil
}

type fakeLister struct{ records []CandidateRecord }

func (f *fakeLister) ListLessonRecords(context.Context) ([]CandidateRecord, error) {
	return f.records, nil
}

type fakeResolver struct{ repos map[string]string } // sourceEntityID -> repo

func (f *fakeResolver) SourceRepo(_ context.Context, id string) (string, bool, error) {
	r, ok := f.repos[id]
	return r, ok, nil
}

func newSyncer(store *fakeStore, cur *fakeCurator, cr *fakeCreator, l *fakeLister, r *fakeResolver) *Syncer {
	return &Syncer{
		Store:      store,
		Curator:    cur,
		Creator:    cr,
		IsConflict: func(err error) bool { return errors.Is(err, errDuplicate) },
		Lister:     l,
		Resolver:   r,
		Org:        "c360",
		Platform:   "semdev",
		Logger:     slog.Default(),
		Now:        func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) },
	}
}

const syncRepo = "C360Studio/target"

var validYAML = []byte("version: 1\nstandards:\n" +
	"  - id: eng-tests\n    text: \"Tests trace to scenarios.\"\n    severity: must\n    roles: [developer]\n" +
	"  - id: rev-cite\n    text: \"Cite evidence in findings.\"\n    severity: should\n    roles: [reviewer]\n")

// (b) a new standard births with the exact D3 mapping and is promoted.
func TestSyncBirthsAndPromotesWithMapping(t *testing.T) {
	store, cur, cr := newFakeStore(), &fakeCurator{}, &fakeCreator{}
	s := newSyncer(store, cur, cr, &fakeLister{}, &fakeResolver{})

	res, err := s.Sync(context.Background(), validYAML, syncRepo)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Born != 2 || res.Promoted != 2 || res.Retired != 0 {
		t.Errorf("result = %+v, want 2 born, 2 promoted", res)
	}
	if len(cr.created) != 1 || !strings.Contains(cr.created[0], "repo.standards.source.") {
		t.Fatalf("source entity not created: %v", cr.created)
	}
	if res.SourceEntityID != cr.created[0] {
		t.Errorf("result source = %q, want the created source %q", res.SourceEntityID, cr.created[0])
	}

	// Every born record was promoted, and only born records were promoted.
	if len(cur.promoted) != 2 {
		t.Fatalf("promoted %v, want exactly the two born records", cur.promoted)
	}
	for _, id := range cur.promoted {
		if _, ok := store.created[id]; !ok {
			t.Errorf("promoted %q which was not born by this sync", id)
		}
	}

	// The D3 mapping on the developer-scoped must.
	var devTriples []message.Triple
	for id, triples := range store.created {
		if !strings.HasPrefix(id, "c360.semdev.agent.lesson.record.") {
			t.Fatalf("record id %q lacks the lesson-record prefix", id)
		}
		for _, tr := range triples {
			if tr.Predicate == "agent.lesson.summary" && tr.Object == "Tests trace to scenarios." {
				devTriples = triples
			}
		}
	}
	if devTriples == nil {
		t.Fatal("the developer standard's record was not born")
	}
	want := map[string]any{
		"agent.lesson.category":       "repo-standard",
		"agent.lesson.polarity":       "best_practice",
		"agent.lesson.severity":       "critical", // must → critical
		"agent.lesson.status":         "proposed",
		"agent.lesson.injection-form": "[std:eng-tests] MUST Tests trace to scenarios.",
		"agent.lesson.applies-to":     "tag:developer",
	}
	got := map[string]any{}
	var evidence string
	for _, tr := range devTriples {
		if _, tracked := want[tr.Predicate]; tracked {
			got[tr.Predicate] = tr.Object
		}
		if tr.Predicate == "agent.lesson.evidence" {
			evidence, _ = tr.Object.(string)
		}
		if tr.Source != "standards-sync" {
			t.Errorf("triple %s carries Source %q, want standards-sync", tr.Predicate, tr.Source)
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if evidence != res.SourceEntityID {
		t.Errorf("evidence = %q, want the source entity %q", evidence, res.SourceEntityID)
	}
}

// (a) unchanged file re-sync is a no-op: same source entity (conflict = the
// duplicate signal), created=false births, promotion still converges, nothing
// retired.
func TestSyncIdempotentOnUnchangedFile(t *testing.T) {
	store, cur, cr := newFakeStore(), &fakeCurator{}, &fakeCreator{}
	s := newSyncer(store, cur, cr, &fakeLister{}, &fakeResolver{})

	first, err := s.Sync(context.Background(), validYAML, syncRepo)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// Second pass: the source entity now conflicts; records pre-exist ACTIVE.
	cr.conflict = map[string]bool{first.SourceEntityID: true}
	for id := range store.created {
		store.existed[id] = true // go-review L1: accumulate, never reassign
	}

	promotedBefore := len(cur.promoted)
	second, err := s.Sync(context.Background(), validYAML, syncRepo)
	if err != nil {
		t.Fatalf("re-sync of an unchanged file must succeed (the duplicate signal is idempotency, not failure): %v", err)
	}
	if second.SourceEntityID != first.SourceEntityID {
		t.Errorf("source entity changed across identical syncs: %q vs %q", second.SourceEntityID, first.SourceEntityID)
	}
	if second.Born != 0 {
		t.Errorf("re-sync born = %d, want 0 (records pre-exist)", second.Born)
	}
	if second.Retired != 0 {
		t.Errorf("re-sync retired = %d, want 0", second.Retired)
	}
	// The steady state issues NO lifecycle writes (semstreams-review M1): the
	// records are already active, so re-sync promotes nothing.
	if len(cur.promoted) != promotedBefore {
		t.Errorf("re-sync of active records issued %d promote calls, want 0", len(cur.promoted)-promotedBefore)
	}
}

// semstreams-review M1: a standard REMOVED (retired) and later RE-DECLARED by
// reverting the file dedups onto the retired record — the sync must resurrect
// it (the re-committed file is the same human gate), never leave the repo's
// declared law permanently inactive.
func TestSyncResurrectsRedeclaredRetiredStandard(t *testing.T) {
	store, cur, cr := newFakeStore(), &fakeCurator{}, &fakeCreator{}
	s := newSyncer(store, cur, cr, &fakeLister{}, &fakeResolver{})

	first, err := s.Sync(context.Background(), validYAML, syncRepo)
	if err != nil {
		t.Fatal(err)
	}
	// The records were later retired (the standard was removed in between).
	var ids []string
	for id := range store.created {
		ids = append(ids, id)
		store.existed[id] = true
		store.statuses[id] = "retired"
	}
	cr.conflict = map[string]bool{first.SourceEntityID: true}
	cur.promoted = nil

	res, err := s.Sync(context.Background(), validYAML, syncRepo)
	if err != nil {
		t.Fatal(err)
	}
	if res.Born != 0 {
		t.Errorf("resurrection born = %d, want 0 (dedup onto the retired records)", res.Born)
	}
	if len(cur.promoted) != len(ids) {
		t.Fatalf("promoted %v, want every retired-but-redeclared record resurrected", cur.promoted)
	}
}

// (c) an oversized standard fails the whole sync closed naming the id — no
// partial birth. (The parser enforces the bound, so the pin drives Sync with
// the oversize declared mid-file and asserts nothing was written.)
func TestSyncOversizedStandardFailsClosed(t *testing.T) {
	store, cur, cr := newFakeStore(), &fakeCurator{}, &fakeCreator{}
	s := newSyncer(store, cur, cr, &fakeLister{}, &fakeResolver{})

	yaml := "version: 1\nstandards:\n" +
		"  - id: fine\n    text: ok\n    severity: may\n" +
		"  - id: way-too-long\n    text: \"" + strings.Repeat("x", 400) + "\"\n    severity: must\n"
	_, err := s.Sync(context.Background(), []byte(yaml), syncRepo)
	if err == nil {
		t.Fatal("an over-bound standard must fail the sync closed")
	}
	if !strings.Contains(err.Error(), "way-too-long") {
		t.Errorf("rejection %q does not name the standard id", err)
	}
	if len(store.created) != 0 || len(cr.created) != 0 || len(cur.promoted) != 0 {
		t.Error("a rejected file must birth nothing (no partial sync)")
	}
}

// (d) a standard removed from the file retires; an edited standard is a
// retire-old + birth-new (content-derived identity).
func TestSyncRetiresRemovedAndEditedStandards(t *testing.T) {
	store, cur, cr := newFakeStore(), &fakeCurator{}, &fakeCreator{}
	s := newSyncer(store, cur, cr, &fakeLister{}, &fakeResolver{})

	first, err := s.Sync(context.Background(), validYAML, syncRepo)
	if err != nil {
		t.Fatal(err)
	}
	var oldIDs []string
	for id := range store.created {
		oldIDs = append(oldIDs, id)
	}

	// The next revision: eng-tests EDITED (new text), rev-cite REMOVED.
	edited := []byte("version: 1\nstandards:\n" +
		"  - id: eng-tests\n    text: \"Tests trace to scenarios BY ID.\"\n    severity: must\n    roles: [developer]\n")
	lister := &fakeLister{}
	resolver := &fakeResolver{repos: map[string]string{first.SourceEntityID: syncRepo}}
	for _, id := range oldIDs {
		lister.records = append(lister.records, CandidateRecord{
			EntityID: id, Category: "repo-standard", Status: "active",
			Evidence: []string{first.SourceEntityID},
		})
	}
	s2 := newSyncer(store, cur, cr, lister, resolver)

	res, err := s2.Sync(context.Background(), edited, syncRepo)
	if err != nil {
		t.Fatal(err)
	}
	if res.Born != 1 {
		t.Errorf("edited revision born = %d, want 1 (the new content identity)", res.Born)
	}
	if len(cur.retired) != 2 {
		t.Errorf("retired %v, want BOTH old records (the removed one and the edited-away identity)", cur.retired)
	}
	for _, id := range cur.retired {
		found := false
		for _, old := range oldIDs {
			if id == old {
				found = true
			}
		}
		if !found {
			t.Errorf("retired %q, which is not an old record", id)
		}
	}
}

// (e) cross-repo isolation: a repo-standard record whose source resolves to a
// DIFFERENT repo is never retired; unresolvable evidence is skipped, not retired.
func TestSyncNeverRetiresAcrossRepos(t *testing.T) {
	store, cur, cr := newFakeStore(), &fakeCurator{}, &fakeCreator{}
	lister := &fakeLister{records: []CandidateRecord{
		{EntityID: "c360.semdev.agent.lesson.record.other-repo", Category: "repo-standard",
			Status: "active", Evidence: []string{"c360.semdev.repo.standards.source.aaaa"}},
		{EntityID: "c360.semdev.agent.lesson.record.unresolvable", Category: "repo-standard",
			Status: "active", Evidence: []string{"c360.semdev.repo.standards.source.gone"}},
		{EntityID: "c360.semdev.agent.lesson.record.not-a-standard", Category: "debug-insight",
			Status: "active", Evidence: []string{"c360.semdev.agent.chain.execution.run1"}},
	}}
	resolver := &fakeResolver{repos: map[string]string{
		"c360.semdev.repo.standards.source.aaaa": "SomeoneElse/other",
	}}
	s := newSyncer(store, cur, cr, lister, resolver)

	if _, err := s.Sync(context.Background(), validYAML, syncRepo); err != nil {
		t.Fatal(err)
	}
	if len(cur.retired) != 0 {
		t.Errorf("retired %v — another repo's standards (or non-standards, or unresolvable evidence) must never be touched", cur.retired)
	}
}

// The deleted-file scenario: SyncAbsent retires THIS repo's records (the file
// is the law), touches no other repo's, and is a no-op for a repo that never
// declared standards.
func TestSyncAbsentRetiresOnlyThisRepo(t *testing.T) {
	store, cur, cr := newFakeStore(), &fakeCurator{}, &fakeCreator{}
	lister := &fakeLister{records: []CandidateRecord{
		{EntityID: "c360.semdev.agent.lesson.record.mine", Category: "repo-standard",
			Status: "active", Evidence: []string{"c360.semdev.repo.standards.source.mmmm"}},
		{EntityID: "c360.semdev.agent.lesson.record.other", Category: "repo-standard",
			Status: "active", Evidence: []string{"c360.semdev.repo.standards.source.oooo"}},
	}}
	resolver := &fakeResolver{repos: map[string]string{
		"c360.semdev.repo.standards.source.mmmm": syncRepo,
		"c360.semdev.repo.standards.source.oooo": "SomeoneElse/other",
	}}
	s := newSyncer(store, cur, cr, lister, resolver)

	res, err := s.SyncAbsent(context.Background(), syncRepo)
	if err != nil {
		t.Fatal(err)
	}
	if res.Retired != 1 || len(cur.retired) != 1 || cur.retired[0] != "c360.semdev.agent.lesson.record.mine" {
		t.Errorf("SyncAbsent retired %v (count %d), want exactly this repo's record", cur.retired, res.Retired)
	}
	if res.Born != 0 || res.Promoted != 0 || len(cr.created) != 0 {
		t.Errorf("SyncAbsent must birth and promote nothing: %+v", res)
	}

	// A repo that never declared standards: nothing matches, nothing retires.
	cur2 := &fakeCurator{}
	s2 := newSyncer(store, cur2, cr, lister, resolver)
	if res2, err := s2.SyncAbsent(context.Background(), "Fresh/never-declared"); err != nil || res2.Retired != 0 || len(cur2.retired) != 0 {
		t.Errorf("never-declared repo must be a no-op: %+v err=%v", res2, err)
	}
}

// (f) a non-file proposed lesson is never promoted: promotion is scoped to the
// records this sync just ensured, never a list-and-promote.
func TestSyncNeverPromotesForeignLessons(t *testing.T) {
	store, cur, cr := newFakeStore(), &fakeCurator{}, &fakeCreator{}
	lister := &fakeLister{records: []CandidateRecord{
		{EntityID: "c360.semdev.agent.lesson.record.agent-emitted", Category: "workflow-insight",
			Status: "proposed", Evidence: []string{"c360.semdev.agent.chain.execution.run1"}},
	}}
	s := newSyncer(store, cur, cr, lister, &fakeResolver{})

	if _, err := s.Sync(context.Background(), validYAML, syncRepo); err != nil {
		t.Fatal(err)
	}
	for _, id := range cur.promoted {
		if id == "c360.semdev.agent.lesson.record.agent-emitted" {
			t.Fatal("the sync promoted a lesson it did not birth from the file — the auto-promotion policy leaked")
		}
	}
}
