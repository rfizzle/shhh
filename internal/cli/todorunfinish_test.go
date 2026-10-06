package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// A suite whose check writes into the tree passes over a tree that is not
// the one it started on. The close row marks that pass stale, and so do the
// unattended verify stage and the unattended close a backlog run takes its
// verdict from, so the run cannot close an item on it. The verify report
// says why in the close row's words.
func TestVerify_AStalePassIsNotAPass(t *testing.T) {
	tests := []struct {
		name   string
		check  string
		wantOK bool
		want   string
	}{
		{"a pass over the tree it ran against", "true", true, `quality gate "default": pass`},
		{"a pass whose tree changed while the checks ran", "echo moved >> moved.txt", false,
			"STALE: the tree changed while the checks ran"},
		{"a pass on a rerun", "if [ -e SCRATCH/ran ]; then exit 0; fi; touch SCRATCH/ran; exit 1", true,
			"PASS — 1/1 checks passed, 1 flaked ("},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := gitWorkspace(t)
			check := strings.ReplaceAll(tc.check, "SCRATCH", t.TempDir())
			suite := `{"checks": [{"name": "c", "exe": "sh", "args": ["-c", "` + check + `"]}]}`
			writeQualityConfig(t, ws, `{"on_close": "default", "suites": {"default": `+suite+`}}`)
			gate := &quality.Runner{Workspace: ws}

			d := &todoDriver{root: ws, tree: ws, gate: gate}
			v := d.verify(context.Background(), &run.State{Slug: "do-it"}, "")
			if v.ok != tc.wantOK || v.blocked != "" {
				t.Fatalf("verify = %+v, want ok %v", v, tc.wantOK)
			}
			if !strings.Contains(v.output, tc.want) {
				t.Fatalf("the report does not say %q: %q", tc.want, v.output)
			}

			// The unattended close over the same suite gives the run the
			// same answer, which is what its verify stage takes instead of
			// running the suite again.
			g := &headlessCloseGate{
				ctx: context.Background(), gate: gate, suite: "default",
				written: func() []string { return []string{"a.go"} },
			}
			g.close("done")
			want := quality.ClosingFailed
			if tc.wantOK {
				want = quality.ClosingPassed
			}
			if got := g.state(); got != want {
				t.Fatalf("the close reads %q, want %q", got, want)
			}
		})
	}
}
