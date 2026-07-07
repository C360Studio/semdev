package measurement

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/c360studio/semstreams/message"
)

// ResultsFromFacts reconstructs the []Result stamped under the measurement.result.*
// namespace (measure_task's owned per-task facts) back into the typed measurements
// the review gate feeds to CanApprove. It is the inverse of the tool's projection
// and shares the fact-key consts (ResultPrefix/Fact*), so the write and read sides
// cannot drift on the KEYS — a round-trip pin in the measure_task tool holds the
// VALUE encoding together.
//
// It FAILS CLOSED. A malformed or incomplete measurement fact — a non-bool ran, a
// non-int exit_code, a missing sub-key — is an ERROR, never a defaulted pass. The
// review gate must not approve on evidence it cannot parse; a silent default here
// would be the exact false-green G3 exists to prevent. Predicates outside the
// namespace (e.g. task.spec facts read off the same entity) are ignored, not
// errors. Results come back sorted by task index.
func ResultsFromFacts(triples []message.Triple) ([]Result, error) {
	byIndex := map[int]map[string]string{}
	for _, tr := range triples {
		rest, ok := strings.CutPrefix(tr.Predicate, ResultPrefix)
		if !ok {
			continue // not a measurement fact
		}
		idxStr, field, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		i, err := strconv.Atoi(idxStr)
		if err != nil {
			continue // measurement.result.<non-int>.* is not a task-indexed measurement
		}
		obj, ok := tr.Object.(string)
		if !ok {
			return nil, fmt.Errorf("measurement fact %q has non-string object %T", tr.Predicate, tr.Object)
		}
		if byIndex[i] == nil {
			byIndex[i] = map[string]string{}
		}
		byIndex[i][field] = obj
	}

	indices := make([]int, 0, len(byIndex))
	for i := range byIndex {
		indices = append(indices, i)
	}
	sort.Ints(indices)

	out := make([]Result, 0, len(indices))
	for _, i := range indices {
		r, err := resultFromFields(i, byIndex[i])
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// resultFromFields builds one Result from a single task's sub-keys, requiring every
// field the writer stamps. A missing or malformed field errors (fail closed): a
// partial measurement is not trustworthy evidence.
func resultFromFields(i int, f map[string]string) (Result, error) {
	r := Result{TaskID: strconv.Itoa(i)}
	var err error
	if r.Command, err = reqString(i, f, FactCommand); err != nil {
		return Result{}, err
	}
	if r.Ran, err = reqBool(i, f, FactRan); err != nil {
		return Result{}, err
	}
	if r.ExitCode, err = reqInt(i, f, FactExitCode); err != nil {
		return Result{}, err
	}
	if r.TimedOut, err = reqBool(i, f, FactTimedOut); err != nil {
		return Result{}, err
	}
	// Passed is read back for completeness/audit; CanApprove re-derives from the
	// raw evidence above and ignores this stored value, so a tampered passed cannot
	// pass the gate — but a MISSING one still fails closed as an incomplete fact.
	if r.Passed, err = reqBool(i, f, FactPassed); err != nil {
		return Result{}, err
	}
	return r, nil
}

func reqString(i int, f map[string]string, field string) (string, error) {
	v, ok := f[field]
	if !ok {
		return "", fmt.Errorf("measurement.result.%d is missing %s", i, field)
	}
	return v, nil
}

func reqBool(i int, f map[string]string, field string) (bool, error) {
	v, ok := f[field]
	if !ok {
		return false, fmt.Errorf("measurement.result.%d is missing %s", i, field)
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("measurement.result.%d.%s is not a bool (%q): %w", i, field, v, err)
	}
	return b, nil
}

func reqInt(i int, f map[string]string, field string) (int, error) {
	v, ok := f[field]
	if !ok {
		return 0, fmt.Errorf("measurement.result.%d is missing %s", i, field)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("measurement.result.%d.%s is not an int (%q): %w", i, field, v, err)
	}
	return n, nil
}
