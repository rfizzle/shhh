package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// laneItem writes one backlog item into a checkout, declaring the paths it
// touches where touches is non-empty.
func laneItem(t *testing.T, root, slug string, touches ...string) {
	t.Helper()
	body := "---\ntitle: " + slug + "\nsize: S\n---\n## Tests\n- true\n"
	if len(touches) > 0 {
		body += "\n## Touches\n"
		for _, p := range touches {
			body += "- `" + p + "`\n"
		}
	}
	if err := os.WriteFile(filepath.Join(todo.Dir(root), slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

var laneSlugIn = regexp.MustCompile(`BACKLOG ITEM (\S+)`)

// laneAnswers is a model that does what each stage asks in the directory the
// stage was spent in, writing the file named after the item at the
// implement stage. hold, where set, is asked at each stage before answering,
// which is how a test arranges who is running when.
type laneAnswers struct {
	mu      sync.Mutex
	running map[string]bool
	// together are the pairs of items that were in a stage at once.
	together [][2]string
	hold     func(slug string, stage run.Stage, dir string)
	write    func(slug string) (file, content string)
}

func (a *laneAnswers) turn(_ context.Context, _ time.Time, dir string, step run.Step) (todoTurn, error) {
	slug := ""
	if m := laneSlugIn.FindStringSubmatch(step.Prompt); m != nil {
		slug = m[1]
	}
	a.mu.Lock()
	for other := range a.running {
		if other != slug {
			a.together = append(a.together, [2]string{slug, other})
		}
	}
	a.running[slug] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.running, slug)
		a.mu.Unlock()
	}()
	if a.hold != nil {
		a.hold(slug, step.Stage, dir)
	}
	text := "?"
	switch step.Stage {
	case run.StageResearch:
		text = headlessPlan
	case run.StageImplement, run.StageRemediate:
		file, content := slug+".go", "package "+strings.ReplaceAll(slug, "-", "")+"\n"
		if a.write != nil {
			file, content = a.write(slug)
		}
		_ = os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644)
		text = "Changed " + file + "."
	case run.StageReview:
		text = "verdict: clean"
	case run.StageCommit:
		text = "COMMIT: Build " + slug + "\n\nBecause.\n\nREPORT: ## Report\nSummary: done."
	}
	return todoTurn{text: text, code: exitDone}, nil
}

func laneDriverFor(t *testing.T, root string, a *laneAnswers) (*todoDriver, *strings.Builder) {
	t.Helper()
	d, _ := headlessDriver(t, root, nil)
	out := &strings.Builder{}
	d.out = out
	a.running = map[string]bool{}
	d.turn = a.turn
	return d, out
}

// Three items whose declared paths meet nowhere are worked at once, each in
// its own copy of the checkout, and each lands as a commit of its own on the
// checkout's branch — one at a time, so the branch holds three commits and no
// merge of two.
func TestTodoRunHeadless_AParallelSprintWorksItemsInLanesAndLandsEachOnTheBranch(t *testing.T) {
	root := todoRepo(t)
	laneItem(t, root, "a-one", "a-one.go")
	laneItem(t, root, "b-two", "b-two.go")
	laneItem(t, root, "c-three", "c-three.go")
	// Every lane waits at research until all three are there, which only a
	// sprint working them at once can satisfy.
	arrived := make(chan struct{}, 3)
	all := make(chan struct{})
	var once sync.Once
	a := &laneAnswers{hold: func(slug string, stage run.Stage, dir string) {
		if stage != run.StageResearch {
			return
		}
		if dir == root {
			t.Errorf("%s was worked in the checkout itself, not in a copy of it", slug)
		}
		arrived <- struct{}{}
		if len(arrived) == 3 {
			once.Do(func() { close(all) })
		}
		select {
		case <-all:
		case <-time.After(30 * time.Second):
			t.Errorf("%s waited alone: the lanes were not worked at once", slug)
		}
	}}
	d, out := laneDriverFor(t, root, a)

	if blocked := d.sprintParallel(context.Background(), 0, 3); blocked {
		t.Fatalf("the sprint should have finished:\n%s", out.String())
	}
	store := todo.Load(todo.BuiltinCode(), root)
	for _, slug := range []string{"a-one", "b-two", "c-three"} {
		if it, ok := store.Find(slug); !ok || !it.Archived {
			t.Fatalf("%s should be archived:\n%s", slug, out.String())
		}
		if _, err := os.Stat(filepath.Join(root, slug+".go")); err != nil {
			t.Fatalf("%s's work should have landed on the checkout: %v", slug, err)
		}
	}
	log, _ := todoGit(root, "log", "--format=%s %p")
	if strings.Count(log, "Build ") != 3 {
		t.Fatalf("one commit per item on the branch:\n%s", log)
	}
	for _, line := range strings.Split(log, "\n") {
		if len(strings.Fields(line)) > 3 {
			t.Fatalf("a landing is a commit with one parent, never a merge:\n%s", log)
		}
	}
	if list, _ := todoGit(root, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Fatalf("every lane's copy is taken away once it has landed:\n%s", list)
	}
	if status, _ := todoGit(root, "status", "--porcelain"); strings.Contains(status, ".go") {
		t.Fatalf("every lane's work is committed: %q", status)
	}
	if _, live := run.Live(root); live {
		t.Fatal("a finished sprint leaves no checkpoint")
	}
	if !strings.Contains(out.String(), "sprint over — 3 items done · "+run.SprintEmpty) {
		t.Fatalf("the ending should name itself:\n%s", out.String())
	}
}

