package chat

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
)

// `/gate flakes` opens the checkout's ledger as a screen, the latest flake
// first, each check with its suite, count and when it last flaked, and the
// way out hands the pane back.
func TestGate_FlakesOpensTheLedgerAsAScreen(t *testing.T) {
	was := clock
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = was })

	m := frameModel(t, 130, 30)
	m.wiring.Gate, m.alertMemo = Gate{
		Manage: func([]string) string { return "the manager answered" },
		Flakes: func() ([]storage.Flake, error) {
			return []storage.Flake{
				{Suite: "default", Check: "test", Command: "make test", Seen: 4, FirstExit: 2,
					FirstAt: now.Add(-72 * time.Hour), LastAt: now.Add(-2 * time.Hour), LastSession: "7"},
				{Suite: "fast", Check: "vet", Command: "go vet ./...", Seen: 1, FirstExit: 1,
					FirstAt: now.Add(-48 * time.Hour), LastAt: now.Add(-48 * time.Hour)},
			}, nil
		},
	}, &alertMemo{}
	next, _ := m.runCommand("/gate flakes", "/gate")
	opened := next.(Model)
	if opened.state != stateFlakes || opened.screens.flakes() == nil {
		t.Fatalf("/gate flakes should open the screen, got state %d", opened.state)
	}
	view := stripANSI(opened.screens.flakes().View(130))
	for _, want := range []string{"/gate flakes", "2 checks · 5 flakes", "test", "default · 4 times", "2h ago",
		"vet", "fast · 1 time", "2d ago", "flaked 4 times in this checkout", "last 2h ago · first 3d ago",
		"the failing run exited 2", "last in session 7", "make test"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
	closed, _ := opened.updateFlakes(keyPress('q'))
	if got := closed.(Model); got.state == stateFlakes || got.screens.flakes() != nil {
		t.Fatalf("the way out should close the screen, got state %d", got.state)
	}
}

// An empty ledger and one that will not read are a line, never an empty
// screen; a session with no reader leaves the verb to the manager, and the
// other verbs are the manager's as they were.
func TestGate_FlakesWithNothingToListIsALine(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		flakes func() ([]storage.Flake, error)
		want   string
	}{
		{"an empty ledger", "/gate flakes",
			func() ([]storage.Flake, error) { return nil, nil }, "no check has flaked in this checkout"},
		{"a ledger that will not read", "/gate flakes",
			func() ([]storage.Flake, error) { return nil, errors.New("store locked") }, "store locked"},
		{"no reader", "/gate flakes", nil, "the manager answered"},
		{"another verb", "/gate result",
			func() ([]storage.Flake, error) { t.Error("result read the ledger"); return nil, nil }, "the manager answered"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := frameModel(t, 110, 30)
			m.wiring.Gate, m.alertMemo = Gate{
				Manage: func([]string) string { return "the manager answered" },
				Flakes: tc.flakes,
			}, &alertMemo{}
			next, _ := m.runCommand(tc.line, "/gate")
			got := next.(Model)
			if got.state == stateFlakes {
				t.Fatal("nothing to list opened the screen")
			}
			if text := stripANSI(got.renderHistory()); !strings.Contains(text, tc.want) {
				t.Errorf("the transcript lacks %q:\n%s", tc.want, text)
			}
		})
	}
}

// The close row's note column says how often a check that flaked had flaked
// before, read off the value the result carried the way a reopened session
// reads it, and off a trimmed row, which keeps it; a first flake says
// nothing more.
func TestResolved_TheCloseRowSaysHowOftenAFlakeHasFlakedBefore(t *testing.T) {
	result := func(before int) *quality.Summary {
		res := &quality.Result{Suite: "fast", Verdict: quality.VerdictPass, Duration: 2 * time.Second,
			Checks: []quality.CheckResult{
				{Name: "vet", Command: "go vet ./...", Duration: time.Second},
				{Name: "test", Command: "make test", Flaked: true, FlakedBefore: before,
					Duration: time.Second, RerunDuration: time.Second},
			}}
		sum := res.Summary(res.Fingerprint)
		return &sum
	}
	tests := []struct {
		name string
		e    entry
		want string
	}{
		{"three before", entry{kind: entryTool, toolName: quality.ToolName, gate: result(3)},
			"flaked 3 times before · /gate flakes"},
		{"one before", entry{kind: entryTool, toolName: quality.ToolName, gate: result(1)},
			"flaked 1 time before · /gate flakes"},
		{"a first flake", entry{kind: entryTool, toolName: quality.ToolName, gate: result(0)}, ""},
		{"a trimmed row keeps its reading", entry{kind: entryTool, toolName: quality.ToolName,
			gate: result(5), elided: &elidedRow{}},
			"flaked 5 times before · /gate flakes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := turnChecksRow([]entry{tc.e}, false)
			if row == nil {
				t.Fatal("no checks row")
			}
			if row.Flakes != tc.want {
				t.Errorf("Flakes = %q, want %q", row.Flakes, tc.want)
			}
			if row.Failed {
				t.Error("a flake is a pass")
			}
		})
	}
}
