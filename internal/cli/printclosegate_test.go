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
