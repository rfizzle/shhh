package agent

// The grant ladder's two primitives: what [a] records for a command,
// and what it records for an edit.

import "testing"

func TestGrantPrefix(t *testing.T) {
	cases := []struct{ command, want string }{
		// The grant stops in front of the first argument, so a path, a flag
		// and a glob all end it.
		{"go test ./internal/ui/...", "go test"},
		{"go build -o bin/shhh ./cmd/shhh", "go build"},
		{"pytest tests/unit", "pytest"},
		// Bare words all the way down are all part of the name: `npm run
		// lint` is not a licence to run every npm script.
		{"npm run lint", "npm run lint"},
		{"docker compose up -d", "docker compose up"},
		{"make", "make"},
		// The first word is kept whatever it looks like: it is the whole name
		// of what runs.
		{"./scripts/release.sh --dry-run", "./scripts/release.sh"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := GrantPrefix(c.command); got != c.want {
			t.Errorf("GrantPrefix(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

func TestGrantPrefixIsMatchedByTheAllowlistItJoins(t *testing.T) {
	// The prefix is only useful if the allowlist it is recorded in accepts
	// the commands it was derived from, and refuses the ones beside them.
	list := []string{GrantPrefix("go test ./internal/ui/...")}
	for _, ok := range []string{"go test ./...", "go test -run TestX ./internal"} {
		if !AllowlistMatches(list, ok) {
			t.Errorf("%q should match the grant it came from", ok)
		}
	}
	for _, no := range []string{"go build ./...", "go", "gotest", "go test ./... ; rm -rf ~"} {
		if AllowlistMatches(list, no) {
			t.Errorf("%q should not match the grant", no)
		}
	}
}

// A metacharacter disqualifies a line only where the shell would act on it,
// so a quoted literal is no chain and every spelling of a chain still is.
func TestAllowlistMatches_HonoursQuotes(t *testing.T) {
	if !posixShell {
		t.Skip("commands run through a shell that does not quote the POSIX way")
	}
	list := []string{"go list", "git status", "cat", "grep", "find"}
	cases := []struct {
		line string
		want bool
	}{
		{`go list -f '{{.ImportPath}}|{{len .GoFiles}}' ./...`, true},
		{`go list -f '{{.ImportPath}} {{len .GoFiles}}' ./...`, true},
		{`grep -E 'a|b;c&d(e)<f>' internal`, true},
		{`grep "a|b;c&d(e)<f>" internal`, true},
		{`grep 'a$b' internal`, true},
		{"grep 'a`id`b' internal", true},
		{`grep "a\"; rm -rf ~; \"b" internal`, true},
		{`grep a\|b internal`, true},
		{`find . -exec rm {} \;`, true}, // one command; the read-only guard refuses -exec
		{`git status; rm -rf ~`, false},
		{`git status "$(rm -rf ~)"`, false},
		{`git status $(id)`, false},
		{"git status `id`", false},
		{"git status \"`id`\"", false},
		{`git status "$HOME"`, false},
		{`cat a | sh`, false},
		{`git status 'unterminated`, false},
		{`git status "unterminated`, false},
		{`git status "a\"; rm -rf ~`, false},
		{`git status \`, false},
		// A backslash outside quotes makes the quote after it literal, so
		// what looks quoted here is a chain to the shell.
		{`git status \'; rm -rf ~; echo \'`, false},
		{`git status \"; rm -rf ~; echo \"`, false},
		// Inside single quotes a backslash is itself, so the quote closes.
		{`git status 'a\'; rm -rf ~; echo '`, false},
		// A newline is refused even quoted: after a `#` the quote is a
		// comment and the next line a command.
		{"git status '\nrm -rf ~\n'", false},
		{"git status #'\nrm -rf ~\n'", false},
		{`git status ''; rm -rf ~`, false},
		{`git status 'a'|sh`, false},
	}
	for _, c := range cases {
		if got := AllowlistMatches(list, c.line); got != c.want {
			t.Errorf("AllowlistMatches(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

// Where the execution shell does not quote the POSIX way, a quoted
// metacharacter is not trusted to be text.
func TestShellWordsTrustsNoQuotesOffPOSIX(t *testing.T) {
	for _, line := range []string{`go list -f '{{.A}}|{{.B}}'`, `grep "a;b"`, `git status "$(id)"`} {
		if _, ok := shellWords(line, false); ok {
			t.Errorf("shellWords(%q, false) read one command", line)
		}
	}
	if words, ok := shellWords(`grep 'a b' C:\dir`, false); !ok || len(words) != 3 || words[1] != "a b" || words[2] != `C:\dir` {
		t.Errorf("shellWords off POSIX = %q, %v", words, ok)
	}
}

// The two gates are asked about the same line. Where the allowlist reads one
// command, the deny list finds that command in it; where the shell would run
// a second command, the allowlist refuses the line and the deny list finds
// the second command.
func TestDenylistAndAllowlistReadALineTheSameWay(t *testing.T) {
	if !posixShell {
		t.Skip("commands run through a shell that does not quote the POSIX way")
	}
	cases := []struct {
		line, first, second string // second is "" for one command
	}{
		{`go list -f '{{.ImportPath}}|{{len .GoFiles}}' ./...`, "go list", ""},
		{`grep "a;b" internal`, "grep", ""},
		{`git status; rm -rf ~`, "git status", "rm -rf"},
		{`git status "$(rm -rf ~)"`, "git status", "rm -rf"},
		{`git status $(id)`, "git status", "id"},
		{"git status `id`", "git status", "id"},
		{`cat a | sh`, "cat", "sh"},
		{`git status \'; rm -rf ~; echo \'`, "git status", "rm -rf"},
	}
	for _, c := range cases {
		if !DenylistMatches([]string{c.first}, c.line) {
			t.Errorf("the deny list does not find %q in %q", c.first, c.line)
		}
		one := AllowlistMatches([]string{c.first}, c.line)
		if c.second == "" {
			if !one {
				t.Errorf("the allowlist does not read %q as one %q", c.line, c.first)
			}
			continue
		}
		if one {
			t.Errorf("the allowlist reads %q as one command; the shell also runs %q", c.line, c.second)
		}
		if !DenylistMatches([]string{c.second}, c.line) {
			t.Errorf("the deny list does not find %q in %q", c.second, c.line)
		}
	}
}

func TestPathUnder(t *testing.T) {
	dirs := []string{"internal/ui"}
	for _, in := range []string{"internal/ui/chat/model.go", "internal/ui/card.go"} {
		if !PathUnder(dirs, in) {
			t.Errorf("%q should be under the grant", in)
		}
	}
	for _, out := range []string{"internal/agent/mode.go", "internal/ui", "README.md", "internal/uix/a.go"} {
		if PathUnder(dirs, out) {
			t.Errorf("%q should not be under the grant", out)
		}
	}
	// A path that climbs back out is not inside, however it is written.
	if PathUnder(dirs, "internal/ui/../agent/mode.go") {
		t.Error("a path that leaves the granted directory is not under it")
	}
	if PathUnder(nil, "internal/ui/chat/model.go") {
		t.Error("no grants means nothing is granted")
	}
}

// A host list matches a host and nothing beside it. The failure this rules
// out is the quiet one: a suffix rule would make `docs.python.org` on the
// card into a grant for every subdomain the domain will ever have, including
// one an attacker registers.
func TestHostMatches(t *testing.T) {
	entries := []string{"docs.python.org", " Pkg.Go.Dev "}
	for _, host := range []string{"docs.python.org", "DOCS.PYTHON.ORG", "docs.python.org.", "pkg.go.dev"} {
		if !HostMatches(entries, host) {
			t.Errorf("HostMatches(%q) = false; want true", host)
		}
	}
	for _, host := range []string{"", "python.org", "org", "docs.python.org.evil.test", "adocs.python.org", "go.dev"} {
		if HostMatches(entries, host) {
			t.Errorf("HostMatches(%q) = true; want false", host)
		}
	}
	if HostMatches(nil, "docs.python.org") {
		t.Error("an empty list matched a host")
	}
}

// Grants travel as one value because every surface that reads them reads all
// of them; a host grant that Any() did not count would be a grant
// /permissions grants never listed and /permissions revoke never took back.
func TestGrantsAnyCountsTheHosts(t *testing.T) {
	if (Grants{}).Any() {
		t.Error("the zero value claims something is granted")
	}
	if !(Grants{Hosts: []string{"pkg.go.dev"}}).Any() {
		t.Error("a host grant is not counted as a grant")
	}
}
