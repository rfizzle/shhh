package approval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/permission"
	"github.com/rfizzle/shhh/internal/tools"
)

// mkdirs and writeFiles are the fixtures' plumbing: a directory or file that
// cannot be made fails the case rather than weakening it.
func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A write to a path the repository's program paths name is refused by the
// rules, in the shared sentence, whatever the spelling: a relative path, an
// unclean one, a link that lands under the store, a dangling link to a hook
// that is not there yet, a hooks directory or an included file the config
// moves (outside the workspace too, spelled absolute, with ~ or with .., on
// the header's own line, with a comment after it, under an includeIf header),
// and the shared config of a linked worktree, found through its gitdir and
// commondir pointers. A neighbouring path is not.
//
// Each scenario builds its own repository and the Router over it; the rows
// are the paths, and whether the rules refuse them. A refusal is held to the
// shared sentence and reason.
func TestApprovalRouter_GitProgramPathsAreRefused(t *testing.T) {
	type row struct {
		path   string
		refuse bool
	}
	for _, sc := range []struct {
		name  string
		build func(t *testing.T) (Router, []row)
	}{
		{"every spelling inside the workspace", func(t *testing.T) (Router, []row) {
			ws := realDir(t)
			dotgit := filepath.Join(ws, ".git")
			mkdirs(t, filepath.Join(dotgit, "hooks"), filepath.Join(dotgit, "info"), filepath.Join(ws, "ci-hooks"), filepath.Join(ws, "src"))
			writeFiles(t, map[string]string{filepath.Join(dotgit, "config"): "[core]\n\thooksPath = ci-hooks\n[include]\n\tpath = ../extra.gitconfig\n"})
			if err := os.Symlink(filepath.Join(dotgit, "hooks"), filepath.Join(ws, "src", "link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dotgit, filepath.Join(ws, "gitlink")); err != nil {
				t.Fatal(err)
			}
			return Router{Workspace: ws}, []row{
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
			}
		}},
		{"a dangling link to a hook", func(t *testing.T) (Router, []row) {
			// A write through a link whose target is not there yet creates
			// the target, so this is the usual way to write a hook.
			ws := realDir(t)
			mkdirs(t, filepath.Join(ws, ".git", "hooks"), filepath.Join(ws, "src"))
			links := map[string]string{
				"abs":    filepath.Join(ws, ".git", "hooks", "pre-commit"),
				"rel":    filepath.Join("..", ".git", "hooks", "pre-commit"),
				"chain":  filepath.Join(ws, "src", "abs"),
				"config": filepath.Join(ws, ".git", "config"),
			}
			var rows []row
			for name, target := range links {
				if err := os.Symlink(target, filepath.Join(ws, "src", name)); err != nil {
					t.Fatal(err)
				}
				rows = append(rows, row{filepath.Join("src", name), true})
			}
			return Router{Workspace: ws}, append(rows, row{"src/plain.go", false})
		}},
		{"paths the config names outside the workspace", func(t *testing.T) (Router, []row) {
			base := realDir(t)
			home := filepath.Join(base, "home")
			ws := filepath.Join(base, "ws")
			t.Setenv("HOME", home)
			mkdirs(t, filepath.Join(ws, ".git"), home, filepath.Join(base, "hooks"), filepath.Join(base, "shared"))
			writeFiles(t, map[string]string{
				filepath.Join(ws, ".git", "config"): "[core] hooksPath = " + filepath.Join(base, "hooks") + " # ours\n" +
					"[includeIf \"gitdir:" + ws + "/\"]\n\tpath = ../../shared/extra.conf ; more\n" +
					"[include]\n\tpath = ~/.shhhconfig\n",
				// An included file can name a further hooks directory.
				filepath.Join(base, "shared", "extra.conf"): "[core]\n\thooksPath = ../more-hooks\n",
			})
			return Router{Workspace: ws}, []row{
				{filepath.Join(base, "hooks", "pre-commit"), true},
				{filepath.Join("..", "hooks", "pre-commit"), true},
				{filepath.Join(base, "shared", "extra.conf"), true},
				{filepath.Join(home, ".shhhconfig"), true},
				{filepath.Join(base, "more-hooks", "post-merge"), true},
				{filepath.Join(base, "shared", "other.conf"), false},
				{"main.go", false},
			}
		}},
		{"a linked worktree reads its store's config", func(t *testing.T) (Router, []row) {
			// .git is a file here: it is refused itself, and the shared config
			// and the worktree's own name the paths.
			base := realDir(t)
			main := filepath.Join(base, "main")
			ws := filepath.Join(base, "linked")
			gitdir := filepath.Join(main, ".git", "worktrees", "linked")
			mkdirs(t, gitdir, ws, filepath.Join(base, "shared-hooks"), filepath.Join(base, "own-hooks"))
			writeFiles(t, map[string]string{
				filepath.Join(ws, ".git"):                "gitdir: " + gitdir + "\n",
				filepath.Join(gitdir, "commondir"):       "../..\n",
				filepath.Join(main, ".git", "config"):    "[core]\n\thooksPath = " + filepath.Join(base, "shared-hooks") + "\n",
				filepath.Join(gitdir, "config.worktree"): "[core]\n\thooksPath = " + filepath.Join(base, "own-hooks") + "\n",
			})
			return Router{Workspace: ws}, []row{
				{".git", true},
				{filepath.Join(base, "shared-hooks", "pre-commit"), true},
				{filepath.Join(base, "own-hooks", "pre-commit"), true},
				{filepath.Join(gitdir, "hooks", "x"), true},
				{filepath.Join(gitdir, "config.worktree"), true},
				{"main.go", false},
			}
		}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			r, rows := sc.build(t)
			for _, c := range rows {
				got, refused := r.Rule(writeCall(c.path))
				if refused != c.refuse {
					t.Errorf("%s: refused = %v, want %v", c.path, refused, c.refuse)
					continue
				}
				if refused && (got.Result != tools.GitProgramPathResult || got.Reason != permission.DenyReasonGitPath) {
					t.Errorf("%s: refusal = %+v", c.path, got)
				}
			}
		})
	}
}

