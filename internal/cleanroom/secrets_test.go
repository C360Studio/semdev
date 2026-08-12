package cleanroom

import (
	"context"
	"os/exec"
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

// Docker-gated: a secret injected via NewContainerRunnerWithSecrets is present in the
// container at exec time, but is NOT baked into the container's persistent config (it
// rides `docker exec -e`, never `docker run -e`) — so `docker inspect` never carries it
// (SB2c). Skips when docker is absent.
func TestContainerRunnerInjectsSecretExecOnly(t *testing.T) {
	ctx := context.Background()
	if err := DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	c := NewContainerRunnerWithSecrets("busybox:latest", map[string]string{"TOKEN": "sekret-value-xyz"})
	sb, err := c.Up(ctx, t.TempDir(), []string{"CACHE"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer func() { _ = c.Down(ctx, sb) }()

	// The secret is visible to a command run in the sandbox.
	res, err := c.Exec(ctx, sb, []string{"sh", "-c", "echo $TOKEN"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Stdout, "sekret-value-xyz") {
		t.Errorf("secret not injected into the exec env; stdout = %q", res.Stdout)
	}

	// ...but it is NOT in the container's persistent config (exec-only, not `docker run -e`).
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.Config.Env}}", sb.Handle).Output()
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if strings.Contains(string(out), "sekret-value-xyz") {
		t.Errorf("secret leaked into the container's persistent config: %s", out)
	}
}
