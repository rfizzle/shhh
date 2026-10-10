package approval

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/tools"
)

// A write to a path the repository's program paths name is refused by the
// rules, in the shared sentence, whatever the spelling: a relative path, an
// unclean one, a link that lands under the store, a hooks directory the
// config moves, a file the config includes. A neighbouring path is not.
func TestApprovalRouter_GitProgramPathsAreRefusedEverywhere(t *testing.T) {
	ws := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}
	dotgit := filepath.Join(ws, ".git")
	for _, d := range []string{filepath.Join(dotgit, "hooks"), filepath.Join(dotgit, "info"), filepath.Join(ws, "ci-hooks"), filepath.Join(ws, "src")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := "[core]\n\thooksPath = ci-hooks\n[include]\n\tpath = ../extra.gitconfig\n"
	if err := os.WriteFile(filepath.Join(dotgit, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotgit, "hooks"), filepath.Join(ws, "src", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotgit, filepath.Join(ws, "gitlink")); err != nil {
		t.Fatal(err)
	}
	r := Router{Workspace: ws}
	for _, c := range []struct {
		path   string
		refuse bool
	}{
		{".git/hooks/pre-commit", true},
		{filepath.Join(ws, ".git", "config"), true},
		{".git/info/attributes", true},
		{".git", true},
		{"src/../.git/hooks/post-commit", true},
		{"src/link/pre-push", true},
		{"gitlink/config", true},
		{"gitlink/hooks/new", true},
		{"ci-hooks/pre-commit", true},
		{"extra.gitconfig", true},
		{".git/HEAD", false},
		{".gitignore", false},
		{"src/main.go", false},
		{"ci-hooks-notes.md", false},
	} {
		got, refused := r.Rule(writeCall(c.path))
		if refused != c.refuse {
			t.Errorf("%s: refused = %v, want %v", c.path, refused, c.refuse)
			continue
		}
		if refused && (got.Result != tools.GitProgramPathResult || got.Reason != agent.DenyReasonGitPath) {
			t.Errorf("%s: refusal = %+v", c.path, got)
		}
	}
	// The tool refuses it too, with the same sentence, on every path that
	// reaches it.
	args, _ := json.Marshal(map[string]string{"path": filepath.Join(ws, ".git", "hooks", "pre-commit"), "content": "x"})
	if _, err := tools.PreviewMutation(tools.WriteFileName, args); !errors.Is(err, tools.ErrGitProgramPath) || "error: "+err.Error() != tools.GitProgramPathResult {
		t.Errorf("the tool's own refusal = %v", err)
	}
}

// A link whose target is not there yet still lands where it points: a write
// through it creates the target, so the dangling link to a hook that does not
// exist is the usual way to write one.
func TestApprovalRouter_ADanglingLinkToAHookIsRefused(t *testing.T) {
	ws := realDir(t)
	for _, d := range []string{filepath.Join(ws, ".git", "hooks"), filepath.Join(ws, "src")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		"abs":    filepath.Join(ws, ".git", "hooks", "pre-commit"),
		"rel":    filepath.Join("..", ".git", "hooks", "pre-commit"),
		"chain":  filepath.Join(ws, "src", "abs"),
		"config": filepath.Join(ws, ".git", "config"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(ws, "src", name)); err != nil {
			t.Fatal(err)
		}
	}
	r := Router{Workspace: ws}
	for name := range links {
		if _, refused := r.Rule(writeCall(filepath.Join("src", name))); !refused {
			t.Errorf("a write through the dangling link %s was not refused", name)
		}
	}
	if _, refused := r.Rule(writeCall("src/plain.go")); refused {
		t.Error("an ordinary file was refused")
	}
}

// The repository read for the paths its configuration names is the
// session's, wherever those paths are: a hooks directory or an included file
// outside the workspace, spelled absolute, with ~ or with .., on the header's
// own line, with a comment after it, under an includeIf header.
func TestApprovalRouter_ConfigNamedPathsOutsideTheWorkspaceAreRefused(t *testing.T) {
	base := realDir(t)
	home := filepath.Join(base, "home")
	ws := filepath.Join(base, "ws")
	t.Setenv("HOME", home)
	for _, d := range []string{filepath.Join(ws, ".git"), home, filepath.Join(base, "hooks"), filepath.Join(base, "shared")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := "[core] hooksPath = " + filepath.Join(base, "hooks") + " # ours\n" +
		"[includeIf \"gitdir:" + ws + "/\"]\n\tpath = ../../shared/extra.conf ; more\n" +
		"[include]\n\tpath = ~/.shhhconfig\n"
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	// An included file can name a further hooks directory.
	if err := os.WriteFile(filepath.Join(base, "shared", "extra.conf"), []byte("[core]\n\thooksPath = ../more-hooks\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Router{Workspace: ws}
	for _, p := range []string{
		filepath.Join(base, "hooks", "pre-commit"),
		filepath.Join("..", "hooks", "pre-commit"),
		filepath.Join(base, "shared", "extra.conf"),
		filepath.Join(home, ".shhhconfig"),
		filepath.Join(base, "more-hooks", "post-merge"),
	} {
		if _, refused := r.Rule(writeCall(p)); !refused {
			t.Errorf("%s: a path the config names was not refused", p)
		}
	}
	for _, p := range []string{filepath.Join(base, "shared", "other.conf"), "main.go"} {
		if _, refused := r.Rule(writeCall(p)); refused {
			t.Errorf("%s: refused", p)
		}
	}
}

// In a linked worktree .git is a file: it is refused itself, and the shared
// config and the worktree's own, found through the gitdir and commondir
// pointers, name the paths.
func TestApprovalRouter_ALinkedWorktreeReadsItsStoresConfig(t *testing.T) {
	base := realDir(t)
	main := filepath.Join(base, "main")
	ws := filepath.Join(base, "linked")
	gitdir := filepath.Join(main, ".git", "worktrees", "linked")
	for _, d := range []string{gitdir, ws, filepath.Join(base, "shared-hooks"), filepath.Join(base, "own-hooks")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(ws, ".git"):                "gitdir: " + gitdir + "\n",
		filepath.Join(gitdir, "commondir"):       "../..\n",
		filepath.Join(main, ".git", "config"):    "[core]\n\thooksPath = " + filepath.Join(base, "shared-hooks") + "\n",
		filepath.Join(gitdir, "config.worktree"): "[core]\n\thooksPath = " + filepath.Join(base, "own-hooks") + "\n",
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := Router{Workspace: ws}
	for _, p := range []string{
		".git",
		filepath.Join(base, "shared-hooks", "pre-commit"),
		filepath.Join(base, "own-hooks", "pre-commit"),
		filepath.Join(gitdir, "hooks", "x"),
		filepath.Join(gitdir, "config.worktree"),
	} {
		if _, refused := r.Rule(writeCall(p)); !refused {
			t.Errorf("%s: not refused in a linked worktree", p)
		}
	}
	if _, refused := r.Rule(writeCall("main.go")); refused {
		t.Error("an ordinary file was refused")
	}
}

// The person's own write is not the model's: the tool-layer refusal stands for
// every model path and WriteByPerson is the one door around it.
func TestWriteByPerson_IsNotRefusedAndTheModelsWriteIs(t *testing.T) {
	ws := realDir(t)
	hooks := filepath.Join(ws, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": filepath.Join(hooks, "pre-commit"), "content": "#!/bin/sh\n"})
	rec := tools.NewRecorder()
	if _, err := rec.ExecuteMutating(tools.WriteFileName, args); !errors.Is(err, tools.ErrGitProgramPath) {
		t.Fatalf("the model's write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hooks, "pre-commit")); err == nil {
		t.Fatal("the model's write landed")
	}
	if _, err := rec.WriteByPerson(args); err != nil {
		t.Fatalf("the person's write: %v", err)
	}
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func writeCall(path string) Call {
	args, _ := json.Marshal(map[string]string{"path": path, "content": "x"})
	c, _ := agent.ClassifyCall(tools.WriteFileName, args, agent.Answers{Has: func(string) bool { return true }})
	return CallOf(tools.WriteFileName, c)
}
