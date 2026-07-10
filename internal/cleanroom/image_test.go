package cleanroom

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/harness"
)

// buildImageArgs is pure — the docker build invocation is unit-testable without docker.
func TestBuildImageArgs(t *testing.T) {
	got := buildImageArgs("/tmp/iid", "/repo/Dockerfile", "/repo")
	want := []string{"build", "--iidfile", "/tmp/iid", "-f", "/repo/Dockerfile", "/repo"}
	if !slices.Equal(got, want) {
		t.Errorf("buildImageArgs = %v, want %v", got, want)
	}
}

// A Dockerfile declaration builds with the repo root as context (so the image can COPY
// the source); an explicit context is honored.
func TestResolveBuildPathsDockerfile(t *testing.T) {
	df, ctx, err := resolveBuildPaths("/repo", harness.ImageDecl{Dockerfile: "Dockerfile"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if df != "/repo/Dockerfile" || ctx != "/repo" {
		t.Errorf("got (%q, %q), want (/repo/Dockerfile, /repo)", df, ctx)
	}
	df, ctx, err = resolveBuildPaths("/repo", harness.ImageDecl{Dockerfile: "docker/Dev.Dockerfile", Context: "sub"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if df != "/repo/docker/Dev.Dockerfile" || ctx != "/repo/sub" {
		t.Errorf("got (%q, %q)", df, ctx)
	}
}

// A devcontainer declaration resolves build.dockerfile/context relative to the
// devcontainer directory (the devcontainer spec's semantics).
func TestResolveBuildPathsDevcontainer(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".devcontainer/devcontainer.json"), `{"build":{"dockerfile":"Dockerfile","context":".."}}`)
	df, ctx, err := resolveBuildPaths(root, harness.ImageDecl{Devcontainer: ".devcontainer/devcontainer.json"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if df != filepath.Join(root, ".devcontainer/Dockerfile") {
		t.Errorf("dockerfile = %q", df)
	}
	if ctx != filepath.Join(root, ".devcontainer/..") {
		t.Errorf("context = %q", ctx)
	}
}

// A devcontainer that declares only a prebuilt image (no build) fails closed — M0 builds
// a Dockerfile, it does not pull a prebuilt image (SB2).
func TestResolveBuildPathsPrebuiltImageFailsClosed(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".devcontainer/devcontainer.json"), `{"image":"golang:1.26"}`)
	if _, _, err := resolveBuildPaths(root, harness.ImageDecl{Devcontainer: ".devcontainer/devcontainer.json"}); !errors.Is(err, ErrNoImage) {
		t.Errorf("prebuilt-image devcontainer: got %v, want ErrNoImage", err)
	}
}

// LocateImageInDir returns the committed declaration, or ErrNoImage when the repo
// declares none — the typed "declare an image" fault the caller parks on.
func TestLocateImageInDir(t *testing.T) {
	withDockerfile := t.TempDir()
	mustWrite(t, filepath.Join(withDockerfile, "Dockerfile"), "FROM busybox\n")
	decl, err := LocateImageInDir(withDockerfile)
	if err != nil || decl.Dockerfile != "Dockerfile" {
		t.Errorf("LocateImageInDir(withDockerfile) = (%+v, %v)", decl, err)
	}

	bare := t.TempDir()
	mustWrite(t, filepath.Join(bare, "go.mod"), "module x\n")
	if _, err := LocateImageInDir(bare); !errors.Is(err, ErrNoImage) {
		t.Errorf("LocateImageInDir(bare) err = %v, want ErrNoImage", err)
	}
}

// An undeclared image fails closed before any docker call — ErrNoImage, testable
// without a daemon.
func TestBuildImageUndeclaredFailsClosed(t *testing.T) {
	if _, err := BuildImage(context.Background(), "docker", "/repo", harness.ImageDecl{}); !errors.Is(err, ErrNoImage) {
		t.Errorf("BuildImage(undeclared) = %v, want ErrNoImage", err)
	}
}

// Docker-gated: BuildImage builds a real tiny image and returns its digest-pinned
// sha256 ref (the pin). Skips when docker is absent (unit tests need no daemon).
func TestBuildImageReal(t *testing.T) {
	ctx := context.Background()
	if err := DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Dockerfile"), "FROM busybox:latest\nRUN true\n")

	decl, err := LocateImageInDir(root)
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	img, err := BuildImage(ctx, "docker", root, decl)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	t.Cleanup(func() {
		_ = exec2(ctx, "docker", "image", "rm", "-f", img.Ref)
		_ = exec2(ctx, "docker", "image", "rm", "-f", img.Digest)
	})

	// Digest is the immutable sha256 pin; Ref is a non-dangling semdev-scoped tag.
	if !strings.HasPrefix(img.Digest, "sha256:") {
		t.Errorf("built digest = %q, want a sha256: pin", img.Digest)
	}
	if !strings.HasPrefix(img.Ref, imageTagRepo+":") {
		t.Errorf("built ref = %q, want a %s: tag (non-dangling)", img.Ref, imageTagRepo)
	}
	// Both the pin and the tag resolve to a real, runnable image.
	if err := exec2(ctx, "docker", "image", "inspect", img.Ref); err != nil {
		t.Errorf("built image ref %q not inspectable: %v", img.Ref, err)
	}
	if err := exec2(ctx, "docker", "image", "inspect", img.Digest); err != nil {
		t.Errorf("built image digest %q not inspectable: %v", img.Digest, err)
	}
}

// A declared-but-absent Dockerfile fails closed (ErrNoImage) BEFORE any docker call —
// the operator committed a broken declaration, provable offline (no docker gate).
func TestBuildImageMissingDockerfileFailsClosed(t *testing.T) {
	root := t.TempDir() // no Dockerfile written
	if _, err := BuildImage(context.Background(), "docker", root, harness.ImageDecl{Dockerfile: "Dockerfile"}); !errors.Is(err, ErrNoImage) {
		t.Errorf("BuildImage(missing Dockerfile) = %v, want ErrNoImage", err)
	}
}

// BuildImage requires an absolute repoRoot (consistency with ContainerRunner.Up), so a
// relative path can't silently build against the wrong cwd.
func TestBuildImageRejectsRelativeRoot(t *testing.T) {
	_, err := BuildImage(context.Background(), "docker", "relative/root", harness.ImageDecl{Dockerfile: "Dockerfile"})
	if err == nil || errors.Is(err, ErrNoImage) {
		t.Errorf("BuildImage(relative root) = %v, want a path error (not ErrNoImage)", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// exec2 runs a docker command discarding output — a tiny test helper for cleanup/asserts.
func exec2(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}
