package run

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/todo"
)

// gitRepo is a repository with one commit already in it, so the index this
// tests against is the one a run would find.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on the path")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-q", "-m", "root"},
	} {
		if out, code := Git(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return root
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The commit is the run package's, so both drivers make exactly the same
// one: the run's paths staged by name, the message written as it stands, and
// nothing else in the tree carried along.
func TestCommit_StagesTheRunsPathsAndNothingElse(t *testing.T) {
	root := gitRepo(t)
	write(t, root, "a.go", "package a\n")
	write(t, root, "b.go", "package b\n")
	write(t, root, "stranger.go", "package stranger\n")

	files, err := Commit(root, []string{"a.go", "b.go"}, "feat(a): do the thing\n\nBecause.", "ask for it without one", true, Secrets{})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if strings.Join(files, ",") != "a.go,b.go" {
		t.Fatalf("committed %v", files)
	}
	out, code := Git(root, "show", "--name-only", "--format=%s%n%n%b", "HEAD")
	if code != 0 {
		t.Fatalf("git show: %s", out)
	}
	if !strings.Contains(out, "feat(a): do the thing") || !strings.Contains(out, "Because.") {
		t.Errorf("the message was not written as it stands:\n%s", out)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
		t.Errorf("the run's paths are not in the commit:\n%s", out)
	}
	if strings.Contains(out, "stranger.go") {
		t.Errorf("a file the run did not change rode along:\n%s", out)
	}
}

// A commit that would carry a stranger is refused instead: one that cannot
// be reverted, cited or read as a unit is worse than none.
func TestCommit_RefusesAnIndexItDidNotFill(t *testing.T) {
	root := gitRepo(t)
	write(t, root, "a.go", "package a\n")
	write(t, root, "theirs.go", "package theirs\n")
	if out, code := Git(root, "add", "--", "theirs.go"); code != 0 {
		t.Fatalf("git add: %s", out)
	}
	_, err := Commit(root, []string{"a.go"}, "subject", "ask for it without one", true, Secrets{})
	if err == nil || !strings.Contains(err.Error(), "already holds staged changes") {
		t.Fatalf("err = %v", err)
	}
}

// Outside a repository the refusal says so and offers the way through,
// which is the archive finish under whatever name the surface gives it.
func TestCommit_OutsideARepositorySaysSoAndOffersTheWayThrough(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "package a\n")
	_, err := Commit(root, []string{"a.go"}, "subject", "--no-commit runs it without one", true, Secrets{})
	if err == nil || !strings.Contains(err.Error(), "not a git repository") ||
		!strings.Contains(err.Error(), "--no-commit runs it without one") {
		t.Fatalf("err = %v", err)
	}
}

// A run that changed nothing has nothing to commit, and says that rather
// than making an empty one.
func TestCommit_RefusesAnEmptyRun(t *testing.T) {
	if _, err := Commit(t.TempDir(), nil, "subject", "ask", true, Secrets{}); err == nil ||
		!strings.Contains(err.Error(), "changed no files") {
		t.Fatalf("err = %v", err)
	}
}

// Every commit a run, a session's card or the unattended runner makes goes
// through Commit, so the refusal is here: a credential shape in a line the
// commit adds is refused by kind, file and line before anything is staged,
// and the value is in neither the error nor the index. A fixture the ignore
// list names is committed, and so is a finding the person said yes to.
func TestCommit_ASecretIsRefusedOnEveryPath(t *testing.T) {
	const token = "ghp_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"
	body := strings.Repeat("# line\n", 11) + "GITHUB_TOKEN=" + token + "\n"

	root := gitRepo(t)
	write(t, root, "config/dev.env", body)
	_, err := Commit(root, []string{"config/dev.env"}, "subject", "ask", true, Secrets{})
	var refused *structural.SecretRefusal
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want the secret refusal", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "github token at config/dev.env:12") || strings.Contains(msg, token[:10]) {
		t.Errorf("the refusal names the kind, file and line and never the value: %q", msg)
	}
	if out, _ := Git(root, "diff", "--cached", "--name-only"); out != "" {
		t.Errorf("a refused commit staged %q", out)
	}

	// The same file under a glob the checkout's list names is a fixture.
	if _, err := Commit(root, []string{"config/dev.env"}, "subject", "ask", true, Secrets{Ignore: []string{"config/*.env"}}); err != nil {
		t.Fatalf("an ignored fixture was refused: %v", err)
	}

	// And a finding the person said yes to on the card is theirs to commit.
	root = gitRepo(t)
	write(t, root, "config/dev.env", body)
	if _, err := Commit(root, []string{"config/dev.env"}, "subject", "ask", true, Secrets{Allow: true}); err != nil {
		t.Fatalf("the override was refused: %v", err)
	}

	// A secret already in history is not this commit's to refuse: only the
	// lines it adds are read.
	write(t, root, "config/dev.env", body+"OTHER=1\n")
	if _, err := Commit(root, []string{"config/dev.env"}, "subject", "ask", true, Secrets{}); err != nil {
		t.Fatalf("a line already committed was read as added: %v", err)
	}
}

