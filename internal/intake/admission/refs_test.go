package admission

import "testing"

// SplitRef parses the host-neutral "owner/repo#number" coordinate, and rejects
// every malformed shape (the fail-closed parse the launch driver + the conversation
// channel depend on).
func TestSplitRef(t *testing.T) {
	owner, repo, n, err := SplitRef("c360studio/semdev-fixture#7")
	if err != nil || owner != "c360studio" || repo != "semdev-fixture" || n != 7 {
		t.Errorf("SplitRef = %s/%s#%d (%v)", owner, repo, n, err)
	}
	for _, bad := range []string{"", "no-hash", "#7", "owner#7", "o/r#zero", "o/r#0"} {
		if _, _, _, err := SplitRef(bad); err == nil {
			t.Errorf("SplitRef(%q) must error", bad)
		}
	}
}
