// Package verify is the deterministic core of semdev's clean-room gate (G4, the
// make-or-break): the release decision that makes "green" mean a verified PR. It
// ports the shape of semspec's pure verify.Decide — an ordered, fail-closed verdict
// over authoritative inputs the wiring gathers — re-homed to semdev's single-run
// clean-room model (a fresh-isolation resolve+build+test), and deliberately leaves
// behind semspec's execution-bridge reconciler shell (B3) and its multi-unit
// assembly guards.
//
// Decide is PURE — no exec, no git, no graph, no LLM — so the release decision is
// exhaustively unit-testable offline: the false-green class (a warm cache masking a
// fabricated dependency) is a `go test` failure here, never a paid-run discovery.
// The I/O that gathers the evidence (spin up fresh isolation with a distinct
// cache home, run the artifact's own resolve+build+test, capture exit codes) lives
// at the wiring; every field of Input is HARNESS-MEASURED (G3) — no model supplies
// an outcome. The harness draws the one line Decide cannot: it sets Completed=false
// for ANY transport/infrastructure error (including a resolve step that could not
// reach the network), so by the time Decide sees Completed=true a failing check is
// a genuine artifact failure, not an infra hiccup.
package verify

import (
	"sort"
	"strings"
)

// Outcome is the clean-room gate's terminal classification.
//
//   - Pass  — every check passed in fresh isolation; the PR may open.
//   - Fail  — a GENUINE artifact failure (unresolved/fabricated dependency, failing
//     tests); terminal — the harness records it and the run does not open a PR.
//   - Retry — the proof could not be trusted for an infrastructure reason (the
//     harness did not complete, or did not establish a fresh cache home). NOT a
//     terminal reject: a transient/infra fault must never fail a good artifact
//     (the wiring re-runs, and a persistent retry parks toward the human).
type Outcome string

// The three terminal classifications a clean-room verdict can carry.
const (
	OutcomePass  Outcome = "pass"
	OutcomeFail  Outcome = "fail"
	OutcomeRetry Outcome = "retry"
)

// Check names for the four ordered guards, in evaluation order.
const (
	checkCompletion = "completion"
	checkIsolation  = "isolation"
	checkResolution = "resolution"
	checkTests      = "tests"
)

// CheckResult is one guard's outcome, named for the operator.
type CheckResult struct {
	Name   string
	Passed bool
	Detail string
}

// Input is the clean-room execution evidence the verify harness gathered. Every
// field is harness-measured (G3); Decide only judges it.
type Input struct {
	// Completed is true when the harness reached a definitive artifact conclusion —
	// it ran the resolve+build+test steps far enough to judge the artifact (all
	// steps ran, OR a step failed GENUINELY so later steps are moot, e.g. a resolve
	// that could not find a coordinate cold makes running the tests pointless).
	// False means it could NOT complete — a transport/infrastructure error, not a
	// verdict. TransportError carries the reason.
	Completed      bool
	TransportError string

	// FreshCacheHome asserts the proof ran with a distinct build-cache home
	// isolated from any warm environment (the universal G4 control, design D5). A
	// proof without a fresh cache home cannot prove reproducibility. In a correct
	// harness this is always true once Completed is true (the harness establishes
	// isolation before running); Decide asserts it as defense in depth.
	FreshCacheHome  bool
	CacheHomeDetail string

	// Resolved is the dependency-resolution proof: the artifact's OWN declared
	// dependencies resolved in the fresh cache. A fabricated or missing dependency
	// that only a warm cache masked FAILS here — the cache-masked-fabrication
	// reject. ResolveDetail carries the unresolved excerpt.
	Resolved      bool
	ResolveDetail string

	// TestsPassed is the artifact's own tests, run in the fresh isolation.
	TestsPassed bool
	TestsDetail string
}

// Verdict is the composed clean-room decision.
type Verdict struct {
	Outcome Outcome
	Checks  []CheckResult
}

