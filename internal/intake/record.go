package intake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/graphown"
)

// The admission record (forge-io-real-lanes): the canonical forge-io spec
// requires admission to be "recorded as intake.actor.admitted", and the run
// does not exist at decide time — so the record births its OWN evidence entity
// (the emit_diagnosis birth pattern), one per ADMITTED event. Rejected events
// are deliberately NOT recorded in the graph: the webhook lane is the security
// door, and an entity per spam event would hand unauthenticated traffic a graph
// write. Rejects are logged + metered only.
//
// The entity ID is CONTENT-DERIVED (UUIDv5 over ref + delivery GUID), which is
// the intake lane's idempotency backstop: a webhook REDELIVERY collapses onto
// the same entity, and the strict create's CONFLICT tells the consumer the
// event was already processed (skip the wake rather than double-mint a run).

const (
	// ActorLoginPredicate records the admitted event's code-host actor (G5
	// writer admission-check, declared at m0).
	ActorLoginPredicate = "intake.actor.login"
	// ActorAdmittedPredicate records the admission verdict (always "true" —
	// only admitted events are recorded; see the package rationale above).
	ActorAdmittedPredicate = "intake.actor.admitted"
	// EventRefPredicate correlates the admission record to its issue —
	// deliberately NOT run.issue.ref (that predicate lives on the RUN with its
	// own rule writer; sharing it here would give one predicate two writers, G5).
	EventRefPredicate = "intake.event.ref"
	// RecordSource is the G5 writer of the admission-record facts.
	RecordSource = "admission-check"
)

// recordNamespace is the fixed UUIDv5 namespace for admission-record identity.
// Generated once (uuidgen); never reuse another namespace's UUID.
var recordNamespace = uuid.MustParse("6e1a2f6a-9c3b-5e6d-8f21-4a7b9c0d1e2f")

// AdmissionRecordEntityID returns the content-derived 6-part entity ID for an
// admitted event: {org}.{platform}.forge.intake.event.{uuid5(ref, deliveryID)}.
// An empty deliveryID (an e2e journey publishing the flattened shape without a
// receiver) degrades to ref-only identity — one record per issue, which still
// dedupes a replayed journey publish.
func AdmissionRecordEntityID(org, platform, ref, deliveryID string) string {
	id := uuid.NewSHA1(recordNamespace, []byte(ref+"\n"+deliveryID)).String()
	return fmt.Sprintf("%s.%s.forge.intake.event.%s", org, platform, id)
}

// AdmissionRecordMessageType is the typed-origin envelope for the admission
// record entity. MUTATION-ONLY (never published as a payload) — the same
// posture as the framework's ops-diagnosis and agent-lesson entities.
func AdmissionRecordMessageType() message.Type {
	return message.Type{Domain: "semdev", Category: "intake_event", Version: "v1"}
}

// EntityCreator is the narrow birth surface the recorder needs —
// *graphown.Creator satisfies it.
type EntityCreator interface {
	Create(ctx context.Context, entityID string, msgType message.Type, triples []message.Triple) error
}

// ErrAlreadyRecorded reports that the admission record already exists — the
// event was processed before (a webhook redelivery or a consumer redelivery
// after a crash between the record and the ack). The caller SKIPS the wake:
// re-waking would double-mint a run and double-spend tokens; the recovery for
// the rare crash-between-record-and-wake window is a human re-trigger, loud in
// the log, never a silent duplicate run.
var ErrAlreadyRecorded = errors.New("intake: admission already recorded for this event")

// RecordAdmission births the admission-record entity for an admitted event.
// Returns ErrAlreadyRecorded when the content-derived entity already exists
// (idempotent redelivery); any other error is transport-transient (the caller
// retries via its consumer redelivery).
func RecordAdmission(ctx context.Context, creator EntityCreator, entityID, actor, ref string) error {
	now := time.Now().UTC()
	triples := []message.Triple{
		{Subject: entityID, Predicate: ActorLoginPredicate, Object: strings.TrimSpace(actor), Source: RecordSource, Timestamp: now, Confidence: 1.0},
		{Subject: entityID, Predicate: ActorAdmittedPredicate, Object: "true", Source: RecordSource, Timestamp: now, Confidence: 1.0},
		{Subject: entityID, Predicate: EventRefPredicate, Object: ref, Source: RecordSource, Timestamp: now, Confidence: 1.0},
	}
	if err := creator.Create(ctx, entityID, AdmissionRecordMessageType(), triples); err != nil {
		if graphown.IsConflict(err) {
			return ErrAlreadyRecorded
		}
		return fmt.Errorf("intake: record admission on %s: %w", entityID, err)
	}
	return nil
}
