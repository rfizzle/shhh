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
	os.MkdirAll(filepath.Join(dir, todo.DoneSubdir), 0o755)
	os.WriteFile(filepath.Join(dir, "first.md"), []byte("---\ntitle: First thing\npriority: high\nsize: S\n---\n## Notes\nhi\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "second.md"), []byte("---\ntitle: Second\ndepends_on: [first, gone]\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "third.md"), []byte("---\ntitle: Third\nstatus: blocked\ndepends_on: [gone]\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dir, todo.DoneSubdir, "gone.md"), []byte("---\ntitle: Gone\nstatus: done\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.md"), []byte("nope\n"), 0o644)
	return root
}

func TestTodoListing(t *testing.T) {
	s := todo.Load(todoFixture(t))
	out := todoListing(s)
	for _, want := range []string{
		"first   high    S  ready",
		"second  medium  -  waits on first",
		"third   medium  -  blocked",
		"3 item(s), 1 ready, 1 blocked, 1 archived.",
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
	if !strings.HasPrefix(out, "No backlog.") {
		t.Errorf("empty listing = %q", out)
	}
}

func TestTodoDetail(t *testing.T) {
	s := todo.Load(todoFixture(t))
	it, _ := s.Find("first")
	out := todoDetail(s, it)
	for _, want := range []string{"slug:       first", "status:     ready", "size:       S", "## Notes\nhi"} {
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
	defer os.Chdir(orig)

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
	if !strings.Contains(out.String(), "first   high") {
		t.Errorf("shhh todo output = %q", out.String())
	}
}
