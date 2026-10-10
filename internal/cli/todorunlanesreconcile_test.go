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

	"github.com/rfizzle/shhh/internal/subagent/worktree"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// collisionTold is how a test knows a turn is a reconciliation: the first
// words of the findings it is given (run.CollisionFindings).
const collisionTold = "landed on the checkout while this item was being worked"

// reconcilingTurn answers a lane's reconciling turn with resolve, which is
// handed the copy's directory and writes what the turn leaves there, and
// every other turn as the driver's own answers would. It keeps each
// reconciling prompt for the test to read.
func reconcilingTurn(d *todoDriver, resolve func(dir string)) func() []string {
	var mu sync.Mutex
	var prompts []string
	inner := d.turn
	d.turn = func(ctx context.Context, deadline time.Time, dir string, step run.Step) (todoTurn, error) {
		if step.Stage != run.StageRemediate || !strings.Contains(step.Prompt, collisionTold) {
			return inner(ctx, deadline, dir, step)
		}
		mu.Lock()
		prompts = append(prompts, step.Prompt)
		mu.Unlock()
		resolve(dir)
		return todoTurn{text: "Reconciled count.txt.", code: exitDone}, nil
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), prompts...)
	}
}

func writeCount(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "count.txt"), []byte(text), 0o644); err != nil {
		t.Error(err)
	}
}

// Two lanes that each rewrote one line differently meet at the landing, and
// no rule settles it: the second lane is given the region in a remediate turn
// of its own, told only in its findings, and its reconciliation is judged,
// verified, reviewed and committed with the rest of the item, so the
// checkout holds both items' work.
func TestTodoRunHeadless_ACollisionIsReconciledAndVerified(t *testing.T) {
	const both = "x\nn = 2 // a, b\ny\n"
	d, root, out := twoLanesOnOneLine(t, "x\nn = 2 // a\ny\n", "x\nn = 2 // b\ny\n")
	var row string
	prompts := reconcilingTurn(d, func(dir string) {
		if sp, ok := run.Live(root); ok {
			if l, ok := sp.Lane("b-two"); ok {
				row = l.Where()
			}
		}
		writeCount(t, dir, both)
	})
	if blocked := d.sprintParallel(context.Background(), 0, 2); blocked {
		t.Fatalf("a collision the lane reconciled lands:\n%s", out())
	}
	store := todo.Load(todo.BuiltinCode(), root)
	for _, slug := range []string{"a-one", "b-two"} {
		if it, _ := store.Find(slug); !it.Archived {
			t.Fatalf("%s should have landed:\n%s", slug, out())
		}
	}
	if got, _ := os.ReadFile(filepath.Join(root, "count.txt")); string(got) != both {
		t.Fatalf("the checkout holds the reconciliation: %q", got)
	}
	told := prompts()
	if len(told) != 1 {
		t.Fatalf("one reconciling turn, got %d:\n%s", len(told), out())
	}
	for _, want := range []string{"a-one " + collisionTold, "in count.txt the two changed the same lines",
		"a line of `" + strings.Repeat("<", worktree.MarkerSize) + "` and a-one", "a count both raised is raised by both amounts",
		"keeps both entries, a-one's first", "## count.txt\n\n@@ line 2\n"} {
		if !strings.Contains(told[0], want) {
			t.Fatalf("the findings should say %q:\n%s", want, told[0])
		}
	}
	if row != "remediate · reconciling a-one's landing" {
		t.Fatalf("the row reads %q during the turn", row)
	}
	log := out()
	turn := strings.Index(log, "▸ todo run b-two · remediate · reconciling a-one's landing")
	judged := strings.Index(log, "… b-two reconciled a-one's landing in count.txt\n")
	verify := strings.LastIndex(log, "▸ todo run b-two · verify")
	review := strings.LastIndex(log, "▸ todo run b-two · review")
	if turn < 0 || judged < turn || verify < judged || review < verify {
		t.Fatalf("the turn is taken, judged, verified and reviewed, in that order:\n%s", log)
	}
}

// keptIn is the copy a blocked lane's work was kept in, as the log says it.
var keptIn = regexp.MustCompile(`kept in (\S+); `)

