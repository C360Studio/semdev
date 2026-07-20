package conversationchannel

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/forge/conversation"
)

// fakeLister scripts the awaiting-approval enumeration (refs, or a transport error).
type fakeLister struct {
	refs  []string
	err   error
	calls int
}

func (f *fakeLister) ListRunsAwaitingApproval(context.Context, string) ([]string, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.refs, nil
}

func approveComment(id string) conversation.Message {
	return conversation.Message{ID: id, Author: "cglusky", Body: "/semdev approve"}
}

// TestPollerReadsAwaitingApprovalRunsAndReleasesGate pins the poll loop
// (pull-first-transport D2/D5): each tick enumerates awaiting-approval runs, Reads
// each thread, feeds fresh Messages to the shared core; a second tick with NO new
// comments keeps the cursor STABLE and does NOT re-feed (M2); a restart (fresh
// poller, empty cursor) re-reads and re-applies as a no-op (idempotent at the core).
func TestPollerReadsAwaitingApprovalRunsAndReleasesGate(t *testing.T) {
	ctx := context.Background()
	thread := conversation.ThreadRef("acme/widgets#1")
	ch := &fakeChannel{reads: map[conversation.ThreadRef][]conversation.Message{
		thread: {approveComment("100")},
	}}
	lister := &fakeLister{refs: []string{"acme/widgets#1"}}

	var handled []conversation.Message
	handle := func(_ context.Context, msg conversation.Message, _ conversation.ThreadRef) error {
		handled = append(handled, msg)
		return nil
	}
	p := newPoller(ch, lister, handle, "acme/widgets", time.Minute, slog.Default())

	// Tick 1: enumerate → read → feed the approve to the core.
	p.tick(ctx)
	if len(handled) != 1 || handled[0].Body != "/semdev approve" {
		t.Fatalf("tick 1 fed %v, want exactly the /semdev approve message", handled)
	}

	// Tick 2: no new comments → cursor stable, NO re-feed (M2). Read is still called
	// each tick (polling is unconditional), but returns empty against the max cursor.
	p.tick(ctx)
	if len(handled) != 1 {
		t.Errorf("tick 2 re-fed an already-seen approve (%d handled); the cursor must be stable (M2)", len(handled))
	}
	if len(ch.readLog) != 2 {
		t.Errorf("Read should be called once per tick (%d), even when it returns empty", len(ch.readLog))
	}

	// Restart: a fresh poller has an empty cursor map, re-reads from the top, and
	// re-feeds the approve — inert at the real core (alreadyApproved), proving
	// correctness comes from idempotency, not cursor durability (D5).
	handled = nil
	p2 := newPoller(ch, lister, handle, "acme/widgets", time.Minute, slog.Default())
	p2.tick(ctx)
	if len(handled) != 1 {
		t.Errorf("restart re-read fed %d messages, want 1 (idempotent re-apply)", len(handled))
	}
}

// TestPollerEnumerateErrorIsRetriedNotIdle pins H2 at the loop: an enumeration
// TRANSPORT ERROR is logged + retried next tick (the loop survives), NOT conflated
// with an empty result — a later successful tick still releases the gate.
func TestPollerEnumerateErrorIsRetriedNotIdle(t *testing.T) {
	ctx := context.Background()
	thread := conversation.ThreadRef("acme/widgets#1")
	ch := &fakeChannel{reads: map[conversation.ThreadRef][]conversation.Message{thread: {approveComment("100")}}}
	lister := &fakeLister{err: errors.New("graph classified fault")}
	var handled int
	handle := func(context.Context, conversation.Message, conversation.ThreadRef) error { handled++; return nil }
	p := newPoller(ch, lister, handle, "acme/widgets", time.Minute, slog.Default())

	// Faulted tick: no reads, no handles, loop does not crash.
	p.tick(ctx)
	if len(ch.readLog) != 0 || handled != 0 {
		t.Fatalf("faulted enumerate must not read/handle: reads=%d handled=%d", len(ch.readLog), handled)
	}

	// Recovery tick: enumeration succeeds → the gate is released.
	lister.err = nil
	lister.refs = []string{"acme/widgets#1"}
	p.tick(ctx)
	if handled != 1 {
		t.Errorf("recovery tick handled %d, want 1 (the fault was transient, not 'nothing awaiting')", handled)
	}
}

