package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
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

func TestTodoSetReport_Ready(t *testing.T) {
	s := todo.Load(todoFixture(t))
	out := todoSetReport(s, "shhh todo ready", s.Ready()).String()
	for _, want := range []string{"shhh todo ready — 1 item", "· first  First thing · high · S  [ready]"} {
		if !strings.Contains(out, want) {
			t.Errorf("ready lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "second") {
		t.Errorf("a waiting item is in the ready list:\n%s", out)
	}
}

// Nothing ready and no backlog at all are different answers, and each names
// the way out of its own situation.
func TestTodoSetReport_NothingReady(t *testing.T) {
	s := todo.Load(t.TempDir())
	out := todoSetReport(s, "shhh todo next", todoNext(s)).String()
	if !strings.Contains(out, "⊘ nothing is ready") {
		t.Errorf("empty next = %q", out)
	}
	if strings.Contains(out, "no backlog here") {
		t.Errorf("next answered the listing's empty state:\n%s", out)
	}
}

func TestTodoNext(t *testing.T) {
	s := todo.Load(todoFixture(t))
	next := todoNext(s)
	if len(next) != 1 || next[0].Slug != "first" {
		t.Fatalf("next = %+v", next)
	}
}

// The JSON is the store as the screen shows it: the item's own fields, what
// the row's last column says, and the warnings — a script that saw only the
// fields would treat a file with a broken size line as ungraded and never
// learn the line is there.
func TestTodoJSON(t *testing.T) {
	root := sprintFixture(t)
	must(t, os.WriteFile(filepath.Join(todo.Dir(root), "fourth.md"),
		[]byte("---\ntitle: Fourth\nsize: XL\n---\n"), 0o644))
	s := todo.Load(root)

	data, err := json.Marshal(todoJSON(s, s.Items))
	if err != nil {
		t.Fatal(err)
	}
	var doc todoDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the answer does not parse: %v\n%s", err, data)
	}
	if doc.Root != root || doc.Dir != todo.Dir(root) {
		t.Errorf("root = %q, dir = %q", doc.Root, doc.Dir)
	}
	if len(doc.Items) != s.Len() {
		t.Fatalf("%d items for a store of %d", len(doc.Items), s.Len())
	}
	byslug := map[string]todoItemDoc{}
	for _, it := range doc.Items {
		byslug[it.Slug] = it
	}
	if first := byslug["first"]; !first.Ready || first.State != "ready" || first.Size != "S" || first.Title != "First thing" {
		t.Errorf("first = %+v", first)
	}
	if second := byslug["second"]; second.Ready || second.State != "waits on first" ||
		strings.Join(second.Waiting, ",") != "first" || strings.Join(second.DependsOn, ",") != "first,gone" {
		t.Errorf("second = %+v", second)
	}
	if fourth := byslug["fourth"]; len(fourth.Warnings) != 1 || !strings.Contains(fourth.Warnings[0], "unknown size") {
		t.Errorf("fourth = %+v; the screen's warning has to be in the data", fourth)
	}
	if len(doc.Diagnostics) != 1 || !strings.Contains(doc.Diagnostics[0], "bad.md") {
		t.Errorf("diagnostics = %v", doc.Diagnostics)
	}
	if doc.Sprint == nil || doc.Sprint.Name != "caching" || !doc.Sprint.Open || doc.Sprint.Total != 2 {
		t.Fatalf("sprint = %+v", doc.Sprint)
	}
	var states []string
	for _, e := range doc.Sprint.Items {
		states = append(states, e.Slug+"="+e.State)
	}
	if strings.Join(states, " ") != "first=ready second=waiting vanished=dropped" {
		t.Errorf("sprint entries = %v", states)
	}
}

// The sprint verb answers about the sprint: its slugs, in the file's order,
// and not the rest of the backlog.
func TestTodoJSON_SprintVerb(t *testing.T) {
	s := todo.Load(sprintFixture(t))
	doc := todoJSON(s, todoSprintItems(s))
	var slugs []string
	for _, it := range doc.Items {
		slugs = append(slugs, it.Slug)
	}
	if strings.Join(slugs, ",") != "first,second" {
		t.Errorf("sprint items = %v", slugs)
	}
}

// A run in flight is the one reason a verb refuses an item that is otherwise
// there and active, and the refusal names the session because that is where
// the run can be stopped.
func TestTodoVerb_RefusesAHeldItem(t *testing.T) {
	root := todoFixture(t)
	st := run.Start(todo.Item{Slug: "first"}, "todo-run-19700101-000000", "", 0, run.Options{})
	st.Stage = run.StageImplement
	must(t, st.Save(root))

	for _, args := range [][]string{{"block", "first"}, {"open", "first"}, {"done", "first"}, {"drop", "first"}} {
		out, err := todoVerb(root, args)
		if err == nil {
			t.Fatalf("%v changed a held item: %q", args, out)
		}
		for _, want := range []string{"first", "todo-run-19700101-000000", "implement"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v refusal lacks %q: %v", args, want, err)
			}
		}
	}
	// The session says it in its own words, and a script gets the sentence
	// in the exit status instead.
	if out := todoManager(root)([]string{"drop", "first"}); !strings.Contains(out, "Error: ") ||
		!strings.Contains(out, "todo-run-19700101-000000") {
		t.Errorf("session refusal = %q", out)
	}
	if _, err := os.Stat(filepath.Join(todo.Dir(root), "first.md")); err != nil {
		t.Errorf("the held item was dropped anyway: %v", err)
	}

	// A run that ended holds nothing, and the verb goes through.
	run.Discard(root, "first")
	if _, err := todoVerb(root, []string{"block", "first"}); err != nil {
		t.Errorf("block after the run ended: %v", err)
	}
}