// Decide composes the clean-room evidence into one ordered, fail-closed verdict.
//
// Order and fail-closed semantics:
//  1. completion (fail-closed retry) — the harness must have run to completion; a
//     transport/infra failure yields Retry, never a terminal reject.
//  2. isolation (G4) — a distinct fresh cache home must be asserted; its absence is
//     an infra/harness fault (Retry), not an artifact fault.
//  3. resolution — the artifact's declared dependencies resolved cold; an
//     unresolved/fabricated dependency is a GENUINE failure (Fail).
//  4. tests — the artifact's own tests passed in isolation; a failure is genuine
//     (Fail).
//
// Pass requires all four. A completion or isolation failure classifies Retry; a
// resolution or test failure classifies Fail. Completion and isolation are judged
// first because until they hold, the resolution/test signals are not trustworthy
// evidence about the artifact at all.
func Decide(in Input) Verdict {
	completion := completionCheck(in)
	isolation := isolationCheck(in)
	resolution := resolutionCheck(in)
	tests := testsCheck(in)
	checks := []CheckResult{completion, isolation, resolution, tests}

	// Outcome is derived from the check results (single source of truth). Infra
	// faults (completion, isolation) classify Retry and take precedence: an
	// untrusted proof must not be read as an artifact verdict either way. Only once
	// completion and isolation hold is a failed resolution/tests check a genuine
	// artifact failure.
	switch {
	case !completion.Passed, !isolation.Passed:
		return Verdict{Outcome: OutcomeRetry, Checks: checks}
	case !resolution.Passed, !tests.Passed:
		return Verdict{Outcome: OutcomeFail, Checks: checks}
	default:
		return Verdict{Outcome: OutcomePass, Checks: checks}
	}
}

// completionCheck passes only when the harness ran to completion AND reported no
// transport error. A non-empty TransportError alongside Completed=true is an
// incoherent harness report — treated as a completion doubt (Retry, defense in
// depth), never read as a trustworthy verdict.
func completionCheck(in Input) CheckResult {
	if in.Completed && in.TransportError == "" {
		return CheckResult{Name: checkCompletion, Passed: true, Detail: "clean-room harness ran to completion"}
	}
	detail := "clean-room harness did not complete (transport/infrastructure error) — retry, not a verdict"
	if in.TransportError != "" {
		detail += ": " + in.TransportError
	}
	return CheckResult{Name: checkCompletion, Passed: false, Detail: detail}
}

func isolationCheck(in Input) CheckResult {
	if in.FreshCacheHome {
		detail := "distinct fresh build-cache home established"
		if in.CacheHomeDetail != "" {
			detail += ": " + in.CacheHomeDetail
		}
		return CheckResult{Name: checkIsolation, Passed: true, Detail: detail}
	}
	detail := "no distinct fresh build-cache home — a warm cache could mask a fabricated dependency (harness must isolate before proving)"
	if in.CacheHomeDetail != "" {
		detail += ": " + in.CacheHomeDetail
	}
	return CheckResult{Name: checkIsolation, Passed: false, Detail: detail}
}

func resolutionCheck(in Input) CheckResult {
	if in.Resolved {
		return CheckResult{Name: checkResolution, Passed: true, Detail: detailOr(in.ResolveDetail, "dependencies resolved cold from the artifact's own declarations")}
	}
	return CheckResult{Name: checkResolution, Passed: false, Detail: detailOr(in.ResolveDetail, "dependencies did not resolve in the fresh cache — missing or fabricated coordinate")}
}

func testsCheck(in Input) CheckResult {
	if in.TestsPassed {
		return CheckResult{Name: checkTests, Passed: true, Detail: detailOr(in.TestsDetail, "the artifact's own tests passed in fresh isolation")}
	}
	return CheckResult{Name: checkTests, Passed: false, Detail: detailOr(in.TestsDetail, "the artifact's own tests failed in fresh isolation")}
}

func detailOr(detail, fallback string) string {
	if strings.TrimSpace(detail) != "" {
		return detail
	}
	return fallback
}

// FailedChecks returns the checks that did not pass — the operator's actionable
// list when a verdict blocks the PR, in stable name order.
func (v Verdict) FailedChecks() []CheckResult {
	var failed []CheckResult
	for _, c := range v.Checks {
		if !c.Passed {
			failed = append(failed, c)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].Name < failed[j].Name })
	return failed
}
