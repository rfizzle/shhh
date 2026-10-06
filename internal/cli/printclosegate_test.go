package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/quality"
)

// A suite whose check writes into the tree it checks passes over a tree that
// is not the one it started on. The close row marks that pass stale and
// state reads it as failed, and the exit code and the alert read it the same
// way: an unattended run that exited 0 on it would hand a script the one
// answer it takes and ships.
func TestHeadlessCloseGate_AStalePassFailsTheRun(t *testing.T) {
	tests := []struct {
		name      string
		check     string
		wantState quality.Closing
		wantCode  int
		wantWord  string
	}{
		{"a pass over the tree it ran against", "true", quality.ClosingPassed, exitDone, ""},
		{"a pass whose tree changed while the checks ran", "echo moved >> moved.txt",
			quality.ClosingFailed, exitGate, "pass (stale)"},
		{"a failure", "exit 3", quality.ClosingFailed, exitGate, "fail"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := gitWorkspace(t)
			suite := `{"checks": [{"name": "c", "exe": "sh", "args": ["-c", "` + tc.check + `"]}]}`
			writeQualityConfig(t, ws, `{"on_close": "default", "suites": {"default": `+suite+`}}`)
			g := &headlessCloseGate{
				ctx: context.Background(), gate: &quality.Runner{Workspace: ws}, suite: "default",
				written: func() []string { return []string{"a.go"} },
			}
			g.close("done")

			if got := g.state(); got != tc.wantState {
				t.Errorf("state = %q, want %q", got, tc.wantState)
			}
			err := g.err()
			if got := headlessExitCode(observe.TurnDone, err != nil, false); got != tc.wantCode {
				t.Errorf("exit code = %d, want %d (err %v)", got, tc.wantCode, err)
			}
			rows := g.alerts()
			if tc.wantWord == "" {
				if err != nil || rows != nil {
					t.Fatalf("a pass reported err %v, alerts %q", err, rows)
				}
				return
			}
			want := `quality gate "default": ` + tc.wantWord
			if err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
			if len(rows) == 0 || !strings.HasSuffix(rows[0], "— "+tc.wantWord) {
				t.Errorf("the alert does not say %q: %q", tc.wantWord, rows)
			}
		})
	}
}

// A stale pass is handed back to the model for the same retry a failure
// gets, in the gate's own text and within the same budget, before the run
// exits on it: the screen retries it, and the unattended run takes the same
// path on the same verdict. A cancelled run is the run being stopped and is
// never handed back.
func TestHeadlessCloseGate_AStalePassIsHandedBack(t *testing.T) {
	tests := []struct {
		name      string
		check     string
		cancelled bool
		retries   int
		// want is whether each successive close hands the verdict back.
		want []bool
	}{
		{"a stale pass within the budget", "echo moved >> moved.txt", false, 1, []bool{true, false, false}},
		{"a stale pass with two retries", "echo moved >> moved.txt", false, 2, []bool{true, true, false}},
		{"a stale pass with no retries", "echo moved >> moved.txt", false, 0, []bool{false, false}},
		{"a failure within the budget", "exit 3", false, 1, []bool{true, false}},
		{"a pass over the tree it ran against", "true", false, 1, []bool{false, false}},
		{"a cancelled run that moved the tree", "echo moved >> moved.txt", true, 1, []bool{false, false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := gitWorkspace(t)
			suite := `{"checks": [{"name": "c", "exe": "sh", "args": ["-c", "` + tc.check + `"]}]}`
			writeQualityConfig(t, ws, `{"on_close": "default", "suites": {"default": `+suite+`}}`)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			g := &headlessCloseGate{
				ctx: ctx, gate: &quality.Runner{Workspace: ws}, suite: "default", retries: tc.retries,
				written: func() []string { return []string{"a.go"} },
			}
			for i, want := range tc.want {
				fb := g.close("done")
				if g.last == nil {
					t.Fatalf("close %d ran no suite", i+1)
				}
				if !want {
					if fb != "" {
						t.Errorf("close %d handed back %q, want nothing", i+1, fb)
					}
					continue
				}
				if text := g.last.Format(g.at); fb != text {
					t.Errorf("close %d handed back %q, want the gate's text %q", i+1, fb, text)
				}
			}
			if tc.cancelled && g.last.Verdict != quality.VerdictCancelled {
				t.Fatalf("verdict = %q, want cancelled", g.last.Verdict)
			}
		})
	}
}