// An item that declares no paths is not refused; it is worked alone, once
// the lanes in front of it have drained, and nothing is taken past it while
// it runs.
func TestTodoRunHeadless_AnUndeclaredItemIsWorkedAlone(t *testing.T) {
	root := todoRepo(t)
	laneItem(t, root, "a-one", "a-one.go")
	laneItem(t, root, "b-two")
	laneItem(t, root, "c-three", "c-three.go")
	a := &laneAnswers{}
	d, out := laneDriverFor(t, root, a)

	if blocked := d.sprintParallel(context.Background(), 0, 3); blocked {
		t.Fatalf("the sprint should have finished:\n%s", out.String())
	}
	for _, pair := range a.together {
		if pair[0] == "b-two" || pair[1] == "b-two" {
			t.Fatalf("b-two declares nothing and ran beside %v:\n%s", pair, out.String())
		}
	}
	store := todo.Load(todo.BuiltinCode(), root)
	for _, slug := range []string{"a-one", "b-two", "c-three"} {
		if it, ok := store.Find(slug); !ok || !it.Archived {
			t.Fatalf("%s should be archived:\n%s", slug, out.String())
		}
	}
}

// removeLaneCopies takes away the copies a test's lanes left standing for a
// person to read, which the temporary directory's own cleanup cannot reach.
func removeLaneCopies(t *testing.T, root string) {
	t.Cleanup(func() {
		list, _ := todoGit(root, "worktree", "list", "--porcelain")
		for _, line := range strings.Split(list, "\n") {
			if dir, ok := strings.CutPrefix(line, "worktree "); ok && filepath.Base(dir) != filepath.Base(root) {
				_, _ = todoGit(root, "worktree", "remove", "--force", dir)
			}
		}
	})
}

// A lane carries what another lane landed into its copy before its next
// step, and where the landing meets the lane's own work the item blocks with
// the collision as the evidence — nobody is there to be steered — and the
// sprint goes on with the other lanes rather than stopping.
func TestTodoRunHeadless_ALaneALandingWillNotCarryIntoBlocksAndTheSprintGoesOn(t *testing.T) {
	root := todoRepo(t)
	laneItem(t, root, "a-one", "a-one.go")
	laneItem(t, root, "b-two", "b-two.go")
	removeLaneCopies(t, root)
	landed := func() bool {
		log, _ := todoGit(root, "log", "--format=%s")
		return strings.Contains(log, "Build a-one")
	}
	a := &laneAnswers{
		// b-two's declared paths said nothing about a-one's file, and it
		// writes one anyway: the declaration is what a reviewer checks, and
		// the reseed is what catches a lane that strayed from it.
		write: func(slug string) (string, string) {
			if slug == "b-two" {
				return "a-one.go", "package other\n"
			}
			return slug + ".go", "package aone\n"
		},
		hold: func(slug string, stage run.Stage, _ string) {
			// b-two finishes building only once a-one has landed, so its
			// next step is the one that finds the branch moved.
			if slug != "b-two" || stage != run.StageImplement {
				return
			}
			deadline := time.Now().Add(30 * time.Second)
			for !landed() && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
		},
	}
	d, out := laneDriverFor(t, root, a)

	blocked := d.sprintParallel(context.Background(), 0, 2)
	if !blocked {
		t.Fatalf("a blocked lane ends the sprint blocked once nothing more can be taken:\n%s", out.String())
	}
	store := todo.Load(todo.BuiltinCode(), root)
	if it, _ := store.Find("a-one"); !it.Archived {
		t.Fatalf("a-one landed and is archived:\n%s", out.String())
	}
	it, _ := store.Find("b-two")
	if it.Status != todo.StatusBlocked || !strings.Contains(it.Body, "does not carry into this lane's copy over a-one.go") {
		t.Fatalf("b-two blocks with the collision as its evidence: %s\n%s", it.Status, it.Body)
	}
	if !strings.Contains(out.String(), "sprint over — 1 item done · "+run.SprintBlocked+": b-two blocked") {
		t.Fatalf("the ending names the item that blocked:\n%s", out.String())
	}
	if strings.Contains(out.String(), "nothing further was attempted") {
		t.Fatalf("a parallel sprint went on past the block, so it does not say it stopped at it:\n%s", out.String())
	}
	if content, _ := os.ReadFile(filepath.Join(root, "a-one.go")); string(content) != "package aone\n" {
		t.Fatalf("the blocked lane wrote nothing onto the checkout: %q", content)
	}
}

