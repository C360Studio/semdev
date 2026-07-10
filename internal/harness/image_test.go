package harness

import "testing"

// LocateImage finds the operator's committed image declaration from a repo listing
// (pure, like DetectProfile). A devcontainer.json is the richest declaration (it
// carries customizations.semdev too), so it wins over a bare Dockerfile; absent a
// devcontainer, a root Dockerfile is the M0 form; absent both, a .devcontainer/Dockerfile.
func TestLocateImage(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  ImageDecl
		ok    bool
	}{
		{"root-dockerfile", []string{"go.mod", "Dockerfile"}, ImageDecl{Dockerfile: "Dockerfile"}, true},
		{"devcontainer-json", []string{".devcontainer/devcontainer.json"}, ImageDecl{Devcontainer: ".devcontainer/devcontainer.json"}, true},
		{"root-devcontainer-json", []string{".devcontainer.json"}, ImageDecl{Devcontainer: ".devcontainer.json"}, true},
		{"devcontainer-dockerfile", []string{".devcontainer/Dockerfile"}, ImageDecl{Dockerfile: ".devcontainer/Dockerfile"}, true},
		// Devcontainer wins over a co-committed Dockerfile (it references it + carries the run block).
		{"devcontainer-beats-dockerfile", []string{"Dockerfile", ".devcontainer/devcontainer.json"}, ImageDecl{Devcontainer: ".devcontainer/devcontainer.json"}, true},
		// No declaration at all — the caller fails closed toward the operator.
		{"none", []string{"go.mod", "main.go", "README.md"}, ImageDecl{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := LocateImage(c.files)
			if ok != c.ok || got != c.want {
				t.Errorf("LocateImage(%v) = (%+v, %v), want (%+v, %v)", c.files, got, ok, c.want, c.ok)
			}
		})
	}
}

// ReadImageBuild resolves the Dockerfile a devcontainer.json builds from — the
// `build.dockerfile`/`build.context` object form, or the legacy top-level
// `dockerFile`. semdev uses only this to LOCATE the image definition; it owns the run
// itself (SB2 — never `devcontainer up` warm-reuse).
func TestReadImageBuild(t *testing.T) {
	cases := []struct {
		name           string
		json           string
		wantDockerfile string
		wantContext    string
		wantFound      bool
	}{
		{"build-object", `{"build":{"dockerfile":"Dockerfile","context":".."}}`, "Dockerfile", "..", true},
		{"build-dockerfile-only", `{"build":{"dockerfile":"docker/Dev.Dockerfile"}}`, "docker/Dev.Dockerfile", "", true},
		{"legacy-top-level", `{"dockerFile":"Dockerfile"}`, "Dockerfile", "", true},
		// A devcontainer that only names a prebuilt image (no build) → not found; the M0
		// builder fails closed on it (M0 builds a Dockerfile, does not pull a prebuilt image).
		{"image-only", `{"image":"golang:1.26"}`, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			df, ctx, found, err := ReadImageBuild([]byte(c.json))
			if err != nil {
				t.Fatalf("ReadImageBuild: %v", err)
			}
			if df != c.wantDockerfile || ctx != c.wantContext || found != c.wantFound {
				t.Errorf("ReadImageBuild(%s) = (%q, %q, %v), want (%q, %q, %v)", c.json, df, ctx, found, c.wantDockerfile, c.wantContext, c.wantFound)
			}
		})
	}
}
