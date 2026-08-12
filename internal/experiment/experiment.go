// Package experiment owns the semsource A/B condition surface
// (integrate-semsource-ab-harness): the operator-declared condition read from
// boot config, the fail-closed per-signal readiness gate at launch (D4), and
// the single evidence-label fact `experiment.run.condition` stamped on the run
// at mint (D3, writer experiment-intake — G5).
//
// The condition is an EVIDENCE LABEL, never a routing input: no rule document
// references any experiment.* field (whole-document conformance lint), so the
// deterministic arc cannot behave differently on the label — only on the
// tools actually advertised (the variant dispatch pack). The ledger reads it;
// nothing else does.
package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/forge/semsource"
	"github.com/c360studio/semdev/internal/graphown"
)

// Condition values. Unconfigured ("") is the baseline DEFAULT with zero new
// facts: no condition is stamped and no semsource client exists — the
// pre-change arc byte-for-byte.
const (
	// ConditionBaseline labels a run explicitly as the baseline arm.
	ConditionBaseline = "baseline"
	// ConditionSemsource labels a run as the semsource arm: the variant
	// dispatch pack advertises the four semsource read tools and launch gates
	// on semsource's per-signal readiness.
	ConditionSemsource = "semsource"
)

// ConditionPredicate is the run-side evidence label (canonical 3-seg,
// declared in internal/vocab; capability semsource-ab).
const ConditionPredicate = "experiment.run.condition"

// Source is the G5 writer of ConditionPredicate — the experiment-intake
// launch path, the single writer.
const Source = "experiment-intake"

// Config is semdev's own `experiment` section of the bootstrap config file.
// The framework's config loader ignores unknown top-level keys, so this is
// parsed by a SECOND read of the same file (LoadConfig) — one operator-facing
// config, semdev-owned keys.
type Config struct {
	// Condition is "" (unconfigured baseline default), "baseline", or
	// "semsource".
	Condition string `json:"condition"`
	// SemsourceEndpoint is semsource's HTTP base (e.g.
	// "http://localhost:8080"). Required when Condition is "semsource";
	// unused otherwise.
	SemsourceEndpoint string `json:"semsource_endpoint"`
}

