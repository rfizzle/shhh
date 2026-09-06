package structural

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitWriteArgvFor builds one write's argv with its paths already resolved, so
// a test can read what a verb produces without a repository or a spawn.
func gitWriteArgvFor(t *testing.T, ts *Toolset, raw, messageFile string, hooks bool) []string {
	t.Helper()
	var a gitWriteArgs
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	paths, err := ts.resolveGitPaths(a.Paths)
	if err != nil {
		t.Fatalf("resolving paths for %s: %v", raw, err)
	}
	argv, err := buildGitWriteArgv(a, paths, messageFile, hooks)
	if err != nil {
		t.Fatalf("building argv for %s: %v", raw, err)
	}
	return argv
}

// writeRepo is a repository with an identity configured, which is what the
// write tool needs and the reader does not: the tool passes no --author, so
// git's own refusal is what a session with no identity gets.
func writeRepo(t *testing.T) string {
	t.Helper()
	root := newGitRepo(t)
	for _, kv := range [][2]string{{"user.name", "Test"}, {"user.email", "test@example.com"}} {
		cmd := exec.Command("git", "-C", root, "config", kv[0], kv[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git config %s failed (%v): %s", kv[0], err, out)
		}
	}
	return root
}

// writeToolset is a toolset with git and the write tool registered, rooted at
// a repository the test built.
func writeToolset(t *testing.T, root string, w Writes) *Toolset {
	t.Helper()
	ts := &Toolset{root: root, bins: map[string]string{GitToolName: "git"}, timeout: SpawnTimeout}
	ts.AllowWrites(w)
	return ts
}

func TestBuildGitWriteArgvPerVerb(t *testing.T) {
	ts := newTestToolset(t, nil)
	file := filepath.Join(ts.root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		args    string
		msgFile string
		hooks   bool
		want    []string
	}{
		{
			name: "add names its paths after the delimiter",
			args: `{"verb":"add","paths":["main.go"]}`,
			want: []string{"--no-pager", "--no-optional-locks", "add", "--", file},
		},
		{
			name:    "commit reads the message out of a file, hooks on",
			args:    `{"verb":"commit","message":"feat: do it"}`,
			msgFile: "/tmp/msg.txt",
			hooks:   true,
			want:    []string{"--no-pager", "--no-optional-locks", "commit", "--file=/tmp/msg.txt", "--"},
		},
		{
			name:    "commit on an untrusted checkout runs no hooks",
			args:    `{"verb":"commit","message":"feat: do it"}`,
			msgFile: "/tmp/msg.txt",
			want:    []string{"--no-pager", "--no-optional-locks", "commit", "--no-verify", "--file=/tmp/msg.txt", "--"},
		},
		{
			name: "branch creates and takes nothing else",
			args: `{"verb":"branch","branch":"topic"}`,
			want: []string{"--no-pager", "--no-optional-locks", "branch", "--", "topic"},
		},
		{
			name: "switch moves to a branch that exists",
			args: `{"verb":"switch","branch":"master"}`,
			want: []string{"--no-pager", "--no-optional-locks", "switch", "--no-guess", "--", "master"},
		},
		{
			// The name rides attached: after the delimiter it would be git's
			// start-point, and the switch would fail on its own argument.
			name: "switch creates off the current commit",
			args: `{"verb":"switch","branch":"topic","create":true}`,
			want: []string{"--no-pager", "--no-optional-locks", "switch", "--no-guess", "--create=topic", "--"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gitWriteArgvFor(t, ts, c.args, c.msgFile, c.hooks)
			if !slices.Equal(got, c.want) {
				t.Fatalf("argv =\n  %v\nwant\n  %v", got, c.want)
			}
		})
	}
}

// The verb set is the security boundary, so what is not in it has to be
// absent from every argv this builder can produce — not merely refused
// somewhere upstream.
func TestBuildGitWriteArgvExcludesEverythingThatDiscardsOrLeaves(t *testing.T) {
	ts := newTestToolset(t, nil)
	if err := os.WriteFile(filepath.Join(ts.root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	banned := []string{
		"-A", "--all", ".", "--amend", "--force", "-f", "--discard-changes", "--merge",
		"--hard", "--author", "--allow-empty", "-c", "--config-env", "--exec-path",
		"--output", "-m", "-D", "-d", "--delete", "push", "reset", "clean", "checkout",
		"rebase", "stash", "tag",
	}
	for _, args := range []string{
		`{"verb":"add","paths":["main.go"]}`,
		`{"verb":"commit","message":"feat: do it"}`,
		`{"verb":"branch","branch":"topic"}`,
		`{"verb":"switch","branch":"master"}`,
		`{"verb":"switch","branch":"topic","create":true}`,
	} {
		for _, hooks := range []bool{true, false} {
			argv := gitWriteArgvFor(t, ts, args, "/tmp/msg.txt", hooks)
			for _, tok := range argv {
				if slices.Contains(banned, tok) {
					t.Fatalf("%s produced %q, which is not in the vocabulary: %v", args, tok, argv)
				}
			}
		}
	}
}

func TestBuildGitWriteArgvRefusesAVerbOutsideTheSet(t *testing.T) {
	for _, verb := range []string{"push", "reset", "clean", "checkout", "rebase", "merge", "stash", "tag", ""} {
		_, err := buildGitWriteArgv(gitWriteArgs{Verb: verb}, nil, "/tmp/msg.txt", false)
		if err == nil {
			t.Fatalf("verb %q must not build an argv", verb)
		}
	}
}

func TestBuildGitWriteArgvRefusesFieldsAVerbDoesNotTake(t *testing.T) {
	for _, c := range []struct{ args, want string }{
		{`{"verb":"commit","paths":["main.go"],"message":"x"}`, "commit does not take paths"},
		{`{"verb":"add","paths":["main.go"],"message":"x"}`, "add does not take message"},
		{`{"verb":"branch","branch":"topic","create":true}`, "branch does not take create"},
		{`{"verb":"switch","branch":"topic","message":"x"}`, "switch does not take message"},
	} {
		var a gitWriteArgs
		if err := json.Unmarshal([]byte(c.args), &a); err != nil {
			t.Fatal(err)
		}
		_, err := buildGitWriteArgv(a, []string{"/tmp/main.go"}, "/tmp/msg.txt", false)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want %q", c.args, err, c.want)
		}
	}
}

// A branch name is read under the reader's ref charset, so a flag-shaped one
// cannot become an option on its way through.
func TestBuildGitWriteArgvRefusesFlagShapedBranches(t *testing.T) {
	for _, name := range []string{"--force", "-f", "HEAD:../elsewhere", ""} {
		for _, verb := range []string{gitBranch, gitSwitch} {
			_, err := buildGitWriteArgv(gitWriteArgs{Verb: verb, Branch: name}, nil, "", false)
			if err == nil {
				t.Fatalf("%s %q must be refused", verb, name)
			}
		}
	}
}

// The one thing a shell cannot do: staging is decided by the session's own
// record of what it changed, so work that was in the tree when the session
// opened cannot be carried into a commit.
func TestGitWriteStagesOnlyThisSessionsWork(t *testing.T) {
	ts := newTestToolset(t, nil)
	for _, name := range []string{"mine.go", "theirs.go"} {
		if err := os.WriteFile(filepath.Join(ts.root, name), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts.writes = &Writes{Files: func() []string { return []string{"mine.go"} }}

	if err := ts.stageable([]string{"mine.go"}, []string{filepath.Join(ts.root, "mine.go")}); err != nil {
		t.Fatalf("this session's own file must be stageable: %v", err)
	}
	err := ts.stageable([]string{"theirs.go"}, []string{filepath.Join(ts.root, "theirs.go")})
	if err == nil || !strings.Contains(err.Error(), "theirs.go is not this session's work") {
		t.Fatalf("err = %v, want a refusal naming the file", err)
	}
	// "." resolves to the workspace root, which is not a file the session
	// changed, so the way of staging everything is refused by the same rule
	// rather than by a special case.
	if err := ts.stageable([]string{"."}, []string{ts.root}); err == nil {
		t.Fatal("staging the whole tree must be refused")
	}
	// A session with no record of its own stages nothing at all.
	ts.writes = &Writes{}
	if err := ts.stageable([]string{"mine.go"}, []string{filepath.Join(ts.root, "mine.go")}); err == nil {
		t.Fatal("a session with no changeset must stage nothing")
	}
}

// The refusal happens before the spawn: a path outside the workspace never
// reaches git.
func TestGitWriteRefusesAnEscapingPathBeforeTheSpawn(t *testing.T) {
	ts := &Toolset{root: t.TempDir(), bins: map[string]string{GitWriteToolName: "/nonexistent/git"}, timeout: SpawnTimeout}
	ts.writes = &Writes{Files: func() []string { return nil }}
	_, err := ts.executeGitWrite(json.RawMessage(`{"verb":"add","paths":["../outside.go"]}`))
	if err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("err = %v, want a containment refusal", err)
	}
}

// The write tool is registered by a surface, never by the machine: a toolset
// nobody asked for writes on reads history and cannot change it, which is
// what keeps it off every sub-agent.
func TestGitWriteIsRegisteredOnlyWhereASurfaceAskedForIt(t *testing.T) {
	stubLookPath(t, map[string]string{"git": "/usr/bin/git"})
	stubInsideRepo(t, true)

	ts := NewToolset(t.TempDir())
	if ts == nil || !ts.Has(GitToolName) {
		t.Fatal("git should register inside a repository")
	}
	if ts.Has(GitWriteToolName) {
		t.Fatal("the write tool must not register on its own")
	}
	for _, d := range ts.Definitions() {
		if d.Name == GitWriteToolName {
			t.Fatal("the write tool must not be offered on its own")
		}
	}
	if _, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"commit","message":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "does not write to git") {
		t.Fatalf("expected a clean not-available error, got %v", err)
	}

	ts.AllowWrites(Writes{Files: func() []string { return nil }})
	if !ts.Has(GitWriteToolName) {
		t.Fatal("a surface that asked for writes should have them")
	}
	var offered bool
	for _, d := range ts.Definitions() {
		offered = offered || d.Name == GitWriteToolName
	}
	if !offered {
		t.Fatal("the write tool should be offered once a surface asked for it")
	}

	// Outside a repository there is nothing to write to, and asking changes
	// nothing.
	stubInsideRepo(t, false)
	outside := NewToolset(t.TempDir())
	outside.AllowWrites(Writes{Files: func() []string { return nil }})
	if outside.Has(GitWriteToolName) {
		t.Fatal("the write tool must not register outside a repository")
	}
}

// The deny list matches a line, so every call has to stand for one.
func TestGitWriteLineNamesTheAct(t *testing.T) {
	for args, want := range map[string]string{
		`{"verb":"commit","message":"x"}`:    "git commit",
		`{"verb":"add","paths":["a.go"]}`:    "git add",
		`{"verb":"branch","branch":"topic"}`: "git branch",
		`{"verb":"switch","branch":"main"}`:  "git switch",
		`{"verb":"push"}`:                    "git",
		`not json`:                           "git",
	} {
		if got := WriteLine(json.RawMessage(args)); got != want {
			t.Fatalf("%s stands for %q, want %q", args, got, want)
		}
	}
}

// The unattended runner's commit is built here, so the two cannot spell a
// commit differently.
func TestRunnerArgvIsTheToolsArgv(t *testing.T) {
	ts := newTestToolset(t, nil)
	if err := os.WriteFile(filepath.Join(ts.root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := ts.resolveGitPaths([]string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	add, err := AddArgv(paths)
	if err != nil {
		t.Fatal(err)
	}
	if want := gitWriteArgvFor(t, ts, `{"verb":"add","paths":["main.go"]}`, "", false); !slices.Equal(add, want) {
		t.Fatalf("AddArgv = %v, want %v", add, want)
	}
	for _, hooks := range []bool{true, false} {
		commit, err := CommitArgv("/tmp/msg.txt", hooks)
		if err != nil {
			t.Fatal(err)
		}
		want := gitWriteArgvFor(t, ts, `{"verb":"commit","message":"x"}`, "/tmp/msg.txt", hooks)
		if !slices.Equal(commit, want) {
			t.Fatalf("CommitArgv(hooks=%v) = %v, want %v", hooks, commit, want)
		}
	}
}

func TestExecuteGitWriteEndToEnd(t *testing.T) {
	if _, ok := lookPath("git"); !ok {
		t.Skip("git is not on PATH")
	}
	root := writeRepo(t)
	changed := []string{"feature.go"}
	if err := os.WriteFile(filepath.Join(root, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Somebody else's uncommitted work, present before the session opened.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := writeToolset(t, root, Writes{Files: func() []string { return changed }, Hooks: true})

	// An empty index is named rather than turned into an empty commit.
	if _, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"commit","message":"nothing"}`)); err == nil ||
		!strings.Contains(err.Error(), "nothing is staged") {
		t.Fatalf("err = %v, want a refusal naming the empty index", err)
	}

	if _, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"add","paths":["README.md"]}`)); err == nil ||
		!strings.Contains(err.Error(), "not this session's work") {
		t.Fatalf("err = %v, want a refusal naming the file", err)
	}

	out, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"add","paths":["feature.go"]}`))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if out != "staged 1 file" {
		t.Fatalf("add answered %q", out)
	}

	out, err = ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"commit","message":"feat: a subject\n\nAnd a body with \"quotes\" and $shell punctuation."}`))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !strings.HasPrefix(out, "committed 1 file as ") || !strings.Contains(out, " on main") {
		t.Fatalf("commit answered %q", out)
	}
	if strings.Contains(out, "hooks skipped") {
		t.Fatalf("a trusted checkout runs its hooks: %q", out)
	}
	// The message reached git verbatim, punctuation and all, because it was
	// never on a command line.
	body, code := gitOut(t, root, "log", "-1", "--pretty=%B")
	if code != 0 || !strings.Contains(body, `"quotes"`) || !strings.Contains(body, "$shell punctuation") {
		t.Fatalf("commit message = %q", body)
	}
	// And the stranger's file is still uncommitted.
	if status, _ := gitOut(t, root, "status", "--porcelain"); !strings.Contains(status, "README.md") {
		t.Fatalf("work that was not this session's must still be uncommitted: %q", status)
	}

	if out, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"branch","branch":"topic"}`)); err != nil || out != "created branch topic" {
		t.Fatalf("branch: %q %v", out, err)
	}
	if out, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"switch","branch":"topic"}`)); err != nil || out != "switched to topic" {
		t.Fatalf("switch: %q %v", out, err)
	}
	if out, _ := gitOut(t, root, "branch", "--show-current"); strings.TrimSpace(out) != "topic" {
		t.Fatalf("switch did not move: %q", out)
	}
	if _, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"push"}`)); err == nil {
		t.Fatal("a verb outside the set must not reach git")
	}
}

// A switch that would lose work is refused the way git refuses it, and there
// is no flag here that discards.
func TestExecuteGitWriteRefusesASwitchThatWouldLoseWork(t *testing.T) {
	if _, ok := lookPath("git"); !ok {
		t.Skip("git is not on PATH")
	}
	root := writeRepo(t)
	ts := writeToolset(t, root, Writes{Files: func() []string { return []string{"main.go"} }, Hooks: true})
	if _, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"branch","branch":"topic"}`)); err != nil {
		t.Fatalf("branch: %v", err)
	}
	// main.go differs between the branches, and the tree is dirty on it.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main // theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"add", "main.go"}, {"commit", "-q", "-m", "second"}} {
		if out, code := gitOut(t, root, argv...); code != 0 {
			t.Skipf("git %v: %s", argv, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main // mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"switch","branch":"topic"}`)); err == nil {
		t.Fatal("a switch that would overwrite local changes must be refused")
	}
	if out, _ := gitOut(t, root, "status", "--porcelain"); !strings.Contains(out, "main.go") {
		t.Fatalf("the refused switch must leave the change alone: %q", out)
	}
}

