// Package floors is semdev's deterministic floor library (S1): a set of pure,
// offline structural checks over a dev-loop attempt's authored source. Each floor
// answers one fabrication-shaped question a persona cannot be trusted to answer
// about its own work — did it author a test at all, is that test vacuous, did it
// ship a stub, does the source even parse, did it "test" only a mock of the code
// under test. A rejecting Finding blocks advance to semantic review; the loop
// routes on it and cannot skip it (tasks 6.5–6.6).
//
// These are the M0 Go profiles. semspec discovered its floors severed from
// production and JVM-shaped (the OSH hard#2 rejects); semdev re-homes them as pure
// functions with red-first tables so the checks are unit-truth, not persona
// compliance. The floors are STRUCTURAL: they read source, they never run it —
// running a command and stamping its outcome is harness-measurement (group 7),
// and proving it builds cold is clean-room verify (group 8). Naming each floor
// for exactly what it inspects keeps it from overclaiming.
//
// The package writes no facts: a floor is a pure func(Attempt) Finding. The
// floor-tools wrapper (a later increment) stamps each Finding as a floor.finding
// fact; that keeps the deterministic checks offline-testable and G5-clean.
package floors

// Floor names — the stable identifiers a Finding carries and the loop routes on.
const (
	FloorPresence       = "presence"
	FloorTestsMustExist = "tests-must-exist"
	FloorVacuousTest    = "vacuous-test"
	FloorStub           = "stub"
	FloorSourceBuild    = "source-build"
	FloorAntiMock       = "anti-mock"
	FloorCleanTree      = "clean-tree"
)

// File is one file an attempt authored or changed, with its full contents. Floors
// read Content; nothing here touches the filesystem, so every floor is a pure
// function and its table is offline.
type File struct {
	Path    string
	Content string
}

// Attempt is the deterministic input to the floors: the files this dev-loop
// iteration authored (with contents), the task's declared target files (from
// the projected task.spec), and the working tree's dirty paths as of resolution.
// Everything a floor needs to reach a verdict is in here — no working directory,
// no exec, no network; the impure git/filesystem reads happen in the resolver.
type Attempt struct {
	Files       []File
	TargetFiles []string
	// DirtyPaths is `git status --porcelain` over the checkout at resolution time —
	// one entry per path that differs from the committed attempt. Non-empty means the
	// working tree diverged from the commit the harness recorded (test-time residue or
	// tampering), so what floors evaluate and what the cold verify clones would differ.
	// The clean-tree floor rejects on it (G7). Empty for a clean tree.
	DirtyPaths []string
}

// Finding is one floor's verdict. A rejecting finding (Passed == false) is a hard
// gate: the loop must not advance that attempt to semantic review while it stands
// (task 6.6). Detail explains the rejection (or notes why the floor passed / did
// not apply), so a park-toward-human is legible.
type Finding struct {
	Floor  string
	Passed bool
	Detail string
}

// pass builds a passing finding for floor with an explanatory note.
func pass(floor, detail string) Finding {
	return Finding{Floor: floor, Passed: true, Detail: detail}
}

// reject builds a rejecting finding for floor with the reason.
func reject(floor, detail string) Finding {
	return Finding{Floor: floor, Passed: false, Detail: detail}
}

// CheckAll runs every floor over the attempt and returns the findings in a stable
// order. It is a convenience for the loop harness and the tests; each floor is
// also callable on its own.
func CheckAll(a Attempt) []Finding {
	return []Finding{
		PresenceOfWork(a),
		TestsMustExist(a),
		VacuousTest(a),
		StubArtifact(a),
		SourceBuild(a),
		AntiMock(a),
		CleanTree(a),
	}
}

// AnyRejected reports whether any finding rejects — the loop's gate predicate.
func AnyRejected(findings []Finding) bool {
	for _, f := range findings {
		if !f.Passed {
			return true
		}
	}
	return false
}
