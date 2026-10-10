package cli

import (
	"context"
	"testing"

	"github.com/rfizzle/shhh/internal/todo/run"
)

// The record a surface reads during a lane's turn already holds the lane's
// row: the step it is on and the reconciliation it is taking.
func TestLaneRow_IsLiveBeforeTheTurn(t *testing.T) {
	d, root, out := twoLanesOnOneLine(t, "x\nn = 2 // a\ny\n", "x\nn = 2 // b\ny\n")
	var stage run.Stage
	var words string
	reconcilingTurn(d, func(dir string) {
		if sp, ok := run.Live(root); ok {
			if l, ok := sp.Lane("b-two"); ok {
				stage, words = l.Stage, l.Reconcile
			}
		}
		writeCount(t, dir, "x\nn = 2 // a, b\ny\n")
	})
	if blocked := d.sprintParallel(context.Background(), 0, 2); blocked {
		t.Fatalf("a collision the lane reconciled lands:\n%s", out())
	}
	if stage != run.StageRemediate || words != "reconciling a-one's landing" {
		t.Fatalf("the lane's row during the turn is %q with %q", stage, words)
	}
}
