package observe

import (
	"testing"
)

// Each line is one the record has to file under the right word. The first
// block is the shapes the model actually reaches the shell for — a file's
// size and head, a heading outline, the highest id across files, the end of a
// file, an append — and the rest are the spellings that would put a command
// under the wrong word if the line were read as text rather than as what it
// runs.
func TestCommandPurpose(t *testing.T) {
	for _, c := range []struct {
		line, want string
	}{
		{"wc -c docs/big.md && head -c 400 docs/big.md", PurposeRead},
		{"grep -nE '^#+ ' docs/capabilities/coding-agent.md", PurposeSearch},
		{"grep -oh 'S-[0-9]+' a.md b.md c.md | sort -n | tail -1", PurposeSearch},
		{"tail -40 internal/cli/observe.go", PurposeRead},
		{"cat >> notes.md <<'EOF'\n## heading\nmake the thing | grep it\nEOF", PurposeWrite},
		{"ls -la internal/", PurposeList},
		{"find . -name '*.go' -newer go.mod", PurposeList},
		{"sed -n '1,80p' main.go", PurposeRead},
		{"sed -i '' 's/a/b/' main.go", PurposeEdit},
		{"perl -pi -e 's/a/b/' main.go", PurposeEdit},
		{"go test ./internal/observe 2>&1 | tail -20", PurposeBuild},
		{"cd internal && go build ./...", PurposeBuild},
		{"make test", PurposeBuild},
		{"git log --oneline -5", PurposeVCS},
		{"git commit -m \"$(cat <<'EOF'\nmake it so\nEOF\n)\"", PurposeVCS},
		{"echo hello > out.txt", PurposeWrite},
		{"ls missing 2>/dev/null || echo none", PurposeList},
		{"mkdir -p build/out", PurposeWrite},
		{"./scripts/run.sh | tail", PurposeOther},
		{"curl -s https://example.com | jq .", PurposeOther},
		{"python3 -c 'import json; print(1)'", PurposeOther},
		{"echo done", PurposeOther},
		{"", PurposeOther},
		// A quoted alternation is one pattern, not a pipe into a program.
		{"grep -E 'make|build' Makefile", PurposeSearch},
		// Behind an escalation the real command is somewhere after its
		// options, and it is the one filed.
		{"sudo -u root rm -rf /tmp/x", PurposeWrite},
		{"env GOFLAGS=-p=2 go vet ./...", PurposeBuild},
		{"timeout 30 go test ./...", PurposeBuild},
		// A shell's -c is the line it runs.
		{`bash -lc "cd /tmp && git status"`, PurposeVCS},
		{"bash script.sh", PurposeOther},
		{"for f in *.go; do wc -l \"$f\"; done", PurposeRead},
		{"if grep -q foo main.go; then echo yes; fi", PurposeSearch},
		// A find that acts on what it found is the act, not the listing.
		{"find . -name '*.go' -exec grep -l foo {} \\;", PurposeSearch},
		{"find . -name '*.tmp' -exec rm {} +", PurposeWrite},
		{"find . -name '*.tmp' -delete", PurposeWrite},
		{"cat $FILE | head", PurposeRead},
		{"while read -r l; do echo \"$l\"; done < list.txt", PurposeOther},
		{"diff <(sort a) <(sort b)", PurposeRead},
	} {
		if got := CommandPurpose(c.line); got != c.want {
			t.Errorf("CommandPurpose(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

// The word is always one of the set: a line is content, and whatever it
// holds must come out as a code.
func TestCommandPurpose_IsAlwaysAWordOfTheSet(t *testing.T) {
	set := map[string]bool{}
	for _, p := range Purposes() {
		set[p] = true
	}
	for _, line := range []string{
		"cat /home/someone/.ssh/id_rsa", "rm -rf ~", "'unterminated", "\"", ";;;", "| |",
		"$(", "<<EOF", "a=b", "sudo", "--flag", "x\ny\nz",
	} {
		if got := CommandPurpose(line); !set[got] {
			t.Errorf("CommandPurpose(%q) = %q, not a word of the set", line, got)
		}
	}
}

func TestToolPurpose(t *testing.T) {
	for _, c := range []struct {
		tool, args, want string
	}{
		{"execute_command", `{"command":"git status"}`, PurposeVCS},
		{"execute_command", `not json`, PurposeOther},
		{"read_file", `{"path":"x"}`, ""},
		{"search", `{"pattern":"x"}`, ""},
	} {
		if got := ToolPurpose(c.tool, c.args); got != c.want {
			t.Errorf("ToolPurpose(%q, %q) = %q, want %q", c.tool, c.args, got, c.want)
		}
	}
}
