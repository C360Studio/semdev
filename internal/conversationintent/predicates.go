package conversationintent

// The canonical graph predicates + Sources for the conversation-intent gate
// (nl-conversation-intent D2/D10). They MIRROR internal/vocab's registered
// entries (all registered in group 1, BEFORE any writer — beta.150 fails closed
// at the graph-write boundary on an unregistered canonical predicate). The G5
// writer census (TestToolSourceMatchesVocabWriter) ties each Source constant to
// the single writer vocab declares, so these cannot silently drift from the
// registry. They live here — the feature's neutral domain package — so the
// classifier tool (writes conversation.intent.*, reads conversation.pending.*),
// the conversation adapter (writes conversation.pending.*), and the apply
// consumer (reads conversation.intent.author) share ONE definition.

// The authorized human message awaiting classification — stamped on the RUN by
// the conversation adapter (handleMessage, group 3) and READ by classify_intent
// to HARNESS-BIND identity (never model-supplied — D2). Writer:
// conversation-adapter. (The .body member the spawn rule templates onto the
// classifier prompt is added with its writer in group 3; classify_intent reads
// only the id + author it copies onto the intent.)
const (
	PendingPrefix             = "conversation.pending."
	PendingMessageIDPredicate = PendingPrefix + "message-id"
	PendingAuthorPredicate    = PendingPrefix + "author"
	// PendingBodyPredicate is the authorized human message text the group-4 spawn
	// rule templates onto the classifier prompt (classify_intent reads only the id
	// + author it copies onto the intent — never the body). Writer:
	// conversation-adapter (handleMessage's NL bridge, group 3).
	PendingBodyPredicate = PendingPrefix + "body"
)

// AdapterSource is the single G5 writer stamped on every conversation.pending.*
// triple the conversation adapter's NL bridge (handleMessage) writes. It is a
// DISTINCT Source from the gate writer (approval-adapter) the same struct also
// emits — the transport lane vocab declares for the pending namespace — kept
// honest by the sanctioned-writer census. It MUST equal the writer internal/vocab
// declares for conversation.pending.* (TestToolSourceMatchesVocabWriter checks it).
const AdapterSource = "conversation-adapter"

// The classifier's ROUTING output — stamped on the RUN by classify_intent
// (group 2, subject-overridden to the run so handleMessage's dedup and the
// routing rule can read it — H2). value ∈ {approve, reject, none}; message-id +
// author are COPIED from the run's pending triples (harness-bound, D2); reason
// is the model's inert echo; classified is the MULTI-VALUED append-set ledger of
// message ids already classified (the dedup key, D5). Writer:
// conversation-classifier.
const (
	IntentValuePredicate      = "conversation.intent.value"
	IntentMessageIDPredicate  = "conversation.intent.message-id"
	IntentAuthorPredicate     = "conversation.intent.author"
	IntentReasonPredicate     = "conversation.intent.reason"
	IntentClassifiedPredicate = "conversation.intent.classified"
)

// ClassifierSource is the single G5 writer stamped on every
// conversation.intent.* triple. It MUST equal the writer internal/vocab declares
// for that namespace (TestToolSourceMatchesVocabWriter cross-checks it).
const ClassifierSource = "conversation-classifier"

// ClassifierDispatchedPredicate is the spawn rule's fire-once marker
// (writer conversation-spawn-rule, a rule add_triple — group 4): its object is
// the pending message id the classifier loop was dispatched FOR. classify_intent
// READS it as the read-once binding check (grp4-review HIGH-1): the pending slot
// is latest-wins and can move during the model turn, so the tool faults —
// stamping and deduping NOTHING — when the slot's id no longer matches the
// dispatched id, rather than bind the new message's identity to a judgment of
// the old message's text. The terminal-release rule then retires the slot and
// the marker, and the fallback-note lane surfaces the miss to the human.
const ClassifierDispatchedPredicate = "conversation.classifier.dispatched"

// ClassifierAttemptedPredicate is the run's APPEND-SET classifier spend ledger
// (writer conversation-spawn-rule, a rule add_triple — group 8, design D14): the
// spawn rule appends the dispatched message id in the same action set that arms
// the fire-once marker, and guards on `length_lte ClassifierAttemptBudget-1`.
// Counting on the SPAWN side makes the bound honest — a faulted, truncated,
// refused, or cap-exhausted classification consumes budget exactly like a
// successful one. The terminal-release rule clears the pending slot and the
// marker but NEVER this ledger: it is the run's durable spend record. The marker
// SERIALIZES spawns (one at a time); this ledger BOUNDS them.
const ClassifierAttemptedPredicate = "conversation.classifier.attempted"

// ClassifierAttemptBudget is N: the maximum paid classifier turns one run's gate
// may spend (D14) — enough for a human to rephrase twice, small enough that a
// runaway thread costs three short loops. The rule condition carries N-1
// (length_lte is evaluated BEFORE the appending fire), so the conformance pin
// derives the rule value from this constant.
const ClassifierAttemptBudget = 3

// BudgetNotedPredicate is the exhaustion note's once-per-run self-extinguishing
// marker (writer conversation-budget-rule — group 8, design D14): stamped by the
// budget-exhausted rule BEFORE it publishes the user.note escape hatch, so the
// note posts exactly once per run and never re-posts on a RULE_STATE replay (the
// grp6 lesson: a human-visible post with no marker re-posts on state loss). Its
// object is the pending message id that tripped exhaustion — forensics, not
// mechanism.
const BudgetNotedPredicate = "conversation.budget.noted"

// BudgetNoteProperty / BudgetNoteValue discriminate the exhaustion note from the
// classifier fault note on the SHARED user.note.> lane: the budget rule's publish
// carries properties.note = "budget-exhausted" (the rule engine's publish payload
// carries substituted action properties), and the note consumer selects the body
// by it. The fault note publishes bare and keeps the default body — no graph-state
// inference, no subject split.
const (
	BudgetNoteProperty = "note"
	BudgetNoteValue    = "budget-exhausted"
)

// ClassifierRecordedPredicate is stamped by classify_intent on ITS OWN LOOP
// entity (not the run) when a classification actually lands; its object is the
// classified message id. The fault-note rule (conversation/05) fires on the LOOP
// and so can only read loop-local facts — the run's conversation.intent.* is
// unreachable from there — which makes the ABSENCE of this fact at a terminal the
// complete discriminator for "this classifier produced no reading".
//
// It replaced an agent.loop.outcome == "failed" condition that could not work: a
// tool returning a ToolResult error does NOT fail its loop, so a classifier that
// deliberately refused to classify (the read-once binding fault) terminated
// outcome=success and the human got silence. Writer: conversation-classifier.
const ClassifierRecordedPredicate = "conversation.classifier.recorded"
