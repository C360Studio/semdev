package cleanroom

import (
	"context"
	"strings"
	"testing"
)

func upGo(t *testing.T, r LocalRunner, workDir string) Sandbox {
	t.Helper()
	sb, err := r.Up(context.Background(), workDir, []string{"GOMODCACHE", "GOCACHE"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	t.Cleanup(func() { _ = r.Down(context.Background(), sb) })
	return sb
}

// G4 (8.7 pin): every proof gets a DISTINCT fresh build-cache home — two Up calls
// never share a cache directory, so a warm cache from one proof cannot mask a
// fabrication in another.
func TestLocalRunnerDistinctCacheHomesPerUp(t *testing.T) {
	r := LocalRunner{}
	a := upGo(t, r, t.TempDir())
	b := upGo(t, r, t.TempDir())

	if len(a.CacheHomes) != 2 || len(b.CacheHomes) != 2 {
		t.Fatalf("want two cache homes each, got %d and %d", len(a.CacheHomes), len(b.CacheHomes))
	}
	seen := map[string]bool{}
	for _, h := range append(append([]string{}, a.CacheHomes...), b.CacheHomes...) {
		if seen[h] {
			t.Errorf("cache home %q reused across proofs — not distinct (G4)", h)
		}
		seen[h] = true
	}
}

// Up binds each fresh cache home into the sandbox environment under its env name,
// and Exec runs commands under that environment — so the resolve/test steps see the
// COLD cache, the universal G4 control.
func TestLocalRunnerInjectsFreshCacheEnv(t *testing.T) {
	r := LocalRunner{}
	sb := upGo(t, r, t.TempDir())
	if sb.Env["GOMODCACHE"] != sb.CacheHomes[0] || sb.Env["GOCACHE"] != sb.CacheHomes[1] {
		t.Fatalf("env not bound to fresh homes: env=%v homes=%v", sb.Env, sb.CacheHomes)
	}
	res, err := r.Exec(context.Background(), sb, []string{"sh", "-c", "printf %s \"$GOMODCACHE\""})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := strings.TrimSpace(res.Stdout); got != sb.CacheHomes[0] {
		t.Errorf("GOMODCACHE inside sandbox = %q, want the fresh home %q", got, sb.CacheHomes[0])
	}
}

// The exit-vs-transport contract: a command that RAN returns its exit code with a
// nil error (a verdict), even non-zero; a command that could not be STARTED returns
// a non-nil error (a transport fault the harness reads as "did not complete").
func TestLocalRunnerExitVsTransport(t *testing.T) {
	r := LocalRunner{}
	sb := upGo(t, r, t.TempDir())

	ok, err := r.Exec(context.Background(), sb, []string{"sh", "-c", "exit 0"})
	if err != nil || ok.ExitCode != 0 {
		t.Errorf("exit 0: got (%+v, %v), want ExitCode 0 nil err", ok, err)
	}

	nonzero, err := r.Exec(context.Background(), sb, []string{"sh", "-c", "exit 7"})
	if err != nil {
		t.Errorf("a completed non-zero command must return nil error (a verdict), got %v", err)
	}
	if nonzero.ExitCode != 7 {
		t.Errorf("exit 7: ExitCode = %d, want 7", nonzero.ExitCode)
	}

	_, err = r.Exec(context.Background(), sb, []string{"semdev-definitely-not-a-real-binary-xyz"})
	if err == nil {
		t.Error("a command that could not start must return a non-nil (transport) error")
	}
}

// Down removes the minted cache homes; a torn-down sandbox leaves nothing behind.
func TestLocalRunnerDownCleansCacheHomes(t *testing.T) {
	r := LocalRunner{}
	sb, err := r.Up(context.Background(), t.TempDir(), []string{"GOMODCACHE"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := r.Down(context.Background(), sb); err != nil {
		t.Fatalf("Down: %v", err)
	}
	// Re-running Exec's dir is fine; assert the home is gone via a probe.
	probe, _ := r.Exec(context.Background(), sb, []string{"sh", "-c", "test -d \"$GOMODCACHE\""})
	if probe.ExitCode == 0 {
		t.Errorf("cache home %q still exists after Down", sb.CacheHomes[0])
	}
}
