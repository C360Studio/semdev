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
