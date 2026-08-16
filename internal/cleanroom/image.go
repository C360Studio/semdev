package cleanroom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/pathguard"
)

// buildTimeout bounds a single `docker build`. Image builds (base pull + resolve of the
// operator's toolchain layers) can be slow the first time; the digest-pinned result is
// reused, so this is a first-build ceiling, not a per-run cost.
const buildTimeout = 20 * time.Minute

// BuiltImage is the result of building an operator-declared image (SB2). Digest is the
// `sha256:…` content id captured via `docker build --iidfile` — the immutable pin
// (content-addressable, directly runnable) stamped into the provisioning attestation
// (group 5). Ref is the runnable reference ContainerRunner runs: a stable
// semdev-scoped tag pointing at that digest, so the image is NOT dangling (it survives
// `docker image prune` between build and use); it falls back to the digest itself if
// tagging fails (still runnable). A registry-digest split lands if/when images are
// pushed.
type BuiltImage struct {
	// Ref is the runnable image reference — a non-dangling semdev-scoped tag (or the
	// digest as a fallback).
	Ref string
	// Digest is the sha256 content digest — the immutable pin.
	Digest string
}

// imageTagRepo is the stable, semdev-scoped tag namespace for built sandbox images, so
// a built image is non-dangling. The tag suffix is content-derived (a short slice of
// the digest), so re-building identical content lands the same tag.
const imageTagRepo = "semdev-sandbox"

// LocateImageInDir scans a checkout root for the operator's committed image declaration
// (a Dockerfile / devcontainer, SB2) and returns it, or ErrNoImage when the repo
// declares none — the typed "declare an image" fault the caller PARKS on (never a
// guess, never a silent skip). It reads the root directory and .devcontainer/, then
// applies the pure harness.LocateImage precedence.
func LocateImageInDir(root string) (harness.ImageDecl, error) {
	var candidates []string
	for _, dir := range []string{"", ".devcontainer"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue // a missing .devcontainer/ is normal; a missing root surfaces below
		}
		for _, e := range entries {
			candidates = append(candidates, filepath.ToSlash(filepath.Join(dir, e.Name())))
		}
	}
	decl, ok := harness.LocateImage(candidates)
	if !ok {
		return harness.ImageDecl{}, fmt.Errorf("%w: %s declares no Dockerfile or devcontainer — the operator must declare one (SB2)", ErrNoImage, root)
	}
	return decl, nil
}

