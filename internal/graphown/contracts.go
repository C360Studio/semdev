// Package graphown derives semdev's pkg/projection ownership contracts from the
// canonical vocabulary table (migrate-semstreams-beta159, ADR-056).
//
// THE LEVER (design D1): internal/vocab already declares exactly one writer per
// predicate (G5). That table IS the contract source — the owner of every
// predicate is read from it, never re-typed here, so the runtime ownership claim
// and the checked-in writer census cannot drift. What vocab does NOT carry is the
// ENTITY CLASS a predicate is stamped on, so that one axis is declared below and
// pinned: a new Go-writer predicate with no entity-class entry fails
// TestProjectionContractsMatchVocabWriters before it can reach a boot.
//
// SCOPE (design D2): only GO writers become projection owners. The rule-writer
// Sources write via the engine's add_triple, not the Go mutation client, and are
// excluded. Predicates that are DECLARED but have no Go write site today are
// excluded too — an ownership claim with no writer would, under enforcement,
// reject the very writer that later lands. Every exclusion set carries a
// staleness census (an entry naming a predicate or Source vocab no longer
// declares fails), and no predicate may sit in two of them.
//
// # WHY ONE CONTRACT PER (OWNER, ENTITY CLASS)
//
// ReplaceOwned is NOT the old ReplaceTriples. The old writer sent
// RemoveTriples = the caller's explicit remove list; ReplaceOwned sends
// RemoveTriples = THE WHOLE SELECTED GROUP (mutation_client.go:714) and then adds
// Desired. So the group is the write's BLAST RADIUS: every predicate in the group
// that Desired does not re-supply is DELETED from that entity.
//
// Two consequences drive the shape here:
//
//  1. A contract carries ONE EntityPattern, and the client REJECTS a write whose
//     entity falls outside it (validateEntityIDForContract). semdev writes onto
//     three entity classes — the run (agent.chain.execution), the agentic loop
//     (agent.agentic-loop.execution), and the admission record
//     (forge.intake.event) — and TWO owners (create-change-author-tool,
//     conversation-classifier) write onto more than one. A single run-patterned
//     contract per owner would hard-fail every loop-entity write.
//
//  2. Grouping an owner's run-entity and loop-entity predicates together would
//     make each write wipe the other class's facts. Split by entity class, every
//     remaining call site already reconciles its COMPLETE owned set for the entity
//     it targets — audited site by site in design.md D3a — so replace-owned is
//     byte-identical to what ReplaceTriples did.
//
// The one place two sites of one (owner, class) write DIFFERENT subsets is
// route-mirror on the loop class: check_floors stamps the floors mirror on L_n and
// submit_review stamps the review mirror on the review loop. They are byte-safe
// ONLY because those are DISJOINT ENTITY POPULATIONS — check_floors is dispatched
// off a developer loop, submit_review is advertised only to reviewer spawns — so
// each site's group-wipe clears only predicates never present on its entity. That
// precondition lives in the RULE CONFIGS, not here, so it is pinned there by
// TestRouteMirrorSitesWriteDisjointEntityPopulations. A third route-mirror site
// writing a subset onto an entity another site also writes would silently drop
// facts.
//
// MODE (design D3, CONSERVATIVE): every write maps to replace-owned;
// append-evidence is adopted nowhere in this change (semdev's "append-mirror"
// ledgers are already simulated via read-all-then-write-full-set, which
// replace-owned preserves byte-identically). The only birth predicates are
// admission-check's entity-creation facts.
//
// # THE ADMISSION-CHECK CONTRACT OWNS NOTHING (deliberate, design D3)
//
// Birth predicates "derive no ownership claim" (projection/contract.go:66-70).
// admission-check therefore has ZERO Groups, and the framework treats it
// accordingly: Bind returns the zero token WITHOUT calling RegisterOwner
// (contract.go:258-260), it needs no heartbeater and no registry
// (contractsRequireHeartbeat / contractsRequireRegistration are both false), and
// canonicalizeCreate leaves the wire OwnerToken empty (mutation_client.go:316-326).
// So the admission record is deliberately UNOWNED and UNGATED: its writes are
// never lease-checked, in either enforcement posture.
//
// As-built caveat: the contract is derived and bound, but NOTHING USES IT YET —
// internal/intake still hand-rolls a graph.CreateEntityWithTriplesRequest, so no
// call site resolves Clients.Writer("admission-check"). It WILL authorize the birth
// predicates and pin the entity pattern once task 4.3 migrates that site; until
// then it is a declaration, not an enforcement. Any claim that migrating it buys
// LEASE coverage is false either way. See design D3/D5.
package graphown

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/c360studio/semstreams/pkg/ownership"
	"github.com/c360studio/semstreams/pkg/projection"
	semtypes "github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semdev/internal/vocab"
)

