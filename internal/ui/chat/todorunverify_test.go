package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// A pass the tree moved under is stale, and the close row says so; the
// verify stage reads the same result the same way, so the run does not
// close an item on a verdict the close row would have marked stale. Its
// report says why in the close row's own words, which is what the fix round
// is handed.
func TestVerify_AStalePassIsNotAPass(t *testing.T) {
	tests := []struct {
		name   string
		res    quality.Result
		wantOK bool
		want   string
	}{
		{"a pass over the tree it ran against",
			quality.Result{Suite: "default", Verdict: quality.VerdictPass},
			true, `quality gate "default": pass`},
		{"a pass whose tree changed while the checks ran",
			quality.Result{Suite: "default", Verdict: quality.VerdictPass, ChangedDuringRun: true},
			false, "STALE: the tree changed while the checks ran"},
		{"a pass with a check that passed on its rerun",
			quality.Result{Suite: "default", Verdict: quality.VerdictPass, Checks: []quality.CheckResult{
				{Name: "test", Command: "make test", Flaked: true}}},
			true, "1/1 checks passed, 1 flaked"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, root := runModel(t)
			res := tc.res
			m.wiring.Gate.Run = func(context.Context, string) (*quality.Result, error) { return &res, nil }
			m.todo.runner.state = &run.State{Slug: "do-it", Tests: []string{"true"}}
			m.todo.runner.item = todo.Item{Slug: "do-it"}
			msg := m.todoVerifyCmd("")().(todoVerifyMsg)
			if msg.ok != tc.wantOK || msg.blocked != "" {
				t.Fatalf("verify = %+v, want ok %v", msg, tc.wantOK)
			}
			if !strings.Contains(msg.output, tc.want) {
				t.Fatalf("the report does not say %q: %q", tc.want, msg.output)
			}
			// The close row over the same result and tree holds the same
			// reading, and a stale one the same words.
			m.appendCloseGateRow(&res, quality.TakeFingerprint(root))
			row := m.transcript[len(m.transcript)-1]
			if row.gate.OK() != msg.ok {
				t.Errorf("the close row reads OK %v, verify %v", row.gate.OK(), msg.ok)
			}
			if !tc.wantOK && !strings.Contains(msg.output, row.toolResult) {
				t.Errorf("the report is not in the close row's words:\nreport %q\nrow    %q", msg.output, row.toolResult)
			}
		})
	}
}