// A lane carries what another lane landed into its copy at its next stage
// boundary, so the stages after it read the tree the lane will land into, and
// its own landing is still its own work alone.
func TestTodoRunHeadless_ALaneCarriesAnotherLanesLandingAtItsNextBoundary(t *testing.T) {
	root := todoRepo(t)
	laneItem(t, root, "a-one", "a-one.go")
	laneItem(t, root, "b-two", "b-two.go")
	landed := func() bool {
		log, _ := todoGit(root, "log", "--format=%s")
		return strings.Contains(log, "Build a-one")
	}
	var carried []byte
	a := &laneAnswers{hold: func(slug string, stage run.Stage, dir string) {
		if slug != "b-two" {
			return
		}
		switch stage {
		case run.StageImplement:
			deadline := time.Now().Add(30 * time.Second)
			for !landed() && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
		case run.StageReview:
			carried, _ = os.ReadFile(filepath.Join(dir, "a-one.go"))
		}
	}}
	d, out := laneDriverFor(t, root, a)

	if blocked := d.sprintParallel(context.Background(), 0, 2); blocked {
		t.Fatalf("the sprint should have finished:\n%s", out.String())
	}
	if string(carried) != "package aone\n" {
		t.Fatalf("b-two's copy should hold what a-one landed by its review: %q\n%s", carried, out.String())
	}
	files, _ := todoGit(root, "show", "--name-only", "--format=", "HEAD")
	if strings.TrimSpace(files) != "b-two.go" {
		t.Fatalf("b-two's commit holds its own work alone: %q", files)
	}
}

