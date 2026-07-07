package measurement

import (
	"strconv"
	"testing"

	"github.com/c360studio/semstreams/message"
)

// mfact builds one measurement.result.<i>.<field> triple as measure_task stamps it.
func mfact(i int, field, obj string) message.Triple {
	return message.Triple{
		Predicate: ResultPrefix + strconv.Itoa(i) + "." + field,
		Object:    obj,
		Source:    "measurement-harness",
	}
}

// fullMeasurement is one task's complete measurement fact set.
func fullMeasurement(i int, exit int, ran, timedOut, passed bool) []message.Triple {
	return []message.Triple{
		mfact(i, FactCommand, "go test ./..."),
		mfact(i, FactRan, strconv.FormatBool(ran)),
		mfact(i, FactExitCode, strconv.Itoa(exit)),
		mfact(i, FactTimedOut, strconv.FormatBool(timedOut)),
		mfact(i, FactPassed, strconv.FormatBool(passed)),
	}
}

// A complete measurement fact set reconstructs to the typed Result.
func TestResultsFromFactsHappy(t *testing.T) {
	rs, err := ResultsFromFacts(fullMeasurement(0, 0, true, false, true))
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("want 1 result, got %d", len(rs))
	}
	got := rs[0]
	if got.TaskID != "0" || got.Command != "go test ./..." || !got.Ran || got.ExitCode != 0 || got.TimedOut || !got.Passed {
		t.Errorf("reconstructed wrong: %+v", got)
	}
}

// Multiple tasks reconstruct sorted by index, and non-measurement predicates on the
// same entity (task.spec facts) are ignored, not errors.
func TestResultsFromFactsMultipleSortedIgnoresForeign(t *testing.T) {
	var triples []message.Triple
	triples = append(triples, fullMeasurement(2, 1, true, false, false)...)
	triples = append(triples, fullMeasurement(0, 0, true, false, true)...)
	triples = append(triples,
		message.Triple{Predicate: "task.spec.0.goal", Object: "add the guard", Source: "task-projector"},
		message.Triple{Predicate: "review.verdict", Object: "approved", Source: "reviewer-quinn"},
	)
	rs, err := ResultsFromFacts(triples)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(rs) != 2 || rs[0].TaskID != "0" || rs[1].TaskID != "2" {
		t.Fatalf("want tasks [0 2] sorted, got %+v", rs)
	}
}

// Fail closed: a missing sub-key is an error, never a defaulted (false-green)
// Result — an incomplete measurement is untrustworthy evidence.
func TestResultsFromFactsMissingSubKeyFailsClosed(t *testing.T) {
	full := fullMeasurement(0, 0, true, false, true)
	for _, drop := range []string{FactRan, FactExitCode, FactTimedOut, FactPassed, FactCommand} {
		var kept []message.Triple
		for _, tr := range full {
			if tr.Predicate != ResultPrefix+"0."+drop {
				kept = append(kept, tr)
			}
		}
		if _, err := ResultsFromFacts(kept); err == nil {
			t.Errorf("dropping %s must error (fail closed), got nil", drop)
		}
	}
}

// Fail closed: a malformed exit_code (non-int) or ran (non-bool) is an error, not a
// default.
func TestResultsFromFactsMalformedFailsClosed(t *testing.T) {
	bad := fullMeasurement(0, 0, true, false, true)
	bad[2] = mfact(0, FactExitCode, "not-an-int")
	if _, err := ResultsFromFacts(bad); err == nil {
		t.Error("a non-int exit_code must error (fail closed)")
	}

	bad2 := fullMeasurement(0, 0, true, false, true)
	bad2[1] = mfact(0, FactRan, "maybe")
	if _, err := ResultsFromFacts(bad2); err == nil {
		t.Error("a non-bool ran must error (fail closed)")
	}
}

// No measurement facts reconstruct to an empty slice, not an error — the review
// gate reads "no evidence" and CanApprove blocks on the missing required tasks.
func TestResultsFromFactsEmpty(t *testing.T) {
	rs, err := ResultsFromFacts(nil)
	if err != nil {
		t.Fatalf("empty must not error: %v", err)
	}
	if len(rs) != 0 {
		t.Errorf("want empty, got %+v", rs)
	}
}

// The reconstruction preserves the stored passed verbatim (audit), while CanApprove
// re-derives — so a measurement whose stored passed was flipped to true but whose
// exit code is non-zero still cannot approve.
func TestResultsFromFactsCanApproveReDerivesPastTamperedPassed(t *testing.T) {
	// Stored passed=true but exit_code=1: a tampered/stale headline.
	tampered := fullMeasurement(0, 1, true, false, true)
	rs, err := ResultsFromFacts(tampered)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if !rs[0].Passed {
		t.Fatal("reconstruction should report the stored passed verbatim")
	}
	if CanApprove([]string{"0"}, rs) {
		t.Error("CanApprove must re-derive from exit_code=1 and block, ignoring the stored passed=true")
	}
}
