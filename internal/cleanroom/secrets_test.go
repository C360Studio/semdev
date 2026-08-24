package cleanroom

import (
	"slices"
	"strings"
	"testing"
)

// execArgs injects governed secrets as the PASS-THROUGH `-e TOKEN` form (name only) —
// the VALUE must never appear on the argv (world-readable /proc/<pid>/cmdline), only in
// the docker process env (SB2c). Provable without docker.
func TestExecArgsInjectsSecretsByNameNotValue(t *testing.T) {
	c := NewContainerRunnerWithSecrets("img", map[string]string{"TOKEN": "sekret"})
	got := c.execArgs(Sandbox{WorkDir: "/work", Handle: "abc"})
	// The name rides a bare `-e TOKEN` pass-through...
	if !slices.Contains(got, "TOKEN") {
		t.Errorf("execArgs = %v, want a bare -e TOKEN pass-through", got)
	}
	// ...and the VALUE never appears on the argv.
	for _, a := range got {
		if strings.Contains(a, "sekret") {
			t.Errorf("secret VALUE leaked onto the exec argv: %q", a)
		}
	}
	// A no-secret runner injects nothing extra.
	plain := NewContainerRunner("img").execArgs(Sandbox{WorkDir: "/work", Handle: "abc"})
	if slices.Contains(plain, "TOKEN") {
		t.Errorf("a no-secret runner leaked a secret flag: %v", plain)
	}
}