// A turn that leaves a mark, leaves the file as the merge wrote it, or keeps
// either side whole is not a reconciliation: the lane blocks naming the file
// and why, the checkout holds the landed item alone, and the lane's own work
// is kept in its copy as it was before the merge, with no mark.
func TestTodoRunHeadless_AnUnreconciledCollisionStillBlocks(t *testing.T) {
	const aText, bText = "x\nn = 2 // a\ny\n", "x\nn = 2 // b\ny\n"
	for _, c := range []struct {
		name, why string
		resolve   func(t *testing.T, dir string)
	}{
		{"a mark left", "count.txt still holds a conflict mark", func(t *testing.T, dir string) {
			marked, _ := os.ReadFile(filepath.Join(dir, "count.txt"))
			writeCount(t, dir, strings.Replace(string(marked), "y\n", "z\n", 1))
		}},
		{"the file as the merge left it", "count.txt is as the merge left it", func(*testing.T, string) {}},
		{"the landed side whole", "count.txt is a-one's side whole, which drops this lane's change", func(t *testing.T, dir string) {
			writeCount(t, dir, aText)
		}},
		{"the lane's side whole", "count.txt is this lane's side whole, which drops a-one's change", func(t *testing.T, dir string) {
			writeCount(t, dir, bText)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, root, out := twoLanesOnOneLine(t, aText, bText)
			reconcilingTurn(d, func(dir string) { c.resolve(t, dir) })
			if blocked := d.sprintParallel(context.Background(), 0, 2); !blocked {
				t.Fatalf("a turn that did not reconcile blocks the lane:\n%s", out())
			}
			it, _ := todo.Load(todo.BuiltinCode(), root).Find("b-two")
			if it.Status != todo.StatusBlocked || !strings.Contains(it.Body, "a-one landed on the checkout over count.txt") ||
				!strings.Contains(it.Body, c.why) {
				t.Fatalf("b-two blocks naming the file and why: %s\n%s", it.Status, it.Body)
			}
			if got, _ := os.ReadFile(filepath.Join(root, "count.txt")); string(got) != aText {
				t.Fatalf("the checkout holds the landed item alone: %q", got)
			}
			m := keptIn.FindStringSubmatch(out())
			if m == nil {
				t.Fatalf("the lane's copy is kept:\n%s", out())
			}
			if got, _ := os.ReadFile(filepath.Join(m[1], "count.txt")); string(got) != bText {
				t.Fatalf("the kept copy holds the lane's own work, unmarked: %q", got)
			}
		})
	}
}

// laneContinuedAtCommit is laneAtItsCommitOnCount's lane picked up at its
// commit step by the driver's own loop: the item in progress, its run's
// checkpoint saved.
func laneContinuedAtCommit(t *testing.T, tests func(root string) string) (*todoDriver, *todoLane, todo.Item, string) {
	t.Helper()
	d, lane, st, it, root := laneAtItsCommitOnCount(t, "", "x\nn = 1\ny\n")
	st.Tests = []string{tests(root)}
	lane.set.sp.Lanes = append(lane.set.sp.Lanes, run.SprintLane{Slug: "b-two"})
	d.turn = (&laneAnswers{running: map[string]bool{}}).turn
	if err := st.Save(root); err != nil {
		t.Fatal(err)
	}
	if err := todo.SetStatus(it.Path, todo.StatusInProgress); err != nil {
		t.Fatal(err)
	}
	it, _ = todo.Load(todo.BuiltinCode(), root).Find("b-two")
	return d, lane, it, root
}

// landOn commits a file on the checkout and adds its patch to the set's
// landings, as a lane landing it would.
func landOn(t *testing.T, lane *todoLane, root, slug, file, base, text string) {
	t.Helper()
	patch := "diff --git a/" + file + " b/" + file + "\n"
	if base == "" {
		patch += "new file mode 100644\n--- /dev/null\n+++ b/" + file + "\n@@ -0,0 +1 @@\n+" + strings.TrimSuffix(text, "\n") + "\n"
	} else {
		patch += "--- a/" + file + "\n+++ b/" + file + "\n" + base
	}
	if err := worktree.ApplyPatch(root, patch); err != nil {
		t.Error(err)
		return
	}
	for _, args := range [][]string{{"add", file}, {"commit", "-q", "-m", "Build " + slug}} {
		if out, code := run.Git(root, args...); code != 0 {
			t.Errorf("git %v: %s", args, out)
		}
	}
	lane.set.land.Lock()
	lane.set.landings = append(lane.set.landings, todoLanding{slug: slug, patch: patch})
	lane.set.land.Unlock()
}

