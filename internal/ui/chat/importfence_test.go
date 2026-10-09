package chat

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// screenModule is the import path every package of this module starts with.
const screenModule = "github.com/rfizzle/shhh/internal/"

// fencedForNames are the packages the screen once imported only to name a
// tool. What a call did is the receipt's to say, so a file that reaches one
// of them for a tool's name and nothing else is the screen growing a table
// of tools again — the thing that made a redrawn row a change to a dozen
// files (docs/architecture.md#one-agent-several-front-ends). A file may still
// import one for what it really is: the question a card asks, the memory
// store, the gate's verdict, the sources ledger.
var fencedForNames = []string{
	"ask", "evidence", "lsp", "memory", "process", "quality", "reports",
	"skill", "structural", "web",
}

// keptImports are the packages the screen goes on importing although each
// also names tools, and what it still needs from each. They are not fenced:
// what they hold is the screen's to use.
var keptImports = []string{
	// The prompts a server offers, which the /mcp surface lists and runs as
	// commands of its own.
	"mcp",
	// The children a session spawns: their lanes, their fan-out, their
	// asks, and the spawn a card is put to.
	"subagent",
	// The runner's typed ending of a command, the base toolset's tiers and
	// the mutating tools the screen previews and applies.
	"tools",
}

// isToolName reports whether a package-level identifier is a tool's name:
// every package spells its name constant that way.
func isToolName(ident string) bool { return strings.HasSuffix(ident, "ToolName") }

// namesOnly lists the fenced packages a file imports and reaches only for a
// tool's name.
func namesOnly(t *testing.T, f *ast.File) []string {
	t.Helper()
	locals := map[string]string{} // local name → fenced package
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		pkg, ok := strings.CutPrefix(path, screenModule)
		if !ok || !fenced(pkg) {
			continue
		}
		local := filepath.Base(pkg)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		locals[local] = pkg
	}
	other := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// A field of the same name — `m.mcp` — hangs off a selector rather
		// than an identifier, and so is never taken for the package.
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkg, ok := locals[x.Name]; ok && !isToolName(sel.Sel.Name) {
			other[pkg] = true
		}
		return true
	})
	var bad []string
	for _, pkg := range locals {
		if !other[pkg] {
			bad = append(bad, pkg)
		}
	}
	sort.Strings(bad)
	return bad
}

func fenced(pkg string) bool {
	for _, p := range fencedForNames {
		if p == pkg {
			return true
		}
	}
	return false
}

// behindBackend are what the screen reaches only through its backend, each
// a package and, where the package is still the screen's for other things,
// the names in it that are not. Every capability moved behind the backend
// adds what it took away, so a later edit that reaches around the seam
// fails here rather than quietly undoing the move
// (docs/architecture.md#one-agent-several-front-ends). The backend's own
// files are what stand on the far side of it, and are not fenced.
var behindBackend = []backendFence{
	// The stream's events. The screen hands the channel back to the backend
	// to be read and never reads an event off it itself; the messages and
	// their payloads, the conversation and its calls, stay the screen's.
	{"provider", []string{"StreamEvent"}},
}

// backendFence is one entry of that list.
type backendFence struct {
	pkg   string
	names []string // empty: the whole package
}

// backendFiles are the backend's own files.
var backendFiles = []string{"backend.go"}

// aroundBackend lists what a file reaches that the screen may reach only
// through its backend, as package or package.Name.
func aroundBackend(t *testing.T, f *ast.File, fences []backendFence) []string {
	t.Helper()
	locals := map[string]int{} // local name → index into fences
	var bad []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		pkg, ok := strings.CutPrefix(path, screenModule)
		if !ok {
			continue
		}
		for i, fence := range fences {
			if fence.pkg != pkg {
				continue
			}
			if len(fence.names) == 0 {
				bad = append(bad, pkg)
				continue
			}
			local := filepath.Base(pkg)
			if imp.Name != nil {
				local = imp.Name.Name
			}
			locals[local] = i
		}
	}
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// As above, a field of the same name hangs off a selector and is
		// never taken for the package.
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		i, ok := locals[x.Name]
		if !ok {
			return true
		}
		fence := fences[i]
		what := fence.pkg + "." + sel.Sel.Name
		if slices.Contains(fence.names, sel.Sel.Name) && !seen[what] {
			seen[what] = true
			bad = append(bad, what)
		}
		return true
	})
	sort.Strings(bad)
	return bad
}