// The six-position entity-ID globs semdev's Go writers stamp on. These are the
// ONLY entity classes a projection contract may claim; ContractFor resolves a
// write's contract by matching its entity against them.
const (
	// RunPattern is the run entity (agentic.ChainExecutionEntityID) — the anchor
	// almost every semdev harness fact lands on.
	RunPattern = "*.*.agent.chain.execution.*"
	// LoopPattern is the agentic-loop execution entity
	// (agentic.LoopExecutionEntityID) — where the route mirrors, the authored
	// marker, and the classifier's recorded marker land.
	LoopPattern = "*.*.agent.agentic-loop.execution.*"
	// AdmissionPattern is the admission-record entity (AdmissionRecordEntityID) —
	// the one entity semdev CREATES.
	AdmissionPattern = "*.*.forge.intake.event.*"
	// AgentExecPattern spans BOTH the run and the agentic loop. Exactly one fact
	// needs it: station.dispatch.failed is stamped on whatever entity the station
	// was dispatched FOR, which is a run for run-triggered stations and a loop for
	// loop-triggered ones. Widening one predicate's claim is safe because ownership
	// overlap requires pattern AND predicate intersection (ownership/overlap.go),
	// and this predicate has a single writer — so the wider pattern captures no
	// other owner's cell. Narrowing it would silently un-own the loop-fired station
	// park; TestAgentExecPatternSpansBothExecutionClasses pins both halves.
	AgentExecPattern = "*.*.agent.*.execution.*"
)

// OwnedGroup is the name of the single replace-owned predicate group every
// non-birth contract carries. Call sites pass it as ReplaceOwnedMutation.Group so
// the selection is explicit rather than relying on the client's
// exactly-one-group default.
const OwnedGroup = "owned"

// entityClass declares which entity class each GO-WRITER predicate is stamped on.
// vocab owns the predicate→writer half of the contract; this owns the
// predicate→entity half. Every Go-writer predicate MUST appear here — Contracts
// fails closed on a missing entry, and the census pins it offline so a new vocab
// entry cannot reach a boot unclassified.
var entityClass = map[string]string{
	// admission-check — the birth facts on the admission record.
	"intake.actor.login":    AdmissionPattern,
	"intake.actor.admitted": AdmissionPattern,
	"intake.event.ref":      AdmissionPattern,

	// The run entity: the harness fact packages the routing rules read.
	"run.change.decision":             RunPattern,
	"experiment.run.condition":        RunPattern,
	"conversation.pending.message-id": RunPattern,
	"conversation.pending.author":     RunPattern,
	"conversation.pending.body":       RunPattern,
	"conversation.intent.value":       RunPattern,
	"conversation.intent.message-id":  RunPattern,
	"conversation.intent.author":      RunPattern,
	"conversation.intent.reason":      RunPattern,
	"conversation.intent.classified":  RunPattern,
	"openspec.change.document":        RunPattern,
	"openspec.change.slug":            RunPattern,
	"openspec.change.revision":        RunPattern,
	"openspec.change.validated":       RunPattern,
	"task.spec.goal":                  RunPattern,
	"task.spec.budget":                RunPattern,
	"task.spec.assumptions":           RunPattern,
	"task.spec.non-goals":             RunPattern,
	"task.spec.target-files":          RunPattern,
	"task.spec.test-command":          RunPattern,
	"attempt.commit.sha":              RunPattern,
	"floor.finding.rejected":          RunPattern,
	"floor.finding.detail":            RunPattern,
	"floor.finding.attempt":           RunPattern,
	"measurement.result.passed":       RunPattern,
	"measurement.result.command":      RunPattern,
	"measurement.result.commit":       RunPattern,
	"measurement.result.ran":          RunPattern,
	"measurement.result.exit-code":    RunPattern,
	"measurement.result.timed-out":    RunPattern,
	"review.verdict.value":            RunPattern,
	"review.findings.value":           RunPattern,
	"verify.cleanroom.result":         RunPattern,
	"sandbox.provision.ready":         RunPattern,
	"sandbox.provision.blocked":       RunPattern,
	"sandbox.attestation.image":       RunPattern,
	"sandbox.attestation.tier":        RunPattern,
	"delivery.pr.ref":                 RunPattern,

	// The agentic-loop entity: the fired-once markers and the route mirrors the
	// rules read off the FIRING loop.
	"openspec.change.authored":         LoopPattern,
	"conversation.classifier.recorded": LoopPattern,
	"route.attempt.passed":             LoopPattern,
	"route.attempt.rejected":           LoopPattern,
	"route.attempt.instance":           LoopPattern,
	"route.attempt.transient":          LoopPattern,
	"route.review.verdict":             LoopPattern,
	"route.transient.instance":         LoopPattern,
	"route.task.budget":                LoopPattern,

	// Either class — see AgentExecPattern.
	"station.dispatch.failed": AgentExecPattern,
}

