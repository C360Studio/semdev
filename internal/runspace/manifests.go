package runspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/harness"
)

// Manifests resolves a checkout's reproducibility contract — the operator-declared image
// + the run fields — from what the repo COMMITTED (design SB2). It is the concrete
// verify_artifact.Manifests seam (and the source measure_task/the provisioning station
// read the declared image from): it detects the ecosystem profile, locates the committed
// Dockerfile/devcontainer, and overlays any customizations.semdev block onto the profile
// convention. It holds no harvest/inference logic — semdev never guesses an image (SB2);
// a repo that declares none fails closed (ErrNoImage) toward the operator.
type Manifests struct{}

// Resolve reads the checkout's declared manifest. It fails closed: an unrecognized
// ecosystem (no profile marker) or an undeclared image both error toward the operator,
// never a guessed toolchain or image.
func (Manifests) Resolve(_ context.Context, checkoutRoot string) (harness.Manifest, error) {
	entries, err := os.ReadDir(checkoutRoot)
	if err != nil {
		return harness.Manifest{}, fmt.Errorf("runspace: read checkout %s: %w", checkoutRoot, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	profile, ok := harness.DetectProfile(names)
	if !ok {
		return harness.Manifest{}, fmt.Errorf("runspace: no ecosystem profile marker in %s — cannot resolve a manifest (declare a supported project)", checkoutRoot)
	}

	decl, err := cleanroom.LocateImageInDir(checkoutRoot)
	if err != nil {
		return harness.Manifest{}, err // wraps ErrNoImage when nothing is declared (SB2)
	}

	var dcJSON []byte
	if decl.Devcontainer != "" {
		if dcJSON, err = os.ReadFile(filepath.Join(checkoutRoot, decl.Devcontainer)); err != nil {
			return harness.Manifest{}, fmt.Errorf("runspace: read declared devcontainer %s: %w", decl.Devcontainer, err)
		}
	}
	return harness.ResolveManifest(profile, decl, dcJSON)
}
