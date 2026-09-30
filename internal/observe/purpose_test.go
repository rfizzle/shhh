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

// The archive, compression and database programs and python each have a
// form that only shows what a file holds, which a built-in reader answers,
// and forms that change something, which keep the word they always had.
func TestCommandPurpose_TheDataProgramsReadOnlyInTheirReadingForms(t *testing.T) {
	for _, c := range []struct {
		line, want string
	}{
		{"tar tzf release.tgz", PurposeList},
		{"tar -tvf release.tar", PurposeList},
		{"tar --list -f release.tar", PurposeList},
		{"tar tzf release.tgz | grep src/", PurposeSearch},
		{"tar xzf release.tgz", PurposeWrite},
		{"tar -czf out.tgz src", PurposeWrite},
		{"tar -xf a.tar -C dest", PurposeWrite},
		{"unzip -l release.zip", PurposeList},
		{"unzip -Z1 release.zip", PurposeList},
		{"unzip -p release.zip docs/README.md", PurposeRead},
		{"unzip release.zip", PurposeWrite},
		{"unzip -o release.zip -d out", PurposeWrite},
		{"zcat app.log.gz | grep ERROR", PurposeSearch},
		{"gzcat app.log.gz | tail -5", PurposeRead},
		{"gzip -dc app.log.gz | tail -5", PurposeRead},
		{"gzip -d -c app.log.gz", PurposeRead},
		{"gzip -l app.log.gz", PurposeRead},
		{"gunzip -c app.log.gz", PurposeRead},
		{"gzip app.log", PurposeWrite},
		{"gzip -d app.log.gz", PurposeWrite},
		{"gunzip app.log.gz", PurposeWrite},
		{"gzip -c app.log > app.log.gz", PurposeWrite},
		{`sqlite3 app.db "SELECT count(*) FROM orders"`, PurposeRead},
		{"sqlite3 -header -column app.db 'select name from users limit 5'", PurposeRead},
		{"sqlite3 app.db .schema", PurposeRead},
		{"sqlite3 app.db '.tables'", PurposeRead},
		{`sqlite3 app.db "DELETE FROM orders"`, PurposeOther},
		{`sqlite3 app.db "SELECT 1; DROP TABLE orders"`, PurposeOther},
		{`sqlite3 app.db ".output dump.sql"`, PurposeOther},
		{`sqlite3 app.db "PRAGMA journal_mode=delete"`, PurposeOther},
		{"sqlite3 -cmd '.read x.sql' app.db 'SELECT 1'", PurposeOther},
		{"sqlite3 app.db", PurposeOther},
		{"sqlite3 app.db < migrate.sql", PurposeOther},
		{`python3 -c 'import json; print(json.load(open("package.json"))["version"])'`, PurposeRead},
		{`cat package.json | python -c 'import json,sys; print(json.load(sys.stdin)["name"])'`, PurposeRead},
		{`python3 -c 'import json; d=json.load(open("a.json")); open("b.json","w").write(str(d))'`, PurposeOther},
		{`python3 -c 'import json; json.dump({}, open("a.json", "w"))'`, PurposeOther},
		{`python3 -c 'import json,os; os.remove("a.json")'`, PurposeOther},
		{`python3 -c 'import csv; print(1)'`, PurposeOther},
		{"python3 script.py", PurposeOther},
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
