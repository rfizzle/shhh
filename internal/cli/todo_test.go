package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

func todoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := todo.Dir(root)
	must(t, os.MkdirAll(filepath.Join(dir, todo.DoneSubdir), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "first.md"), []byte("---\ntitle: First thing\npriority: high\nsize: S\n---\n## Notes\nhi\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "second.md"), []byte("---\ntitle: Second\ndepends_on: [first, gone]\n---\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "third.md"), []byte("---\ntitle: Third\nstatus: blocked\ndepends_on: [gone]\n---\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, todo.DoneSubdir, "gone.md"), []byte("---\ntitle: Gone\nstatus: done\n---\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "bad.md"), []byte("nope\n"), 0o644))
	return root
}

func TestTodoListing(t *testing.T) {
	s := todo.Load(todoFixture(t))
	out := todoListing(s)
	for _, want := range []string{
		"· first   First thing · high · S  [ready]",
		"⊘ second  Second · medium  [waits on first]",
		"✗ third   Third · medium  [blocked]",
		"shhh todo — 3 items",
		"1 ready · 1 blocked · 1 archived",
		"bad.md: skipped: no header",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gone.md") && !strings.Contains(out, "archived") {
		t.Errorf("archived item listed as active:\n%s", out)
	}
}

func TestTodoListing_Empty(t *testing.T) {
	out := todoListing(todo.Load(t.TempDir()))
	if !strings.Contains(out, "⊘ no backlog here") {
		t.Errorf("empty listing = %q", out)
	}
}

func TestTodoDetail(t *testing.T) {
	s := todo.Load(todoFixture(t))
	it, _ := s.Find("first")
	out := todoDetail(s, it)
	for _, want := range []string{"shhh todo first — First thing", "status:    ready", "size:      S", "## Notes", "hi"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail lacks %q:\n%s", want, out)
		}
	}
}

func TestTodoCommand_ShowUnknownSlug(t *testing.T) {
	root := todoFixture(t)
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"todo", "show", "nope"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `no backlog item "nope"`) {
		t.Fatalf("err = %v", err)
	}
	var out strings.Builder
	cmd = NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"todo"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "· first   First thing · high") {
		t.Errorf("shhh todo output = %q", out.String())
	}
}

func TestTodoManager(t *testing.T) {
	root := todoFixture(t)
	manage := todoManager(root)

	if out := manage(nil); !strings.Contains(out, "· first   First thing · high") {
		t.Errorf("bare = %q", out)
	}
	if out := manage([]string{"show", "first"}); !strings.Contains(out, "shhh todo first") {
		t.Errorf("show = %q", out)
	}
	if out := manage([]string{"show", "nope"}); !strings.Contains(out, "✗ no backlog item nope") {
		t.Errorf("show missing = %q", out)
	}
	out := manage([]string{"add", "Fix", "the", "#12", "parser", "crash"})
	if !strings.Contains(out, "✓ added fix-the-12-parser-crash") {
		t.Errorf("add = %q", out)
	}
	it, ok := todo.Load(root).Find("fix-the-12-parser-crash")
	if !ok || it.Title != "Fix the #12 parser crash" || it.Priority != todo.PriorityMedium || it.Created == "" || !strings.Contains(it.Body, "## Acceptance criteria") {
		t.Errorf("added item = %+v", it)
	}
	if out := manage([]string{"add", "Fix the #12 parser crash"}); !strings.HasPrefix(out, "Error:") || !strings.Contains(out, "already exists") {
		t.Errorf("add collision = %q", out)
	}
	if out := manage([]string{"add"}); !strings.HasPrefix(out, "Usage:") {
		t.Errorf("add without text = %q", out)
	}

	if out := manage([]string{"block", "first", "needs", "a", "decision"}); !strings.Contains(out, "✓ blocked first") {
		t.Errorf("block = %q", out)
	}
	it, _ = todo.Load(root).Find("first")
	if it.Status != todo.StatusBlocked || !strings.HasSuffix(it.Body, "## Blocked\nneeds a decision\n") {
		t.Errorf("blocked item = %+v", it)
	}
	if out := manage([]string{"open", "first"}); !strings.Contains(out, "✓ reopened first") {
		t.Errorf("open = %q", out)
	}
	if out := manage([]string{"block", "gone"}); !strings.Contains(out, "no active backlog item") {
		t.Errorf("block archived = %q", out)
	}
	if out := manage([]string{"done", "first"}); !strings.Contains(out, "✓ archived first → ") {
		t.Errorf("done = %q", out)
	}
	if out := manage([]string{"done", "first"}); !strings.HasPrefix(out, "Error:") {
		t.Errorf("done twice = %q", out)
	}
	if out := manage([]string{"drop", "second"}); !strings.Contains(out, "✓ dropped second · the file is deleted") {
		t.Errorf("drop = %q", out)
	}
	if _, err := os.Stat(filepath.Join(todo.Dir(root), "second.md")); !os.IsNotExist(err) {
		t.Error("dropped file still there")
	}
	if out := manage([]string{"drop", "gone"}); !strings.HasPrefix(out, "Error:") {
		t.Errorf("drop archived = %q", out)
	}
	if out := manage([]string{"wat"}); !strings.HasPrefix(out, "Usage:") {
		t.Errorf("unknown = %q", out)
	}
}
