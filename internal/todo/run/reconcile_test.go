package run

import (
	"fmt"
	"strings"
	"testing"
)

// verifiedAt is a run of an item of this size at a passed verify, its review
// next.
func verifiedAt(t *testing.T, size string) *State {
	t.Helper()
	it := item(size)
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	s.Observe(it, strings.Replace(planText, "size: S", "size: "+size, 1))
	s.Observe(it, "done")
	if step := s.VerifyResult(it, true, ""); step.Stage != StageReview {
		t.Fatalf("the run should be at its review: %+v", step)
	}
	return s
}

// A reconciliation is the remediation step entered for a landing: the model
// is told the collision in the findings block alone, and the turn returns to
// verify as every remediate turn does, spending no remediation round.
func TestRun_ReconcileReturnsToVerify(t *testing.T) {
	it := item("M")
	s := verifiedAt(t, "M")
	findings := CollisionFindings("a-one", "x", []string{"count.txt"}, "## count.txt\n\n@@ line 2\nx\n")
	step := s.Reconcile(it, "a-one", findings)
	if step.Action != ActionPrompt || step.Stage != StageRemediate || !strings.HasSuffix(step.Shown, "· remediate · reconciling a-one's landing") {
		t.Fatalf("a reconciliation is a remediate turn: %+v", step)
	}
	if !strings.Contains(step.Prompt, findings) || !strings.HasPrefix(step.Prompt, "REMEDIATE stage.") {
		t.Fatalf("the remediate prompt carries the collision as its findings:\n%s", step.Prompt)
	}
	if s.Round != 0 || s.Reconciled != 1 {
		t.Fatalf("a reconciliation counts on its own counter: round %d, reconciled %d", s.Round, s.Reconciled)
	}
	if back := s.Observe(it, "Reconciled count.txt."); back.Action != ActionVerify {
		t.Fatalf("a reconciliation goes back to verify: %+v", back)
	}
	if step := s.VerifyResult(it, true, ""); step.Stage != StageReview {
		t.Fatalf("a verified reconciliation is reviewed with the item: %+v", step)
	}
}

// Reconciliations are bounded by the remediation step's rounds at the item's
// grade, on a counter of their own: a small item takes one, blocks naming the
// count on the next, and its own round is still there for a real failure.
// A remediation the run was about to take when the landing met it is taken
// back rather than spent.
func TestRun_ReconciliationsAreBoundedByTheGradesRounds(t *testing.T) {
	for _, c := range []struct {
		size   string
		rounds int
	}{{"S", 1}, {"M", 2}} {
		it := item(c.size)
		s := verifiedAt(t, c.size)
		for i := range c.rounds {
			if step := s.Reconcile(it, "a-one", "collision"); step.Action != ActionPrompt {
				t.Fatalf("%s: reconciliation %d should be taken: %+v %s", c.size, i+1, step, s.Blocked)
			}
			s.Observe(it, "reconciled")
			s.VerifyResult(it, true, "")
		}
		step := s.Reconcile(it, "b-two", "the collision")
		if step.Action != ActionBlocked || !strings.HasPrefix(s.Blocked, fmt.Sprintf("reconciliations spent (%d): b-two landed on the checkout", c.rounds)) {
			t.Fatalf("%s: the reconciliation past the bound blocks naming the count: %+v %q", c.size, step, s.Blocked)
		}
	}

	t.Run("the item's own round is still there", func(t *testing.T) {
		it := item("S")
		s := verifiedAt(t, "S")
		s.Reconcile(it, "a-one", "collision")
		s.Observe(it, "reconciled")
		if step := s.VerifyResult(it, false, "FAIL"); step.Stage != StageRemediate || s.Round != 1 {
			t.Fatalf("a failure after a reconciliation spends the item's own round: %+v %s", step, s.Blocked)
		}
	})

	t.Run("a pending remediation is taken back", func(t *testing.T) {
		it := item("S")
		s := verifiedAt(t, "S")
		s.Observe(it, "verdict: findings\n1. off by one")
		if s.Round != 1 || s.Stage != StageRemediate {
			t.Fatalf("the review's findings should be a remediation: %+v", s)
		}
		s.Reconcile(it, "a-one", "collision")
		if s.Round != 0 || s.Reconciled != 1 {
			t.Fatalf("the remediation the reconciliation replaced is not spent: round %d, reconciled %d", s.Round, s.Reconciled)
		}
	})
}
