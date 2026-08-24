package boot

import (
	"fmt"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/vocab"
)

// declaredGraphClients declares semdev's canonical predicate vocabulary with the
// framework registry and THEN builds the contract-validated mutation client. It is
// the sole graphown.NewClients construction site in this package, pinned by
// TestBootBuildsGraphClientsOnlyThroughTheDeclaringHelper.
//
// The ordering is load-bearing, not tidiness. projection.Contract validation calls
// vocabulary.RequireDeclaredPredicate on every group and birth predicate, so a client
// built before vocab.Register() fails construction outright with "predicate %q is
// canonical but not declared in the vocabulary registry". An entry point that gets
// this wrong is not degraded — it is DEAD, at its first line of real work.
//
// That is exactly what happened to the launch lane: RunLaunch built its client with no
// Register call, so every `semdev launch` failed at construction from beta.159 (when the
// mutation client landed) until this helper. The runtime lane survived only because
// NewRuntime happens to call vocab.Register() early, for an unrelated reason (beta.147's
// unconditional rule-load predicate check). Funnelling both through one helper makes the
// ordering a property of the package rather than a coincidence of two call sites.
//
// vocab.Register is idempotent (vocabulary.Register amends), so repeated calls across
// NewRuntime/RunLaunch in one process are safe.
func declaredGraphClients(natsClient *natsclient.Client) (*graphown.Clients, error) {
	vocab.Register()
	clients, err := graphown.NewClients(natsClient)
	if err != nil {
		return nil, fmt.Errorf("build graph mutation client: %w", err)
	}
	return clients, nil
}
