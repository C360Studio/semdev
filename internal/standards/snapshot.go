package standards

// The provision-time standards snapshot (standards-via-lessons D7, revised).
//
// The checks lane needs the repo's declared law AS PROVISIONED — the attempt must not be
// able to weaken, delete, or add a gating check by editing the file it is being judged
// against. The obvious implementation reads the file from the run's base git ref, and it
// is NOT sufficient: the run's checkout is bind-mounted read-WRITE into the sandbox
// (cleanroom.buildRunArgs mounts the checkout root at /work with no readonly flag), the
// `.git` directory rides along inside it, and model-authored code executes in that
// container as a matter of normal operation. An attempt that runs
// `git update-ref refs/semdev/base <other-commit>` — a single line in a TestMain — moves
// the ref the gate reads, and every required check silently disappears.
//
// So the bytes are captured at PROVISION, before any model turn for this run has run, and
// held here — outside the container's reach entirely. What the gate reads is not a thing
// the attempt can address.
//
// The store is in-memory and shared between the provision station and the floors station,
// exactly like runspace.Checkouts and runspace.Sandboxes. A missing snapshot therefore
// FAILS CLOSED (the lane faults), never "this repo declared no checks": after a process
// restart the warm sandbox is gone too, so a run in that state cannot honestly be measured
// anyway.

import "sync"

// Snapshot is one run's captured standards file. Declared=false records that the run was
// provisioned and the repo declared nothing — which is different from never having been
// captured at all.
type Snapshot struct {
	Raw      []byte
	Declared bool
}

// Snapshots holds the per-run provision-time captures.
type Snapshots struct {
	mu sync.RWMutex
	m  map[string]Snapshot
}

// NewSnapshots builds the shared store boot wires into both stations.
func NewSnapshots() *Snapshots { return &Snapshots{m: map[string]Snapshot{}} }

// Capture records what the run was provisioned with. raw is nil when the repo declared no
// standards file.
func (s *Snapshots) Capture(runEntityID string, raw []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy: the caller's slice came from a file read it may reuse, and this value has to
	// stay identical to what provisioning actually validated.
	var stored []byte
	if raw != nil {
		stored = append([]byte(nil), raw...)
	}
	s.m[runEntityID] = Snapshot{Raw: stored, Declared: raw != nil}
}

// Lookup returns the run's capture. ok=false means provisioning never captured one for
// this run, which callers must treat as a fault rather than as "declared nothing".
func (s *Snapshots) Lookup(runEntityID string) (Snapshot, bool) {
	if s == nil {
		return Snapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.m[runEntityID]
	return snap, ok
}

// Forget drops a run's capture (the runtime reaper's hook; unused today, kept so the map
// is not architecturally unbounded).
func (s *Snapshots) Forget(runEntityID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, runEntityID)
}
