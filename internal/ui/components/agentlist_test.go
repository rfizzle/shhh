package components

// Agent manager v2: a row reports through the fan-out lane's
// renderer, states what a blocked or failed child is waiting on or died of,
// and offers [a] and [r] only where they do something.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func agentKey(s string) tea.KeyPressMsg {
	if s == "enter" {
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// managerRows is one of each state, blocked first the way the host sorts it.
func managerRows() []AgentRow {
	p := func(v AgentProgress) *AgentProgress { return &v }
	return []AgentRow{
		{State: AgentCurrent, Name: "orchestrator", Status: "round 7", Spend: "$0.12"},
		{State: AgentBlocked, Name: "runner-2", Task: "go test ./...", Answerable: true,
			Progress: p(AgentProgress{State: FanoutBlocked, Tools: 3, Spend: "$0.01"}),
			Note:     "waiting approval: run go test ./..."},
		{State: AgentRunning, Name: "writer-1", Task: "docs/loop.md",
			Progress: p(AgentProgress{State: FanoutRunning, Step: 2, Steps: 5, Tools: 6, Spend: "$0.02"})},
		{State: AgentFailed, Name: "patcher-4", Task: "apply patch", Retryable: true,
			Progress: p(AgentProgress{State: FanoutFailed, Tools: 1, Spend: "$0.01"}),
			Note:     "round limit (25) reached"},
	}
}

// TestAgentRowAndLaneAgreeOnProgress is the "one renderer" claim, asserted:
// the same child drawn as a manager row and as a fan-out lane reports the
// same thing in the same words.
func TestAgentRowAndLaneAgreeOnProgress(t *testing.T) {
	progress := AgentProgress{State: FanoutRunning, Step: 2, Steps: 5, Tools: 6, Spend: "$0.02"}
	row := AgentRow{State: AgentRunning, Name: "writer-1", Task: "docs/loop.md", Progress: &progress}
	lane := FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md",
		Step: 2, Steps: 5, Tools: 6, Spend: "$0.02"}
	if got, want := row.rightField(), lane.outcomeField(); got != want {
		t.Fatalf("row reports %q, lane reports %q — they must be one renderer", got, want)
	}
}

func TestAgentListStatesWhatARowIsWaitingOnAndDiedOf(t *testing.T) {
	l := &AgentList{Rows: managerRows()}
	view := ansi.Strip(l.View(96))
	for _, want := range []string{
		"⚠ needs you",
		"waiting approval: run go test ./...",
		"failed",
		"round limit (25) reached",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("manager missing %q:\n%s", want, view)
		}
	}
}

// TestAgentListOffersOnlyWhatTheRowCanDo: a key hint is a promise, so [a] and
// [r] appear on the rows that can act on them and nowhere else.
func TestAgentListOffersOnlyWhatTheRowCanDo(t *testing.T) {
	rows := managerRows()
	cases := []struct {
		focus int
		want  []string
		gone  []string
	}{
		{focus: 0, gone: []string{"a answer", "r retry"}},
		{focus: 1, want: []string{"a answer"}, gone: []string{"r retry"}},
		{focus: 2, gone: []string{"a answer", "r retry"}},
		{focus: 3, want: []string{"r retry"}, gone: []string{"a answer"}},
	}
	for _, c := range cases {
		view := ansi.Strip((&AgentList{Rows: rows, Focus: c.focus}).View(96))
		for _, want := range c.want {
			if !strings.Contains(view, want) {
				t.Fatalf("focus %d missing %q:\n%s", c.focus, want, view)
			}
		}
		for _, gone := range c.gone {
			if strings.Contains(view, gone) {
				t.Fatalf("focus %d must not offer %q:\n%s", c.focus, gone, view)
			}
		}
	}
}

