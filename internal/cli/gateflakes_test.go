package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent/worktree"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// flakyCheckout is a checkout whose committed default suite has one check
// that fails on every other run: the marker it toggles lives outside the
// tree, so the gate's fingerprint never moves and the rerun is made.
func flakyCheckout(t *testing.T) string {
	t.Helper()
	root := todoRepo(t)
	marker := filepath.Join(t.TempDir(), "failed-last")
	script, _ := json.Marshal(fmt.Sprintf(`if [ -e %[1]q ]; then rm %[1]q; exit 0; fi; touch %[1]q; exit 3`, marker))
	cfg := fmt.Sprintf(`{"suites": {"default": {"timeout_seconds": 10, "checks": [{"name": "flaky", "exe": "sh", "args": ["-c", %s]}]}}}`, script)
	if err := os.MkdirAll(filepath.Join(root, ".shhh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, quality.ConfigRelPath), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", quality.ConfigRelPath}, {"commit", "-q", "-m", "a flaky suite"}} {
		if out, code := run.Git(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return root
}

// Every runner that can rerun a check writes the same ledger: a session's
// gate, the headless driver's and a lane's, which runs in a copy of the
// checkout and counts its flake against the checkout itself. Each flake is
// told how many came before it, so the third run's line says two.
func TestGate_EveryRunnerWritesTheLedger(t *testing.T) {
	root := flakyCheckout(t)
	withProjectTrust(t, project.Trust{Granted: true})

	session := &quality.Runner{Workspace: root}
	recordGateVerdicts(session, nil)

	d, err := newTodoDriver(&bytes.Buffer{}, root, config.Config{}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.close)
	if d.gate == nil {
		t.Fatal("a trusted checkout's driver has no gate")
	}

	wt, err := worktree.NewWorktree(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wt.Remove)
	lane := d.laneDriver(&todoLane{slug: "a-lane", wt: wt})
	if lane.gate.Workspace == root {
		t.Fatal("the lane's gate should run in the lane's copy")
	}

	for i, r := range []struct {
		name string
		gate *quality.Runner
	}{{"a session's gate", session}, {"the headless driver", d.gate}, {"a lane", lane.gate}} {
		res, err := r.gate.Run(context.Background(), "default")
		if err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		c := res.Checks[0]
		if res.Verdict != quality.VerdictPass || !c.Flaked {
			t.Fatalf("%s: verdict %s, flaked %v; the check should flake:\n%s", r.name, res.Verdict, c.Flaked, res.Format(res.Fingerprint))
		}
		if c.FlakedBefore != i {
			t.Errorf("%s: FlakedBefore = %d, want %d", r.name, c.FlakedBefore, i)
		}
	}

	db, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	flakes, err := db.FlakesFor(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(flakes) != 1 || flakes[0].Seen != 3 || flakes[0].Suite != "default" || flakes[0].Check != "flaky" ||
		flakes[0].FirstExit != 3 || !strings.HasPrefix(flakes[0].Command, "sh -c ") {
		t.Errorf("the checkout's ledger = %+v, want one row for flaky, seen 3, first exit 3", flakes)
	}
	if lanes, _ := db.FlakesFor(wt.Root()); len(lanes) != 0 {
		t.Errorf("the lane's copy has a ledger of its own: %+v", lanes)
	}
}

// A ledger that cannot be written is never a verdict: the flake is still a
// pass and still says it flaked, it just cannot say how often.
func TestGate_ALedgerThatWillNotOpenIsNeverAVerdict(t *testing.T) {
	root := flakyCheckout(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", blocked)
	r := &quality.Runner{Workspace: root}
	recordGateFlakes(r, root, func() string { return "" })
	res, err := r.Run(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Checks[0]; res.Verdict != quality.VerdictPass || !c.Flaked || c.FlakedBefore != 0 {
		t.Errorf("verdict %s, flaked %v, before %d; want a flaked pass with no count:\n%s",
			res.Verdict, c.Flaked, c.FlakedBefore, res.Format(res.Fingerprint))
	}
}

// `/gate flakes` answers in text where there is no screen, and the usage
// line names the verb; the dashboard's gate section carries the flakes line.
func TestGate_TheFlakesVerbAndTheDashboardLine(t *testing.T) {
	at := time.Date(2026, 10, 7, 9, 30, 0, 0, time.Local)
	flakes := []storage.Flake{
		{Suite: "default", Check: "test", Command: "make test", Seen: 4, LastAt: at},
		{Suite: "fast", Check: "vet", Command: "go vet ./...", Seen: 1, LastAt: at},
	}
	text := flakesText(flakes, nil)
	for _, want := range []string{"test · default · 4 times · last 2026-10-07 09:30 — make test",
		"vet · fast · 1 time · last 2026-10-07 09:30 — go vet ./..."} {
		if !strings.Contains(text, want) {
			t.Errorf("/gate flakes lacks %q:\n%s", want, text)
		}
	}
	if got := flakesText(nil, nil); got != "no check has flaked in this checkout" {
		t.Errorf("an empty ledger = %q", got)
	}
	if got := gateManager(&quality.Runner{Workspace: t.TempDir()})([]string{"nope"}); !strings.Contains(got, "/gate flakes") {
		t.Errorf("the usage line does not name the verb: %q", got)
	}

	body := observeReport(observeData{Window: "30d",
		Sessions: []storage.AgentSessionSummary{{ID: 1}}, Flakes: flakes}).Render(110)
	for _, want := range []string{"GATE", "flakes", "2 checks in this checkout", "most test (default) · 4 times", "5 flakes"} {
		if !strings.Contains(body, want) {
			t.Errorf("the dashboard lacks %q:\n%s", want, body)
		}
	}
}