// A sprint records the item it has taken before that item has a checkpoint,
// and the gap is exactly where a second terminal would find it unheld.
func TestTodoVerb_RefusesAnItemTheSprintHolds(t *testing.T) {
	root := todoFixture(t)
	sp := run.StartSprint("todo-run-19700101-000000", "", 0, false)
	sp.Current = "first"
	must(t, sp.Save(root))

	_, err := todoVerb(root, []string{"done", "first"})
	if err == nil || !strings.Contains(err.Error(), "sprint") || !strings.Contains(err.Error(), "todo-run-19700101-000000") {
		t.Fatalf("err = %v", err)
	}
	if _, err := todoVerb(root, []string{"done", "third"}); err != nil {
		t.Errorf("an item the sprint is not on was refused: %v", err)
	}
}

// Redirected, `show` is the file: a script asked for the item, and a
// rendering of prose is not one.
func TestTodoShow_PipedIsTheFile(t *testing.T) {
	root := todoFixture(t)
	var out strings.Builder
	if err := todoShow(&out, root, "first"); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(todo.Dir(root), "first.md"))
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want) {
		t.Errorf("show = %q, want the file %q", out.String(), want)
	}
}

// The header report is the same one the transcript draws, minus the body the
// terminal path lays out itself.
func TestTodoItemReport_WithoutTheBody(t *testing.T) {
	s := todo.Load(todoFixture(t))
	it, _ := s.Find("first")
	out := todoItemReport(s, it, false).String()
	if !strings.Contains(out, "status:    ready") {
		t.Errorf("header lacks the status:\n%s", out)
	}
	if strings.Contains(out, "## Notes") {
		t.Errorf("the body is in the report the renderer draws under:\n%s", out)
	}
}

// Every claim is a row, the corrections carry the line they would write, and
// the report says what it did not do: a script gets the reading and nothing
// is written by it.
func TestTodoGroomReport_IsTheWholeReadingAndWritesNothing(t *testing.T) {
	root := t.TempDir()
	dir := todo.Dir(root)
	must(t, os.MkdirAll(dir, 0o755))
	const item = "---\ntitle: First thing\npriority: high\nsize: S\n---\n\n" +
		"## Acceptance criteria\n- [ ] internal/cache/store.go:88 reads the config\n- [ ] The reader drops a stale entry\n"
	path := filepath.Join(dir, "first.md")
	must(t, os.WriteFile(path, []byte(item), 0o644))
	it, err := todo.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := todo.Groom(it, "claim: - [ ] internal/cache/store.go:88 reads the config\n"+
		"verdict: moved\nnow: - [ ] internal/cache/reader.go:120 reads the config\nevidence: moved in 9f2a11c\n\n"+
		"claim: - [ ] The reader drops a stale entry\nverdict: holds\nevidence: reader.go:44 still does it\n")
	if err != nil {
		t.Fatal(err)
	}
	out := todoGroomReport(it, r).String()
	for _, want := range []string{"moved", "holds", "reader.go:120", "moved in 9f2a11c", "nothing written"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != item {
		t.Errorf("the reading wrote to the file:\n%s", data)
	}
}

// An item every criterion of which the tree already satisfies is named as
// one to archive, and never archived by the command.
func TestTodoGroomReport_NamesAnItemTheTreeHasFinished(t *testing.T) {
	it := todo.Item{Slug: "first", Title: "First thing"}
	r := todo.Reading{Slug: "first", Findings: []todo.Finding{
		{Verdict: todo.VerdictDone, Claim: "- [ ] it works", Criterion: true, Evidence: "cache.go:12 does it"},
	}}
	out := todoGroomReport(it, r).String()
	if !strings.Contains(out, "shhh todo done first") {
		t.Errorf("no archive proposal in:\n%s", out)
	}
}

func TestTodoGroomTargets(t *testing.T) {
	s := todo.Load(todoFixture(t))
	all, err := todoGroomTargets(s, "", true)
	if err != nil || len(all) != len(s.Items) {
		t.Fatalf("--all = %d items, %v", len(all), err)
	}
	one, err := todoGroomTargets(s, "first", false)
	if err != nil || len(one) != 1 || one[0].Slug != "first" {
		t.Fatalf("one = %+v, %v", one, err)
	}
	if _, err := todoGroomTargets(s, "gone", false); err == nil {
		t.Error("an archived item is not one to read against the tree")
	}
	if _, err := todoGroomTargets(s, "", false); err == nil {
		t.Error("naming nothing should be refused")
	}
}