func TestAgentListAnswerAndRetryKeys(t *testing.T) {
	rows := managerRows()

	l := &AgentList{Rows: rows, Focus: 1}
	done, result := l.Update(agentKey("a"))
	res := result
	if res.Action != AgentAnswer || res.Index != 1 {
		t.Fatalf("[a] on a blocked row = %#v, want AgentAnswer on row 1", result)
	}
	if done {
		// The card renders over the list and hands it back, so the list stays.
		t.Fatal("answering must not dismiss the list")
	}

	l = &AgentList{Rows: rows, Focus: 3}
	done, result = l.Update(agentKey("r"))
	res = result
	if res.Action != AgentRetry || res.Index != 3 || done {
		t.Fatalf("[r] on a failed row = %#v (done=%v), want AgentRetry on row 3", result, done)
	}
}

func TestAgentListIgnoresKeysARowDoesNotOffer(t *testing.T) {
	rows := managerRows()
	// [a] on the running child and [r] on the blocked one do nothing rather
	// than reporting a failure the row already predicted.
	if _, result := (&AgentList{Rows: rows, Focus: 2}).Update(agentKey("a")); result.Action != AgentNone {
		t.Fatalf("[a] on a row that cannot be answered = %#v, want nothing", result)
	}
	if _, result := (&AgentList{Rows: rows, Focus: 1}).Update(agentKey("r")); result.Action != AgentNone {
		t.Fatalf("[r] on a row that cannot be retried = %#v, want nothing", result)
	}
}

func TestAgentListKeepsTodaysSemantics(t *testing.T) {
	rows := managerRows()
	l := &AgentList{Rows: rows, Focus: 2}
	if done, result := l.Update(agentKey("enter")); !done || result.Action != AgentAttach {
		t.Fatalf("enter = %#v (done=%v), want AgentAttach", result, done)
	}
	if done, result := l.Update(agentKey("x")); done || result.Action != AgentCancel {
		t.Fatalf("x = %#v (done=%v), want AgentCancel with the list open", result, done)
	}
	if done, result := l.Update(agentKey("X")); done || result.Action != AgentKill {
		t.Fatalf("X = %#v (done=%v), want AgentKill with the list open", result, done)
	}
	if done, result := l.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done || result.Action != AgentBack {
		t.Fatalf("esc = %#v (done=%v), want AgentBack", result, done)
	}
}

// TestAgentListTallyIsTheFanoutHeader: the manager's title rail and the
// fan-out header are the same sentence about the same children.
func TestAgentListTallyIsTheFanoutHeader(t *testing.T) {
	rows := managerRows()
	block := FanoutBlock{Lanes: []FanoutLane{
		{State: FanoutBlocked}, {State: FanoutRunning}, {State: FanoutFailed},
	}}
	if got, want := (&AgentList{Rows: rows}).tally(), block.headerOutcome(); got != want {
		t.Fatalf("manager tally %q, fan-out header %q", got, want)
	}
	if view := ansi.Strip((&AgentList{Rows: rows}).View(96)); !strings.Contains(view, "1 needs you") {
		t.Fatalf("the title rail must state who needs you:\n%s", view)
	}
}

// TestAgentListWithoutChildrenHasNoTally: a manager holding only the
// orchestrator states nothing about children it does not have.
func TestAgentListWithoutChildrenHasNoTally(t *testing.T) {
	l := &AgentList{Rows: []AgentRow{{State: AgentCurrent, Name: "orchestrator", Status: "ready"}}}
	if tally := l.tally(); tally != "" {
		t.Fatalf("tally = %q, want empty with no children", tally)
	}
}

func TestAgentListFitsItsWidth(t *testing.T) {
	for _, width := range []int{60, 80, 110, 130} {
		view := (&AgentList{Rows: managerRows(), Focus: 1}).View(width)
		for _, line := range strings.Split(view, "\n") {
			if got := len([]rune(ansi.Strip(line))); got > width {
				t.Fatalf("width %d: line is %d columns: %q", width, got, ansi.Strip(line))
			}
		}
	}
}
