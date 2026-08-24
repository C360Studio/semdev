package cleanroom

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// RED-FIRST PIN P5 (security-forge-containment 3.2): repo-authored build paths must
// resolve INSIDE the checkout. A devcontainer's build.dockerfile / build.context are
// attacker-authored strings; `..` traversal would read host files outside the checkout
// into the docker build context (host-file exfiltration into an image whose tests the
// repo also authors). Traversal fails closed into the SAME ErrNoImage park as a repo
// with no buildable image — and nothing outside the checkout is read.
func TestResolveBuildPathsRejectsTraversal(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"dockerfile traversal", `{"build":{"dockerfile":"../../outside/Dockerfile"}}`},
		{"context traversal", `{"build":{"dockerfile":"Dockerfile","context":"../../outside"}}`},
		{"absolute dockerfile", `{"build":{"dockerfile":"/etc/evil/Dockerfile"}}`},
		{"absolute context", `{"build":{"dockerfile":"Dockerfile","context":"/etc"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, ".devcontainer/devcontainer.json"), c.json)
			mustWrite(t, filepath.Join(root, ".devcontainer/Dockerfile"), "FROM scratch\n")
			_, _, err := resolveBuildPaths(root, harness.ImageDecl{Devcontainer: ".devcontainer/devcontainer.json"})
			if !errors.Is(err, ErrNoImage) {
				t.Errorf("declared escape must fail closed as ErrNoImage (the uniform no-image park); got %v", err)
			}
		})
	}

	// The Dockerfile-branch inputs get the same guard (uniformity, design D5).
	if _, _, err := resolveBuildPaths(t.TempDir(), harness.ImageDecl{Dockerfile: "../outside/Dockerfile"}); !errors.Is(err, ErrNoImage) {
		t.Errorf("Dockerfile-branch traversal must fail closed as ErrNoImage; got %v", err)
	}
	if _, _, err := resolveBuildPaths(t.TempDir(), harness.ImageDecl{Dockerfile: "Dockerfile", Context: "../.."}); !errors.Is(err, ErrNoImage) {
		t.Errorf("Dockerfile-branch context traversal must fail closed as ErrNoImage; got %v", err)
	}
}

// RED-FIRST PIN (groups-2-3 go-review MEDIUM-1): the SYMLINK lane. pathguard.SafeJoin is
// lexical — a repo-COMMITTED symlink (`Dockerfile -> /host/file`, or the devcontainer.json
// itself) passes the string guard and achieves exactly the host-file read D5 closes for
// `..` strings, from the same repo-authored threat actor. Resolution must follow symlinks
// and re-check containment, failing closed as the same ErrNoImage park.
func TestResolveBuildPathsRejectsSymlinkEscape(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "host-secret")
	mustWrite(t, outside, "FROM scratch\n# host file\n")

	t.Run("symlinked Dockerfile", func(t *testing.T) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, ".devcontainer/devcontainer.json"), `{"build":{"dockerfile":"Dockerfile"}}`)
		if err := os.Symlink(outside, filepath.Join(root, ".devcontainer/Dockerfile")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := resolveBuildPaths(root, harness.ImageDecl{Devcontainer: ".devcontainer/devcontainer.json"}); !errors.Is(err, ErrNoImage) {
			t.Errorf("symlinked Dockerfile escaping the checkout must fail closed as ErrNoImage; got %v", err)
		}
	})

	t.Run("symlinked build.context", func(t *testing.T) {
		// The highest-value exfiltration channel: a context symlink makes docker tar a
		// host DIRECTORY into a build whose repo-authored Dockerfile/tests read it
		// (semstreams-review MEDIUM — pinned so a context-join refactor cannot
		// silently reopen it).
		root := t.TempDir()
		outsideDir := t.TempDir()
		mustWrite(t, filepath.Join(outsideDir, "host-data"), "sensitive\n")
		mustWrite(t, filepath.Join(root, ".devcontainer/devcontainer.json"), `{"build":{"dockerfile":"Dockerfile","context":"data"}}`)
		mustWrite(t, filepath.Join(root, ".devcontainer/Dockerfile"), "FROM scratch\n")
		if err := os.Symlink(outsideDir, filepath.Join(root, ".devcontainer/data")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := resolveBuildPaths(root, harness.ImageDecl{Devcontainer: ".devcontainer/devcontainer.json"}); !errors.Is(err, ErrNoImage) {
			t.Errorf("symlinked build.context escaping the checkout must fail closed as ErrNoImage; got %v", err)
		}
	})

	t.Run("symlinked devcontainer.json", func(t *testing.T) {
		root := t.TempDir()
		dcOutside := filepath.Join(t.TempDir(), "devcontainer.json")
		mustWrite(t, dcOutside, `{"build":{"dockerfile":"Dockerfile"}}`)
		if err := os.MkdirAll(filepath.Join(root, ".devcontainer"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dcOutside, filepath.Join(root, ".devcontainer/devcontainer.json")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := resolveBuildPaths(root, harness.ImageDecl{Devcontainer: ".devcontainer/devcontainer.json"}); !errors.Is(err, ErrNoImage) {
			t.Errorf("symlinked devcontainer.json escaping the checkout must fail closed as ErrNoImage; got %v", err)
		}
	})
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