// A landing that meets what landed before it in the same lines is not
// reconciled by the merge: nothing is written, both patches are kept — the
// first on the checkout, the lane's in its copy — and the answer names the
// file. The window it happens in is a landing between a lane's last boundary
// and its own landing, which no stage can be held in, so the lane is driven
// directly.
func TestTodoLane_ALandingThatConflictsKeepsBothPatches(t *testing.T) {
	root := todoRepo(t)
	shared := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(shared, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "shared.txt"}, {"commit", "-q", "-m", "shared"}} {
		if out, code := todoGit(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	wt, err := subagent.NewWorktree(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wt.Remove)
	if err := os.WriteFile(filepath.Join(wt.Root(), "shared.txt"), []byte("one\nB\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// What an earlier lane landed, on the checkout since the copy was made.
	if err := os.WriteFile(shared, []byte("one\nA\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set := &todoLanes{root: root, out: &strings.Builder{}, top: todoRepoTop(root)}
	lane := &todoLane{set: set, slug: "b-two", wt: wt}

	files, err := lane.land()
	if err == nil || !strings.Contains(err.Error(), "conflicts with what landed on the checkout before it, in shared.txt") {
		t.Fatalf("the landing names the conflicting file: %v (landed %v)", err, files)
	}
	if !lane.kept || lane.landed {
		t.Fatalf("the lane's copy is kept and nothing landed: kept %v, landed %v", lane.kept, lane.landed)
	}
	if content, _ := os.ReadFile(shared); string(content) != "one\nA\nthree\n" {
		t.Fatalf("the checkout keeps what landed first, with no conflict markers: %q", content)
	}
	if content, _ := os.ReadFile(filepath.Join(wt.Root(), "shared.txt")); string(content) != "one\nB\nthree\n" {
		t.Fatalf("the lane's patch is kept in its copy: %q", content)
	}
	if len(set.landings) != 0 {
		t.Fatal("a landing that wrote nothing is nothing for the other lanes to carry")
	}
}

// A stop asked for from another surface interrupts every lane at the step it
// is on and puts the item back to open, and the sprint ends stopped.
func TestTodoRunHeadless_AParallelSprintStopsWhenAsked(t *testing.T) {
	root := todoRepo(t)
	laneItem(t, root, "a-one", "a-one.go")
	removeLaneCopies(t, root)
	a := &laneAnswers{}
	d, out := laneDriverFor(t, root, a)
	d.turn = func(ctx context.Context, deadline time.Time, dir string, step run.Step) (todoTurn, error) {
		if step.Stage == run.StageImplement {
			_ = os.WriteFile(filepath.Join(dir, "a-one.go"), []byte("package aone\n"), 0o644)
			if err := run.RequestStop(root); err != nil {
				t.Error(err)
			}
			select {
			case <-ctx.Done():
				return todoTurn{}, ctx.Err()
			case <-time.After(30 * time.Second):
				t.Error("the stop never reached the lane's step")
			}
		}
		return a.turn(ctx, deadline, dir, step)
	}

	d.sprintParallel(context.Background(), 0, 2)
	if !strings.Contains(out.String(), "sprint over — 0 items done · "+run.SprintStopped) {
		t.Fatalf("the sprint ends stopped:\n%s", out.String())
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("a-one"); it.Status != todo.StatusOpen {
		t.Fatalf("the stopped item is open again, not %s", it.Status)
	}
	if !strings.Contains(out.String(), "its work so far is kept in") {
		t.Fatalf("the stop names where the lane's work is:\n%s", out.String())
	}
	if run.StopRequested(root) {
		t.Fatal("the request is taken away once it has been answered")
	}
}

// A sprint picked up after its process died cannot continue the lanes that
// process was working — their copies went with it — so each of their items
// blocks naming where its copy was, the spend they had written is counted
// once, and the sprint goes on with the rest.
func TestTodoRunHeadless_AParallelSprintAnswersForTheLanesADeadProcessLeft(t *testing.T) {
	root := todoRepo(t)
	laneItem(t, root, "a-one", "a-one.go")
	laneItem(t, root, "b-two", "b-two.go")
	if err := todo.SetStatus(filepath.Join(todo.Dir(root), "a-one.md"), todo.StatusInProgress); err != nil {
		t.Fatal(err)
	}
	dead := &run.Sprint{Session: "dead", Parallel: 2, Attempts: []string{"a-one"}, CapCents: 10000,
		Lanes: []run.SprintLane{{Slug: "a-one", Paths: []string{"a-one.go"}, Tree: "/tmp/shhh-agent-gone",
			Stage: run.StageImplement, Ledger: "dead#1/a-one", Turns: 2, Cost: 1.5}}}
	if err := dead.Save(root); err != nil {
		t.Fatal(err)
	}
	a := &laneAnswers{}
	d, out := laneDriverFor(t, root, a)

	if n := todoParallelism(root, 0); n != 2 {
		t.Fatalf("a continued sprint keeps the number of lanes it was started with, got %d", n)
	}
	if blocked := d.sprintParallel(context.Background(), 0, 2); !blocked {
		t.Fatalf("the orphaned item blocks the sprint's ending:\n%s", out.String())
	}
	store := todo.Load(todo.BuiltinCode(), root)
	if it, _ := store.Find("a-one"); it.Status != todo.StatusBlocked || !strings.Contains(it.Body, "/tmp/shhh-agent-gone") {
		t.Fatalf("a-one blocks naming its copy: %s\n%s", it.Status, it.Body)
	}
	if it, _ := store.Find("b-two"); !it.Archived {
		t.Fatalf("the sprint goes on with b-two:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "spend $1.50 of $100") {
		t.Fatalf("what the dead lane spent is counted:\n%s", out.String())
	}
}

// The chat starts a parallel sprint as this runner in a process of its own,
// with its lines in a log beside the checkpoint.
func TestTodoParallelStarter_StartsTheRunnerWithItsLinesInALog(t *testing.T) {
	root := t.TempDir()
	back := todoRunnerBin
	t.Cleanup(func() { todoRunnerBin = back })
	todoRunnerBin = func() (string, error) { return "/bin/echo", nil }
	log, err := todoParallelStarter(root)([]string{"--all", "--parallel", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if log != filepath.Join(run.Dir(root), "sprint.log") {
		t.Fatalf("the log is beside the checkpoint, got %s", log)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, _ := os.ReadFile(log); strings.Contains(string(data), "todo run --all --parallel 3") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(log)
	t.Fatalf("the runner was started as `todo run` with the sprint's answers; the log holds %q", data)
}

// --parallel is a sprint's answer, and a count of items.
func TestTodoRunHeadless_ParallelNeedsASprint(t *testing.T) {
	for _, flags := range []todoRunFlags{{parallel: 3}, {all: true, parallel: -1}} {
		if err := todoRunHeadless(newTodoRunCmd(), "", flags); err == nil {
			t.Errorf("%+v should be refused", flags)
		}
	}
}
