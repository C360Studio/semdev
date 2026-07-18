package measurement

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/c360studio/semstreams/message"
)

// ResultsFromFacts reconstructs the run's measurement (measure_task's owned facts
// under measurement.result.*) back into the typed Result the review gate feeds to
// CanApprove. It is the inverse of the tool's projection and shares the fact-key
// consts (ResultPrefix/Fact*), so the write and read sides cannot drift on the KEYS —
// a round-trip pin in the measure_task tool holds the VALUE encoding together.
//
// Single-task at M0 (beta.147 D1): the per-task index is out of the predicate, so
// every measurement.result.<field> fact belongs to the one task (TaskID "0"). It
// returns a slice (0 or 1 Result) so CanApprove's required-task shape is unchanged.
//
// It FAILS CLOSED. A malformed or incomplete measurement fact — a non-bool ran, a
// non-int exit-code, a missing sub-key — is an ERROR, never a defaulted pass. The
// review gate must not approve on evidence it cannot parse; a silent default here
// would be the exact false-green G3 exists to prevent. Predicates outside the
// family (e.g. task.spec facts read off the same entity) are ignored, not errors.
func ResultsFromFacts(triples []message.Triple) ([]Result, error) {
	fields := map[string]string{}
	seen := false
	for _, tr := range triples {
		field, ok := strings.CutPrefix(tr.Predicate, ResultPrefix)
		if !ok || field == "" || strings.Contains(field, ".") {
			continue // not a flat measurement.result.<field> fact
		}
		obj, ok := tr.Object.(string)
		if !ok {
			return nil, fmt.Errorf("measurement fact %q has non-string object %T", tr.Predicate, tr.Object)
		}
		fields[field] = obj
		seen = true
	}
	if !seen {
		return nil, nil
	}
	r, err := resultFromFields(fields)
	if err != nil {
		return nil, err
	}
	return []Result{r}, nil
}

// resultFromFields builds the run's Result from its measurement sub-keys, requiring
// every field the writer stamps. A missing or malformed field errors (fail closed):
// a partial measurement is not trustworthy evidence. TaskID is the single-task "0".
func resultFromFields(f map[string]string) (Result, error) {
	r := Result{TaskID: "0"}
	var err error
	if r.Command, err = reqString(f, FactCommand); err != nil {
		return Result{}, err
	}
	if r.Ran, err = reqBool(f, FactRan); err != nil {
		return Result{}, err
	}
	if r.ExitCode, err = reqInt(f, FactExitCode); err != nil {
		return Result{}, err
	}
	if r.TimedOut, err = reqBool(f, FactTimedOut); err != nil {
		return Result{}, err
	}
	// Passed is read back for completeness/audit; CanApprove re-derives from the
	// raw evidence above and ignores this stored value, so a tampered passed cannot
	// pass the gate — but a MISSING one still fails closed as an incomplete fact.
	if r.Passed, err = reqBool(f, FactPassed); err != nil {
		return Result{}, err
	}
	return r, nil
}

func reqString(f map[string]string, field string) (string, error) {
	v, ok := f[field]
	if !ok {
		return "", fmt.Errorf("measurement.result.%s is missing", field)
	}
	return v, nil
}

func reqBool(f map[string]string, field string) (bool, error) {
	v, ok := f[field]
	if !ok {
		return false, fmt.Errorf("measurement.result.%s is missing", field)
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("measurement.result.%s is not a bool (%q): %w", field, v, err)
	}
	return b, nil
}

func reqInt(f map[string]string, field string) (int, error) {
	v, ok := f[field]
	if !ok {
		return 0, fmt.Errorf("measurement.result.%s is missing", field)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("measurement.result.%s is not an int (%q): %w", field, v, err)
	}
	return n, nil
}