// unwiredPredicates are DECLARED in the vocab census but have NO Go write site
// today. They get no contract: an ownership claim with no writer buys nothing
// under observe-only and, under enforcement, would reject the very writer that
// eventually lands (it would have to be bound to the claiming owner first). The
// value is why, so re-including one is a deliberate edit rather than a guess —
// and Contracts REJECTS a predicate listed here that also has an entityClass
// entry, so "wiring it up" cannot silently no-op.
var unwiredPredicates = map[string]string{
	// Excluding this one is also load-bearing for BLAST RADIUS, not just tidiness:
	// in conversation-adapter's owned group, every pending-message stamp
	// (approval.go, which supplies only the three conversation.pending.* triples)
	// would group-wipe it.
	"human.opt.signal":         "the ask_human reply/resume lane is unimplemented (a paper vocab reassignment, conversation-channel-seam D5)",
	"openspec.change.archived": "no archive harness exists yet — the predicate is reserved for it",
	"openspec.spec.document":   "brownfield.Triples has no caller; the spec entity's ID grammar is not fixed yet",
	"evidence.ledger.run":      "the ledger writer lands with the evidence-ledger capability at M1 (internal/ledger is schema-only)",
}

// ruleWriterSources are the vocab Sources whose predicates are stamped by the RULE
// ENGINE (add_triple), NOT by a Go mutation client. They are NOT projection owners
// (design D2). Kept as an explicit exclusion so a NEW rule Source is a deliberate
// addition here; TestExclusionSetsAreNotStale fails on an entry vocab no longer
// declares.
//
// NOTE (design D2b): this is the STAMPING split only. The rule engine also REMOVES
// predicates that Go writers own — conversation/04-classifier-terminal-release
// clears conversation.pending.* on the run — over graph.mutation.triple.remove,
// a lane ADR-056 does not lease-gate. vocab records the stamping writer, so no
// census here can see that; it is documented and decided in design.md D2b.
var ruleWriterSources = map[string]bool{
	"issue-ref-rule":          true,
	"park-rule":               true,
	"dev-rewake-rule":         true,
	"dev-projection-rule":     true,
	"dev-dispatch-rule":       true,
	"dev-floors-rule":         true,
	"dev-route-rule":          true,
	"sandbox-provision-rule":  true,
	"conversation-spawn-rule": true,
}

// createOwners write via CreateWithTriples (birth predicates), not ReplaceOwned.
// OQ2 RESOLVED: admission-check is the only one (experiment-intake upserts on the
// already-minted run; the run entity itself is framework-minted). Their contracts
// own nothing — see the package doc.
var createOwners = map[string]bool{
	"admission-check": true,
}

// OwnedContract pairs a derived contract with the vocab Source that binds it. The
// owner is carried explicitly rather than parsed back out of the contract name:
// a name-stripping inverse is lossy the moment an owner id ends in a class suffix.
type OwnedContract struct {
	Owner    string
	Contract projection.Contract
}

// IsRuleWriter reports whether a vocab Source writes via the rule engine (add_triple)
// rather than the Go mutation client — i.e. it is NOT a projection owner (design D2).
func IsRuleWriter(source string) bool { return ruleWriterSources[source] }

