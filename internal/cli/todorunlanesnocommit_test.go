package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo/run"
)

// A run without a commit lands the way a committing run does, less the
// commit: the lane carries what landed since its last boundary, verifies the
// copy that carry made, and lands plain only if that passed. A failing verify
// lands nothing, keeps the copy and blocks the item.
func TestTodoRunHeadless_ANoCommitLaneIsVerifiedAgainBeforeItLands(t *testing.T) {
	t.Run("a carry is verified in the copy before it lands", func(t *testing.T) {
		logf := filepath.Join(t.TempDir(), "verify.log")
		d, lane, st, it, root := laneAtItsCommit(t, "test -f a-one.go && echo v >> "+logf)
		st.Stage = run.StageDone
		landOnCheckout(t, root, lane)

		lane.end(context.Background(), d, st, it)

		if st.Stage == run.StageBlocked {
			t.Fatalf("the lane should have landed: %s", st.Blocked)
		}
		if log, _ := os.ReadFile(logf); string(log) != "v\n" {
			t.Fatalf("the verify ran once over the copy with the landing in it: %q", log)
		}
		if _, err := os.Stat(filepath.Join(root, "b-two.go")); err != nil {
			t.Fatalf("b-two.go landed: %v", err)
		}
		if !lane.landed || lane.kept {
			t.Fatalf("landed and not kept: landed %v kept %v", lane.landed, lane.kept)
		}
	})
	t.Run("a failing verify keeps the copy and blocks the lane", func(t *testing.T) {
		d, lane, st, it, root := laneAtItsCommit(t, "false")
		st.Stage = run.StageDone
		landOnCheckout(t, root, lane)

		lane.end(context.Background(), d, st, it)

		if st.Stage != run.StageBlocked || !strings.Contains(st.Blocked, "no longer verifies after a-one landed") {
			t.Fatalf("the lane blocks on the verify: %s %q", st.Stage, st.Blocked)
		}
		if _, err := os.Stat(filepath.Join(root, "b-two.go")); err == nil {
			t.Fatal("nothing landed")
		}
		if !lane.kept || lane.landed {
			t.Fatalf("the copy is kept and nothing landed: kept %v landed %v", lane.kept, lane.landed)
		}
		if _, err := os.Stat(filepath.Join(lane.wt.Root(), "b-two.go")); err != nil {
			t.Fatalf("the copy still holds the lane's work: %v", err)
		}
	})
	t.Run("a checkout a hand moved is not merged over", func(t *testing.T) {
		d, lane, st, it, root := laneAtItsCommit(t, "true")
		st.Stage = run.StageDone
		for _, dir := range []string{lane.wt.Root(), root} {
			if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("from "+filepath.Base(dir)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		lane.end(context.Background(), d, st, it)

		if st.Stage != run.StageBlocked || !strings.Contains(st.Blocked, "moved outside the sprint in shared.txt") {
			t.Fatalf("the lane blocks: %s %q", st.Stage, st.Blocked)
		}
		if !lane.kept || lane.landed {
			t.Fatalf("the copy is kept and nothing landed: kept %v landed %v", lane.kept, lane.landed)
		}
	})
}
