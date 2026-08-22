package standards

import (
	"strings"
	"testing"
)

// A repo-declared check is a GATE semdev has never watched fail. semdev's own floors
// earn their authority by being red-first pinned (G6); a repo's command arrives with no
// such history, and the dangerous failure of a checker is fail-OPEN — nothing crashes,
// the layer prints pass, and it sits green forever while the thing it claims to guard
// rots (DR-0001).
//
// These tables cover the half the parser can decide: commands that cannot fail BY
// CONSTRUCTION. They are a denylist over nameable shapes, not a proof of gate soundness
// — D7a's negative control carries the positive claim.

func checkYAML(command string, required bool) string {
	req := "false"
	if required {
		req = "true"
	}
	return "version: 1\nchecks:\n  - name: c\n    command: " + command + "\n    required: " + req + "\n"
}

func TestParseRejectsChecksThatCannotFail(t *testing.T) {
	cases := []struct {
		name      string
		command   string
		required  bool
		errSubstr string
	}{
		// A required gate's status must be ITS OWN. Every separator below hands the
		// status to something else, and each was a live escape in the first draft of
		// this file — the reviewers broke all of them by deleting a space.
		{"|| true", `"go vet ./... || true"`, true, "||"},
		{"|| true, no spaces", `"go vet ./...||true"`, true, "||"},
		{"|| : ", `"go vet ./... || :"`, true, "||"},
		{"trailing ; true", `"go vet ./...; true"`, true, ";"},
		{"; true, no space", `"go vet ./...;true"`, true, ";"},
		{"; with extra spaces", `"go vet ./... ;  true"`, true, ";"},
		{"; echo done", `"go vet ./...; echo done"`, true, ";"},
		{"backgrounded", `"go vet ./... &"`, true, "&"},

		// The same bug written as a pipeline: the status is the LAST command's.
		{"required pipeline", `"go test -cover ./... | tail -1"`, true, "|"},
		{"pipeline, no spaces", `"go test ./...|tail -1"`, true, "|"},

		// A YAML block scalar is how people write more than one command, and the last
		// line is the only one whose status becomes the gate.
		{"multi-line trailing true", "|\n      go vet ./...\n      true", true, "multiple top-level commands"},

		// Vacuity, judged on the resolved basename so a path spelling is the same answer.
		{"bare true", `"true"`, true, "cannot fail"},
		{"absolute true", `"/bin/true"`, true, "cannot fail"},
		{"bare colon", `":"`, true, "cannot fail"},
		{"bare echo", `"echo ok"`, true, "cannot fail"},
		{"printf", `"printf ok"`, true, "cannot fail"},
		{"true with a comment that looks like a gate", `"true # go vet ./..."`, true, "cannot fail"},
		{"explicit exit 0", `"exit 0"`, true, "exit 0"},
		{"whitespace only", `"   "`, true, "no command"},
		{"comment only", `"# go vet ./..."`, true, "no executable command"},

		// Vacuity is rejected on a NON-required check too: it gates nothing, but a check
		// that cannot fail is not a report either.
		{"vacuous non-required", `"true"`, false, "cannot fail"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(checkYAML(c.command, c.required)))
			if err == nil {
				t.Fatalf("a check that cannot fail was accepted: %s", c.command)
			}
			if !strings.Contains(err.Error(), c.errSubstr) {
				t.Errorf("rejection %q does not name the offending construct %q", err, c.errSubstr)
			}
			if !strings.Contains(err.Error(), `"c"`) {
				t.Errorf("rejection %q does not name which check is at fault", err)
			}
		})
	}
}

