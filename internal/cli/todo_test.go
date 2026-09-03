package cli

import (
	"io"
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

// `shhh todo show` takes exactly one slug, and cobra answers that before the
// backlog is read — so the wiring is checked without a checkout to read.
func TestTodoCommand_ShowNeedsASlug(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"todo", "show"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("`shhh todo show` with no slug should say so")
	}
}

func TestTodoCommand_ShowUnknownSlug(t *testing.T) {
	root := todoFixture(t)

	var out strings.Builder
	err := todoShow(&out, root, "nope")
	if err == nil || !strings.Contains(err.Error(), `no backlog item "nope"`) {
		t.Fatalf("err = %v", err)
	}
	if err := todoList(&out, root); err != nil {
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

// sprintFixture is the backlog fixture with a sprint over three of its
// slugs: one ready, one waiting on a dependency, and one the backlog does
// not hold at all.
func sprintFixture(t *testing.T) string {
	t.Helper()
	root := todoFixture(t)
	must(t, os.WriteFile(filepath.Join(todo.Dir(root), todo.SprintFile),
		[]byte("---\nname: caching\ncrew: two\n---\nMake the cache trustworthy.\n\n## Items\n- first\n- second\n- vanished\n"), 0o644))
	return root
}

func TestTodoSprintReport(t *testing.T) {
	out := todoSprintReport(todo.Load(sprintFixture(t))).String()
	for _, want := range []string{
		"shhh todo sprint — caching",
		"Make the cache trustworthy.",
		"· first     First thing · high · S  [ready]",
		"⊘ second    Second · medium  [waits on first]",
		"⊘ vanished    [dropped from the backlog]",
		"0 of 2 done · open",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sprint view lacks %q:\n%s", want, out)
		}
	}
}

func TestTodoSprintReport_Empty(t *testing.T) {
	out := todoSprintReport(todo.Load(t.TempDir())).String()
	if !strings.Contains(out, "⊘ no sprint here") {
		t.Errorf("empty sprint view = %q", out)
	}
}

// The sprint scopes the ready set, so the listing's tally has to say which
// sprint it is counting or the rows and the tally read as a contradiction.
func TestTodoListing_NamesTheSprintItCountsReadyIn(t *testing.T) {
	out := todoListing(todo.Load(sprintFixture(t)))
	if !strings.Contains(out, "1 ready in caching") {
		t.Errorf("listing tally = %q", out)
	}
}

func TestTodoManager_SprintVerbs(t *testing.T) {
	root := sprintFixture(t)
	manage := todoManager(root)

	if out := manage([]string{"sprint"}); !strings.Contains(out, "shhh todo sprint — caching") {
		t.Errorf("bare sprint = %q", out)
	}
	if out := manage([]string{"sprint", "drop", "vanished"}); !strings.Contains(out, "✓ dropped from caching vanished") {
		t.Errorf("drop = %q", out)
	}
	if out := manage([]string{"sprint", "add", "third"}); !strings.Contains(out, "✓ added to caching third") {
		t.Errorf("add = %q", out)
	}
	if out := manage([]string{"sprint", "add", "nope"}); !strings.Contains(out, "no active backlog item") {
		t.Errorf("add unknown = %q", out)
	}
	if out := manage([]string{"sprint", "goal", "Make", "it", "provable."}); !strings.Contains(out, "✓ goal of caching rewritten") {
		t.Errorf("goal = %q", out)
	}
	sp, err := todo.LoadSprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Goal != "Make it provable." || strings.Join(sp.Slugs, ",") != "first,second,third" {
		t.Fatalf("sprint = %+v", sp)
	}
	if len(sp.Extra) != 1 || sp.Extra[0].Key != "crew" {
		t.Errorf("extra = %v; an unknown field has to survive the verbs", sp.Extra)
	}
	if out := manage([]string{"sprint", "wat"}); !strings.HasPrefix(out, "Usage:") {
		t.Errorf("unknown verb = %q", out)
	}

	out := manage([]string{"sprint", "close"})
	if !strings.Contains(out, "closed caching") || !strings.Contains(out, "first   left undone  [ready]") {
		t.Errorf("close = %q", out)
	}
	if _, err := os.Stat(todo.SprintPath(root)); !os.IsNotExist(err) {
		t.Error("the sprint file is still in place after close")
	}
	if out := manage([]string{"sprint", "add", "third"}); !strings.Contains(out, "There is no sprint") {
		t.Errorf("verb with no sprint = %q", out)
	}
}

// Archiving the last of a sprint's slugs by hand closes the sprint, and the
// row says so where the archive row is.
func TestTodoManager_DoneClosesAFinishedSprint(t *testing.T) {
	root := todoFixture(t)
	must(t, os.WriteFile(filepath.Join(todo.Dir(root), todo.SprintFile),
		[]byte("---\nname: caching\n---\ngoal\n\n## Items\n- first\n"), 0o644))
	out := todoManager(root)([]string{"done", "first"})
	if !strings.Contains(out, "✓ archived first → ") || !strings.Contains(out, "✓ sprint closed") {
		t.Fatalf("done = %q", out)
	}
	if _, err := os.Stat(todo.SprintPath(root)); !os.IsNotExist(err) {
		t.Error("the sprint file is still in place")
	}
}

// A sprint marked closed by hand is a record that was never filed. Its
// verbs refuse rather than edit it; close still works, because filing it
// is the way out.
func TestTodoManager_SprintVerbsRefuseAClosedSprint(t *testing.T) {
	root := todoFixture(t)
	must(t, os.WriteFile(filepath.Join(todo.Dir(root), todo.SprintFile),
		[]byte("---\nname: caching\nstatus: closed\n---\ngoal\n\n## Items\n- first\n"), 0o644))
	manage := todoManager(root)
	for _, args := range [][]string{{"sprint", "add", "second"}, {"sprint", "drop", "first"}, {"sprint", "goal", "new"}} {
		if out := manage(args); !strings.Contains(out, "caching is closed") {
			t.Errorf("%v = %q", args, out)
		}
	}
	if out := manage([]string{"sprint", "close"}); !strings.Contains(out, "closed caching") {
		t.Errorf("close = %q", out)
	}
}