// IsCreateOwner reports whether an owner writes via CreateWithTriples (birth
// predicates) rather than ReplaceOwned (design D3 / OQ2).
func IsCreateOwner(source string) bool { return createOwners[source] }

// IsUnwired reports whether a declared predicate has no Go write site today and so
// derives no contract.
func IsUnwired(predicate string) bool { _, ok := unwiredPredicates[predicate]; return ok }

// UnwiredPredicates returns the declared-but-unwritten predicates and the reason
// each is excluded, for the census to assert against.
func UnwiredPredicates() map[string]string {
	return maps(unwiredPredicates)
}

// EntityClasses returns the declared predicate→entity-class table, for the census
// to check for staleness against vocab.
func EntityClasses() map[string]string { return maps(entityClass) }

// RuleWriterSources returns the excluded engine-writing Sources, for the census to
// check for staleness against vocab.
func RuleWriterSources() []string {
	out := make([]string, 0, len(ruleWriterSources))
	for s := range ruleWriterSources {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// CreateOwners returns the sanctioned entity-creation owners, sorted.
func CreateOwners() []string {
	out := make([]string, 0, len(createOwners))
	for o := range createOwners {
		out = append(out, o)
	}
	slices.Sort(out)
	return out
}

func maps(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// contractKey identifies one contract: an owner plus the entity class it claims.
type contractKey struct {
	owner   string
	pattern string
}

// classSuffix names the entity class in a contract name, for the owners that claim
// more than one. Single-class owners keep the bare owner id so error messages read
// as the writer's own name. This is presentation only — OwnedContract.Owner is the
// authority, never a parse of the name.
var classSuffix = map[string]string{
	RunPattern:       "run",
	LoopPattern:      "loop",
	AdmissionPattern: "admission",
	AgentExecPattern: "agent-exec",
}

// Contracts derives the complete set of semdev projection contracts from the vocab
// table: one Contract per (Go-writer Source, entity class), grouping that owner's
// predicates for that class into ONE replace-owned group. Rule-writer Sources and
// unwired predicates are excluded (design D2). Create owners carry their predicates
// as BirthPredicates instead of a group (design D3 / OQ2). The result is
// deterministic (sorted by owner, then entity class, then predicate) so the census
// and the bind order are stable.
//
// It fails closed — never silently — on either classification gap: a Go-writer
// predicate with NO entity-class entry (it would be silently un-owned and its
// writes un-tokened), and a predicate listed BOTH unwired and entity-classed
// (wiring a writer up would otherwise no-op).
//
// The derivation is a pure function of package-level tables, so it is computed
// once; ContractFor is on the per-write path and must not rebuild it.
func Contracts() ([]OwnedContract, error) { return derived() }

var derived = sync.OnceValues(deriveContracts)

func deriveContracts() ([]OwnedContract, error) {
	byKey := map[contractKey][]string{}
	ownerClasses := map[string]map[string]bool{}
	for _, p := range vocab.Predicates {
		pattern, classified := entityClass[p.Name]
		if _, unwired := unwiredPredicates[p.Name]; unwired {
			if classified {
				return nil, fmt.Errorf(
					"graphown: predicate %q is BOTH listed unwired and entity-classed as %q — "+
						"wiring a writer means DELETING its unwiredPredicates entry, not just adding the class; "+
						"leaving both silently derives no contract and no error",
					p.Name, pattern,
				)
			}
			continue // declared, but nothing writes it yet
		}
		if ruleWriterSources[p.Writer] {
			continue // engine-written, not a projection owner
		}
		if !classified {
			return nil, fmt.Errorf(
				"graphown: predicate %q (writer %q) has no entity-class entry — classify it in entityClass "+
					"or add it to unwiredPredicates; an unclassified Go-writer predicate would be silently un-owned",
				p.Name, p.Writer,
			)
		}
		key := contractKey{owner: p.Writer, pattern: pattern}
		byKey[key] = append(byKey[key], p.Name)
		if ownerClasses[p.Writer] == nil {
			ownerClasses[p.Writer] = map[string]bool{}
		}
		ownerClasses[p.Writer][pattern] = true
	}

	keys := make([]contractKey, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b contractKey) int {
		if a.owner != b.owner {
			return strings.Compare(a.owner, b.owner)
		}
		return strings.Compare(a.pattern, b.pattern)
	})

	contracts := make([]OwnedContract, 0, len(keys))
	for _, key := range keys {
		preds := byKey[key]
		slices.Sort(preds)
		c := projection.Contract{
			Name:          contractName(key, len(ownerClasses[key.owner]) > 1),
			EntityPattern: key.pattern,
		}
		if createOwners[key.owner] {
			// Creation facts are authorized only on CreateWithTriples and derive no
			// ownership claim (design D3 / OQ2) — so this contract owns nothing and
			// its writes are never lease-gated. See the package doc.
			c.BirthPredicates = preds
		} else {
			c.Groups = []projection.PredicateGroup{{
				Name:       OwnedGroup,
				Mode:       ownership.ModeReplaceOwned,
				Predicates: preds,
			}}
		}
		contracts = append(contracts, OwnedContract{Owner: key.owner, Contract: c})
	}
	return contracts, nil
}

// contractName is the owner id for a single-class owner and owner-<class> for an
// owner that claims more than one entity class.
func contractName(key contractKey, multiClass bool) string {
	if !multiClass {
		return key.owner
	}
	return key.owner + "-" + classSuffix[key.pattern]
}

// ContractsFor returns the contracts one owner binds — its FULL claim set, which is
// what BindMutationClient takes (the one-registration-per-owner invariant forbids
// binding them separately).
func ContractsFor(owner string) ([]projection.Contract, error) {
	all, err := Contracts()
	if err != nil {
		return nil, err
	}
	out := make([]projection.Contract, 0, len(classSuffix))
	for _, oc := range all {
		if oc.Owner == owner {
			out = append(out, oc.Contract)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("graphown: no projection contract for owner %q", owner)
	}
	return out, nil
}

// ContractFor resolves the contract name a call site names when owner writes onto
// entityID. Resolution is by ENTITY MATCH, not by naming convention: the owner's
// contracts are matched against the entity's six-position id and exactly one must
// claim it.
//
// It exists so no call site hardcodes a contract string, and so writing to an
// unexpected entity class fails LOUDLY at the call site with the owner and entity
// named — rather than as the mutation client's generic "entity outside contract
// pattern" rejection deep in a retry loop. A call site that can only log its write
// failure (station.stampDispatchFailed) MUST still surface this error, or a
// misclassified predicate becomes a silent lost fact.
func ContractFor(owner, entityID string) (string, error) {
	contracts, err := ContractsFor(owner)
	if err != nil {
		return "", err
	}
	var matched []string
	for _, c := range contracts {
		ok, merr := semtypes.MatchEntityIDPattern(c.EntityPattern, entityID)
		if merr != nil {
			return "", fmt.Errorf("graphown: match %q against contract %q: %w", entityID, c.Name, merr)
		}
		if ok {
			matched = append(matched, c.Name)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return "", fmt.Errorf(
			"graphown: owner %q has no projection contract claiming entity %q — it writes an entity class it does not own",
			owner, entityID,
		)
	default:
		return "", fmt.Errorf(
			"graphown: owner %q has %d contracts claiming entity %q (%v) — overlapping entity patterns are a contract bug",
			owner, len(matched), entityID, matched,
		)
	}
}

// Owners returns every Go-writer Source that binds at least one contract (the
// identities the composition root binds), sorted and de-duplicated.
func Owners() ([]string, error) {
	cs, err := Contracts()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(cs))
	for _, oc := range cs {
		if seen[oc.Owner] {
			continue
		}
		seen[oc.Owner] = true
		out = append(out, oc.Owner)
	}
	slices.Sort(out)
	return out, nil
}

// OwningOwners returns the owners whose contracts carry at least one replace-owned
// group — the ones that actually register a claim, mint a token, and REQUIRE the
// shared heartbeater. It excludes the birth-only create owners, which bind without
// registering (see the package doc). The composition root's
// one-bind-per-owner/ErrOwnerAlreadyBound pin applies to THIS set, not to Owners().
func OwningOwners() ([]string, error) {
	cs, err := Contracts()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(cs))
	for _, oc := range cs {
		if len(oc.Contract.Groups) == 0 || seen[oc.Owner] {
			continue
		}
		seen[oc.Owner] = true
		out = append(out, oc.Owner)
	}
	slices.Sort(out)
	return out, nil
}