// BuildImage builds the operator-declared image and returns its digest-pinned ref
// (SB2). It resolves the Dockerfile + build context from the declaration (reading a
// devcontainer's build.dockerfile when the declaration is a devcontainer), runs
// `docker build --iidfile` so the result is captured by its immutable sha256 id, and
// fails CLOSED: an undeclared image is ErrNoImage, an absent docker daemon is
// ErrDockerUnavailable, a declared-but-missing Dockerfile or a failed build is a wrapped
// error the caller parks on. semdev never harvests or synthesizes the image — it builds
// exactly what the operator committed.
func BuildImage(ctx context.Context, docker, repoRoot string, decl harness.ImageDecl) (BuiltImage, error) {
	if docker == "" {
		docker = "docker"
	}
	// Pure/offline declaration checks FIRST, so a broken declaration (undeclared,
	// missing Dockerfile, prebuilt-only or malformed devcontainer) is provable without a
	// daemon (G6 red-first-offline) and is attributed to the operator — not masked as
	// ErrDockerUnavailable when docker happens to be down.
	if !decl.Declared() {
		return BuiltImage{}, ErrNoImage
	}
	if !filepath.IsAbs(repoRoot) {
		return BuiltImage{}, fmt.Errorf("cleanroom: repoRoot %q must be an absolute path", repoRoot)
	}
	dockerfileAbs, contextAbs, err := resolveBuildPaths(repoRoot, decl)
	if err != nil {
		return BuiltImage{}, err
	}
	if _, err := os.Stat(dockerfileAbs); err != nil {
		return BuiltImage{}, fmt.Errorf("%w: declared Dockerfile %s not found: %v", ErrNoImage, dockerfileAbs, err)
	}

	if err := DockerAvailable(ctx, docker); err != nil {
		return BuiltImage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	iid, err := os.CreateTemp("", "semdev-iid-*")
	if err != nil {
		return BuiltImage{}, fmt.Errorf("cleanroom: create iidfile: %w", err)
	}
	iidPath := iid.Name()
	_ = iid.Close()
	defer func() { _ = os.Remove(iidPath) }()

	out, err := runCombined(ctx, docker, buildImageArgs(iidPath, dockerfileAbs, contextAbs))
	if err != nil {
		return BuiltImage{}, fmt.Errorf("cleanroom: docker build of %s failed: %w%s", dockerfileAbs, err, buildTail(out))
	}

	id, err := os.ReadFile(iidPath)
	if err != nil {
		return BuiltImage{}, fmt.Errorf("cleanroom: read built image id: %w", err)
	}
	digest := strings.TrimSpace(string(id))
	if digest == "" {
		return BuiltImage{}, errors.New("cleanroom: docker build produced no image id")
	}

	// Tag the freshly built (dangling) image with a stable content-derived tag so it is
	// not reaped by `docker image prune` before the run uses it. Best-effort: if the tag
	// fails, the digest itself is still a runnable ref.
	ref := digest
	tag := imageTagRepo + ":" + shortDigest(digest)
	if tagErr := runDocker(ctx, docker, "tag", digest, tag); tagErr == nil {
		ref = tag
	}
	return BuiltImage{Ref: ref, Digest: digest}, nil
}

// shortDigest derives a stable, legible tag suffix from a `sha256:…` id (its first 16
// hex chars), so identical content re-tags identically.
func shortDigest(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	if len(hex) > 16 {
		hex = hex[:16]
	}
	return hex
}

// resolveBuildPaths turns a declaration into absolute Dockerfile + build-context paths.
// A Dockerfile declaration builds with the repo root as context (so the image can COPY
// the source); a devcontainer declaration resolves build.dockerfile/build.context
// relative to the devcontainer's own directory (the devcontainer spec's semantics). It
// reads the devcontainer.json from disk only to LOCATE the Dockerfile — semdev owns the
// run (SB2). Every declared path resolves through the shared checkout containment guard
// (pathguard.SafeJoin, design D5): the devcontainer's build.dockerfile/build.context are
// repo-AUTHORED strings, and a `..` traversal or absolute path would read host files
// outside the checkout into the build context. An escaping declaration fails closed as
// ErrNoImage — the exact same park as a repo with no buildable image.
func resolveBuildPaths(repoRoot string, decl harness.ImageDecl) (dockerfileAbs, contextAbs string, err error) {
	if decl.Dockerfile != "" {
		dockerfileAbs, err = safeBuildPath(repoRoot, decl.Dockerfile)
		if err != nil {
			return "", "", err
		}
		contextRel := decl.Context
		if contextRel == "" {
			contextRel = "." // build the checkout root so the image can COPY the source
		}
		contextAbs, err = safeBuildPath(repoRoot, contextRel)
		if err != nil {
			return "", "", err
		}
		return dockerfileAbs, contextAbs, nil
	}

	// Devcontainer declaration: read its build.dockerfile, resolved relative to the
	// devcontainer directory.
	dcDir := filepath.Dir(decl.Devcontainer)
	dcPath, err := safeBuildPath(repoRoot, decl.Devcontainer)
	if err != nil {
		return "", "", err
	}
	raw, err := os.ReadFile(dcPath)
	if err != nil {
		return "", "", fmt.Errorf("%w: read declared devcontainer %s: %v", ErrNoImage, decl.Devcontainer, err)
	}
	df, buildCtx, found, err := harness.ReadImageBuild(raw)
	if err != nil {
		// A malformed devcontainer is a broken declaration — share the ErrNoImage
		// sentinel with the other "no buildable image" faults so the caller's park path
		// is uniform.
		return "", "", fmt.Errorf("%w: %v", ErrNoImage, err)
	}
	if !found {
		return "", "", fmt.Errorf("%w: devcontainer %s declares no buildable Dockerfile (a prebuilt image is not built at M0)", ErrNoImage, decl.Devcontainer)
	}
	// Reject absolute declared paths BEFORE the join — filepath.Join would silently
	// re-root them under dcDir, masking a misdeclaration the operator should see.
	if filepath.IsAbs(df) {
		return "", "", fmt.Errorf("%w: devcontainer build.dockerfile %q is absolute — declare a repo-relative path", ErrNoImage, df)
	}
	if buildCtx == "" {
		buildCtx = "."
	}
	if filepath.IsAbs(buildCtx) {
		return "", "", fmt.Errorf("%w: devcontainer build.context %q is absolute — declare a repo-relative path", ErrNoImage, buildCtx)
	}
	dockerfileAbs, err = safeBuildPath(repoRoot, filepath.Join(dcDir, df))
	if err != nil {
		return "", "", err
	}
	contextAbs, err = safeBuildPath(repoRoot, filepath.Join(dcDir, buildCtx))
	if err != nil {
		return "", "", err
	}
	return dockerfileAbs, contextAbs, nil
}

// safeBuildPath resolves a declared repo-relative build path inside repoRoot via the
// shared containment guard, classifying an escaping or absolute declaration as
// ErrNoImage so it parks exactly like a repo with no buildable image (design D5).
func safeBuildPath(repoRoot, rel string) (string, error) {
	abs, err := pathguard.SafeJoin(repoRoot, rel)
	if err != nil {
		return "", fmt.Errorf("%w: declared build path %q does not resolve inside the checkout: %v", ErrNoImage, rel, err)
	}
	if err := resolvedWithin(repoRoot, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// resolvedWithin closes the SYMLINK lane the lexical guard cannot see (go-review
// MEDIUM-1): a repo-COMMITTED symlink under a string-legal path can point anywhere on
// the host, and the subsequent read/`docker build -f` would follow it. Follow symlinks
// (EvalSymlinks) and re-check containment against the resolved checkout root. A
// NONEXISTENT path skips the check — there is nothing to read, and the build fails
// closed on it downstream (the fake-root unit shapes stay pure path computation). Any
// other resolution failure fails closed: a path that cannot be resolved is a path whose
// containment cannot be trusted.
func resolvedWithin(repoRoot, abs string) error {
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%w: resolve declared build path %q: %v", ErrNoImage, abs, err)
	}
	rootResolved, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return fmt.Errorf("%w: resolve checkout root %q: %v", ErrNoImage, repoRoot, err)
	}
	within, err := filepath.Rel(rootResolved, resolved)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: declared build path %q resolves outside the checkout (symlink escape)", ErrNoImage, abs)
	}
	return nil
}

