// Package registry is semdev's checked-in inventory of its OWN components and
// tools (G1 — primitive-first). semstreams' framework components are not listed
// here; only Go that semdev adds. Every entry links a framework-alignment note:
// which primitive was considered and why it cannot express the behavior. The G1
// conformance pin fails the build if semdev registers a component the framework
// did not provide without a matching entry, or if an entry points at a note that
// does not exist.
//
// semdev starts empty on purpose: the M0 spine is rule packs, persona fragments,
// and reused framework tools — no new Go components. Entries land here as later
// capability groups introduce G1-gated Go, each with its alignment note.
package registry

// Kind distinguishes a registered semstreams component from an agentic tool
// executor — the two surfaces the G1 census enumerates separately.
type Kind string

// KindComponent and KindTool are the two registered surfaces (G1) the census
// enumerates separately.
const (
	KindComponent Kind = "component"
	KindTool      Kind = "tool"
)

// Entry is one semdev-owned component or tool. Name is the registered factory or
// tool name; AlignmentNote is the section anchor of its note in
// docs/alignment-notes.md (e.g. "measurement-tool").
type Entry struct {
	Name          string
	Kind          Kind
	Capability    string
	AlignmentNote string
}

// Entries is semdev's own component/tool inventory.
var Entries = []Entry{
	{Name: "create_change", Kind: KindTool, Capability: "openspec-io", AlignmentNote: "create-change-author-tool"},
	{Name: "render_openspec", Kind: KindTool, Capability: "openspec-io", AlignmentNote: "render-openspec-hydrate-tool"},
	{Name: "write_change", Kind: KindTool, Capability: "openspec-io", AlignmentNote: "write-change-workspace-tool"},
}

// ComponentNames returns the declared names of Entries of KindComponent.
func ComponentNames() map[string]bool {
	return namesOfKind(KindComponent)
}

// ToolNames returns the declared names of Entries of KindTool.
func ToolNames() map[string]bool {
	return namesOfKind(KindTool)
}

func namesOfKind(kind Kind) map[string]bool {
	out := make(map[string]bool)
	for _, e := range Entries {
		if e.Kind == kind {
			out[e.Name] = true
		}
	}
	return out
}
