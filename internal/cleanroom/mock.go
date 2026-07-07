package cleanroom

import (
	"context"
	"fmt"
)

// MockRunner is a scripted Runner for unit tests: it records what it was asked to
// provision and run, and returns canned outcomes — so the verify harness's
// evidence-gathering and Retry-vs-Fail classification are testable offline, with no
// real exec, filesystem, or network. Exec results are consumed in call order.
type MockRunner struct {
	// UpErr, when non-nil, makes Up fail (a provisioning transport fault).
	UpErr error
	// Execs are the scripted outcomes, one per Exec call in order. Exec past the end
	// of the script returns an error (a test misconfiguration, surfaced loudly).
	Execs []MockExec
	// DownErr, when non-nil, is returned by Down.
	DownErr error

	// Recorded for assertions.
	UpCalls    int
	UpWorkDir  string
	UpCaches   []string
	ExecArgv   [][]string
	ExecCalls  int
	DownCalls  int
	sandboxRef Sandbox
}

// MockExec is one scripted Exec outcome: the Result to return and, when non-nil, the
// transport error that means the command could not be run.
type MockExec struct {
	Result Result
	Err    error
}

// Up records the request and returns a synthetic sandbox binding a fresh (fake)
// cache-home path per requested env, so isolation assertions see distinct homes.
func (m *MockRunner) Up(_ context.Context, workDir string, cacheHomeEnvs []string) (Sandbox, error) {
	m.UpCalls++
	m.UpWorkDir = workDir
	m.UpCaches = cacheHomeEnvs
	if m.UpErr != nil {
		return Sandbox{}, m.UpErr
	}
	env := map[string]string{}
	homes := make([]string, 0, len(cacheHomeEnvs))
	for i, name := range cacheHomeEnvs {
		dir := fmt.Sprintf("/mock/cache/%d-%d/%s", m.UpCalls, i, name)
		env[name] = dir
		homes = append(homes, dir)
	}
	m.sandboxRef = Sandbox{WorkDir: workDir, Env: env, CacheHomes: homes}
	return m.sandboxRef, nil
}

// Exec returns the next scripted outcome.
func (m *MockRunner) Exec(_ context.Context, _ Sandbox, argv []string) (Result, error) {
	m.ExecArgv = append(m.ExecArgv, argv)
	i := m.ExecCalls
	m.ExecCalls++
	if i >= len(m.Execs) {
		return Result{}, fmt.Errorf("cleanroom mock: unscripted Exec call #%d for %v", i+1, argv)
	}
	e := m.Execs[i]
	return e.Result, e.Err
}

// Down records the teardown.
func (m *MockRunner) Down(_ context.Context, _ Sandbox) error {
	m.DownCalls++
	return m.DownErr
}