func TestSecretIgnored_ReadsGlobsTheWayAnIgnoreFileDoes(t *testing.T) {
	for _, c := range []struct {
		globs []string
		path  string
		want  bool
	}{
		{[]string{"testdata"}, "internal/secret/testdata/key.pem", true},
		{[]string{"*.fixture"}, "a/b/c.fixture", true},
		{[]string{"internal/secret/testdata"}, "internal/secret/testdata/key.pem", true},
		{[]string{"config/*.env"}, "config/dev.env", true},
		{[]string{"config/*.env"}, "other/config/dev.env", false},
		{[]string{"testdata"}, "internal/secret/patterns.go", false},
		{nil, "config/dev.env", false},
	} {
		if got := structural.SecretIgnored(c.globs, c.path); got != c.want {
			t.Errorf("SecretIgnored(%v, %q) = %v, want %v", c.globs, c.path, got, c.want)
		}
	}
}

func TestSourcesSection_TheReadAndTheOnlyCited(t *testing.T) {
	block := SourcesSection([]Source{
		{URL: "https://go.dev/doc/go1.24", Title: "Go 1.24 release notes", Read: true},
		{URL: "https://docs.rs/tokio/", Read: true},
		{URL: "https://example.com/invented", Title: "Nobody opened this"},
	})
	for _, want := range []string{
		"## Sources",
		"- https://go.dev/doc/go1.24 — Go 1.24 release notes",
		"- https://docs.rs/tokio/\n",
		"Cited, not read:",
		"- https://example.com/invented — Nobody opened this",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block is missing %q:\n%s", want, block)
		}
	}
	if strings.Index(block, "Cited, not read:") < strings.Index(block, "https://docs.rs/tokio/") {
		t.Error("what was cited and never read is listed above what was read")
	}
	if SourcesSection(nil) != "" {
		t.Error("a run that read nothing still wrote a sources block")
	}
}

// The block is built from what the session fetched and never from the
// write-up's own prose, so it is there whether or not the turn wrote one.
func TestFileNote_TheWriteUpCarriesTheSourcesItWasHanded(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".shhh/todo/q-1.md", "---\nstatus: doing\n---\n\n# A question\n")
	it := todo.Item{Path: filepath.Join(root, ".shhh/todo/q-1.md"), Slug: "q-1", Title: "A question"}
	s := &State{Slug: "q-1", Report: "## Report\nThe answer is yes.", NoCommit: true,
		Sources: []Source{{URL: "https://go.dev/doc", Title: "The docs", Read: true}}}

	var noted string
	if _, err := FileNote(root, s, it, func(author, title, body string) (string, error) {
		noted = body
		return "n1", nil
	}); err != nil {
		t.Fatalf("FileNote: %v", err)
	}
	if !strings.Contains(noted, "## Sources") || !strings.Contains(noted, "https://go.dev/doc") {
		t.Errorf("the note carries no sources block:\n%s", noted)
	}
	archived, err := os.ReadFile(filepath.Join(root, ".shhh/todo/done/q-1.md"))
	if err != nil {
		t.Fatalf("read the archived item: %v", err)
	}
	if !strings.Contains(string(archived), "https://go.dev/doc") {
		t.Errorf("the archived item carries no sources block:\n%s", archived)
	}
}

// The project's trailers are shhh's to append: once, after a blank line,
// and never a second time when the message already ends with the line.
func TestCommit_TrailersAreAppendedOnce(t *testing.T) {
	trailer := "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
	for _, tc := range []struct{ name, message, want string }{
		{"subject only", "feat(a): do the thing", "feat(a): do the thing\n\n" + trailer},
		{"subject and body", "feat(a): do the thing\n\nBecause.", "feat(a): do the thing\n\nBecause.\n\n" + trailer},
		{"already ends with it", "feat(a): do the thing\n\n" + trailer, "feat(a): do the thing\n\n" + trailer},
		{"another trailer above", "feat(a): x\n\nSigned-off-by: T <t@example.com>", "feat(a): x\n\nSigned-off-by: T <t@example.com>\n" + trailer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WithTrailers(tc.message, []string{trailer}); got != tc.want {
				t.Errorf("WithTrailers = %q, want %q", got, tc.want)
			}
		})
	}
	if got := WithTrailers("feat(a): x", nil); got != "feat(a): x" {
		t.Errorf("no trailers should leave the message, got %q", got)
	}

	root := gitRepo(t)
	write(t, root, "a.go", "package a\n")
	secrets := Secrets{Trailers: []string{trailer}}
	// The model wrote the line itself; the commit still carries it once.
	if _, err := Commit(root, []string{"a.go"}, "feat(a): do the thing\n\n"+trailer, "x", true, secrets); err != nil {
		t.Fatalf("commit: %v", err)
	}
	out, code := Git(root, "log", "-1", "--format=%B")
	if code != 0 {
		t.Fatalf("git log: %s", out)
	}
	if n := strings.Count(out, trailer); n != 1 {
		t.Errorf("the trailer should be on the commit once, found %d:\n%s", n, out)
	}
	write(t, root, "b.go", "package b\n")
	if _, err := Commit(root, []string{"b.go"}, "feat(b): another", "x", true, secrets); err != nil {
		t.Fatalf("commit: %v", err)
	}
	out, _ = Git(root, "log", "-1", "--format=%B")
	if !strings.Contains(out, "feat(b): another\n\n"+trailer) {
		t.Errorf("the trailer should follow a blank line:\n%s", out)
	}
}
