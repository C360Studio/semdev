package admission

import (
	"fmt"
	"strconv"
	"strings"
)

// The flattened github.event.* subjects semdev's receiver publishes onto the
// GITHUB stream. The receiver (issue-intake) publishes BOTH issue and comment
// events; issue-intake consumes SubjectIssue, the conversation-channel component
// consumes SubjectComment — so these shared subject constants live in the shared
// admission core, imported by both. PR/review subjects are declared for the
// receiver's completeness; no consumer subscribes them at M0.
const (
	SubjectIssue   = "github.event.issue"
	SubjectPR      = "github.event.pr"
	SubjectComment = "github.event.comment"
	SubjectReview  = "github.event.review"
)

// SplitRef parses a host-neutral "owner/repo#number" coordinate into its parts.
// Exported for the operator launch driver (which needs the issue number to read
// the issue's content) and the conversation channel (which resolves a thread's
// code-host coordinate to post a comment).
func SplitRef(ref string) (owner, repo string, number int, err error) {
	hash := strings.LastIndexByte(ref, '#')
	slash := strings.IndexByte(ref, '/')
	if hash <= 0 || slash <= 0 || slash > hash {
		return "", "", 0, fmt.Errorf("ref %q is not owner/repo#number", ref)
	}
	n, err := strconv.Atoi(ref[hash+1:])
	if err != nil || n <= 0 {
		return "", "", 0, fmt.Errorf("ref %q has no issue number", ref)
	}
	return ref[:slash], ref[slash+1 : hash], n, nil
}
