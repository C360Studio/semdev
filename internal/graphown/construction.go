package graphown

import (
	"fmt"
	"slices"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/projection"
)

// Clients is the composition root's write surface: ONE contract-validating
// projection.MutationClient carrying every derived contract, resolved to a
// per-writer *Writer on demand.
//
// ADR-091 removed ownership: a contract validates local caller intent and
// reserves nothing, so a single shared client safely serves every writer — there
// is no registration, no token, and no way for one holder to fence out another.
// What remains per-writer is the contract RESOLUTION (ContractFor) that Writer
// makes unavoidable (design D3b).
//
// A nil *Clients is VALID and yields nil writers: that is the schema-scanning
// census path (no NATS client), where a tool registers schema-only and fails
// loudly if a write is ever attempted. This is why Writer must return a nil
// *Writer rather than a Writer wrapping a nil client — tools guard on
// `writer == nil`, and the wrapper would pass that guard and fail later, at the
// write.
type Clients struct {
	client *projection.MutationClient
	owners map[string]bool
}

// NewClients builds the process's one mutation client from the COMPLETE derived
// contract set, run ONCE per process before any writer registers. Construction is
// purely local (contract validation; no wire traffic), so a failure here is a
// contract bug, never a transport blip.
func NewClients(nc *natsclient.Client) (*Clients, error) {
	if nc == nil {
		return nil, fmt.Errorf("graphown: NATS client is required to build the mutation client")
	}
	all, err := Contracts()
	if err != nil {
		return nil, err
	}
	flat := make([]projection.Contract, 0, len(all))
	owners := make(map[string]bool, len(all))
	for _, oc := range all {
		flat = append(flat, oc.Contract)
		owners[oc.Owner] = true
	}
	client, err := projection.NewMutationClient(projection.MutationClientConfig{
		NATS:      nc,
		Contracts: flat,
	})
	if err != nil {
		return nil, fmt.Errorf("graphown: build mutation client (%d contracts): %w", len(flat), err)
	}
	return &Clients{client: client, owners: owners}, nil
}

// Writer returns owner's contract-validated write surface, or nil when the owner
// derives no contract (the census path, or a typo'd Source — RequireWriters
// catches the latter at boot).
func (c *Clients) Writer(owner string) *Writer {
	if !c.knows(owner) {
		return nil
	}
	return NewWriter(owner, c.client)
}

// ReadWriter is Writer plus the authoritative read-back, for the two
// shrinking-package sites (check_floors' findings clear, project_tasks'
// immutability gate). One MutationClient serves both roles.
func (c *Clients) ReadWriter(owner string) *Writer {
	if !c.knows(owner) {
		return nil
	}
	return NewReadWriter(owner, c.client, c.client)
}

func (c *Clients) knows(owner string) bool {
	if c == nil || c.client == nil {
		return false
	}
	return c.owners[owner]
}

// RequireWriters asserts every owner in want resolves a writer, returning a named
// error listing the misses. It is the boot census: a Source with no derived
// contract, or a typo'd Source at a call site, otherwise surfaces only as a nil
// writer at the FIRST WRITE — deep inside a station handler, where several paths
// can do nothing but log. Fail at boot instead.
func (c *Clients) RequireWriters(want ...string) error {
	var missing []string
	for _, owner := range want {
		if !c.knows(owner) {
			missing = append(missing, owner)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return fmt.Errorf(
		"graphown: %d writer(s) resolve no contract-validated client: %v — a write under one would fail at its first call site, not here",
		len(missing), missing,
	)
}
