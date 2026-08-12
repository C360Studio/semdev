// Package openpr is the shared DELIVERY CORE (group 8D, R6; made REAL by
// forge-io-real-lanes): the run's DELIVERY step. When the coherence gate has
// cleared a run — the clean-room verify passed, the change validated, every
// task approved — Delivery.Deliver pushes the run's committed attempt branch
// to the configured forge remote and creates-or-adopts the evidence-bearing
// pull request, recording delivery.pr.ref = the REAL PR URL. It is the
// terminal of the issue→PR arc. The delivery-station component
// (internal/station/delivery) drives it off a rule publish (R6); it is not a
// model tool.
//
// The M0 `local-delivery:` stub is DELETED (forge-io-real-lanes 4.4): an
// unconfigured forge FAILS delivery closed, the station's retries exhaust, and
// the run PARKS (station-failure-parks) — a stand-in reference can no longer
// masquerade as a delivery, and the spec forbids one from satisfying the
// requirement.
//
// G3: delivery takes no model-supplied arguments — the harness pushes the
// committed bytes and forms the ref, never the model. G2: Deliver stamps
// pr.ref and fires no lifecycle transition (a rule closing the run reads
// pr.ref). Single G5 writer of pr.ref (open-pr). Doubly idempotent (graph
// read-guard + forge query-by-head) — see Delivery.Deliver.
package openpr

// Source is stamped on the pr.ref triple. It MUST equal the writer declared for pr.ref in
// internal/vocab (G5) — a conformance pin cross-checks it. (The vocab writer was reconciled
// from the placeholder pr-delivery-adapter to this real Source at 8D.)
const Source = "open-pr"

// RefPredicate is the terminal delivery fact this package owns on the run entity. Exact
// predicate → latest-wins.
const RefPredicate = "delivery.pr.ref"
