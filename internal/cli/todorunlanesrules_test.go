package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/subagent/worktree"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// twoLanesOnOneLine is a sprint of two lanes over a checkout holding one file
// with a line `n = 2` in it: a-one writes that file as aText and lands first,
// and b-two writes it as bText and finishes verifying only once a-one has
// landed, so its next step is the one that carries the landing.
func twoLanesOnOneLine(t *testing.T, aText, bText string) (*todoDriver, string, func() string) {
	t.Helper()
	root := todoRepo(t)
	if err := os.WriteFile(filepath.Join(root, "count.txt"), []byte("x\nn = 2\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "count.txt"}, {"commit", "-q", "-m", "count"}} {
		if out, code := run.Git(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	laneItem(t, root, "a-one", "a-one.go")
	laneItem(t, root, "b-two", "b-two.go")
	removeLaneCopies(t, root)
	started := newLaneStarted()
	landed := func() bool {
		log, _ := run.Git(root, "log", "--format=%s")
		return strings.Contains(log, "Build a-one")
	}
	a := &laneAnswers{
		write: func(slug string) (string, string) {
			if slug == "b-two" {
				return "count.txt", bText
			}
			return "count.txt", aText
		},
		hold: func(slug string, stage run.Stage, _ string) {
			switch {
			case slug == "b-two" && stage == run.StageResearch:
				started.arrive()
			case slug == "a-one" && stage == run.StageImplement:
				started.wait(t, slug)
			case slug == "b-two" && stage == run.StageReview:
				eventually(landed)
			}
		},
	}
	d, out := laneDriverFor(t, root, a)
	return d, root, out.String
}

// Two lanes that each raised one count by one meet at the landing, and the
// rule settles it: the lane carries the sum, verifies again and lands, with
// both items' changes on the checkout and no model told anything.
func TestTodoRunHeadless_ACollisionSettledByRuleVerifiesAgain(t *testing.T) {
	d, root, out := twoLanesOnOneLine(t, "x\nn = 3\ny\n", "x\nn = 4\ny\n")
	if blocked := d.sprintParallel(context.Background(), 0, 2); blocked {
		t.Fatalf("a count both lanes raised is settled, not blocked:\n%s", out())
	}
	store := todo.Load(todo.BuiltinCode(), root)
	for _, slug := range []string{"a-one", "b-two"} {
		if it, _ := store.Find(slug); !it.Archived {
			t.Fatalf("%s should have landed:\n%s", slug, out())
		}
	}
	got, _ := os.ReadFile(filepath.Join(root, "count.txt"))
	if string(got) != "x\nn = 5\ny\n" {
		t.Fatalf("the checkout holds both raises: %q", got)
	}
	if !strings.Contains(out(), "b-two verifying again · a-one landed") {
		t.Fatalf("a settled carry is verified again:\n%s", out())
	}
}

// A region neither rule fits blocks the lane as a collision always did: the
// regions quoted, the checkout holding the landed item alone, no marker in it.
func TestTodoRunHeadless_AMarkedRegionStillBlocksAndKeepsBothPatches(t *testing.T) {
	d, root, out := twoLanesOnOneLine(t, "x\nn = 3\ny\n", "x\nn = 1\ny\n")
	if blocked := d.sprintParallel(context.Background(), 0, 2); !blocked {
		t.Fatalf("a region no rule settles blocks the lane:\n%s", out())
	}
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("b-two")
	if it.Status != todo.StatusBlocked || !strings.Contains(it.Body, "no rule settles the regions below") ||
		!strings.Contains(it.Body, "over count.txt") || !strings.Contains(it.Body, "@@ line 2") {
		t.Fatalf("b-two blocks naming the file and quoting the region: %s\n%s", it.Status, it.Body)
	}
	got, _ := os.ReadFile(filepath.Join(root, "count.txt"))
	if string(got) != "x\nn = 3\ny\n" {
		t.Fatalf("the checkout holds the landed item alone, unmarked: %q", got)
	}
}

// laneAtItsCommitOnCount is a lane at its commit step whose copy holds
// count.txt as laneText over a checkout that has since landed `n = 3` on it,
// from the same `n = 2` the copy was taken at.
func laneAtItsCommitOnCount(t *testing.T, tests, laneText string) (*todoDriver, *todoLane, *run.State, todo.Item, string) {
	t.Helper()
	root := todoRepo(t)
	commitCount(t, root, "x\nn = 2\ny\n", "count")
	laneItem(t, root, "b-two", "b-two.go")
	removeLaneCopies(t, root)
	d, _ := laneDriverFor(t, root, &laneAnswers{})
	wt, err := worktree.NewWorktree(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wt.Remove)
	if err := os.WriteFile(filepath.Join(wt.Root(), "count.txt"), []byte(laneText), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := run.StartSprint("s", "", 0, false)
	set := &todoLanes{root: root, out: d.out, sp: sp, top: todoRepoTop(root)}
	lane := &todoLane{set: set, slug: "b-two", wt: wt}
	d.lane, d.tree = lane, wt.Root()
	patch := "diff --git a/count.txt b/count.txt\n--- a/count.txt\n+++ b/count.txt\n@@ -1,3 +1,3 @@\n x\n-n = 2\n+n = 3\n y\n"
	if err := worktree.ApplyPatch(root, patch); err != nil {
		t.Fatal(err)
	}
	commitCount(t, root, "x\nn = 3\ny\n", "Build a-one")
	set.landings = append(set.landings, todoLanding{slug: "a-one", patch: patch})
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("b-two")
	st := run.Start(it, "s", "", 0, run.Options{Pipeline: d.pipeline, Repo: true})
	st.Stage, st.Verified = run.StageCommit, true
	st.Tests = []string{tests}
	st.Message = "Build b-two\n\nBecause."
	return d, lane, st, it, root
}

// commitCount writes count.txt on the checkout and commits it.
func commitCount(t *testing.T, root, text, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "count.txt"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "count.txt"}, {"commit", "-q", "-m", message}} {
		if out, code := run.Git(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
}

// The commit-time carry meets the same collisions a boundary carry does: one
// a rule settles is verified in the copy and lands, and one no rule settles
// blocks with the copy put back to the lane's own patch.
func TestTodoRunHeadless_ACollisionAtTheCommitIsSettledOrBlocks(t *testing.T) {
	t.Run("a settled carry verifies again and lands the sum", func(t *testing.T) {
		logf := filepath.Join(t.TempDir(), "verify.log")
		d, _, st, it, root := laneAtItsCommitOnCount(t, "grep -q 'n = 5' count.txt && echo v >> "+logf, "x\nn = 4\ny\n")
		if step := d.laneCommit(context.Background(), st, it); step.Action != run.ActionDone {
			t.Fatalf("the lane should have committed: %v %s", step.Action, st.Blocked)
		}
		if log, _ := os.ReadFile(logf); string(log) != "v\n" {
			t.Fatalf("the verify ran over the settled copy: %q", log)
		}
		if got, _ := os.ReadFile(filepath.Join(root, "count.txt")); string(got) != "x\nn = 5\ny\n" {
			t.Fatalf("the checkout holds the sum: %q", got)
		}
	})
	t.Run("an unsettled region blocks and the copy is put back", func(t *testing.T) {
		d, lane, st, it, root := laneAtItsCommitOnCount(t, "true", "x\nn = 1\ny\n")
		step := d.laneCommit(context.Background(), st, it)
		if step.Action != run.ActionBlocked || !strings.Contains(st.Blocked, "no rule settles the regions below") {
			t.Fatalf("the lane blocks: %v %q", step.Action, st.Blocked)
		}
		if got, _ := os.ReadFile(filepath.Join(lane.wt.Root(), "count.txt")); string(got) != "x\nn = 1\ny\n" {
			t.Fatalf("the copy holds the lane's own text, unmarked: %q", got)
		}
		if got, _ := os.ReadFile(filepath.Join(root, "count.txt")); string(got) != "x\nn = 3\ny\n" {
			t.Fatalf("the checkout holds the landed item alone: %q", got)
		}
	})
}
