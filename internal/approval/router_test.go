package approval

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// Both doors build the Call a gated call is read as from its classification,
// through CallOf, and neither fills one in by hand: a field added to Call is
// then filled at both doors or at neither, and never at the one its author
// was looking at.
//
// The one literal left is the one-shot's: a command shhh proposed to a
// person, which is not a tool call and has no classification to build from.
func TestApprovalCall_IsBuiltOnce(t *testing.T) {
	startOf := func(name string, args json.RawMessage) (string, bool, bool, error) {
		if name != "process" {
			return "", false, false, nil
		}
		var a struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(args, &a)
		return a.Command, a.Command != "", true, nil
	}
	holds := agent.Answers{Has: func(string) bool { return true }, Command: startOf}
	for _, c := range []struct {
		name string
		tool string
		args string
		host string
		want Call
	}{
		{name: "a command", tool: tools.ExecCommandName, args: `{"command":"npm publish"}`,
			want: Call{Command: "npm publish", InDir: true, Runs: true}},
		{name: "a process start", tool: "process", args: `{"command":"npm publish"}`,
			want: Call{Command: "npm publish", Runs: true}},
		{name: "a git write", tool: structural.GitWriteToolName, args: `{"verb":"commit","message":"feat: x"}`,
			want: Call{Command: "git commit", Write: true}},
		{name: "a file edit", tool: tools.WriteFileName, args: `{"path":"a.go","content":"x"}`,
			want: Call{Write: true}},
		{name: "a fetch", tool: web.FetchToolName, args: `{"url":"https://evil.test/page"}`, host: "evil.test",
			want: Call{Host: "evil.test"}},
		{name: "a server's tool", tool: "github__create_issue", args: `{}`, want: Call{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			classified, err := agent.ClassifyCall(c.tool, json.RawMessage(c.args), holds)
			if err != nil {
				t.Fatal(err)
			}
			classified.Action.Host = c.host
			if got := CallOf(c.tool, classified); got != c.want {
				t.Fatalf("CallOf = %+v, want %+v", got, c.want)
			}
		})
	}

	// The two doors' packages, named from this one's directory (a test runs
	// in its package's own) and read one level deep, so a testdata
	// directory or a worktree checked out somewhere beneath is never read.
	allowed := map[string]bool{filepath.Join("..", "cli", "cmd.go"): true}
	for _, dir := range []string{filepath.Join("..", "cli"), filepath.Join("..", "ui", "chat")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || len(lit.Elts) == 0 {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Call" {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "approval" && !allowed[path] {
					t.Errorf("%s fills in an approval.Call by hand; build it with approval.CallOf from the call's classification", path)
				}
				return true
			})
		}
	}
}
