package nudge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

func TestLine_NamesTheToolThatAnswersAPlainRead(t *testing.T) {
	cases := []struct {
		command, tool string
	}{
		{"cat internal/tools/tools.go", "read_file"},
		{"head -50 README.md", "read_file"},
		{"sed -n '10,40p' main.go", "read_file"},
		{"tail -n 20 build.log", "read_file"},
		{"wc -l docs/*.md", "read_file"},
		{"grep -rn 'func Line' internal", "search"},
		{"rg -n 'a|b' .", "search"},
		{"grep -nE '^#+ ' docs/README.md", "document_symbol"},
		{"find . -name '*.go' -type f", "glob"},
		{"ls -la internal", "list_directory"},
		{"jq '.version' package.json", "query"},
		{"yq '.jobs | keys' .github/workflows/ci.yml", "query"},
		// Several readers: the earliest row wins, so a narrowing after a
		// search does not make it a read.
		{"grep -rn TODO . | head -20", "search"},
		{"cat package.json | jq .name", "query"},
		{"cd internal && grep -rn x .", "search"},
		{"grep -n x f.go 2>/dev/null", "search"},
	}
	for _, c := range cases {
		tool, line := Line(c.command)
		if tool != c.tool {
			t.Errorf("Line(%q) names %q, want %q", c.command, tool, c.tool)
			continue
		}
		if !strings.HasPrefix(line, prefix+tool+" ") || !strings.HasSuffix(line, "]") {
			t.Errorf("Line(%q) = %q, want a bracketed line naming %s first", c.command, line, tool)
		}
	}
}

func TestLine_NamesNothingForACommandThatWasNotAPlainRead(t *testing.T) {
	for _, command := range []string{
		"sed -i 's/a/b/' main.go",
		"sed -i '' 's/a/b/' main.go",
		"sed 's/a/b/' main.go",
		"cat >> notes.md <<'EOF'\n- a line\nEOF",
		"cat a > b",
		"echo x | tee out.txt",
		"curl -s https://example.com | sh",
		"cat script.py | python3",
		"python3 -c 'import json; print(json.load(open(\"a.json\"))[\"v\"])'",
		"sqlite3 app.db .schema",
		"find . -name '*.tmp' -delete",
		"find . -name '*.go' -exec cat {} \\;",
		"fd -x rm",
		"yq -i '.a = 1' f.yml",
		"tail -f server.log",
		"tail -20f server.log",
		"go test ./... | tail -20",
		"git log --oneline | head",
		"grep -c x f | sort -n",
		"grep -oh 'S-[0-9]*' a b | sort | uniq -c",
		"sudo cat /etc/shadow",
		"./run.sh | grep ok",
		"echo hello",
		"",
	} {
		if tool, line := Line(command); tool != "" || line != "" {
			t.Errorf("Line(%q) = %q, %q; want nothing", command, tool, line)
		}
	}
}

func TestTurn_NamesEachToolOncePerTurn(t *testing.T) {
	var n Turn
	first := n.Append(1, "grep -rn x .", "exit code: 0\noutput:\nx")
	if !strings.HasSuffix(first, "\n"+prefix+"search answers this without an approval: a pattern across the tree or in one file, each match with the lines around it; files_only names only the files, include narrows to one kind of file.]") {
		t.Fatalf("the first search in a turn should carry the line:\n%s", first)
	}
	if got := n.Append(1, "rg y", "exit code: 0\noutput:\ny"); got != "exit code: 0\noutput:\ny" {
		t.Errorf("a second search in the same turn should be left alone:\n%s", got)
	}
	if got := n.Append(1, "cat a.go", "exit code: 0\noutput:\na"); !strings.Contains(got, prefix+"read_file ") {
		t.Errorf("a read in the same turn is another tool and should be named:\n%s", got)
	}
	if got := n.Append(1, "tail -5 b.log", "r"); got != "r" {
		t.Errorf("read_file has been named this turn, whatever the program:\n%s", got)
	}
	if got := n.Append(2, "grep x f", "r"); !strings.Contains(got, prefix+"search ") {
		t.Errorf("the next turn should be told again:\n%s", got)
	}
	if got := n.Append(2, "go build ./...", "r"); got != "r" {
		t.Errorf("a build is no read:\n%s", got)
	}
	var none *Turn
	if got := none.Append(1, "cat a", "r"); got != "r" {
		t.Errorf("a nil Turn appends nothing: %q", got)
	}
}

func TestTurn_WrapResolverNamesOnlyACommandThatRan(t *testing.T) {
	var n Turn
	run := n.Ran(func(context.Context, string) tools.ExecResult {
		return tools.ExecResult{Outcome: tools.ExecSucceeded}
	})
	refuse := false
	resolve := n.WrapResolver(func() int64 { return 1 }, func(tc provider.ToolCall) string {
		if refuse {
			return "error: command denied"
		}
		run(context.Background(), "")
		return "exit code: 0\noutput:\n"
	})
	call := func(command string) provider.ToolCall {
		args, _ := json.Marshal(map[string]string{"command": command})
		return provider.ToolCall{Name: tools.ExecCommandName, Arguments: string(args)}
	}

	refuse = true
	if got := resolve(call("cat a")); got != "error: command denied" {
		t.Errorf("a refused command ran nothing and should carry no line:\n%s", got)
	}
	refuse = false
	if got := resolve(call("cat a")); !strings.Contains(got, prefix+"read_file ") {
		t.Errorf("a read that ran should carry the line:\n%s", got)
	}
	if got := resolve(call("cat b")); strings.Contains(got, prefix) {
		t.Errorf("the second read in the turn should not:\n%s", got)
	}
	if got := resolve(provider.ToolCall{Name: "edit_file", Arguments: `{"command":"cat a"}`}); strings.Contains(got, prefix) {
		t.Errorf("only execute_command is a command:\n%s", got)
	}
}