// TestScreenImportFence reads every non-test file of the package and fails
// on one that reaches around one of two seams. The first is the receipt: a
// file that imports a fenced package only for a tool's name has found a
// question the receipt should answer, since a row asks it what a call was
// rather than comparing names. The second is the backend: a file outside it
// that names what the screen may reach only through it has gone around it.
func TestScreenImportFence(t *testing.T) {
	for _, pkg := range keptImports {
		if fenced(pkg) {
			t.Fatalf("%s is both kept and fenced", pkg)
		}
	}

	// The detector itself, on sources small enough to read.
	probes := []struct {
		name, src string
		want      string
	}{
		{"a name and nothing else",
			`package p; import "github.com/rfizzle/shhh/internal/lsp"; var _ = lsp.HoverToolName`, "lsp"},
		{"a name beside a real use",
			`package p; import "github.com/rfizzle/shhh/internal/web"; var _, _ = web.FetchToolName, web.Ledger{}`, ""},
		{"a real use alone",
			`package p; import "github.com/rfizzle/shhh/internal/quality"; var _ = quality.Summarize`, ""},
		{"a kept package's name is not this fence's",
			`package p; import "github.com/rfizzle/shhh/internal/subagent"; var _ = subagent.SpawnToolName`, ""},
		{"a renamed import is still the package",
			`package p; import q "github.com/rfizzle/shhh/internal/process"; var _ = q.ToolName`, "process"},
	}
	for _, p := range probes {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p.name+".go", p.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		if got := strings.Join(namesOnly(t, f), ","); got != p.want {
			t.Errorf("%s: flagged %q, want %q", p.name, got, p.want)
		}
	}

	// The backend's detector, the same way.
	wholeStore := []backendFence{{pkg: "storage"}}
	backendProbes := []struct {
		name, src string
		fences    []backendFence
		want      string
	}{
		{"the stream's event named",
			`package p; import "github.com/rfizzle/shhh/internal/provider"; var _ provider.StreamEvent`, behindBackend, "provider.StreamEvent"},
		{"the rest of the package is the screen's",
			`package p; import "github.com/rfizzle/shhh/internal/provider"; var _ provider.Message`, behindBackend, ""},
		{"a renamed import is still the package",
			`package p; import pv "github.com/rfizzle/shhh/internal/provider"; var _ []pv.StreamEvent`, behindBackend, "provider.StreamEvent"},
		{"a field of the same name is not the package",
			`package p; import "github.com/rfizzle/shhh/internal/provider"; var m struct{ provider struct{ StreamEvent int } }; var _, _ = m.provider.StreamEvent, provider.Message{}`, behindBackend, ""},
		{"a whole package fenced is refused at the import",
			`package p; import "github.com/rfizzle/shhh/internal/storage"; var _ *storage.Store`, wholeStore, "storage"},
		{"a whole package fenced is not refused where it is not imported",
			`package p; import "github.com/rfizzle/shhh/internal/provider"; var _ provider.StreamEvent`, wholeStore, ""},
	}
	for _, p := range backendProbes {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p.name+".go", p.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		if got := strings.Join(aroundBackend(t, f, p.fences), ","); got != p.want {
			t.Errorf("%s: flagged %q, want %q", p.name, got, p.want)
		}
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	read := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		read++
		for _, pkg := range namesOnly(t, f) {
			t.Errorf("%s imports %s only for a tool's name; ask the receipt instead", name, pkg)
		}
		if slices.Contains(backendFiles, name) {
			continue
		}
		for _, what := range aroundBackend(t, f, behindBackend) {
			t.Errorf("%s reaches %s around the backend; ask the backend instead", name, what)
		}
	}
	if read == 0 {
		t.Fatal("the fence read no files")
	}
}
