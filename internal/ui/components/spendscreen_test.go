package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// spendFixture is a session's bill: the total, two models — the session's
// own with its turns and a classifier, the small one with the summary and a
// child's share — two children, and three turns, the newest still running.
func spendFixture() []SpendRow {
	return []SpendRow{
		{Kind: SpendTotal, Cost: "$0.4210", Tokens: "↑182.4k ↓9.1k · 96.0k cached", Requests: 23,
			Parts: []SpendPart{
				{Word: "main", Cost: "$0.3620", Tokens: "↑140.2k ↓7.8k · 96.0k cached", Requests: 14},
				{Word: "classifier", Cost: "$0.0030", Tokens: "↑12.0k ↓120", Requests: 4},
				{Word: "summary", Cost: "$0.0010", Tokens: "↑6.2k ↓410", Requests: 2},
				{Word: "sub-agent", Cost: "$0.0550", Tokens: "↑24.0k ↓770", Requests: 3},
			}},
		{Kind: SpendModel, Name: "claude-opus-5-5", Cost: "$0.3650", Children: "$0.0520",
			Tokens: "↑152.2k ↓7.9k · 96.0k cached", Requests: 18,
			Parts: []SpendPart{
				{Word: "main", Cost: "$0.3620", Tokens: "↑140.2k ↓7.8k · 96.0k cached", Requests: 14},
				{Word: "classifier", Cost: "$0.0030", Tokens: "↑12.0k ↓120", Requests: 4},
			}},
		{Kind: SpendModel, Name: "claude-haiku-4-5", Cost: "$0.0010", Children: "$0.0030",
			Tokens: "↑6.2k ↓410", Requests: 2,
			Parts: []SpendPart{{Word: "summary", Cost: "$0.0010", Tokens: "↑6.2k ↓410", Requests: 2}}},
		{Kind: SpendChild, Name: "writer-1", Cost: "$0.0520", Models: []string{"claude-opus-5-5"},
			Tokens: "↑20.0k ↓600", Requests: 2},
		{Kind: SpendChild, Name: "researcher-1", Cost: "$0.0030", Models: []string{"claude-haiku-4-5"},
			Tokens: "↑4.0k ↓170", Requests: 1},
		{Kind: SpendTurn, Cost: "$0.0410", Turn: &TurnsItem{N: 3, Running: &InspectorTurn{Running: true}}},
		{Kind: SpendTurn, Cost: "$0.2870", Turn: &TurnsItem{N: 2, Close: &TurnClose{Spend: "$0.2870", Elapsed: "2m 10s"}}},
		{Kind: SpendTurn, Cost: "$0.0930", Turn: &TurnsItem{N: 1, Close: &TurnClose{State: TurnCancelled, Spend: "$0.0930", Elapsed: "31s"}}},
	}
}

// spendScreen is the screen over the fixture with the pointer on focus.
func spendScreen(focus int) *SpendScreen {
	s := &SpendScreen{Rows: spendFixture(), MaxLines: 24}
	s.Focus = focus
	return s
}

// The list is the total and the bill three ways under headings, a model in
// the SPEND block's own words, and the header counts the cuts beside the
// total.
func TestSpendScreen_ListsTheBillThreeWays(t *testing.T) {
	view := ansi.Strip(spendScreen(0).View(130))
	for _, want := range []string{
		"/stats", "2 models · 2 children · 3 turns", "$0.4210 spent", "[q] back",
		"session total", "by model", "claude-opus-5-5", "main · classifier · $0.0520 ◇",
		"by child", "◇ writer-1", "by turn", "▸ turn 3", "running", "⊘ turn 1", "cancelled",
		"by kind of request", "sub-agent", "96.0k cached",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
}

// A model's account is each kind of request with what it cost and was billed
// for, and what its children cost after their ◇.
func TestSpendScreen_AModelsAccountIsItsKinds(t *testing.T) {
	view := ansi.Strip(spendScreen(1).View(130))
	for _, want := range []string{"model", "classifier", "$0.0030 · ↑12.0k ↓120 · 4 requests", "children", "$0.0520 ◇"} {
		if !strings.Contains(view, want) {
			t.Errorf("the model's account is missing %q:\n%s", want, view)
		}
	}
}

// The pointer walks the rows over the headings, enter on a turn leaves with
// that turn and anywhere else does nothing, and esc leaves.
func TestSpendScreen_EnterOpensATurnAndNothingElse(t *testing.T) {
	s := spendScreen(0)
	s.View(130)
	if done, _ := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done {
		t.Fatal("enter on the total should do nothing")
	}
	for range 5 {
		s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if s.Focus != 5 || s.Rows[s.Focus].Kind != SpendTurn {
		t.Fatalf("five steps down from the total is the newest turn, got row %d", s.Focus)
	}
	if !strings.Contains(ansi.Strip(s.View(130)), "[enter] open the turn") {
		t.Fatalf("a turn under the pointer offers to open it:\n%s", ansi.Strip(s.View(130)))
	}
	done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || result.Turn != 3 {
		t.Fatalf("enter on turn 3: done %v, turn %d", done, result.Turn)
	}
	if done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done || result.Turn != 0 {
		t.Fatal("esc should leave with nothing")
	}
}

// Nothing a narrow screen draws runs past the terminal.
func TestSpendScreen_NarrowStaysInside(t *testing.T) {
	s := spendScreen(1)
	s.MaxLines = 40
	for _, line := range strings.Split(s.View(60), "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", ansi.Strip(line))
		}
	}
}

// A session that has spent nothing opens on a sentence saying so.
func TestSpendScreen_EmptySaysNothingWasBilled(t *testing.T) {
	s := &SpendScreen{MaxLines: 12}
	view := ansi.Strip(s.View(130))
	if !strings.Contains(view, "the session has not been billed for anything yet") || strings.Contains(view, "spent") {
		t.Errorf("the empty screen:\n%s", view)
	}
	if done, _ := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done {
		t.Fatal("enter on an empty screen should do nothing")
	}
}

// TestGolden_SpendScreen captures `/stats` over a bill of two models, two
// children and three turns: the pointer on the total, on a model, on a
// child and on a turn, and a session that has spent nothing.
func TestGolden_SpendScreen(t *testing.T) {
	captureGolden(t, "spend-screen", "the spend screen", goldenWidths, func(width int) []golden.Panel {
		return []golden.Panel{
			{Label: "the total · the whole bill by kind of request", View: spendScreen(0).View(width)},
			{Label: "a model · its own kinds of request, and its children's share after the ◇", View: spendScreen(1).View(width)},
			{Label: "a child · its share by name, and the model it ran on", View: spendScreen(3).View(width)},
			{Label: "a turn · its cost as its close states it, enter opens it on the turns screen", View: spendScreen(6).View(width)},
			{Label: "empty · a session that has spent nothing", View: (&SpendScreen{MaxLines: 12}).View(width)},
		}
	})
}
