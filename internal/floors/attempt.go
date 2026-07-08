package floors

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// AttemptID is a stable content hash of an attempt's evaluated source — the identity
// a floor finding is bound to (stamped as floor.finding.<taskIndex>.attempt). It lets
// a later gate tell whether a stamped finding evaluated the CURRENT attempt or a
// stale earlier one: a floor.finding whose attempt id does not match the run's
// current attempt must NOT be read as a current pass ("floors cannot be skipped"
// would otherwise false-green on stale evidence). It hashes each file's path and
// content — and the task's target files — in sorted, length-prefixed order, so it is
// deterministic and changes iff the evaluated source changes. Shared so the writer
// (check_floors) and any future gate/loop compute the same id and cannot drift.
func AttemptID(a Attempt) string {
	files := append([]File(nil), a.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	targets := append([]string(nil), a.TargetFiles...)
	sort.Strings(targets)

	h := sha256.New()
	for _, f := range files {
		// Length-prefix path and content so no concatenation of a different split can
		// collide (e.g. {"ab","c"} vs {"a","bc"}).
		fmt.Fprintf(h, "f%d:%s%d:%s", len(f.Path), f.Path, len(f.Content), f.Content)
	}
	for _, t := range targets {
		fmt.Fprintf(h, "t%d:%s", len(t), t)
	}
	return hex.EncodeToString(h.Sum(nil))
}
