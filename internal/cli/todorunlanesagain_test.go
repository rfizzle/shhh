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

// A carry that changes the lane's copy after its verify passed sends the run
// back to verify before the review and the commit, and no fix round is spent
// on it: the first verdict was about a tree that is no longer the lane's.
func TestTodoRunHeadless_ACarryAfterVerifyVerifiesAgain(t *testing.T) {
	root := todoRepo(t)
	laneItem(t, root, "a-one", "a-one.go")
	verifyLog := filepath.Join(t.TempDir(), "verify.log")
	// One line when the landing is not in the copy yet, two when it is.
	body := "---\ntitle: b-two\nsize: S\n---\n## Tests\n- `test -f a-one.go && echo w >> " + verifyLog + " || echo v >> " + verifyLog + "`\n\n## Touches\n- `b-two.go`\n"
	if err := os.WriteFile(filepath.Join(todo.Dir(root), "b-two.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	removeLaneCopies(t, root)
	landed := func() bool {
		log, _ := run.Git(root, "log", "--format=%s")
		return strings.Contains(log, "Build a-one")
	}
	started := newLaneStarted()
	a := &laneAnswers{hold: func(slug string, stage run.Stage, _ string) {
		if slug != "b-two" {
			if stage == run.StageImplement {
				started.wait(t, slug)
			}
			return
		}
		switch stage {
		case run.StageResearch:
			started.arrive()
		case run.StageReview:
			// The verify has passed; a-one lands before the review is done.
			eventually(landed)
		}
	}}
	d, out := laneDriverFor(t, root, a)

	if blocked := d.sprintParallel(context.Background(), 0, 2); blocked {
		t.Fatalf("the sprint should have finished:\n%s", out.String())
	}
	log, _ := os.ReadFile(verifyLog)
	if string(log) != "v\nw\n" {
		t.Fatalf("b-two should verify before the carry and again after it: %q\n%s", log, out.String())
	}
	if !strings.Contains(out.String(), "b-two verifying again · a-one landed") {
		t.Fatalf("the log says why it verifies again:\n%s", out.String())
	}
	if strings.Contains(out.String(), "remediate") {
		t.Fatalf("a carry spends no fix round:\n%s", out.String())
	}
	if it, _ := todo.Load(todo.BuiltinCode(), root).Find("b-two"); !it.Archived {
		t.Fatalf("b-two lands after verifying again:\n%s", out.String())
	}
}

// laneAtItsCommit is a lane driven straight to its commit step, with a copy
// that holds one file of its own.
func laneAtItsCommit(t *testing.T, tests string) (*todoDriver, *todoLane, *run.State, todo.Item, string) {
	t.Helper()
	root := todoRepo(t)
	laneItem(t, root, "b-two", "b-two.go")
	removeLaneCopies(t, root)
	d, _ := laneDriverFor(t, root, &laneAnswers{})
	wt, err := worktree.NewWorktree(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wt.Remove)
	if err := os.WriteFile(filepath.Join(wt.Root(), "b-two.go"), []byte("package btwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := run.StartSprint("s", "", 0, false)
	set := &todoLanes{root: root, out: d.out, sp: sp, top: todoRepoTop(root)}
	lane := &todoLane{set: set, slug: "b-two", wt: wt}
	d.lane, d.tree = lane, wt.Root()
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("b-two")
	st := run.Start(it, "s", "", 0, run.Options{Pipeline: d.pipeline, Repo: true})
	st.Stage, st.Verified = run.StageCommit, true
	st.Tests = []string{tests}
	st.Message = "Build b-two\n\nBecause."
	return d, lane, st, it, root
}

// landOnCheckout is another lane's landing: a file on the checkout, committed,
// and the patch recorded for the lanes to carry.
func landOnCheckout(t *testing.T, root string, lane *todoLane) {
	t.Helper()
	patch := "diff --git a/a-one.go b/a-one.go\nnew file mode 100644\n--- /dev/null\n+++ b/a-one.go\n@@ -0,0 +1 @@\n+package aone\n"
	if err := worktree.ApplyPatch(root, patch); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "a-one.go"}, {"commit", "-q", "-m", "Build a-one"}} {
		if out, code := run.Git(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	lane.set.landings = append(lane.set.landings, todoLanding{slug: "a-one", patch: patch})
}

// At its commit a lane carries what landed since its last boundary, verifies
// the copy that carry made, and lands only if that passed; a failure is a fix
// round and not a commit, and a checkout a hand moved is never merged over.
func TestTodoRunHeadless_ALaneLandsOnlyOnItsGatedTree(t *testing.T) {
	t.Run("a carry is verified in the copy before the commit", func(t *testing.T) {
		logf := filepath.Join(t.TempDir(), "verify.log")
		d, lane, st, it, root := laneAtItsCommit(t, "test -f a-one.go && echo v >> "+logf)
		landOnCheckout(t, root, lane)
		step := d.laneCommit(context.Background(), st, it)
		if step.Action != run.ActionDone {
			t.Fatalf("the lane should have committed: %v %s", step.Action, st.Blocked)
		}
		if log, _ := os.ReadFile(logf); string(log) != "v\n" {
			t.Fatalf("the verify ran once over the copy with the landing in it: %q", log)
		}
		if _, err := os.Stat(filepath.Join(root, "b-two.go")); err != nil {
			t.Fatalf("b-two.go landed: %v", err)
		}
	})
	t.Run("a failure there is a fix round and lands nothing", func(t *testing.T) {
		d, lane, st, it, root := laneAtItsCommit(t, "false")
		landOnCheckout(t, root, lane)
		step := d.laneCommit(context.Background(), st, it)
		if step.Action != run.ActionPrompt || step.Stage != run.StageRemediate || st.Round != 1 {
			t.Fatalf("a failed verify is a fix round: %v %s round %d", step.Action, step.Stage, st.Round)
		}
		if _, err := os.Stat(filepath.Join(root, "b-two.go")); err == nil {
			t.Fatal("nothing landed")
		}
	})
	t.Run("a checkout a hand moved is not merged over", func(t *testing.T) {
		d, lane, st, it, root := laneAtItsCommit(t, "true")
		// The copy adds shared.txt and so does a hand on the checkout.
		for _, dir := range []string{lane.wt.Root(), root} {
			if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("from "+filepath.Base(dir)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		step := d.laneCommit(context.Background(), st, it)
		if step.Action != run.ActionBlocked || !strings.Contains(st.Blocked, "moved outside the sprint in shared.txt") {
			t.Fatalf("the lane blocks: %v %q", step.Action, st.Blocked)
		}
		if !lane.kept || lane.landed {
			t.Fatalf("the copy is kept and nothing landed: kept %v landed %v", lane.kept, lane.landed)
		}
	})
}
