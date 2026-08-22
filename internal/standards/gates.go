package standards

// Gate honesty for repo-declared checks (standards-via-lessons D1/D7a, DR-0001).
//
// A check command is a GATE, and a gate that cannot fail is worse than no gate: it reads
// green forever while the thing it claims to guard rots, and nothing in a green run says
// so. semdev's own floors earn their authority by being red-first pinned (G6); a
// repo-authored command arrives with no such history.
//
// The rule for a REQUIRED check is deliberately strict and structural rather than a hunt
// for bad spellings: **its exit status must be the gate**. In a shell, a top-level `;`,
// newline, `|`, or `||` all mean the command's status is somebody else's — `go vet ./...;
// true` and `go vet ./... || true` and `go test -cover ./... | tail -1` are the same bug
// wearing three hats. So a required check must be ONE top-level command. `&&` is allowed
// because it can still fail (`a && b` is non-zero when either is), and everything inside
// quotes, `$( )`, or backticks is data whose status never surfaces.
//
// An earlier draft of this file matched literal spellings (`"|| true"`, `"; true"`) and
// was defeated by deleting a space; both reviewers broke it in under a minute. Matching
// the SEPARATOR is what makes the rule hold, and the escape hatch is the right shape
// anyway: put the pipeline or the sequence in a repo-committed script whose own
// `set -euo pipefail` owns the status, and name the script here.
//
// This is still a denylist over shapes we can name, not a proof of soundness. The
// positive claim is D7a's negative control, and even that proves only that ONE known-bad
// case reaches a failure path.

import (
	"fmt"
	"strings"
)

// vacuousBases are command basenames that exit 0 no matter what. Matched on the first
// word's basename so `/bin/true` and `true` are the same answer.
var vacuousBases = map[string]bool{"true": true, ":": true, "echo": true, "printf": true}

// alwaysFailBases are the mirror image, and they matter only for a negative control: a
// control's whole job is to reach the CHECKER's failure path, so one that fails on its
// own — without ever invoking the checker — certifies the gate while proving nothing.
// That is worse than declaring no control at all, because an absent control is stamped
// `unproven` and an always-failing one reads `proven`.
var alwaysFailBases = map[string]bool{"false": true}

// diagnosticSuppressors hide a command's OUTPUT (not its status). They do not break the
// gate, so they warn rather than reject — but a failure nobody can read is most of the way
// to a failure nobody notices.
var diagnosticSuppressors = []string{"2>/dev/null", "2> /dev/null", "2>&-", "2> &-"}

// checkDefect returns a rejection reason and any warnings for a declared command.
// requiredGate selects the single-command rule, which only matters when the exit status
// actually gates an attempt.
func checkDefect(command string, requiredGate bool) (defect string, warnings []string) {
	effective := effectiveLines(command)
	if len(effective) == 0 {
		return "has no executable command (only blank lines or comments)", nil
	}

	for _, s := range diagnosticSuppressors {
		if strings.Contains(command, s) {
			warnings = append(warnings, fmt.Sprintf("contains %q, so a failure's diagnostics are discarded — "+
				"the gate still works, but nobody will be able to read why it fired", s))
			break
		}
	}

	if requiredGate {
		// More than one effective line means a top-level newline separator: the status is
		// the LAST line's, and everything above it is unguarded.
		if len(effective) > 1 {
			return "spans multiple top-level commands, so only the LAST one's exit status becomes the gate — " +
				"move the sequence into a repo script that sets `set -euo pipefail` and name the script here", nil
		}
		if sep, found := topLevelSeparator(effective[0]); found {
			return fmt.Sprintf("contains a top-level %q, so its exit status is not this command's — "+
				"a required gate must be one command (use `&&` if you need a sequence that still fails, or move it "+
				"into a repo script that sets `set -euo pipefail`)", sep), nil
		}
	}

	// Vacuity is judged on the LAST effective line, since that is what sets the status.
	if base := firstWordBase(effective[len(effective)-1]); vacuousBases[base] {
		return fmt.Sprintf("resolves to %q, which cannot fail — the check would read green forever", base), warnings
	}
	if exitsZero(effective[len(effective)-1]) {
		return "is an explicit `exit 0`, which cannot fail — the check would read green forever", warnings
	}
	return "", warnings
}

