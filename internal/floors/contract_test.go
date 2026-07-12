package floors

import (
	"strings"
	"testing"
)

// The check_floors wrapper upserts floor.finding facts WITHOUT clearing — it relies
// on a FIXED floor set so the sub-keys replace by predicate. This pins that contract:
// CheckAll must always return exactly one finding per declared floor, in a stable
// set, for any attempt. If a floor ever became conditional (a variable-length
// result), a stale floor.finding.<idx>.<floor>.* sub-key would survive a re-eval and
// the loop gate would read a stale verdict — the single-valued-append seam. Red-first
// guard for the wrapper's no-clear upsert (semstreams-reviewer MEDIUM).
func TestCheckAllReturnsFixedFloorSet(t *testing.T) {
	want := map[string]bool{
		FloorPresence:       true,
		FloorTestsMustExist: true,
		FloorVacuousTest:    true,
		FloorStub:           true,
		FloorSourceBuild:    true,
		FloorAntiMock:       true,
	}
	// Attempts that pass, that don't apply, and that fail to parse — the RETURNED
	// floor set must be identical each time: never shorter, never duplicated.
	for _, a := range []Attempt{
		{}, // empty: floors that do not apply still return a finding
		{TargetFiles: []string{"h.go"}, Files: []File{{Path: "h.go", Content: "package h\n\nfunc F() {}\n"}}},
		{Files: []File{{Path: "x.go", Content: "package x\n@@@ not valid go"}}}, // unparseable
	} {
		findings := CheckAll(a)
		if len(findings) != len(want) {
			t.Fatalf("CheckAll returned %d findings, want exactly %d (the fixed floor set) for %+v", len(findings), len(want), a)
		}
		seen := map[string]bool{}
		for _, f := range findings {
			if !want[f.Floor] {
				t.Errorf("CheckAll returned an unexpected floor %q", f.Floor)
			}
			if seen[f.Floor] {
				t.Errorf("CheckAll returned floor %q twice — the fixed-set / no-clear upsert assumption breaks", f.Floor)
			}
			seen[f.Floor] = true
		}
	}
}

// Floor names are dot-free: the wrapper keys floor.finding.<taskIndex>.<floorName>.
// <field>, so a dot inside a floor name would fracture the predicate segments and
// mis-key the finding.
func TestFloorNamesAreDotFree(t *testing.T) {
	for _, name := range []string{FloorPresence, FloorTestsMustExist, FloorVacuousTest, FloorStub, FloorSourceBuild, FloorAntiMock} {
		if strings.Contains(name, ".") {
			t.Errorf("floor name %q contains a dot — it must be a single predicate segment", name)
		}
	}
}
