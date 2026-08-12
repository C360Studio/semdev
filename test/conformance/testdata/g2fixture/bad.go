// Package g2fixture is a deliberate G2 violation fixture: it fires lifecycle
// transitions from Go. It lives under testdata so it is invisible to the normal
// build, to `go vet ./...`, and to the real G2 census; the census's red-first
// test loads it explicitly and asserts the scan flags every call here.
package g2fixture

import (
	"context"

	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/pkg/lifecycle"
)

func fireTransitions(ctx context.Context, m *lifecycle.Manager) {
	_ = m.Complete(ctx, "agent-run", "entity-1")
	_ = m.Fail(ctx, "agent-run", "entity-1", "boom")
	_ = m.Transition(ctx, "agent-run", "entity-1", "executing", lifecycle.TransitionSourceComponent, "note")
	_ = m.Create(ctx, nil)
	_ = m.TransitionWith(ctx, "agent-run", "entity-1", "executing", lifecycle.TransitionSourceComponent, "note", nil)
}

// wrapper embeds *lifecycle.Manager; calling the promoted method is still a G2
// violation, and the census must catch it via the method's own receiver.
type wrapper struct {
	*lifecycle.Manager
}

func fireViaEmbedding(ctx context.Context, w wrapper) {
	_ = w.Complete(ctx, "agent-run", "entity-2")
}

func mintRun(ctx context.Context, mgr agentrun.MintableManager) {
	_, _ = agentrun.Mint(ctx, mgr, "org", "platform", "root-loop")
}
