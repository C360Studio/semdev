package standards

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validFile = `version: 1
standards:
  - id: eng-test-traceability
    text: "Every new test must reference the scenario it verifies."
    severity: must
`

// recordingSyncer captures which sync path ran and with what.
type recordingSyncer struct {
	raw      []byte
	repo     string
	absent   bool
	syncErr  error
	syncRan  bool
	absentAt bool
}

func (r *recordingSyncer) Sync(_ context.Context, raw []byte, repo string) (Result, error) {
	r.syncRan, r.raw, r.repo = true, raw, repo
	return Result{}, r.syncErr
}

func (r *recordingSyncer) SyncAbsent(_ context.Context, repo string) (Result, error) {
	r.absentAt, r.repo = true, repo
	return Result{}, r.syncErr
}

type fixedRepos struct {
	repo  string
	found bool
	err   error
}

func (f fixedRepos) Repo(context.Context, string) (string, bool, error) {
	return f.repo, f.found, f.err
}

func writeStandards(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(Path)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, Path), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func TestProvisionSyncRunsTheDeclaredFileForTheResolvedRepo(t *testing.T) {
	syncer := &recordingSyncer{}
	p := &ProvisionSync{Syncer: syncer, Repos: fixedRepos{repo: "C360Studio/semdev-test", found: true}}

	reason, err := p.Sync(context.Background(), "run-1", writeStandards(t, validFile))
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if reason != "" {
		t.Fatalf("a valid file must not block; got %q", reason)
	}
	if !syncer.syncRan {
		t.Fatal("the declared file was never synced")
	}
	if string(syncer.raw) != validFile {
		t.Errorf("the syncer received %q, want the file's exact bytes", string(syncer.raw))
	}
	if syncer.repo != "C360Studio/semdev-test" {
		t.Errorf("synced for repo %q, want the run's resolved target", syncer.repo)
	}
}

func TestProvisionSyncBlocksOnAMalformedFileWithTheExactDefect(t *testing.T) {
	// A real parse defect, so the block reason is the parser's own words rather than
	// a paraphrase the human then has to translate back to the file.
	syncer := &recordingSyncer{syncErr: &DeclarationError{Err: errors.New(`standards: check name "Go Vet" is not a lower-kebab token`)}}
	p := &ProvisionSync{Syncer: syncer, Repos: fixedRepos{repo: "o/r", found: true}}

	reason, err := p.Sync(context.Background(), "run-1", writeStandards(t, validFile))
	if err != nil {
		t.Fatalf("a declaration fault must be a block reason, not an error: %v", err)
	}
	if !strings.Contains(reason, "lower-kebab") {
		t.Errorf("the block reason must carry the parser's exact defect; got %q", reason)
	}
}

func TestProvisionSyncReturnsSubstrateFaultsAsErrors(t *testing.T) {
	syncer := &recordingSyncer{syncErr: errors.New("graph mutation request timed out")}
	p := &ProvisionSync{Syncer: syncer, Repos: fixedRepos{repo: "o/r", found: true}}

	reason, err := p.Sync(context.Background(), "run-1", writeStandards(t, validFile))
	if err == nil {
		t.Fatalf("a substrate fault must return an error so the station retries; got block %q", reason)
	}
	if reason != "" {
		t.Errorf("a substrate fault must not also block; got %q", reason)
	}
}

func TestProvisionSyncRetiresWhenTheFileIsAbsent(t *testing.T) {
	syncer := &recordingSyncer{}
	p := &ProvisionSync{Syncer: syncer, Repos: fixedRepos{repo: "o/r", found: true}}

	reason, err := p.Sync(context.Background(), "run-1", t.TempDir())
	if err != nil {
		t.Fatalf("an absent file is not a fault: %v", err)
	}
	if reason != "" {
		t.Fatalf("an absent file must not block; got %q", reason)
	}
	if !syncer.absentAt {
		t.Error("an absent file must run the retirement-only path — deleting the file is how a repo " +
			"withdraws its standards, and skipping the sync entirely would leave them active forever")
	}
	if syncer.syncRan {
		t.Error("the declared-file path ran for an absent file")
	}
}