// TestParseAcceptsHonestChecks is the positive control: the denylist must not reject
// ordinary commands. A gate lint that fails a legitimate repo is worse than none — it
// blocks provisioning on a file the human cannot see anything wrong with.
func TestParseAcceptsHonestChecks(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		required bool
	}{
		{"plain vet", `"go vet ./..."`, true},
		{"command substitution", `"test -z \"$(gofmt -l .)\""`, true},
		// A pipe INSIDE a substitution does not set the outer status — rejecting these
		// blocks provisioning on an ordinary Go idiom, the expensive failure direction.
		{"pipe inside $( )", `"go test $(go list ./... | grep -v vendor)"`, true},
		{"pipe inside backticks", "\"go test `go list ./... | head -1`\"", true},
		{"pipe inside double quotes", `"grep -R \"a|b\" ."`, true},
		{"pipe inside single quotes", `"grep -R 'a|b' ."`, true},
		{"escaped quote inside a quoted span", `"grep -e \"a\\\"b|c\" ."`, true},
		// && propagates failure, so a sequence built with it can still fail.
		{"&& sequence", `"go build ./... && go vet ./..."`, true},
		{"a repo script owns its own pipefail", `"./scripts/coverage.sh"`, true},
		// A non-required check gates nothing, so the single-command rule does not apply.
		{"non-required pipeline", `"go test -cover ./... | tail -1"`, false},
		{"non-required sequence", `"go vet ./...; echo done"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := Parse([]byte(checkYAML(c.command, c.required)))
			if err != nil {
				t.Fatalf("an honest check was rejected: %v", err)
			}
			if len(f.Checks) != 1 {
				t.Fatalf("parsed %d checks, want 1", len(f.Checks))
			}
		})
	}
}

// TestParseReadsTheNegativeControl covers D7a's `proof` field: the optional command that
// MUST exit non-zero, which is how a repo demonstrates its gate can reach its failure
// path at all.
func TestParseReadsTheNegativeControl(t *testing.T) {
	f, err := Parse([]byte("version: 1\nchecks:\n" +
		"  - name: go-vet\n    command: go vet ./...\n    required: true\n    proof: go vet ./testdata/badvet\n" +
		"  - name: gofmt\n    command: gofmt -l .\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Checks[0].Proof; got != "go vet ./testdata/badvet" {
		t.Errorf("proof = %q, want the declared control command", got)
	}
	if f.Checks[1].Proof != "" {
		t.Errorf("an undeclared proof must stay empty, got %q", f.Checks[1].Proof)
	}
}

// TestParseHoldsTheProofToTheSameStandardAsTheCheck: a negative control that cannot fail
// is worse than none — it certifies the gate while proving nothing.
func TestParseHoldsTheProofToTheSameStandardAsTheCheck(t *testing.T) {
	cases := []struct{ name, proof, errSubstr string }{
		{"suppressed proof", `"badvet || true"`, "||"},
		{"vacuous proof", `"true"`, "cannot fail"},
		{"empty proof", `"  "`, "empty"},
		// The mirror image of vacuity: a control that fails on its own never reaches the
		// checker, so it certifies the gate while demonstrating nothing about it.
		{"always-failing proof", `"false"`, "cannot PASS"},
		{"absolute false", `"/bin/false"`, "cannot PASS"},
		{"bare exit 1", `"exit 1"`, "cannot PASS"},
		{"exit 2", `"exit 2"`, "cannot PASS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte("version: 1\nchecks:\n  - name: c\n    command: go vet ./...\n" +
				"    required: true\n    proof: " + c.proof + "\n"))
			if err == nil {
				t.Fatal("a negative control that cannot fail was accepted")
			}
			if !strings.Contains(err.Error(), c.errSubstr) {
				t.Errorf("rejection %q does not name the defect %q", err, c.errSubstr)
			}
		})
	}
}

// TestTopLevelPipelineScanner pins the two traps a naive scan falls into, feeding the
// pure function directly so each is unambiguous.
func TestTopLevelSeparatorScanner(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string // "" = no masking separator; ";|" means either is acceptable
	}{
		{"plain pipeline", "a | b", ";|"},
		{"or", "a || b", "||"},
		{"or with no spaces", "a||b", "||"},
		{"semicolon", "a; b", ";"},
		{"background", "a &", "&"},
		{"and is not a separator that masks", "a && b", ""},
		{"pipe in double quotes", `grep "a|b" .`, ""},
		{"pipe in single quotes", `grep 'a|b' .`, ""},
		{"pipe inside $( )", `go test $(go list ./... | grep -v x)`, ""},
		{"pipe inside backticks", "go test `ls | head -1`", ""},
		{"escaped quote inside a quoted span", `grep -e "a\"b|c" .`, ""},
		{"escaped pipe", `printf a\|b`, ""},
		{"pipe after a quoted span", `grep "a" . | wc -l`, "|"},
		{"no separator at all", "go vet ./...", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found := topLevelSeparator(c.cmd)
			if c.want == "" {
				if found {
					t.Errorf("topLevelSeparator(%q) reported %q; a false REJECT blocks provisioning on an "+
						"ordinary command, which is the expensive direction", c.cmd, got)
				}
				return
			}
			if !found {
				t.Fatalf("topLevelSeparator(%q) found nothing; the command's status is not its own, so a "+
					"required gate built on it can never fail", c.cmd)
			}
			if !strings.Contains(c.want, got) {
				t.Errorf("topLevelSeparator(%q) = %q, want one of %q", c.cmd, got, c.want)
			}
		})
	}
}