// buildImageArgs assembles the `docker build` args: the iidfile that captures the
// digest-pinned image id, the Dockerfile, and the context dir. Pure — unit-testable
// without docker. NOTE: no BUILD-time secret channel yet (M0 injects secrets at RUN
// time, internal/secrets); when a build-secret channel lands (BuildKit `--secret`), the
// buildTail this surfaces on failure must be scrubbed — it is not today.
func buildImageArgs(iidfile, dockerfileAbs, contextAbs string) []string {
	return []string{"build", "--iidfile", iidfile, "-f", dockerfileAbs, contextAbs}
}

// runCombined runs a docker subcommand capturing combined stdout+stderr (the build log),
// so a failure carries a legible tail.
func runCombined(ctx context.Context, docker string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, docker, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runDocker runs a docker subcommand discarding output, returning only the error — for
// side-effecting commands (e.g. `docker tag`) whose output is not needed.
func runDocker(ctx context.Context, docker string, args ...string) error {
	return exec.CommandContext(ctx, docker, args...).Run()
}

// buildTail returns a short trailing excerpt of a failed build log for the error.
func buildTail(log string) string {
	log = strings.TrimSpace(log)
	if log == "" {
		return ""
	}
	const maxLen = 600
	if len(log) > maxLen {
		log = strings.ToValidUTF8(log[len(log)-maxLen:], "")
	}
	return ": " + log
}