func TestProvisionSyncBlocksWhenTheRunHasNoTargetCoordinate(t *testing.T) {
	syncer := &recordingSyncer{}
	p := &ProvisionSync{Syncer: syncer, Repos: fixedRepos{found: false}}

	reason, err := p.Sync(context.Background(), "run-1", writeStandards(t, validFile))
	if err != nil {
		t.Fatalf("a missing coordinate is a park, not a retry: %v", err)
	}
	if !strings.Contains(reason, "run.issue.ref") {
		t.Errorf("the block reason must name the missing coordinate; got %q", reason)
	}
	if syncer.syncRan || syncer.absentAt {
		t.Error("the sync ran without a resolved repo — standards would be born and retired unscoped, " +
			"which is how one target's standards reach another's briefs")
	}
}

// TestProvisionSyncRefusesASymlinkedStandardsFile closes a repo-authored read-anything
// vector: the standards path is fixed, but a repo can commit a SYMLINK there. Following it
// would read a file outside the checkout, and its content reaches the human through the
// parse error in the park message. The file is repo-authored input, so it gets the
// untrusted-input treatment.
func TestProvisionSyncRefusesASymlinkedStandardsFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("sk-live-000-not-a-standards-file"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(Path)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, Path)); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	syncer := &recordingSyncer{}
	p := &ProvisionSync{Syncer: syncer, Repos: fixedRepos{repo: "o/r", found: true}}

	reason, err := p.Sync(context.Background(), "run-1", root)
	if err != nil {
		t.Fatalf("a symlinked standards file is the repo's mistake — a block, not a retry: %v", err)
	}
	if !strings.Contains(reason, "symlink") {
		t.Errorf("the block reason must name the symlink; got %q", reason)
	}
	if strings.Contains(reason, "sk-live") {
		t.Error("the block reason leaked the linked file's CONTENT — that reaches the human through " +
			"the park comment, which is the exfiltration this check exists to prevent")
	}
	if syncer.syncRan {
		t.Error("the symlinked file was synced")
	}
}

// TestProvisionSyncRefusesAStandardsFileReachedThroughASymlinkedDirectory is the guard an
// Lstat-only check does NOT provide: git stores DIRECTORY symlinks, so a repo can commit
// `.semdev` itself as a link out of the checkout. Lstat declines to follow only the final
// component, so the leaf reports as a perfectly ordinary regular file while the bytes come
// from outside the tree anyone reviewed — which would promote unreviewed law to active and
// echo outside content into the human-facing park message.
func TestProvisionSyncRefusesAStandardsFileReachedThroughASymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, filepath.Base(Path)), []byte(validFile), 0o644); err != nil {
		t.Fatalf("write outside standards file: %v", err)
	}
	// .semdev -> an outside directory that really does contain a valid standards.yaml.
	if err := os.Symlink(outsideDir, filepath.Join(root, filepath.Dir(Path))); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	syncer := &recordingSyncer{}
	p := &ProvisionSync{Syncer: syncer, Repos: fixedRepos{repo: "o/r", found: true}}

	reason, err := p.Sync(context.Background(), "run-1", root)
	if err != nil {
		t.Fatalf("an escaping standards path is the repo's mistake — a block, not a retry: %v", err)
	}
	if reason == "" {
		t.Fatal("the sync accepted a standards file reached through a symlinked .semdev directory — " +
			"auto-promotion rests on the file having been reviewed in the repo, and this one never was")
	}
	if !strings.Contains(reason, "outside") {
		t.Errorf("the block reason must say the path escapes the checkout; got %q", reason)
	}
	if syncer.syncRan {
		t.Error("standards from outside the checkout were synced onto the substrate")
	}
}

// TestProvisionSyncAcceptsASymlinkedCheckoutRoot guards the other direction: the
// containment re-check resolves BOTH sides, so a checkout under a symlinked base (macOS
// /var -> /private/var, which t.TempDir hands out) must still be accepted.
func TestProvisionSyncAcceptsASymlinkedCheckoutRoot(t *testing.T) {
	actualRoot := writeStandards(t, validFile)
	link := filepath.Join(t.TempDir(), "checkout-link")
	if err := os.Symlink(actualRoot, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	syncer := &recordingSyncer{}
	p := &ProvisionSync{Syncer: syncer, Repos: fixedRepos{repo: "o/r", found: true}}

	reason, err := p.Sync(context.Background(), "run-1", link)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if reason != "" {
		t.Fatalf("a checkout reached through a symlinked base must be accepted; got block %q", reason)
	}
	if !syncer.syncRan {
		t.Error("the standards file under a symlinked checkout root was never synced")
	}
}
