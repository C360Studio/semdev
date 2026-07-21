package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/vocab"
)

// The G5 single-writer PIVOT (conversation-channel-seam D5): human.opt.signal is
// written by the channel-neutral `conversation-adapter`, NOT the GitHub-specific
// `comment-adapter` — so N channels stay G5-legal (one writer, impls behind it).
// Its capability home, and run.change.decision's, move from `forge-io` to
// `conversation-channel` to match where their production is now spec'd (G10 census
// coherence). run.change.decision's WRITER stays `approval-adapter` (read by
// run-lifecycle/02 — a paper cap-tag move, not a writer change). This pin flips red
// until the internal/vocab reassignment lands, and guards against a re-coupling.
func TestConversationChannelVocabReassignment(t *testing.T) {
	byName := make(map[string]vocab.Predicate, len(vocab.Predicates))
	for _, p := range vocab.Predicates {
		byName[p.Name] = p
	}

	optSignal, ok := byName["human.opt.signal"]
	if !ok {
		t.Fatal("human.opt.signal absent from the vocab census")
	}
	if optSignal.Writer != "conversation-adapter" {
		t.Errorf("human.opt.signal writer = %q, want conversation-adapter (the channel-neutral G5 writer, not comment-adapter)", optSignal.Writer)
	}
	if optSignal.Capability != "conversation-channel" {
		t.Errorf("human.opt.signal capability = %q, want conversation-channel", optSignal.Capability)
	}

	approved, ok := byName["run.change.decision"]
	if !ok {
		t.Fatal("run.change.decision absent from the vocab census")
	}
	if approved.Capability != "conversation-channel" {
		t.Errorf("run.change.decision capability = %q, want conversation-channel (spec'd home moved)", approved.Capability)
	}
	if approved.Writer != "approval-adapter" {
		t.Errorf("run.change.decision writer = %q, want approval-adapter UNCHANGED (read by run-lifecycle/02)", approved.Writer)
	}
}

// The conversation seam's EXPORTED surface must never name a githubwebhook type —
// the whole point of the carve (conversation-channel-seam D2/D4). The GitHub impl's
// CommentEvent→Message normalize is legitimately host-specific, but it is
// UNEXPORTED and contained; the port, the neutral Message/ThreadRef, and every
// exported func/field stay channel-neutral so a second channel plugs in behind the
// same public API. A host type reaching an exported signature fails the build.
//
// Scope: this guards the githubwebhook shape SPECIFICALLY (the named coupling
// point 2 — the CommentEvent the carve dissolves), resolving the import's local
// name so an alias cannot evade it. It is not a general all-host-packages proof.
func TestConversationExportedSurfaceIsHostNeutral(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal/forge/conversation")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		leaks, err := exportedTypeLeaks(src, "githubwebhook")
		if err != nil {
			t.Fatalf("scan %s: %v", e.Name(), err)
		}
		for _, l := range leaks {
			t.Errorf("%s: exported surface names %q — the conversation port must stay channel-neutral (contain the host normalize in an UNEXPORTED func)", e.Name(), l)
		}
		scanned++
	}
	if scanned == 0 {
		t.Fatal("scanned no conversation source files; the host-neutrality pin would pass vacuously")
	}
}

// Red-first: the exported-surface scanner must FIRE on a host type reaching an
// exported field or param (even via an alias, a dot-import, or an inferred var
// type), and stay QUIET when the same host type is confined to an unexported
// normalize — proving the pin catches a real leak without punishing the intended
// contained shape.
func TestExportedSurfaceLeakScanFires(t *testing.T) {
	leaky := []byte(`package conversation
import "x/githubwebhook"
type Message struct { Event githubwebhook.CommentEvent }
func Post(e githubwebhook.CommentEvent) error { return nil }
`)
	leaks, err := exportedTypeLeaks(leaky, "githubwebhook")
	if err != nil {
		t.Fatalf("scan leaky: %v", err)
	}
	if len(leaks) < 2 {
		t.Errorf("scanner missed exported leaks; got %v (want the struct field + the func param)", leaks)
	}

	// An ALIASED host import must not evade the guard (the load-bearing grp2 case).
	aliased := []byte(`package conversation
import gw "x/forge/githubwebhook"
func Post(e gw.CommentEvent) error { return nil }
var Default = gw.NewThing()
`)
	leaks, err = exportedTypeLeaks(aliased, "githubwebhook")
	if err != nil {
		t.Fatalf("scan aliased: %v", err)
	}
	if len(leaks) < 2 {
		t.Errorf("scanner missed an aliased host leak; got %v (want the func param + the inferred exported var)", leaks)
	}

	// A dot-import of the host package is a leak in itself (types enter scope namelessly).
	dotted := []byte(`package conversation
import . "x/githubwebhook"
`)
	leaks, err = exportedTypeLeaks(dotted, "githubwebhook")
	if err != nil {
		t.Fatalf("scan dotted: %v", err)
	}
	if len(leaks) == 0 {
		t.Errorf("scanner missed a dot-import of the host package; got %v (want a leak)", leaks)
	}

	contained := []byte(`package conversation
import "x/githubwebhook"
func normalize(e githubwebhook.CommentEvent) Message { return Message{} }
type Message struct { author string }
`)
	leaks, err = exportedTypeLeaks(contained, "githubwebhook")
	if err != nil {
		t.Fatalf("scan contained: %v", err)
	}
	if len(leaks) != 0 {
		t.Errorf("scanner flagged a contained unexported normalize / unexported field; got %v (want none)", leaks)
	}
}
