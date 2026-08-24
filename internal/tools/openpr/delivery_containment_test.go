package openpr

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semstreams/message"
)

// envRecordingRunner is an EnvRunner fake recording every invocation's args, extra env,
// which entry point was used, and whether the context carried a deadline — the seam the
// credential-containment and push-deadline pins assert at.
type envRecordingRunner struct {
	args        [][]string
	env         [][]string
	viaEnv      []bool
	hadDeadline []bool
	deadlines   []time.Time
	res         cliexec.Result
}

func (r *envRecordingRunner) record(ctx context.Context, env []string, name string, args []string, viaEnv bool) {
	r.args = append(r.args, append([]string{name}, args...))
	r.env = append(r.env, env)
	r.viaEnv = append(r.viaEnv, viaEnv)
	d, ok := ctx.Deadline()
	r.hadDeadline = append(r.hadDeadline, ok)
	r.deadlines = append(r.deadlines, d)
}

func (r *envRecordingRunner) Run(ctx context.Context, _, name string, args ...string) (cliexec.Result, error) {
	r.record(ctx, nil, name, args, false)
	return r.res, nil
}

func (r *envRecordingRunner) RunWithEnv(ctx context.Context, _ string, env []string, name string, args ...string) (cliexec.Result, error) {
	r.record(ctx, env, name, args, true)
	return r.res, nil
}

func containmentDelivery(runner cliexec.Runner, token, remote string) (*Delivery, *fakeForge) {
	reader := &fakeReader{triples: []message.Triple{
		{Subject: runEntity, Predicate: "attempt.commit.sha", Object: "abc123def", Source: "patch-committer"},
	}}
	forge := &fakeForge{}
	return &Delivery{
		Reader: reader,
		Writer: writerFor(&fakeWriter{}),
		API:    forge,
		Roots:  fakeRoots{root: "/tmp/checkout"},
		Runner: runner,
		Forge:  ForgeConfig{Owner: "acme", Repo: "repo", RemoteURL: remote, BaseBranch: "main"},
		Token:  token,
	}, forge
}

// RED-FIRST PIN P3 (security-forge-containment 2.2): the push carries no credential
// material on argv — the token rides the subprocess env through the shared askpass
// assembly (the clone design-D3 channel), never the URL. RED against the pre-fix
// shape, where pushURL embedded the token as URL userinfo and the whole thing landed
// on `git push`'s argv, visible to every process on the host and re-exposed on every
// station retry.
func TestDeliverPushCarriesNoCredentialOnArgv(t *testing.T) {
	const token = "tok-secret-123"
	runner := &envRecordingRunner{}
	d, _ := containmentDelivery(runner, token, "https://github.com/acme/repo.git")

	if _, err := d.Deliver(context.Background(), runEntity); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(runner.args) != 1 {
		t.Fatalf("want exactly 1 git call (the push), got %d: %v", len(runner.args), runner.args)
	}
	for _, a := range runner.args[0] {
		if strings.Contains(a, token) {
			t.Errorf("credential material on push argv: %q", a)
		}
	}
	// The push authenticates the clone way: env-injected, prompt-free.
	if !runner.viaEnv[0] {
		t.Fatal("push did not go through RunWithEnv — the token has no env channel")
	}
	env := runner.env[0]
	wantEntries := []string{"GIT_TERMINAL_PROMPT=0", cliexec.GitTokenEnv + "=" + token}
	for _, want := range wantEntries {
		found := false
		for _, e := range env {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Errorf("push env missing %q; env=%v", want, env)
		}
	}
	hasAskpass := false
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_ASKPASS=") {
			hasAskpass = true
		}
	}
	if !hasAskpass {
		t.Errorf("push env missing GIT_ASKPASS; env=%v", env)
	}
	// The push URL still names the non-secret x-access-token user so git asks askpass.
	if joined := strings.Join(runner.args[0], " "); !strings.Contains(joined, "x-access-token@github.com") {
		t.Errorf("push URL lost the x-access-token username form: %v", runner.args[0])
	}
}

// RED-FIRST PIN P3b (security-forge-containment 2.2): fail-closed parity with clone — a
// configured token plus a runner that cannot inject env REFUSES the push rather than
// falling back to token-on-argv. RED against the pre-fix shape, where the push happily
// proceeded with the token in the URL.
func TestDeliverRefusesPushWithTokenOnNonEnvRunner(t *testing.T) {
	runner := &fakeRunner{} // plain Runner: no RunWithEnv
	d, forge := containmentDelivery(runner, "tok-secret-123", "https://github.com/acme/repo.git")

	_, err := d.Deliver(context.Background(), runEntity)
	if err == nil {
		t.Fatal("a token with a non-env runner must refuse the push (never argv fallback)")
	}
	if !strings.Contains(err.Error(), "refus") {
		t.Errorf("want the argv-refusal error shape, got: %v", err)
	}
	if len(runner.runs) != 0 {
		t.Errorf("the push ran despite the refusal: %v", runner.runs)
	}
	if len(forge.calls) != 0 {
		t.Errorf("forge API called despite the refused push: %v", forge.calls)
	}
}

// RED-FIRST PIN P4 (security-forge-containment 2.3): every push is deadline-bounded.
// The pin asserts the bound at the seam (the runner's context carries a deadline within
// the push budget); the kill-on-expiry mechanics are OSRunner's already-pinned
// CommandContext behavior, so bound-present IS the behavior. RED against the pre-fix
// shape, where the push context was unbounded and a stalled remote wedged the delivery
// station's handler.
func TestDeliverPushIsDeadlineBounded(t *testing.T) {
	runner := &envRecordingRunner{}
	d, _ := containmentDelivery(runner, "", "file:///tmp/bare.git")

	start := time.Now()
	if _, err := d.Deliver(context.Background(), runEntity); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(runner.hadDeadline) != 1 || !runner.hadDeadline[0] {
		t.Fatal("the push context carries no deadline — a stalled remote would wedge the run")
	}
	if bound := runner.deadlines[0].Sub(start); bound <= 0 || bound > 3*time.Minute {
		t.Errorf("push deadline %v from start, want a bound within the push budget", bound)
	}
	// A tokenless push (the journeys' file:// shape) is still prompt-free and askpass-free.
	env := runner.env[0]
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_ASKPASS=") || strings.HasPrefix(e, cliexec.GitTokenEnv+"=") {
			t.Errorf("tokenless push carries a credential env entry: %q", e)
		}
	}
	promptFree := false
	for _, e := range env {
		if e == "GIT_TERMINAL_PROMPT=0" {
			promptFree = true
		}
	}
	if !promptFree {
		t.Errorf("tokenless push must still set GIT_TERMINAL_PROMPT=0; env=%v", env)
	}
}