// A commit hook is a program the checkout can point git at, so it runs only
// where the checkout is trusted — and the receipt says when it did not.
func TestExecuteGitWriteRunsHooksOnlyOnATrustedCheckout(t *testing.T) {
	if _, ok := lookPath("git"); !ok {
		t.Skip("git is not on PATH")
	}
	for _, trusted := range []bool{true, false} {
		root := writeRepo(t)
		ran := filepath.Join(t.TempDir(), "ran")
		hooks := filepath.Join(root, ".git", "hooks")
		if err := os.MkdirAll(hooks, 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\necho ran >> " + ran + "\n"
		if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "feature.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := writeToolset(t, root, Writes{Files: func() []string { return []string{"feature.go"} }, Hooks: trusted})
		if _, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"add","paths":["feature.go"]}`)); err != nil {
			t.Fatalf("add: %v", err)
		}
		out, err := ts.Execute(GitWriteToolName, json.RawMessage(`{"verb":"commit","message":"feat: x"}`))
		if err != nil {
			t.Fatalf("commit (trusted=%v): %v", trusted, err)
		}
		_, statErr := os.Stat(ran)
		switch {
		case trusted && statErr != nil:
			t.Fatal("a trusted checkout's hook should have run")
		case trusted && strings.Contains(out, "hooks skipped"):
			t.Fatalf("a trusted commit should not say hooks were skipped: %q", out)
		case !trusted && statErr == nil:
			t.Fatal("an untrusted checkout's hook must not run")
		case !trusted && !strings.Contains(out, "hooks skipped · checkout not trusted"):
			t.Fatalf("an untrusted commit must say so: %q", out)
		}
	}
}

// The card a person reads before approving a write is built here, so what it
// says about each verb — and about the hooks a commit will or will not run —
// is worth pinning.
func TestWritePlanIsWhatTheCardSays(t *testing.T) {
	ts := newTestToolset(t, nil)
	ts.writes = &Writes{Files: func() []string { return nil }}

	for _, c := range []struct {
		args, title, summary string
	}{
		{`{"verb":"add","paths":["a.go","b.go"]}`, "stage 2 files", "a.go, b.go"},
		{`{"verb":"commit","message":"feat: a subject\n\nand a body"}`, "commit", "feat: a subject"},
		{`{"verb":"branch","branch":"topic"}`, "branch topic", "create the branch topic"},
		{`{"verb":"switch","branch":"master"}`, "switch to master", "switch to the branch master"},
		{`{"verb":"switch","branch":"topic","create":true}`, "switch to topic", "create topic off the current commit and switch to it"},
	} {
		w, err := ts.WritePlan(json.RawMessage(c.args))
		if err != nil {
			t.Fatalf("%s: %v", c.args, err)
		}
		if w.Title != c.title || w.Summary != c.summary {
			t.Fatalf("%s: title %q summary %q, want %q and %q", c.args, w.Title, w.Summary, c.title, c.summary)
		}
		if w.Hooks {
			t.Fatalf("%s: a session that trusted nothing runs no hooks", c.args)
		}
	}

	ts.writes.Hooks = true
	if w, err := ts.WritePlan(json.RawMessage(`{"verb":"commit","message":"x"}`)); err != nil || !w.Hooks {
		t.Fatalf("a trusted checkout's card should say the hooks run: %+v %v", w, err)
	}
	// A verb outside the set has no card, because it has no act.
	if _, err := ts.WritePlan(json.RawMessage(`{"verb":"push"}`)); err == nil {
		t.Fatal("a verb outside the set must not produce a card")
	}
}

// gitOut runs git in root for a test's own assertions.
func gitOut(t *testing.T, root string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return string(out), code
}
