package openspec

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseTasks_DarkModeFixture(t *testing.T) {
	tk := ParseTasks(readFixture(t, "dark-mode-tasks.md"))
	want := &Tasks{
		Title: "Tasks",
		Sections: []TaskSection{
			{
				Name: "1. Theme Infrastructure",
				Tasks: []Task{
					{Number: "1.1", Text: "Create ThemeContext with light/dark state"},
					{Number: "1.2", Text: "Add CSS custom properties for colors", Done: true},
				},
			},
			{
				Name: "2. UI Components",
				Tasks: []Task{
					{Number: "2.1", Text: "Create ThemeToggle component"},
					{Number: "2.2", Text: "Add toggle to settings page"},
				},
			},
		},
	}
	if diff := cmp.Diff(want, tk); diff != "" {
		t.Errorf("ParseTasks mismatch (-want +got):\n%s", diff)
	}
}

func TestTasksRoundTrip(t *testing.T) {
	first := ParseTasks(readFixture(t, "dark-mode-tasks.md"))
	if diff := cmp.Diff(first, ParseTasks(RenderTasks(first))); diff != "" {
		t.Errorf("tasks round-trip not stable (-first +second):\n%s", diff)
	}
	once := RenderTasks(first)
	if twice := RenderTasks(ParseTasks(once)); once != twice {
		t.Errorf("tasks render not idempotent:\n once =\n%s\n twice =\n%s", once, twice)
	}
}

func TestParseTasks_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want *Tasks
	}{
		{
			name: "plain bullet without a checkbox becomes an undone task",
			md:   "# Tasks\n\n## S\n- do a thing\n",
			want: &Tasks{Title: "Tasks", Sections: []TaskSection{{Name: "S", Tasks: []Task{{Text: "do a thing"}}}}},
		},
		{
			name: "task before any section lands in an implicit section",
			md:   "# Tasks\n\n- [ ] 1.1 early\n",
			want: &Tasks{Title: "Tasks", Sections: []TaskSection{{Tasks: []Task{{Number: "1.1", Text: "early"}}}}},
		},
		{
			name: "uppercase done marker",
			md:   "# Tasks\n\n## S\n- [X] 1.1 done\n",
			want: &Tasks{Title: "Tasks", Sections: []TaskSection{{Name: "S", Tasks: []Task{{Number: "1.1", Text: "done", Done: true}}}}},
		},
		{
			name: "task with no number keeps all text",
			md:   "# Tasks\n\n## S\n- [ ] just text no number\n",
			want: &Tasks{Title: "Tasks", Sections: []TaskSection{{Name: "S", Tasks: []Task{{Text: "just text no number"}}}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseTasks(tt.md)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseTasks mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(got, ParseTasks(RenderTasks(got))); diff != "" {
				t.Errorf("round-trip unstable (-got +reparsed):\n%s", diff)
			}
		})
	}
}

// TestSplitTaskNumber pins the number/text split (and isTaskNumber transitively),
// including the lenient cases (multi-level, no-dot, trailing-dot, number-in-text).
func TestSplitTaskNumber(t *testing.T) {
	tests := []struct {
		name             string
		in               string
		wantNum, wantTxt string
	}{
		{"dotted number + text", "1.1 do thing", "1.1", "do thing"},
		{"multi-level number", "1.2.3 deep", "1.2.3", "deep"},
		{"no-dot number", "12 twelve", "12", "twelve"},
		{"number only", "3.4", "3.4", ""},
		{"non-numeric head", "do a thing", "", "do a thing"},
		{"number inside text is kept", "1.1 3 blind mice", "1.1", "3 blind mice"},
		{"trailing dot accepted as number", "1. text", "1.", "text"},
		{"lone dot is not a number", ". text", "", ". text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num, txt := splitTaskNumber(tt.in)
			if num != tt.wantNum || txt != tt.wantTxt {
				t.Errorf("splitTaskNumber(%q) = (%q, %q); want (%q, %q)", tt.in, num, txt, tt.wantNum, tt.wantTxt)
			}
		})
	}
}
