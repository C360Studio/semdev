package conversation

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

// ThreadRef must be an opaque string — a host-neutral handle callers never parse.
// If it grew a struct shape (owner/repo/number), the host coordinate would leak
// back into every caller the port was carved to insulate.
func TestThreadRefIsOpaqueString(t *testing.T) {
	if k := reflect.TypeOf(ThreadRef("")).Kind(); k != reflect.String {
		t.Fatalf("ThreadRef underlying kind = %v, want string (an opaque host-neutral handle)", k)
	}
}

// Message is the neutral unit: exactly {ID, Author, Body, At}, and — the carve's
// whole point — NO host coordinate (owner/repo/number/issueRef). A host field here
// would re-leak GitHub's shape into the arc/component boundary the port dissolves.
func TestMessageCarriesNoHostShape(t *testing.T) {
	want := map[string]reflect.Type{
		"ID":     reflect.TypeOf(""),
		"Author": reflect.TypeOf(""),
		"Body":   reflect.TypeOf(""),
		"At":     reflect.TypeOf(time.Time{}),
	}
	// Field names that would re-introduce a code-host coordinate onto the unit.
	forbidden := map[string]bool{
		"Owner": true, "Repo": true, "Number": true,
		"IssueRef": true, "Ref": true, "Thread": true, "ThreadRef": true,
	}

	ty := reflect.TypeOf(Message{})
	if ty.NumField() != len(want) {
		t.Fatalf("Message has %d fields, want %d (%v)", ty.NumField(), len(want), sortedKeys(want))
	}
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if forbidden[f.Name] {
			t.Errorf("Message carries host-shaped field %q — the neutral unit must not name a code-host coordinate", f.Name)
		}
		wt, ok := want[f.Name]
		if !ok {
			t.Errorf("Message has unexpected field %q (%v); the neutral unit is {ID,Author,Body,At}", f.Name, f.Type)
			continue
		}
		if f.Type != wt {
			t.Errorf("Message.%s is %v, want %v", f.Name, f.Type, wt)
		}
	}
}

// Channel is deliberately the two-verb surface (Post, ResolveThread) — NO Read yet
// (that is pull-first-transport's, and adding it now is latent code).
func TestConversationChannelHasNoReadVerb(t *testing.T) {
	ty := reflect.TypeOf((*Channel)(nil)).Elem()
	want := []string{"Post", "ResolveThread"}
	wantSet := map[string]bool{"Post": true, "ResolveThread": true}
	if ty.NumMethod() != len(want) {
		var got []string
		for i := 0; i < ty.NumMethod(); i++ {
			got = append(got, ty.Method(i).Name)
		}
		t.Fatalf("Channel has methods %v, want exactly %v (no Read until pull-first-transport)", got, want)
	}
	for i := 0; i < ty.NumMethod(); i++ {
		if name := ty.Method(i).Name; !wantSet[name] {
			t.Errorf("Channel names an unexpected verb %q (a Read verb here would be latent code)", name)
		}
	}
}

func sortedKeys(m map[string]reflect.Type) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