// The tool refuses a write to a program path too, with the same sentence, on
// every path that reaches it.
func TestApprovalTool_RefusesAGitProgramPathInTheRulesSentence(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"path": filepath.Join(realDir(t), ".git", "hooks", "pre-commit"), "content": "x"})
	if _, err := tools.PreviewMutation(tools.WriteFileName, args); !errors.Is(err, tools.ErrGitProgramPath) || "error: "+err.Error() != tools.GitProgramPathResult {
		t.Errorf("the tool's own refusal = %v", err)
	}
}

// An edit across files is refused when any file of its list is one git runs,
// at the rule and at the tool alike: the hook in second place is the one a
// reading of the first path alone would have let through.
func TestRouter_RefusesAGitProgramPathAnywhereInAList(t *testing.T) {
	ws := realDir(t)
	hook := filepath.Join(ws, ".git", "hooks", "pre-commit")
	if _, refused := (Router{Workspace: ws}).Rule(Call{Write: true, Path: "a.go", Paths: []string{"a.go", hook}}); !refused {
		t.Error("the rule let a list through with a hook in it")
	}
	args, _ := json.Marshal(map[string]any{"paths": []string{filepath.Join(ws, "a.go"), hook}, "old_text": "x", "new_text": "y"})
	if _, err := tools.PreviewMutation(tools.EditFileName, args); !errors.Is(err, tools.ErrGitProgramPath) {
		t.Errorf("the tool's own refusal = %v", err)
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
	if _, err := rec.ExecuteMutating(context.Background(), tools.WriteFileName, args); !errors.Is(err, tools.ErrGitProgramPath) {
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
	c, _ := permission.ClassifyCall(tools.WriteFileName, args, permission.Answers{Has: func(string) bool { return true }})
	return CallOf(tools.WriteFileName, c)
}
