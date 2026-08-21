package standards

// The provision-time adapter (standards-via-lessons D2/D4 wiring): reads the
// provisioned checkout's standards file, resolves the run's target repo, and
// drives the Syncer — classifying every fault into the two the provision core
// can act on (see provisionsandbox.Standards).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// syncCore is the Syncer surface the adapter drives (fake-able in tests).
type syncCore interface {
	Sync(ctx context.Context, raw []byte, repo string) (Result, error)
	SyncAbsent(ctx context.Context, repo string) (Result, error)
}

// RepoResolver resolves a run's target repo ("owner/repo"). found=false means the
// run carries no target coordinate at all, which is a park, not an error.
type RepoResolver interface {
	Repo(ctx context.Context, runEntityID string) (repo string, found bool, err error)
}

// ProvisionSync satisfies provisionsandbox.Standards.
type ProvisionSync struct {
	Syncer syncCore
	Repos  RepoResolver
	Logger *slog.Logger
}

// Sync reads the checkout's standards file and projects it onto the lesson
// substrate for the run's target repo.
//
// The return is the seam's classification, and every branch below picks one
// deliberately: a non-empty reason parks toward the human (the repo's mistake, or
// a run that cannot be scoped); an error retries (semdev's substrate). Nothing
// returns "" , nil without having actually synced — a silent success here would
// mean a run whose briefs never carry the repo's law, with nothing recording that.
func (p *ProvisionSync) Sync(ctx context.Context, runEntityID, checkoutRoot string) (string, error) {
	if p.Syncer == nil || p.Repos == nil {
		return "", fmt.Errorf("standards: provision sync is partially wired (syncer=%t repos=%t) — a wiring bug, not a repo fault",
			p.Syncer != nil, p.Repos != nil)
	}

	// The repo FIRST: births, retirement and cross-repo isolation all key on it (D2/D5),
	// so an unscoped sync would let one target's standards retire another's records.
	repo, found, err := p.Repos.Repo(ctx, runEntityID)
	if err != nil {
		return "", fmt.Errorf("standards: resolve target repo for %s: %w", runEntityID, err)
	}
	if !found {
		return "the run carries no run.issue.ref, so its target repo is unknown and repo-declared " +
			"standards cannot be scoped to it (park toward the operator)", nil
	}

	path := filepath.Join(checkoutRoot, Path)

	// The standards path is fixed, but every component of it is repo-authored: git stores
	// symlinks, including DIRECTORY symlinks, so a repo can commit `.semdev` itself as a
	// link out of the checkout. Lstat declines to follow only the FINAL component — the
	// kernel still resolves `.semdev` — so an Lstat-only guard reads an outside file and
	// reports IsRegular=true while doing it. Containment therefore has to be checked on the
	// RESOLVED path, not inferred from the leaf's mode (readworkspace.go:155-176 is the same
	// guard for the same reason).
	//
	// The stakes are the auto-promotion policy: file-derived standards promote to active
	// because the git commit / PR review of the file IS the human gate (D4). A path that
	// escapes the checkout promotes law nobody reviewed. Secondarily, parse errors quote
	// file content into this function's block reason, which posts as an issue comment — so
	// an escaping read is also an exfiltration channel with a human-facing sink.
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No file, no law: retirement-only. Deleting the file is how a repo withdraws its
		// standards, so skipping the sync entirely would leave them active forever (D5/L4).
		res, err := p.Syncer.SyncAbsent(ctx, repo)
		if err != nil {
			return classify(err, fmt.Sprintf("retire withdrawn standards for %s", repo))
		}
		// Logged as loudly as the ordinary path: deleting the file is the single most
		// consequential transition a repo can make here (every standard it declared goes
		// inactive at once), and it is the one that would otherwise leave no trace.
		if p.Logger != nil {
			p.Logger.Info("repo standards absent — retirement-only pass",
				slog.String("run_entity_id", runEntityID), slog.String("repo", repo),
				slog.Int("retired", res.Retired))
		}
		return "", nil
	case err != nil:
		return "", fmt.Errorf("standards: stat %s: %w", path, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Sprintf("%s is a symlink; the standards file must be a regular file committed in the "+
			"repo (refusing to follow it out of the checkout)", Path), nil
	case !info.Mode().IsRegular():
		return fmt.Sprintf("%s is not a regular file", Path), nil
	}

	// Containment on the RESOLVED path — the guard an intermediate directory symlink
	// defeats above. Both sides are resolved so a symlinked checkout base (macOS's
	// /var -> /private/var) does not false-reject a legitimate repo.
	if reason, err := requireInsideCheckout(checkoutRoot, path); err != nil || reason != "" {
		return reason, err
	}

	// Size-bound BEFORE reading: Parse enforces maxFileBytes, but only after the whole
	// repo-controlled file is already in memory. Stat first so a hostile or accidental
	// giant file is refused rather than allocated.
	if info.Size() > maxFileBytes {
		return fmt.Sprintf("%s is %d bytes, over the %d-byte bound", Path, info.Size(), maxFileBytes), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("standards: read %s: %w", path, err)
	}
	res, err := p.Syncer.Sync(ctx, raw, repo)
	if err != nil {
		return classify(err, fmt.Sprintf("sync standards for %s", repo))
	}
	if p.Logger != nil {
		p.Logger.Info("repo standards synced",
			slog.String("run_entity_id", runEntityID), slog.String("repo", repo),
			slog.String("source_entity_id", res.SourceEntityID),
			slog.Int("born", res.Born), slog.Int("promoted", res.Promoted), slog.Int("retired", res.Retired))
	}
	return "", nil
}

// requireInsideCheckout re-checks, on the symlink-RESOLVED forms, that the standards
// file really lives inside the run's checkout. Returns a block reason when it escapes
// (the repo's mistake) and an error only when the checkout root itself cannot be
// resolved (semdev's own state).
func requireInsideCheckout(checkoutRoot, path string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(checkoutRoot)
	if err != nil {
		return "", fmt.Errorf("standards: resolve checkout root %s: %w", checkoutRoot, err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		// The path was just confirmed to exist, so this is a broken or looping link —
		// repo-authored, and a park rather than a retry.
		return fmt.Sprintf("%s could not be resolved (broken or looping symlink)", Path), nil
	}
	rel, relErr := filepath.Rel(realRoot, realPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Sprintf("%s resolves outside the run's checkout; the standards file must be a regular "+
			"file committed in the repo (refusing to read law from outside the reviewed tree)", Path), nil
	}
	return "", nil
}

// classify splits a sync fault into the seam's two outcomes: the repo's declaration
// defect (block, carrying the exact defect) or semdev's substrate (error, retryable).
func classify(err error, what string) (string, error) {
	if IsDeclaration(err) {
		return err.Error(), nil
	}
	return "", fmt.Errorf("standards: %s: %w", what, err)
}
