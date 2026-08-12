package graphown

import "github.com/c360studio/semstreams/component"

// MutationSubjectFamily is the canonical typed mutation port's subject family
// (ADR-091). Named HERE, once: the no-hand-rolled-subject census requires that
// no Go file outside this package spells a graph.mutation subject, so component
// port declarations build through RequesterPortDefinition instead.
const MutationSubjectFamily = "graph.mutation.>"

// MutationInterfaceType and MutationInterfaceVersion identify the typed
// request port every mutation provider/requester declares. String literals by
// necessity — the framework exports no Go constants for the adopter seam; the
// values come from the graph-foundation cutover contract and the framework's
// own shipped configs.
const (
	MutationInterfaceType    = "semstreams.graph.mutation"
	MutationInterfaceVersion = "v1"
)

// RequesterPortDefinition builds the declared graph-mutation requester output —
// the port every component that mutates the graph carries, paired by flow
// validation with graph-ingest's provider input.
func RequesterPortDefinition(description string) component.PortDefinition {
	return component.PortDefinition{
		Name:        "graph_mutations",
		Required:    true,
		Description: description,
		Config: component.NATSRequestPort{
			Subject:   MutationSubjectFamily,
			Interface: &component.InterfaceContract{Type: MutationInterfaceType, Version: MutationInterfaceVersion},
		},
	}
}
