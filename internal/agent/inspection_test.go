package agent

import (
	"slices"
	"testing"

	"github.com/rfizzle/shhh/internal/safety"
)

// writingForms is, for each built-in entry that has a form which writes or
// runs a program, the invocations that are that form. Every one is refused.
var writingForms = map[string][]string{
	"find":     {"find . -delete", "find . -exec rm {} ;", "find . -fprint out.txt", "find . '-ok' rm {} ;"},
	"fd":       {"fd -x rm", "fd -Hx rm", "fd --exec-batch rm"},
	"sort":     {"sort -o out go.mod", "sort -ro out go.mod", "sort -oout go.mod", "sort --output=out go.mod", "sort --compress-program=sh go.mod"},
	"tree":     {"tree -o out.html", "tree -Fo out.html"},
	"rg":       {"rg --pre ./x foo", "rg --pre=./x foo", "rg -z foo", "rg -uz foo", "rg --search-zip foo", "rg --hostname-bin ./x foo"},
	"file":     {"file -C", "file --compile -m x"},
	"uniq":     {"uniq in out", "uniq -c in out"},
	"date":     {"date -s 2020-01-01", "date --set=2020-01-01", "date 010100002020", "date -u -s x"},
	"hostname": {"hostname newname", "hostname -F /etc/hostname", "hostname -b x"},
	"env":      {"env FOO=bar sh", "env sh -c id"},
	"git log":  {"git log --output=out", "git log --output out", "git log --outp=out"},
	"git diff": {"git diff --output=out", "git diff --ext-diff", "git diff --ext"},
	"git show": {"git show --output=out HEAD"},
	"git branch": {
		"git branch topic", "git branch topic HEAD~1", "git branch -D topic", "git branch -m old new",
		"git branch -vD topic", "git branch --set-upstream-to=origin/x", "git branch -c a b", "git branch --edit-description",
		"git branch --contains HEAD topic", "git branch -f topic",
	},
	"git remote -v":           {"git remote -v add x https://example.com/x.git", "git remote -v remove origin"},
	"git tag -l":              {"git tag -l -d v1", "git tag -ld v1", "git tag -l -a v1 -m x", "git tag -l -f v1", "git tag -l --delete v1"},
	"git stash list":          {"git stash list --output=out"},
	"go list":                 {"go list -toolexec=/tmp/x ./...", "go list -exec /tmp/x ./...", "go list -mod=mod ./...", "go list -compiled ./...", "go list -export ./...", "go list -modfile=x ./...", "go list '-toolexec' x ./..."},
	"go doc":                  {"go doc -http fmt", "go doc --http fmt"},
	"go mod graph":            {"go mod graph -modfile=/tmp/x"},
	"go mod why":              {"go mod why -modfile=/tmp/x x"},
	"go env":                  {"go env -w GOFLAGS=-x", "go env -u GOFLAGS"},
	"gofmt -l":                {"gofmt -l -w .", "gofmt -l -cpuprofile=x ."},
	"go vet":                  {"go vet -vettool=/tmp/x ./...", "go vet -fix ./..."},
	"go build -n":             {"go build -n -o out ./...", "go build -n -n=false ./..."},
	"node --version":          {"node --version -e x", "node --version x.js"},
	"python --version":        {"python --version -c x"},
	"python3 --version":       {"python3 --version -c x"},
	"cargo --version":         {"cargo --version install x"},
	"rustc --version":         {"rustc --version x.rs"},
	"golangci-lint --version": {"golangci-lint --version run --fix"},
}

// noWritingForm are the entries whose every invocation reads: a tool that
// prints what it is asked about and has no flag, operand or subcommand that
// writes a file, changes state or runs another program. A new built-in entry
// is in neither table until someone has decided which it is.
var noWritingForm = []string{
	"ls", "pwd", "cat", "head", "tail", "wc", "stat", "du", "realpath", "basename",
	"dirname", "readlink", "grep", "which", "type", "diff", "cmp", "cut", "column",
	"git status", "git blame", "git ls-files", "git describe", "git rev-parse",
	"git shortlog", "go version", "npm ls", "whoami", "printenv", "uname", "id",
}