// proofDefect holds a negative control to the check rules plus the inverse one.
//
// The honest bound, stated here so nothing downstream overclaims: this rejects controls
// broken by SHAPE. It cannot verify that a control exercises the same checker as its
// check — `proof: go vet ./testdata/bad` paired with `command: staticcheck ./...` passes
// every rule and demonstrates nothing. A control proves that one known-bad case reaches
// SOME failure path; treating `proven` as "sound" is the overclaim D7a exists to refuse.
func proofDefect(proof string) (string, []string) {
	effective := effectiveLines(proof)
	if len(effective) == 0 {
		return "has no executable command (only blank lines or comments)", nil
	}
	last := effective[len(effective)-1]
	if base := firstWordBase(last); alwaysFailBases[base] {
		return fmt.Sprintf("resolves to %q, which fails on its own without ever invoking the check — a control "+
			"that cannot PASS demonstrates nothing about the gate it certifies", base), nil
	}
	if exitsNonZero(last) {
		return "is an explicit non-zero `exit`, which fails without ever invoking the check — a control that " +
			"cannot PASS demonstrates nothing about the gate it certifies", nil
	}
	return checkDefect(proof, true)
}

// effectiveLines splits a command into its executable lines, dropping blanks and
// whole-line comments. A YAML block scalar is the natural way to write more than one
// command, so a single-line assumption is not one this can make.
func effectiveLines(command string) []string {
	var out []string
	for _, raw := range strings.Split(command, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// stripComment removes a trailing `#` comment that starts outside quotes and
// substitutions, so `true # go vet ./...` is seen for the no-op it is, and a `|` inside a
// comment does not read as a pipeline.
func stripComment(line string) string {
	s := newShellScan(line)
	for s.next() {
		if s.bare() && s.c == '#' && (s.i == 0 || isSpace(line[s.i-1])) {
			return line[:s.i]
		}
	}
	return line
}

// topLevelSeparator reports the first shell separator outside quotes and substitutions
// whose presence means this command's status is not the gate. `&&` is deliberately absent:
// `a && b` is non-zero whenever either part is, so it can still fail.
func topLevelSeparator(command string) (string, bool) {
	s := newShellScan(command)
	for s.next() {
		if !s.bare() {
			continue
		}
		switch s.c {
		case ';':
			return ";", true
		case '|':
			if s.peek() == '|' {
				return "||", true
			}
			return "|", true
		case '&':
			if s.peek() == '&' {
				s.skip() // `&&` is fine — it propagates failure
				continue
			}
			return "&", true
		}
	}
	return "", false
}

// shellScan walks a command tracking quotes and substitutions, so the rules above apply to
// SYNTAX and never to data. Both directions matter: a missed separator ships a gate that
// cannot fail, and a `|` inside `$( )`, backticks, or quotes falsely rejects an ordinary
// command and parks the run at provision.
type shellScan struct {
	src   string
	i     int
	c     byte
	quote byte // 0, '\'' or '"'
	depth int  // $( ) nesting
	tick  bool // inside backticks
}

func newShellScan(src string) *shellScan { return &shellScan{src: src, i: -1} }

// bare reports whether the cursor is at top level (not quoted, not in a substitution).
func (s *shellScan) bare() bool { return s.quote == 0 && s.depth == 0 && !s.tick }

func (s *shellScan) peek() byte {
	if s.i+1 < len(s.src) {
		return s.src[s.i+1]
	}
	return 0
}

func (s *shellScan) skip() { s.i++ }

func (s *shellScan) next() bool {
	s.i++
	if s.i >= len(s.src) {
		return false
	}
	s.c = s.src[s.i]

	// A backslash escapes the next byte everywhere except inside single quotes, where the
	// shell treats it literally. Checking the quote state FIRST is what an earlier draft
	// got backwards: it made `"a\"|b"` close its quote early and read the `|` as syntax.
	if s.c == '\\' && s.quote != '\'' {
		s.i++
		return s.i < len(s.src) || s.next()
	}
	switch {
	case s.quote != 0:
		if s.c == s.quote {
			s.quote = 0
		}
	case s.tick:
		if s.c == '`' {
			s.tick = false
		}
	case s.c == '\'' || s.c == '"':
		s.quote = s.c
	case s.c == '`':
		s.tick = true
	case s.c == '$' && s.peek() == '(':
		s.depth++
		s.skip()
	case s.depth > 0 && s.c == ')':
		s.depth--
	}
	return true
}

// firstWordBase returns the basename of the command's first word, so `/bin/true` and
// `true` answer the same. Quotes are stripped; an empty command yields "".
func firstWordBase(command string) string {
	word := command
	if i := strings.IndexAny(word, " \t"); i >= 0 {
		word = word[:i]
	}
	word = strings.Trim(word, `"'`)
	if i := strings.LastIndexByte(word, '/'); i >= 0 {
		word = word[i+1:]
	}
	return word
}

func exitsZero(command string) bool {
	f := strings.Fields(command)
	return len(f) == 2 && f[0] == "exit" && f[1] == "0"
}

func exitsNonZero(command string) bool {
	f := strings.Fields(command)
	return len(f) == 2 && f[0] == "exit" && f[1] != "0"
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }
