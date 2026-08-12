package conversation

import (
	"reflect"
	"sort"
	"strings"
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

// Channel is now the THREE-verb surface (Post, ResolveThread, Read) — the poll
// transport (pull-first-transport) gave Read a caller, so it is no longer latent.
// The signature must stay host-neutral: no githubwebhook type on any verb, or the
// GitHub shape re-leaks into the arc/component boundary the port dissolves.
func TestChannelReadVerb(t *testing.T) {
	ty := reflect.TypeOf((*Channel)(nil)).Elem()
	wantSet := map[string]bool{"Post": true, "ResolveThread": true, "Read": true}
	if ty.NumMethod() != len(wantSet) {
		var got []string
		for i := 0; i < ty.NumMethod(); i++ {
			got = append(got, ty.Method(i).Name)
		}
		t.Fatalf("Channel has methods %v, want exactly {Post, ResolveThread, Read}", got)
	}
	for i := 0; i < ty.NumMethod(); i++ {
		if name := ty.Method(i).Name; !wantSet[name] {
			t.Errorf("Channel names an unexpected verb %q", name)
		}
	}

	// Read(ctx, ThreadRef, Cursor) ([]Message, Cursor, error).
	read, ok := ty.MethodByName("Read")
	if !ok {
		t.Fatal("Channel has no Read verb — pull-first-transport's poll transport needs it")
	}
	rt := read.Type
	// Interface method type has no receiver: In(0)=ctx, In(1)=ThreadRef, In(2)=Cursor.
	if rt.NumIn() != 3 || rt.NumOut() != 3 {
		t.Fatalf("Read signature = %v, want (context.Context, ThreadRef, Cursor) ([]Message, Cursor, error)", rt)
	}
	if rt.In(1) != reflect.TypeOf(ThreadRef("")) {
		t.Errorf("Read's thread arg is %v, want conversation.ThreadRef", rt.In(1))
	}
	if rt.In(2) != reflect.TypeOf(Cursor("")) {
		t.Errorf("Read's cursor arg is %v, want conversation.Cursor", rt.In(2))
	}
	if rt.Out(0) != reflect.TypeOf([]Message(nil)) {
		t.Errorf("Read's first result is %v, want []conversation.Message", rt.Out(0))
	}
	if rt.Out(1) != reflect.TypeOf(Cursor("")) {
		t.Errorf("Read's second result is %v, want conversation.Cursor (the next cursor)", rt.Out(1))
	}

	// Host-neutrality: no verb's signature may name a type from EITHER host adapter
	// package — internal/forge/github (the REST client: github.Comment, …) or
	// internal/forge/githubwebhook (the webhook shapes). Both share the
	// "internal/forge/github" path prefix and neither is the port's own package
	// (internal/forge/conversation), so one substring check catches both leaks. Walk
	// every in/out type of every method, unwrapping ptr/slice.
	for i := 0; i < ty.NumMethod(); i++ {
		mt := ty.Method(i).Type
		types := make([]reflect.Type, 0, mt.NumIn()+mt.NumOut())
		for j := 0; j < mt.NumIn(); j++ {
			types = append(types, mt.In(j))
		}
		for j := 0; j < mt.NumOut(); j++ {
			types = append(types, mt.Out(j))
		}
		for _, tt := range types {
			el := tt
			for el.Kind() == reflect.Ptr || el.Kind() == reflect.Slice {
				el = el.Elem()
			}
			if strings.Contains(el.PkgPath(), "internal/forge/github") {
				t.Errorf("Channel.%s signature names host-adapter type %v — the port must stay host-neutral", ty.Method(i).Name, tt)
			}
		}
	}
}

// Cursor is an opaque string — the poller stores and passes it back, never parses
// it. Only the impl encodes/decodes it (a comment ID for GitHub v1).
func TestCursorIsOpaqueString(t *testing.T) {
	if k := reflect.TypeOf(Cursor("")).Kind(); k != reflect.String {
		t.Fatalf("Cursor underlying kind = %v, want string (an opaque impl-defined read position)", k)
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