// The commit-time carry meets a collision no rule settles: the land lock is
// let go and the turn taken, a landing made during the turn is carried at the
// next boundary, and the lane verifies, is reviewed and commits again, with
// what the turn changed outside the regions named. A collision past the
// item's reconciliations blocks naming the count, with the copy put back.
func TestTodoRunHeadless_AReconciliationAtTheCommitLandsAfterItsVerify(t *testing.T) {
	t.Run("the turn is taken and the lane lands after its verify", func(t *testing.T) {
		logf, rowf := filepath.Join(t.TempDir(), "verify.log"), filepath.Join(t.TempDir(), "row.log")
		// The verify writes down the lane's row as the checkpoint has it
		// while the verify runs, which is after the judge.
		d, lane, it, root := laneContinuedAtCommit(t, func(root string) string {
			return "grep -q 'n = 7' count.txt && echo v >> " + logf +
				" && grep -o '\"reconcile\": *\"[^\"]*\"' " + filepath.Join(run.Dir(root), "sprint.json") + " >> " + rowf
		})
		var lockFree bool
		reconcilingTurn(d, func(dir string) {
			if lockFree = lane.set.land.TryLock(); lockFree {
				lane.set.land.Unlock()
			}
			landOn(t, lane, root, "c-three", "c.txt", "", "c\n")
			writeCount(t, dir, "x\nn = 7\ny\n")
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("why\n"), 0o644); err != nil {
				t.Error(err)
			}
		})
		st := d.work(context.Background(), it, nil)
		if st.Stage != run.StageDone {
			t.Fatalf("the lane should have committed: %s\n%s", st.Blocked, d.out)
		}
		if !lockFree {
			t.Fatal("the land lock is let go for the turn")
		}
		if log, _ := os.ReadFile(logf); string(log) != "v\n" {
			t.Fatalf("one verify ran over the reconciled copy: %q", log)
		}
		if got, _ := os.ReadFile(filepath.Join(root, "count.txt")); string(got) != "x\nn = 7\ny\n" {
			t.Fatalf("the checkout holds the reconciliation: %q", got)
		}
		if files, _ := run.Git(root, "show", "--name-only", "--format=", "HEAD"); strings.TrimSpace(files) != "count.txt\nnotes.txt" {
			t.Fatalf("the lane's commit holds its own work, with c-three's carried into its base: %q", files)
		}
		if !strings.Contains(sprintOut(d), "… b-two reconciled a-one's landing in count.txt; also changed notes.txt\n") {
			t.Fatalf("the log names what the turn changed outside the regions:\n%s", sprintOut(d))
		}
		if row, _ := os.ReadFile(rowf); !strings.Contains(string(row), "reconciled a-one's landing in count.txt; also changed notes.txt") {
			t.Fatalf("the row says what the judge found while the verify after it runs: %q", row)
		}
		if l, _ := lane.set.sp.Lane("b-two"); l.Reconcile != "" {
			t.Fatalf("the row is cleared once the verify after it ran: %q", l.Reconcile)
		}
	})

	t.Run("a collision past the bound blocks naming the count", func(t *testing.T) {
		d, lane, it, root := laneContinuedAtCommit(t, func(string) string { return "true" })
		reconcilingTurn(d, func(dir string) {
			writeCount(t, dir, "x\nn = 7\ny\n")
			// Another landing on the same line during the turn: the item, of
			// the smallest grade, has had its one reconciliation.
			landOn(t, lane, root, "c-three", "count.txt", "@@ -1,3 +1,3 @@\n x\n-n = 3\n+n = 3 // c\n y\n", "")
		})
		st := d.work(context.Background(), it, nil)
		if st.Stage != run.StageBlocked || !strings.HasPrefix(st.Blocked, "reconciliations spent (1): c-three landed") {
			t.Fatalf("the lane blocks naming the count: %q\n%s", st.Blocked, sprintOut(d))
		}
		if got, _ := os.ReadFile(filepath.Join(lane.wt.Root(), "count.txt")); string(got) != "x\nn = 7\ny\n" {
			t.Fatalf("the copy is put back to the lane's work, unmarked: %q", got)
		}
	})
}

// sprintOut is what a driver built over a strings.Builder has printed.
func sprintOut(d *todoDriver) string {
	if b, ok := d.out.(*strings.Builder); ok {
		return b.String()
	}
	return ""
}