// Every built-in entry is read-only: bare, it is allowed and the safety table
// has nothing to say about it; and the table above names a writing form for
// each entry that has one, none of which is allowed.
func TestInspection_EveryAllowlistedInvocationIsReadOnly(t *testing.T) {
	for _, entry := range ReadOnlyCommands() {
		if !ReadOnlyAllowed(entry, nil) {
			t.Errorf("%q is on the list and refused bare", entry)
		}
		if w := safety.Check(entry); len(w) != 0 {
			t.Errorf("%q is on the list and the safety reading warns: %v", entry, w)
		}
		_, forms := writingForms[entry]
		if forms == slices.Contains(noWritingForm, entry) {
			t.Errorf("%q must be in exactly one of writingForms and noWritingForm", entry)
		}
	}
	for entry, commands := range writingForms {
		if !slices.Contains(ReadOnlyCommands(), entry) {
			t.Errorf("writingForms names %q, which is not on the list", entry)
		}
		for _, c := range commands {
			if ReadOnlyAllowed(c, nil) {
				t.Errorf("%q writes or runs a program and the list allows it", c)
			}
		}
	}
	for _, entry := range noWritingForm {
		if !slices.Contains(ReadOnlyCommands(), entry) {
			t.Errorf("noWritingForm names %q, which is not on the list", entry)
		}
	}
}

// The Go read verbs are closed by list: a flag nobody listed is refused, so a
// flag Go adds later that runs a program or writes fails closed.
func TestInspection_GoVerbsRefuseAnUnlistedFlag(t *testing.T) {
	if !posixShell {
		t.Skip("commands run through a shell that does not quote the POSIX way")
	}
	for _, c := range []string{
		"go list -m all", "go list -json ./...", "go list -deps -test -e ./...", "go list -f '{{.Dir}}' ./...",
		"go list -f '{{range .Imports}}{{.}} {{end}}' ./...", "go list --json=Dir ./...",
		"go doc -all fmt", "go doc -src -u fmt.Println", "go mod graph", "go mod why -m x",
	} {
		if !ReadOnlyAllowed(c, nil) {
			t.Errorf("%q is a read on the safe list and the list refuses it", c)
		}
	}
	for _, c := range []string{
		"go list -toolexec=/tmp/x ./...",
		"go list -modfile=x ./...",
		"go list -brand-new-flag ./...",
		"go doc -made-up fmt",
		"go mod graph -x",
		"go list -mjson ./...",
		"go list -f '{{call .X}}' ./...",
		"go list -f '{{template \"x\"}}' .",
		"go list -f '{{.Dir' ./...",
		"go list -f",
		"go list -- -toolexec=x",
	} {
		if ReadOnlyAllowed(c, nil) {
			t.Errorf("%q is not on a verb's safe list and the list allows it", c)
		}
	}
}

// The program name is not the invocation: the same program that is allowed to
// read is refused when its arguments write, however the arguments are quoted,
// and the reads beside them still run.
func TestInspection_AnAllowlistedProgramWithWritingArgumentsIsNotAllowlisted(t *testing.T) {
	if !posixShell {
		t.Skip("commands run through a shell that does not quote the POSIX way")
	}
	reads := []string{
		"git branch", "git branch -a", "git branch --list 'feat/*'", "git branch -l feat*", "git branch --contains HEAD",
		"git branch --merged main --sort=-committerdate", "git branch --show-current",
		"git log --oneline -5", "git diff --stat", "git show HEAD", "git tag -l 'v1.*'",
		"uniq go.mod", "uniq -c go.mod", "uniq -f 1 go.mod", "date", "date -u", "date +%s", "date -d yesterday +%F",
		"hostname", "hostname -f", "rg -n foo internal", "rg -i --glob '*.go' foo", "sort -r go.mod", "file go.mod", "file -b go.mod",
		"go list -f '{{.Dir}}' ./...", "go list -m all", "go doc fmt.Println", "node --version", "'git' status",
	}
	for _, c := range reads {
		if !ReadOnlyAllowed(c, nil) {
			t.Errorf("%q reads and the list refuses it", c)
		}
	}
	for _, c := range []string{
		"'git' branch topic", "git 'branch' topic", "git branch \"topic\"", "git branch $'x'",
		"sort '-o' out go.mod", "sort \"--output=out\" go.mod", "date '-s' 2020-01-01",
	} {
		if ReadOnlyAllowed(c, nil) {
			t.Errorf("%q writes and the list allows it", c)
		}
	}
}
