package chat

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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

// TestScreenImportFence reads every non-test file of the package and fails
// on one that imports a fenced package only for a tool's name. A row asks
// the receipt what a call was rather than comparing names, so a file that
// needs a tool's name has found a question the receipt should answer.
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
	}
	if read == 0 {
		t.Fatal("the fence read no files")
	}
}