// LoadConfig reads the `experiment` section from the bootstrap config file at
// path. A file without the section yields the zero Config (baseline default).
func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("experiment: read config %s: %w", path, err)
	}
	var wrapper struct {
		Experiment Config `json:"experiment"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return Config{}, fmt.Errorf("experiment: parse config %s: %w", path, err)
	}
	cfg := wrapper.Experiment
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("experiment: %s: %w", path, err)
	}
	return cfg, nil
}

// Validate rejects an unknown condition and a semsource condition without an
// endpoint — loudly, at boot, never at first tool call.
func (c Config) Validate() error {
	switch c.Condition {
	case "", ConditionBaseline, ConditionSemsource:
	default:
		return fmt.Errorf("experiment: unknown condition %q (want \"\", %q, or %q)", c.Condition, ConditionBaseline, ConditionSemsource)
	}
	if c.Condition == ConditionSemsource && c.SemsourceEndpoint == "" {
		return fmt.Errorf("experiment: condition %q requires semsource_endpoint", ConditionSemsource)
	}
	return nil
}

// Semsource reports whether the semsource condition is declared.
func (c Config) Semsource() bool { return c.Condition == ConditionSemsource }

// statusReader is the slice of the semsource client the readiness gate needs.
type statusReader interface {
	Status(ctx context.Context) (semsource.Status, error)
}

// CheckReadiness is the D4 per-signal readiness gate: BOTH the structural
// index (gating code_context/code_impact/doc_context) AND the
// retrieval/embedding pipeline (gating code_search) must be available AND
// ready. NOT the aggregate phase alone — "phase: ready" means every source
// reported, and a cold-embeddings gateway returns weak 200-OK code_search
// results: silent degradation with no errResult, the exact hole a phase-only
// probe leaves open. A failure names the signal so the operator knows what to
// wait for.
func CheckReadiness(ctx context.Context, r statusReader) error {
	s, err := r.Status(ctx)
	if err != nil {
		return fmt.Errorf("experiment: semsource readiness probe failed: %w", err)
	}
	if !s.Index.Available || !s.Index.Ready {
		return fmt.Errorf("experiment: semsource structural index not ready (available=%t ready=%t state=%q phase=%q) — code_context/code_impact/doc_context would answer from an incomplete index", s.Index.Available, s.Index.Ready, s.Index.State, s.Phase)
	}
	if !s.Embedding.Available || !s.Embedding.Ready {
		return fmt.Errorf("experiment: semsource embedding pipeline not ready (available=%t ready=%t state=%q phase=%q) — code_search would return weak 200-OK results (silent degradation)", s.Embedding.Available, s.Embedding.Ready, s.Embedding.State, s.Phase)
	}
	return nil
}

// StampCondition writes the single evidence-label fact on the run entity
// (writer experiment-intake, single-valued upsert). Callers only invoke it
// for a DECLARED condition; the unconfigured default stamps nothing.
//
// It takes the CONCRETE *graphown.Writer, not a local interface. An interface here
// would let a test double substitute ABOVE the seam and bypass ContractFor — and
// that resolution is the only behavioral proof that experiment.run.condition is
// classed onto the entity class this writer actually stamps (design D3b). Fake
// projection.OwnedReplacer underneath instead, as the tool suites do.
func StampCondition(ctx context.Context, w *graphown.Writer, runEntityID, condition string) error {
	tr := message.Triple{
		Subject:    runEntityID,
		Predicate:  ConditionPredicate,
		Object:     condition,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := w.Replace(ctx, runEntityID, []message.Triple{tr}); err != nil {
		return fmt.Errorf("experiment: stamp %s=%q on %s: %w", ConditionPredicate, condition, runEntityID, err)
	}
	return nil
}

// Launch is the front-door mint path with the experiment surface applied —
// THE SANCTIONED LAUNCH SEAM: the M1 real-run driver (the intake adapter /
// operator CLI, which does not exist yet) MUST mint condition runs through
// this function; nothing else may stamp a condition (the e2e plumbing journey
// hand-composes the same probe→publish→bind→stamp order against the shared
// front-of-arc helpers and is pinned to it — a deliberate, annotated
// exemption, not a precedent). FAIL-CLOSED in this exact order (D4):
//
//  1. semsource condition → per-signal readiness probe FIRST; a probe failure
//     fails the launch loudly BEFORE anything is published — no run is
//     minted, no half-labeled evidence exists.
//  2. publish the front-door wake (the caller's publisher — the intake
//     adapter or the e2e driver).
//  3. bind the minted run entity (the run is minted downstream by the
//     coordinator rule's run_scope=new spawn, so the driver binds by
//     observation).
//  4. a DECLARED condition is stamped on the run; unconfigured stamps
//     nothing (baseline default, zero new facts).
//
// probe is required only for the semsource condition (pass nil otherwise).
func Launch(
	ctx context.Context,
	cfg Config,
	probe func(context.Context) error,
	publish func(context.Context) error,
	bindRun func(context.Context) (string, error),
	writer *graphown.Writer,
) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	if cfg.Semsource() {
		if probe == nil {
			return "", fmt.Errorf("experiment: condition %q requires a readiness probe (nil probe is a wiring fault, not a pass)", ConditionSemsource)
		}
		if err := probe(ctx); err != nil {
			return "", fmt.Errorf("experiment: launch aborted, no run minted: %w", err)
		}
	}
	if err := publish(ctx); err != nil {
		return "", fmt.Errorf("experiment: publish front-door wake: %w", err)
	}
	runEntityID, err := bindRun(ctx)
	if err != nil {
		return "", fmt.Errorf("experiment: bind the minted run: %w", err)
	}
	if cfg.Condition != "" {
		if err := StampCondition(ctx, writer, runEntityID, cfg.Condition); err != nil {
			// The one unavoidable partial tail: the run exists (published+bound)
			// but the label write failed. The error is LOUD and the run entity is
			// returned so the operator can see which run is unlabeled — the
			// ledger must treat such a run as degraded and ineligible as
			// condition evidence (its rules say exactly that).
			return runEntityID, err
		}
	}
	return runEntityID, nil
}
