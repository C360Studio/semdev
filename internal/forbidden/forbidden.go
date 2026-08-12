// Package forbidden is the clean-room build-file tripwire (design SB3, ported from
// semspec's forbidden_patterns): it scans an artifact's BUILD FILES AND THE SCRIPTS THEY
// INVOKE for hidden-download mechanisms that would dodge the cold-resolution proof. A build
// step that fetches source or dependencies at build time from a raw URL — or invokes a
// semdev network tool — is NOT self-contained: it could make the artifact "build" in any
// environment with network while the cold-resolution proof (which resolves the DECLARED
// dependencies) never sees the smuggled fetch. semspec's fatal bug was exactly this class
// inverted (a harness init.d source-substitution SCRIPT that built in the harness and 401'd
// on a clean checkout); the tripwire catches the committed-artifact version of it.
//
// This is the SOLE STATIC control for the class: the cold-verify container runs with
// network (its whole job is to resolve declared deps), so a smuggled fetch would ALSO
// succeed cold — the cold proof is blind to it by design. That makes the scan's file scope
// the security boundary. It is a heuristic DENYLIST, not a proof of self-containment: it
// reads the build files and the scripts they commonly invoke (the `RUN ./setup.sh` idiom is
// why scripts are in scope), but a determined smuggler could still hide a fetch behind a
// mechanism not in the scan set (a compiled helper, an unusual interpreter). The set errs
// toward false-PARK for scanned files (SB5-safe — a legit build file mentioning a banned
// host parks toward the human) and is extended as new build ecosystems land; when it cannot
// prove a build step self-contained, it does not claim to.
//
// The scan is PURE deterministic Go over the committed bytes (like internal/floors) — no
// container, no network, no model — so a forbidden pattern is a `go test` failure here, and
// the clean-room verify folds a finding into a genuine Fail via verify.ForbiddenVerdict (a
// definitive Fail whose sole check is self-containment), short-circuiting ProveArtifact
// BEFORE any cold build/test (which therefore never run — the honest verdict reports only
// the check that was evaluated, G7). It writes no facts.
package forbidden

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Patterns are the banned substrings in build files (SB3). raw.githubusercontent.com is a
// build-time fetch of source/config from a raw URL (semspec's substitution vector);
// web_search / http_request are semdev's own network tools, which have no business inside
// an artifact's build. The list is a var so a profile can extend it (e.g. an OSH build that
// bans a specific mirror) without forking the scanner.
var Patterns = []string{
	"raw.githubusercontent.com",
	"web_search",
	"http_request",
}

// buildFileNames are the exact basenames the scanner reads — the files that declare or
// script a build and could therefore smuggle a runtime download. Ecosystem-broad on
// purpose (Go + JVM/Gradle for the OSH targets); a name not here (and not a scanned script
// or extension, see isBuildFile) is source/data, not a build file, and is out of scope (a
// URL in a Go const or a README is not a build-time fetch).
//
// Wrapper property files (gradle-wrapper.properties / maven-wrapper.properties) are here
// because they literally declare a distributionUrl the build downloads — the JVM mirror-swap
// surface for the OSH M2 targets.
var buildFileNames = map[string]bool{
	"Dockerfile":                true,
	"go.mod":                    true,
	"go.sum":                    true,
	"Makefile":                  true,
	"makefile":                  true,
	"build.gradle":              true,
	"build.gradle.kts":          true,
	"settings.gradle":           true,
	"settings.gradle.kts":       true,
	"gradle.properties":         true,
	"gradle-wrapper.properties": true, // declares distributionUrl (JVM download; SB3, M2 OSH)
	"maven-wrapper.properties":  true,
	"gradlew":                   true, // the wrapper script a build invokes
	"gradlew.bat":               true,
	"pom.xml":                   true,
	"devcontainer.json":         true,
}

// skipDirs are directories that are not the artifact's OWN committed build declarations —
// third-party vendored code, test fixtures, tooling caches, VCS. A build file basename
// under one of these (a vendored dep's go.mod, a testdata Dockerfile) is not a build step
// that produces the artifact, so scanning it only raises false-parks; the artifact's own
// build files live outside them.
var skipDirs = map[string]bool{
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
	".git":         true,
}

// Finding is one forbidden-pattern hit: the repo-relative build file, the banned pattern,
// and the 1-based line it appeared on (with the line's text for the operator).
type Finding struct {
	File    string
	Pattern string
	Line    int
	Excerpt string
}

// Scan walks root and returns every forbidden-pattern hit in its BUILD FILES. It is
// non-fatal per file where it can be (an unreadable build file is a scan fault surfaced as
// an error, not a silent skip — a build file we cannot read could hide a pattern, so we
// fail toward the caller, which fails closed). A clean artifact returns no findings.
func Scan(root string) ([]Finding, error) {
	var findings []Finding
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// A symlinked build-file basename could point outside the tree; Scan is exported,
		// so skip symlinks defensively (the verify clone already strips them, SB4).
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !isBuildFile(d.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		fileFindings, scanErr := scanFile(path, rel)
		if scanErr != nil {
			return fmt.Errorf("forbidden: scan build file %s: %w", rel, scanErr)
		}
		findings = append(findings, fileFindings...)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return findings, nil
}

// isBuildFile reports whether basename is a build file the scanner reads: an exact name in
// the set, a Gradle build script by extension (named per module, not a fixed basename), or
// a shell script — which a Dockerfile/Makefile commonly INVOKES as a build step (`RUN
// ./setup.sh`), so a fetch smuggled into a committed script is a build-time fetch the raw
// Dockerfile line would hide. Scanning scripts closes that indirection (the semspec init.d
// class); a script that curls a raw URL at build time is exactly what SB3 bans.
func isBuildFile(name string) bool {
	if buildFileNames[name] {
		return true
	}
	switch {
	case strings.HasSuffix(name, ".gradle"), strings.HasSuffix(name, ".gradle.kts"):
		return true
	case strings.HasSuffix(name, ".sh"), strings.HasSuffix(name, ".bash"):
		return true
	default:
		return false
	}
}

// scanFile reads one build file and returns a finding for each forbidden pattern on each
// line. Case-insensitive (a host name is not case-sensitive; a smuggled URL should not
// evade by casing).
func scanFile(path, rel string) ([]Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var findings []Finding
	scanner := bufio.NewScanner(f)
	// Allow long build-file lines (a minified devcontainer.json or a long RUN command).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		lower := strings.ToLower(text)
		for _, pat := range Patterns {
			if strings.Contains(lower, strings.ToLower(pat)) {
				findings = append(findings, Finding{File: rel, Pattern: pat, Line: line, Excerpt: strings.TrimSpace(text)})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return findings, nil
}

// Detail renders findings into a single operator-facing line for the verify verdict —
// which build files carried which banned patterns. Empty when there are no findings.
func Detail(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s:%d references banned %q", f.File, f.Line, f.Pattern))
	}
	return "build files are not self-contained (hidden runtime downloads dodge the cold-resolution proof): " + strings.Join(parts, "; ")
}
