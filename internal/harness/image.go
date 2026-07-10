package harness

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// imageCandidates lists the standard operator declaration paths in precedence order.
// A devcontainer.json is the richest declaration (it references the build + carries the
// customizations.semdev run block), so it beats a bare Dockerfile; a root Dockerfile is
// the M0 form; a .devcontainer/Dockerfile is the last fallback. semdev recognizes only
// these standard, portable paths — it never harvests or infers an environment (SB2).
var imageCandidates = []struct {
	Path   string
	Assign func(*ImageDecl, string)
}{
	{".devcontainer/devcontainer.json", func(d *ImageDecl, p string) { d.Devcontainer = p }},
	{".devcontainer.json", func(d *ImageDecl, p string) { d.Devcontainer = p }},
	{"Dockerfile", func(d *ImageDecl, p string) { d.Dockerfile = p }},
	{".devcontainer/Dockerfile", func(d *ImageDecl, p string) { d.Dockerfile = p }},
}

// LocateImage finds the operator's committed image declaration from a repo file
// listing (pure, like DetectProfile — the caller supplies the listing; this touches no
// filesystem). ok is false when the repo declares no usable image, so the caller fails
// closed toward the operator (SB2 — semdev never guesses an image).
func LocateImage(repoFiles []string) (ImageDecl, bool) {
	present := make(map[string]bool, len(repoFiles))
	for _, f := range repoFiles {
		present[filepath.ToSlash(f)] = true
	}
	for _, c := range imageCandidates {
		if present[c.Path] {
			var d ImageDecl
			c.Assign(&d, c.Path)
			return d, true
		}
	}
	return ImageDecl{}, false
}

// devcontainerBuild is the minimal slice of a devcontainer.json needed to LOCATE the
// image definition it builds from: the `build.dockerfile`/`build.context` object, or
// the legacy top-level `dockerFile`. Everything else (image, features, run args) is
// ignored — semdev owns the run (SB2), it uses the devcontainer only to find the
// Dockerfile to build.
type devcontainerBuild struct {
	Build struct {
		Dockerfile string `json:"dockerfile"`
		Context    string `json:"context"`
	} `json:"build"`
	LegacyDockerfile string `json:"dockerFile"`
	LegacyContext    string `json:"context"`
}

// ReadImageBuild resolves the Dockerfile (and optional build context) a devcontainer.json
// builds from. found is false (nil error) when the devcontainer names no buildable
// Dockerfile — e.g. it declares only a prebuilt `image` — so the M0 builder fails closed
// (it builds a Dockerfile, it does not pull a prebuilt image). Malformed JSON errors.
func ReadImageBuild(devcontainerJSON []byte) (dockerfile, context string, found bool, err error) {
	if len(devcontainerJSON) == 0 {
		return "", "", false, nil
	}
	var dc devcontainerBuild
	if err := json.Unmarshal(devcontainerJSON, &dc); err != nil {
		return "", "", false, fmt.Errorf("harness: parse devcontainer.json build: %w", err)
	}
	switch {
	case strings.TrimSpace(dc.Build.Dockerfile) != "":
		return dc.Build.Dockerfile, dc.Build.Context, true, nil
	case strings.TrimSpace(dc.LegacyDockerfile) != "":
		return dc.LegacyDockerfile, dc.LegacyContext, true, nil
	default:
		return "", "", false, nil
	}
}