// TestPollerReadErrorKeepsCursorAndRetries pins that a Read TRANSPORT ERROR keeps
// the thread's cursor (not conflated with empty) so the approve is re-read next tick.
func TestPollerReadErrorKeepsCursorAndRetries(t *testing.T) {
	ctx := context.Background()
	thread := conversation.ThreadRef("acme/widgets#1")
	ch := &fakeChannel{
		reads:   map[conversation.ThreadRef][]conversation.Message{thread: {approveComment("100")}},
		readErr: errors.New("502 bad gateway"),
	}
	lister := &fakeLister{refs: []string{"acme/widgets#1"}}
	var handled int
	handle := func(context.Context, conversation.Message, conversation.ThreadRef) error { handled++; return nil }
	p := newPoller(ch, lister, handle, "acme/widgets", time.Minute, slog.Default())

	p.tick(ctx) // Read fails → no handle, cursor untouched
	if handled != 0 {
		t.Fatalf("a Read error must not release the gate (handled=%d)", handled)
	}
	ch.readErr = nil
	p.tick(ctx) // Read succeeds from the SAME (empty) cursor → the approve lands
	if handled != 1 {
		t.Errorf("after the Read recovered, handled=%d, want 1 (the cursor was kept, not lost)", handled)
	}
}

// TestPollerAdvancesCursorOnlyAfterHandleSucceeds pins that a transient HANDLE
// fault does not advance the cursor past the unhandled message — it is re-read next
// tick (the core's alreadyApproved makes an eventual double-apply inert).
func TestPollerAdvancesCursorOnlyAfterHandleSucceeds(t *testing.T) {
	ctx := context.Background()
	thread := conversation.ThreadRef("acme/widgets#1")
	ch := &fakeChannel{reads: map[conversation.ThreadRef][]conversation.Message{thread: {approveComment("100")}}}
	lister := &fakeLister{refs: []string{"acme/widgets#1"}}
	attempts := 0
	failFirst := true
	handle := func(context.Context, conversation.Message, conversation.ThreadRef) error {
		attempts++
		if failFirst {
			failFirst = false
			return errors.New("resolve raced the mint")
		}
		return nil
	}
	p := newPoller(ch, lister, handle, "acme/widgets", time.Minute, slog.Default())

	p.tick(ctx) // handle fails → cursor NOT advanced
	p.tick(ctx) // re-read (cursor still empty) → handle succeeds
	if attempts != 2 {
		t.Errorf("handle attempts = %d, want 2 (a failed handle must not advance the cursor past the message)", attempts)
	}
}

// TestPollerPrunesCursorsToAwaitingSet pins L2: a thread that leaves the
// awaiting-approval set (approved) has its cursor pruned, so the map does not grow
// unbounded across run lifetimes.
func TestPollerPrunesCursorsToAwaitingSet(t *testing.T) {
	ctx := context.Background()
	t1 := conversation.ThreadRef("acme/widgets#1")
	t2 := conversation.ThreadRef("acme/widgets#2")
	ch := &fakeChannel{reads: map[conversation.ThreadRef][]conversation.Message{
		t1: {approveComment("10")},
		t2: {approveComment("20")},
	}}
	lister := &fakeLister{refs: []string{"acme/widgets#1", "acme/widgets#2"}}
	handle := func(context.Context, conversation.Message, conversation.ThreadRef) error { return nil }
	p := newPoller(ch, lister, handle, "acme/widgets", time.Minute, slog.Default())

	p.tick(ctx)
	if len(p.cursors) != 2 {
		t.Fatalf("after tick 1 the cursor map has %d entries, want 2", len(p.cursors))
	}
	// #2 is approved and leaves the awaiting set; its cursor must be pruned.
	lister.refs = []string{"acme/widgets#1"}
	p.tick(ctx)
	if _, ok := p.cursors[t2]; ok {
		t.Errorf("cursor for the no-longer-awaiting thread %q was not pruned (L2 unbounded growth)", t2)
	}
	if _, ok := p.cursors[t1]; !ok {
		t.Errorf("cursor for the still-awaiting thread %q was wrongly pruned", t1)
	}
}

// TestPollerRunExitsOnContextCancel pins M5: run is bound to a context the
// component cancels on Stop; cancelling it exits the goroutine (no leak).
func TestPollerRunExitsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := func(context.Context, conversation.Message, conversation.ThreadRef) error { return nil }
	p := newPoller(&fakeChannel{}, &fakeLister{}, handle, "", 10*time.Millisecond, slog.Default())

	done := make(chan struct{})
	go func() { p.run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller.run did not exit after its context was cancelled (M5 — the goroutine would leak)")
	}
}
