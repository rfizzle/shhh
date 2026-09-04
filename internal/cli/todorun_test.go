package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

const headlessPlan = "## Plan: do it\n\n1. Change a.go\n   files: a.go\n   action: edit\n\nsize: S\nquestions: none\n"

// todoRepo is a checkout with a backlog and a git history, which is what a
// run that ends in a commit needs.
func todoRepo(t *testing.T, slugs ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "-q", "--allow-empty", "-m", "seed"},
	} {
		if out, code := todoGit(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	dir := todo.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, slug := range slugs {
		body := "---\ntitle: " + slug + "\nsize: S\n---\n## Tests\n- true\n"
		if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// headlessDriver is the driver over a checkout, with the stage turns answered
// by a stub instead of by a process: the loop and the gates are what these
// tests are about, and standing up a provider to reach them would test
// neither.
func headlessDriver(t *testing.T, root string, answer func(run.Step) string) (*todoDriver, *bytes.Buffer) {
	t.Helper()
	withProjectTrust(t, project.Trust{})
	out := &bytes.Buffer{}
	d, err := newTodoDriver(out, root, config.Config{}, false)
	if err != nil {
		t.Fatal(err)
	}
	d.turn = func(_ context.Context, _ time.Time, step run.Step) (todoTurn, error) {
		return todoTurn{text: answer(step), code: exitDone}, nil
	}
	return d, out
}

// stageAnswers is a model that does what each stage asks, and writes a file
// at the implement stage so the run has something to review and commit.
func stageAnswers(root string) func(run.Step) string {
	return func(step run.Step) string {
		switch step.Stage {
		case run.StageResearch:
			return headlessPlan
		case run.StageImplement, run.StageRemediate:
			_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a // "+time.Now().String()+"\n"), 0o644)
			return "Changed a.go."
		case run.StageReview:
			return "verdict: clean"
		case run.StageCommit:
			return "COMMIT: Change a\n\nBecause.\n\nREPORT: ## Report\nSummary: done."
		}
		return "?"
	}
}

// The whole loop: two items, one at a time, each archived after a real commit
// of its own, and an exit status saying nothing objected.
func TestTodoRunHeadless_SprintCommitsAndArchivesEachItem(t *testing.T) {
	root := todoRepo(t, "a-one", "b-two")
	d, out := headlessDriver(t, root, stageAnswers(root))

	if blocked := d.sprint(context.Background(), 0); blocked {
		t.Fatalf("the sprint should have finished:\n%s", out.String())
	}
	store := todo.Load(todo.BuiltinCode(), root)
	for _, slug := range []string{"a-one", "b-two"} {
		if it, ok := store.Find(slug); !ok || !it.Archived {
			t.Fatalf("%s should be archived: %+v", slug, it)
		}
	}
	if log, _ := todoGit(root, "log", "--format=%s"); strings.Count(log, "Change a") != 2 {
		t.Fatalf("one commit per item, got:\n%s", log)
	}
	if _, live := run.Live(root); live {
		t.Fatal("a finished sprint leaves no checkpoint")
	}
	if !strings.Contains(out.String(), "sprint over") || !strings.Contains(out.String(), run.SprintEmpty) {
		t.Fatalf("the ending should name itself:\n%s", out.String())
	}
	// The backlog is never committed on the project's behalf, whatever the
	// run changed.
	if status, _ := todoGit(root, "status", "--porcelain"); !strings.Contains(status, todo.StateDir) {
		t.Fatalf("the backlog files should be left unstaged and uncommitted: %q", status)
	}
}

// A block stops the sprint where it is, and the process says so with the one
// code the runner adds to the closed set.
func TestTodoRunHeadless_BlockStopsTheSprintAndExitsSeven(t *testing.T) {
	root := todoRepo(t, "a-one", "b-two")
	d, out := headlessDriver(t, root, func(step run.Step) string {
		if step.Stage == run.StageResearch {
			return "## Plan: x\n\n1. a\n\nsize: S\nquestions:\n- keep the flag?\n"
		}
		return "?"
	})

	blocked := d.sprint(context.Background(), 0)
	if !blocked {
		t.Fatalf("an open question on a small item blocks:\n%s", out.String())
	}
	err := exitOf(blocked)
	var ee exitError
	if !asExitError(err, &ee) || ee.code != exitBlocked {
		t.Fatalf("a blocked run should carry the blocked code, got %v", err)
	}
	store := todo.Load(todo.BuiltinCode(), root)
	if it, _ := store.Find("a-one"); it.Status != todo.StatusBlocked || !strings.Contains(it.Body, "## Blocked") {
		t.Fatalf("the evidence should be on the item: %+v", it)
	}
	if it, _ := store.Find("b-two"); it.Status != todo.StatusOpen {
		t.Fatalf("nothing further is attempted: b-two is %s", it.Status)
	}
}

// A sprint asked for without commits leaves each item's work in the tree, so
// what the tree already held has to be read again at each item: the baseline
// from before the sprint would hand every one of the first item's files to
// the second as its own.
func TestTodoRunHeadless_NoCommitSprintKeepsTheItemsApart(t *testing.T) {
	root := todoRepo(t, "a-one", "b-two")
	withProjectTrust(t, project.Trust{})
	out := &bytes.Buffer{}
	d, err := newTodoDriver(out, root, config.Config{}, true)
	if err != nil {
		t.Fatal(err)
	}
	d.turn = func(_ context.Context, _ time.Time, step run.Step) (todoTurn, error) {
		if step.Stage == run.StageImplement {
			// One file per item, so what each run may claim is decidable.
			name := "a.go"
			if strings.Contains(step.Prompt, "b-two") {
				name = "b.go"
			}
			_ = os.WriteFile(filepath.Join(root, name), []byte("package a\n"), 0o644)
			return todoTurn{text: "Changed " + name + ".", code: exitDone}, nil
		}
		if step.Stage == run.StageResearch {
			return todoTurn{text: headlessPlan, code: exitDone}, nil
		}
		return todoTurn{text: "verdict: clean", code: exitDone}, nil
	}

	if blocked := d.sprint(context.Background(), 0); blocked {
		t.Fatalf("the sprint should have finished:\n%s", out.String())
	}
	second, ok := todo.Load(todo.BuiltinCode(), root).Find("b-two")
	if !ok || !second.Archived {
		t.Fatalf("b-two should be archived: %+v", second)
	}
	if strings.Contains(second.Body, "a.go") {
		t.Fatalf("the second item claimed the first item's file:\n%s", second.Body)
	}
	if !strings.Contains(second.Body, "b.go") {
		t.Fatalf("the second item should name its own file:\n%s", second.Body)
	}
}

// --max bounds how many items the sprint starts.
func TestTodoRunHeadless_MaxRunsOneAndStops(t *testing.T) {
	root := todoRepo(t, "a-one", "b-two")
	d, out := headlessDriver(t, root, stageAnswers(root))

	if blocked := d.sprint(context.Background(), 1); blocked {
		t.Fatalf("the cap is not a block:\n%s", out.String())
	}
	store := todo.Load(todo.BuiltinCode(), root)
	if it, _ := store.Find("b-two"); it.Archived {
		t.Fatal("the second item should not have been started")
	}
	if !strings.Contains(out.String(), run.SprintCapped) {
		t.Fatalf("the ending should name the cap:\n%s", out.String())
	}
}

// An implement stage that changed nothing has produced nothing to review and
// nothing to commit, and another round over the same plan would produce the
// same nothing. It is a block rather than a retry.
func TestTodoRunHeadless_AnUnchangedTreeBlocks(t *testing.T) {
	root := todoRepo(t, "a-one")
	answers := stageAnswers(root)
	d, _ := headlessDriver(t, root, func(step run.Step) string {
		if step.Stage == run.StageImplement {
			return "Nothing needed changing."
		}
		return answers(step)
	})

	st := d.work(context.Background(), mustItem(t, root, "a-one"), nil)
	if st.Stage != run.StageBlocked || !strings.Contains(st.Blocked, "changed no files") {
		t.Fatalf("state = %+v", st)
	}
	if st.Round != 0 {
		t.Fatalf("a tree nothing changed is not a remediation round: round %d", st.Round)
	}
}

// The pause gate asks a person, and there is nobody here to ask. Guessing the
// answer is the one thing a deterministic runner must not do.
func TestTodoRunHeadless_APauseBlocks(t *testing.T) {
	root := todoRepo(t, "a-one")
	d, _ := headlessDriver(t, root, func(run.Step) string {
		return "## Plan: big\n\n1. a\n   files: a.go\n\nsize: L\nquestions: none\n"
	})

	st := d.work(context.Background(), mustItem(t, root, "a-one"), nil)
	if st.Stage != run.StageBlocked || !strings.Contains(st.Blocked, "nobody to ask") {
		t.Fatalf("state = %+v", st)
	}
}

// A run picked up in another process continues from the stage its checkpoint
// names rather than starting the item over.
func TestTodoRunHeadless_ContinuesACheckpoint(t *testing.T) {
	root := todoRepo(t, "a-one")
	d, _ := headlessDriver(t, root, stageAnswers(root))
	it := mustItem(t, root, "a-one")
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		t.Fatal(err)
	}
	st := run.Start(it, "earlier", "", 0, run.Options{Repo: true})
	st.Stage, st.Grade, st.Plan = run.StageCommit, "S", headlessPlan
	st.Paths = []string{"a.go"}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(root); err != nil {
		t.Fatal(err)
	}

	out := d.work(context.Background(), mustItem(t, root, "a-one"), nil)
	if out.Stage != run.StageDone {
		t.Fatalf("the continued run should have committed and finished: %+v", out)
	}
	if log, _ := todoGit(root, "log", "--format=%s", "-1"); log != "Change a" {
		t.Fatalf("subject = %q", log)
	}
}

// The cap ends the item with evidence naming it rather than leaving the
// process running for as long as a provider is willing to answer.
func TestTodoRunHeadless_ItemTimeoutBlocks(t *testing.T) {
	root := todoRepo(t, "a-one")
	d, _ := headlessDriver(t, root, stageAnswers(root))
	d.itemTimeout = time.Nanosecond

	st := d.work(context.Background(), mustItem(t, root, "a-one"), nil)
	if st.Stage != run.StageBlocked || !strings.Contains(st.Blocked, "ran past the cap") {
		t.Fatalf("state = %+v", st)
	}
}

func TestTodoRunHeadless_FlagsThatContradictEachOther(t *testing.T) {
	for _, c := range []struct {
		slug  string
		flags todoRunFlags
		want  string
	}{
		{"a-one", todoRunFlags{all: true}, "does not take an item"},
		{"", todoRunFlags{all: true, next: true}, "two different requests"},
		{"a-one", todoRunFlags{next: true}, "does not take one as well"},
		{"", todoRunFlags{max: 2}, "needs --all"},
		{"", todoRunFlags{all: true, max: -1}, "whole items"},
	} {
		err := todoRunHeadless(newTodoRunCmd(), c.slug, c.flags)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("todoRunHeadless(%q, %+v) = %v, want %q", c.slug, c.flags, err, c.want)
		}
	}
}

func TestTodoRunTarget(t *testing.T) {
	root := todoRepo(t, "a-one", "b-two")
	blocked := filepath.Join(todo.Dir(root), "c-three.md")
	if err := os.WriteFile(blocked, []byte("---\ntitle: three\nstatus: blocked\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waits := filepath.Join(todo.Dir(root), "d-four.md")
	if err := os.WriteFile(waits, []byte("---\ntitle: four\ndepends_on: a-one\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := todo.Load(todo.BuiltinCode(), root)
	if it, err := todoRunTarget(s, ""); err != nil || it.Slug != "a-one" {
		t.Fatalf("the next ready item = %q/%v", it.Slug, err)
	}
	if it, err := todoRunTarget(s, "b-two"); err != nil || it.Slug != "b-two" {
		t.Fatalf("a named item = %q/%v", it.Slug, err)
	}
	for _, c := range []struct{ slug, want string }{
		{"nope", "no active backlog item"},
		{"c-three", "is blocked"},
		{"d-four", "waits on a-one"},
	} {
		if _, err := todoRunTarget(s, c.slug); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("todoRunTarget(%q) = %v, want %q", c.slug, err, c.want)
		}
	}
}

// What the run may stage is read out of git's own status, and the backlog is
// never part of it.
func TestTodoPorcelainPaths(t *testing.T) {
	status := " M internal/agent/loop.go\n?? new.go\nR  old.go -> moved.go\n M .shhh/todo/a-one.md\n"
	got := todoPorcelainPaths(status)
	for _, want := range []string{"internal/agent/loop.go", "new.go", "moved.go"} {
		if !got[want] {
			t.Errorf("%s should be in the set: %v", want, got)
		}
	}
	if got[filepath.Join(todo.StateDir, todo.Subdir, "a-one.md")] || len(got) != 3 {
		t.Errorf("the backlog must never be staged: %v", got)
	}
}

func mustItem(t *testing.T, root, slug string) todo.Item {
	t.Helper()
	it, ok := todo.Load(todo.BuiltinCode(), root).Find(slug)
	if !ok {
		t.Fatalf("no item %q", slug)
	}
	return it
}

// asExitError is errors.As without the import ceremony at each call.
func asExitError(err error, target *exitError) bool {
	e, ok := err.(exitError)
	if ok {
		*target = e
	}
	return ok
}

// The stage's own process finishes a reply the ceiling cut, once, and says
// when it could not. A stage answer that came back cut anyway stops the item
// with that written on it: nobody is here to read half a review and notice.
func TestTodoRunHeadless_ACutStageAnswerBlocksTheItem(t *testing.T) {
	root := todoRepo(t, "a-one")
	d, out := headlessDriver(t, root, nil)
	d.turn = func(_ context.Context, _ time.Time, step run.Step) (todoTurn, error) {
		return todoTurn{text: headlessPlan, code: exitDone, truncated: true}, nil
	}

	st := d.work(context.Background(), todo.Load(todo.BuiltinCode(), root).Items[0], nil)

	if st.Stage != run.StageBlocked {
		t.Fatalf("half an answer must not be graded, stage %s:\n%s", st.Stage, out.String())
	}
	if st.Blocked != run.CutAtCeiling(run.StageResearch) {
		t.Fatalf("the ceiling should be the evidence, got %q", st.Blocked)
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("a-one"); it.Status != todo.StatusBlocked {
		t.Fatalf("the item should be blocked, is %s", it.Status)
	}
}

// A whole answer says so by saying nothing, which is how every reader of the
// stage transcript already read it.
func TestTodoRunHeadless_AWholeStageAnswerIsRead(t *testing.T) {
	root := todoRepo(t, "a-one")
	d, _ := headlessDriver(t, root, stageAnswers(root))

	if st := d.work(context.Background(), todo.Load(todo.BuiltinCode(), root).Items[0], nil); st.Stage != run.StageDone {
		t.Fatalf("an answer the model finished is the stage's answer, stage %s", st.Stage)
	}
}

// withBacklogProfile states the profile this process works under, the way a
// settings file resolved into one would. The reader is a variable for
// exactly this: writing a directory to imply the profile would test the
// resolution rather than the run.
func withBacklogProfile(t *testing.T, words todo.Profile, pipeline run.Pipeline) {
	t.Helper()
	held := backlogProfile{words: words, pipeline: pipeline, from: builtinProfileFrom}
	backlogProfileIs = func() backlogProfile { return held }
	t.Cleanup(func() { backlogProfileIs = heldBacklogProfile })
}

// aBacklogOf is a backlog written in another profile's vocabulary, with no
// repository under it: a run whose steps only read wants none, and a
// checkout is the fact this case is about not needing.
func aBacklogOf(t *testing.T, root string, header string, slugs ...string) string {
	t.Helper()
	dir := todo.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, slug := range slugs {
		body := "---\ntitle: " + slug + "\n" + header + "---\n## Acceptance Criteria\n- [ ] say what the sources say\n"
		if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// readingAnswers is a model that does what each step of the shipped reading
// profile asks.
func readingAnswers(step run.Step) string {
	switch step.Stage {
	case "scope":
		return "## Plan: read it\n\n1. Read the paper\n   files: paper.md\n   action: read\n\ndepth: quick\nquestions: none\n"
	case "review":
		return "verdict: clean"
	case "file":
		return "REPORT:\n## Report\nSummary: the sources agree.\n"
	}
	return "The sources say tabs."
}

// A run whose every step reads is spent in conversations from end to end.
// Nothing about it wants the coding agent's editor, its command runner or a
// record of what it changed, and an unattended sprint over a reading list
// must not start one to find that out.
func TestTodoRunHeadless_AReadingRunIsSpentInConversations(t *testing.T) {
	words, pipeline, err := run.BuiltinProfile("research")
	if err != nil {
		t.Fatal(err)
	}
	withBacklogProfile(t, words, pipeline)
	root := aBacklogOf(t, t.TempDir(), "kind: reading\ndepth: quick\n", "a-one")
	d, out := headlessDriver(t, root, nil)
	var argv [][]string
	d.turn = func(_ context.Context, _ time.Time, step run.Step) (todoTurn, error) {
		// The argv the stage's own process would be started with, which is
		// the whole of what the choice of process is.
		argv = append(argv, todoStageArgs(d.steps().Writes(), step.Mode))
		return todoTurn{text: readingAnswers(step), code: exitDone}, nil
	}

	st := d.work(context.Background(), mustReading(t, root, "a-one"), nil)
	if st.Stage != run.StageDone {
		t.Fatalf("the reading stopped at %s:\n%s", st.Stage, out.String())
	}
	if len(argv) == 0 {
		t.Fatal("the run spent no turn at all")
	}
	for _, args := range argv {
		if args[0] != "chat" {
			t.Fatalf("a step that only reads was spent as %v", args)
		}
	}
	if it, _ := todo.Load(words, root).Find("a-one"); !it.Archived {
		t.Fatalf("the reading should be archived: %+v", it)
	}
}

// A reading handed to somebody who did not do it has no change to point at,
// and the run that never writes is the run that has none by design. The
// reader is given what the run gathered instead of being turned away for a
// tree nothing was going to touch.
func TestTodoRunHeadless_AReadingIsReviewedWithNoChangeToPointAt(t *testing.T) {
	dir := t.TempDir()
	aReadingProfile(t, dir)
	words, pipeline, err := run.LoadProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	withBacklogProfile(t, words, pipeline)
	root := aBacklogOf(t, t.TempDir(), "kind: reading\n", "a-one")
	d, out := headlessDriver(t, root, nil)
	var argv [][]string
	d.turn = func(_ context.Context, _ time.Time, step run.Step) (todoTurn, error) {
		argv = append(argv, todoStageArgs(d.steps().Writes(), step.Mode))
		if step.Stage == "check" {
			return todoTurn{text: "verdict: clean", code: exitDone}, nil
		}
		return todoTurn{text: "The sources say tabs.", code: exitDone}, nil
	}

	st := d.work(context.Background(), mustReading(t, root, "a-one"), nil)
	if st.Stage != run.StageDone {
		t.Fatalf("the reading stopped at %s (%s):\n%s", st.Stage, st.Blocked, out.String())
	}
	if len(argv) != 2 {
		t.Fatalf("a reading and a reading of it are two turns, spent %d: %v", len(argv), argv)
	}
	for _, args := range argv {
		if args[0] != "chat" {
			t.Fatalf("a step that only reads was spent as %v", args)
		}
	}
}

// aReadingProfile writes a profile whose steps only read and whose reading is
// handed to somebody else at every grade, which the shipped reading profile
// does only at a grade an unattended run stops at.
func aReadingProfile(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, run.ProfileWordings), 0o755); err != nil {
		t.Fatal(err)
	}
	table := `
name = "reading"
noun = "reading"

[[field]]
name = "kind"
values = [{ name = "reading" }]

[[field]]
name = "priority"

[[step]]
name = "read"
kind = "turn"
mode = "read"
reads = ["findings"]

[[step]]
name = "check"
kind = "agent"
mode = "read"
blocks = ["findings"]

[[step]]
name = "file"
kind = "finish"
finish = "archive"
`
	files := map[string]string{
		run.ProfileFile: table,
		filepath.Join(run.ProfileWordings, run.WordingStandards+".md"): "STANDARDS.\n",
		filepath.Join(run.ProfileWordings, "read.md"):                  "READ IT.\n",
		filepath.Join(run.ProfileWordings, "check.md"):                 "CHECK IT.\n{{findings}}\n",
		filepath.Join(run.ProfileWordings, "check_task.md"):            "CHECK IT.\n{{findings}}\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// mustReading is the item as the profile in force reads it.
func mustReading(t *testing.T, root, slug string) todo.Item {
	t.Helper()
	it, ok := todo.Load(todoProfile(), root).Find(slug)
	if !ok {
		t.Fatalf("no item %q", slug)
	}
	return it
}

// A reading that stops is the one code the runner adds to the closed set,
// whichever profile the backlog is written in: what a run left behind is the
// run's own ending, and not the ending of the process a stage was spent as.
func TestTodoRunHeadless_AReadingThatBlocksExitsSeven(t *testing.T) {
	words, pipeline, err := run.BuiltinProfile("research")
	if err != nil {
		t.Fatal(err)
	}
	withBacklogProfile(t, words, pipeline)
	root := aBacklogOf(t, t.TempDir(), "kind: reading\ndepth: quick\n", "a-one")
	d, _ := headlessDriver(t, root, func(step run.Step) string {
		if step.Stage == "scope" {
			return "## Plan: read it\n\n1. Read it\n   files: paper.md\n\ndepth: quick\nquestions:\n- which edition?\n"
		}
		return "?"
	})

	st := d.work(context.Background(), mustReading(t, root, "a-one"), nil)
	if st.Stage != run.StageBlocked {
		t.Fatalf("an open question on a reading that never pauses blocks, stage %s", st.Stage)
	}
	var ee exitError
	if err := exitOf(true); !asExitError(err, &ee) || ee.code != exitBlocked {
		t.Fatalf("a blocked reading should carry the blocked code, got %v", err)
	}
}

// A reading picked up from a checkpoint is worked under its own profile's
// steps. The checkpoint does not carry them — the words on an item are read
// from the project every time — so a run continued without them stated would
// be worked through the stages a checkout of code has, and refused for
// having changed shape on its way there.
func TestTodoRunHeadless_AContinuedReadingKeepsItsOwnSteps(t *testing.T) {
	words, pipeline, err := run.BuiltinProfile("research")
	if err != nil {
		t.Fatal(err)
	}
	withBacklogProfile(t, words, pipeline)
	root := aBacklogOf(t, t.TempDir(), "kind: reading\ndepth: quick\n", "a-one")
	it := mustReading(t, root, "a-one")
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		t.Fatal(err)
	}
	st := run.Start(it, "earlier", "", 0, run.Options{Pipeline: pipeline})
	st.Stage, st.Grade, st.Plan = "gather", "quick", "## Plan: read it\n\n1. Read the paper\n"
	if err := st.Save(root); err != nil {
		t.Fatal(err)
	}

	d, out := headlessDriver(t, root, nil)
	var stages []string
	d.turn = func(_ context.Context, _ time.Time, step run.Step) (todoTurn, error) {
		stages = append(stages, string(step.Stage))
		return todoTurn{text: readingAnswers(step), code: exitDone}, nil
	}

	got := d.work(context.Background(), mustReading(t, root, "a-one"), nil)
	if got.Stage != run.StageDone {
		t.Fatalf("the continued reading stopped at %s (%s):\n%s", got.Stage, got.Blocked, out.String())
	}
	if strings.Join(stages, " ") != "gather review" {
		t.Fatalf("the continued run took the stages %v", stages)
	}
}

// stageBinary stands in for this executable where a test wants the argv a
// stage's process was really started with. It writes its arguments down, one
// per line, and answers with the transcript shape a stage is read out of.
func stageBinary(t *testing.T, argv string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell to stand in for the binary")
	}
	bin := filepath.Join(t.TempDir(), "stage")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argv + "'\nprintf '{\"final\":\"answered\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// The argv a stage is spent as, read off a process that was really started.
// The choice is made where the process is built, and a test that stubs the
// turn out reads back its own arithmetic instead of the run's.
func TestTodoDriver_TheStageProcessIsStartedWithTheArgsItsModeChose(t *testing.T) {
	research, researchSteps, err := run.BuiltinProfile("research")
	if err != nil {
		t.Fatal(err)
	}
	code, codeSteps, err := run.BuiltinProfile("code")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		words  todo.Profile
		steps  run.Pipeline
		mode   run.Mode
		want   string
		header string
	}{{
		name: "a step of a run that never writes", words: research, steps: researchSteps,
		mode: run.ModePlan, header: "kind: reading\ndepth: quick\n",
		want: "chat --print --output json the prompt",
	}, {
		name: "a step that changes the tree", words: code, steps: codeSteps,
		mode: run.ModeAuto, header: "size: S\n",
		want: "code --print --output json --yes the prompt",
	}, {
		name: "a reading step of a run that writes", words: code, steps: codeSteps,
		mode: run.ModePlan, header: "size: S\n",
		want: "code --print --output json the prompt",
	}} {
		t.Run(c.name, func(t *testing.T) {
			withBacklogProfile(t, c.words, c.steps)
			root := aBacklogOf(t, t.TempDir(), c.header, "a-one")
			d, _ := headlessDriver(t, root, nil)
			argv := filepath.Join(t.TempDir(), "argv")
			d.bin = stageBinary(t, argv)

			got, err := d.ask(context.Background(), time.Time{},
				run.Step{Stage: "scope", Mode: c.mode, Prompt: "the prompt"})
			if err != nil {
				t.Fatalf("the stage did not run: %v", err)
			}
			if got.text != "answered" {
				t.Fatalf("the stage's answer is %q", got.text)
			}
			started, err := os.ReadFile(argv)
			if err != nil {
				t.Fatal(err)
			}
			if line := strings.Join(strings.Fields(string(started)), " "); line != c.want {
				t.Fatalf("the stage was started as %q, want %q", line, c.want)
			}
		})
	}
}
