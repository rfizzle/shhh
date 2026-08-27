package components

// The two lists the sliding window reached late (S-124, DESIGN-TUI.md §4a).
// S-116 gave the window to the selector and left the multi-select and the
// agent manager calling boundRows directly, so a pointer walked past the last
// row on either of those cards was navigating a list it could no longer see —
// the same bug, on the two surfaces that own their own Focus.
//
// These assert what is different about each: a multi-select can scroll the
// user's own answer off the card, and the agent manager holds rows that must
// never scroll at all.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// checkList is a list of choices longer than any bottom panel will hold —
// what `/memory forget` looks like on a session that learned a lot.
func checkList(n int) []SelectOption {
	opts := make([]SelectOption, 0, n)
	for i := 1; i <= n; i++ {
		opts = append(opts, SelectOption{Label: fmt.Sprintf("memory-%02d", i)})
	}
	return opts
}

// agentRows is a fan-out wider than the card: one orchestrator, one blocked
// child under it (the sort the host owes the list, §9a), and the rest running.
func agentRows(children int) []AgentRow {
	rows := []AgentRow{
		{State: AgentCurrent, Name: "orchestrator", Task: "this session", Status: "round 7 · streaming…", Spend: "$0.12"},
		{State: AgentBlocked, Name: "runner-2", Task: "go test ./...", Answerable: true,
			Progress: &AgentProgress{State: FanoutBlocked, Tools: 3, Spend: "$0.01"},
			Note:     "waiting approval: run go test ./internal/agent/..."},
	}
	for i := 1; i <= children; i++ {
		rows = append(rows, AgentRow{
			State: AgentRunning, Name: fmt.Sprintf("writer-%02d", i), Task: "docs/loop.md",
			Progress: &AgentProgress{State: FanoutRunning, Step: 2, Steps: 5, Tools: 6, Spend: "$0.02"},
		})
	}
	return rows
}

// Walking down the whole multi-select, the pointer is on the card at every
// step and the card never outgrows its bound.
func TestMultiSelectWindow_FollowsTheFocus(t *testing.T) {
	s := NewMultiSelect("Forget which memories?", checkList(20))
	s.MaxLines = 10
	for i := 0; i < 20; i++ {
		view := s.View(70)
		want := fmt.Sprintf("[ ] memory-%02d", i+1)
		if got := focusedRow(view); got != want {
			t.Fatalf("at focus %d the pointer should be on %q, got %q:\n%s", i, want, got, view)
		}
		if h := cardHeight(view); h > s.MaxLines {
			t.Fatalf("at focus %d the card grew to %d rows, past its %d bound:\n%s", i, h, s.MaxLines, view)
		}
		s.Update(key("down"))
	}
	for i := 19; i >= 0; i-- {
		view := s.View(70)
		want := fmt.Sprintf("[ ] memory-%02d", i+1)
		if got := focusedRow(view); got != want {
			t.Fatalf("walking back up, at focus %d the pointer should be on %q, got %q:\n%s", i, want, got, view)
		}
		s.Update(key("up"))
	}
}

// A list that fits is not windowed at all: no markers, and no row spent
// saying that nothing was hidden.
func TestMultiSelectWindow_AListThatFitsIsNotWindowed(t *testing.T) {
	s := NewMultiSelect("Forget which memories?", checkList(4))
	s.MaxLines = 12
	if view := ansi.Strip(s.View(70)); strings.Contains(view, "more") || strings.Contains(view, "…") {
		t.Fatalf("a list that fits should carry no markers:\n%s", view)
	}
}

// The window is shared code with the selector, so it is path-dependent in the
// same way: a row reached from above sits at the foot of the window and the
// same row reached from below sits at its head. Neither is a jump.
func TestMultiSelectWindow_IsPathDependentLikeTheSelector(t *testing.T) {
	walk := func(steps int, k string) []string {
		s := NewMultiSelect("Forget which memories?", checkList(20))
		s.MaxLines = 10
		if k == "up" {
			s.Focus = len(s.Options) - 1
		}
		for i := 0; i < steps; i++ {
			s.View(70)
			s.Update(key(k))
		}
		return strings.Split(ansi.Strip(s.View(70)), "\n")
	}
	rowOf := func(lines []string, label string) int {
		for i, line := range lines {
			if strings.Contains(line, label) {
				return i
			}
		}
		return -1
	}
	fromAbove, fromBelow := walk(10, "down"), walk(9, "up")
	if got := focusedRow(strings.Join(fromAbove, "\n")); got != "[ ] memory-11" {
		t.Fatalf("walking down eleven rows should land on memory-11, got %q", got)
	}
	if got := focusedRow(strings.Join(fromBelow, "\n")); got != "[ ] memory-11" {
		t.Fatalf("walking up nine rows should land on memory-11 too, got %q", got)
	}
	above, below := rowOf(fromAbove, "memory-11"), rowOf(fromBelow, "memory-11")
	if above <= below {
		t.Fatalf("reached from above memory-11 should sit lower on the card than when reached from below, got rows %d and %d:\n%s\n%s",
			above, below, strings.Join(fromAbove, "\n"), strings.Join(fromBelow, "\n"))
	}
}

// Scrolling is not answering: a row ticked and then scrolled out of the
// window comes back ticked, and applying takes it.
func TestMultiSelectWindow_SelectionSurvivesScrolling(t *testing.T) {
	s := NewMultiSelect("Forget which memories?", checkList(20))
	s.MaxLines = 10
	s.Update(key(" "))
	s.Update(key("down"))
	s.Update(key(" "))
	for i := 0; i < 18; i++ {
		s.View(70)
		s.Update(key("down"))
	}
	if view := ansi.Strip(s.View(70)); strings.Contains(view, "memory-01") {
		t.Fatalf("the ticked rows should have scrolled out of the window by now:\n%s", view)
	}
	for i := 0; i < 19; i++ {
		s.View(70)
		s.Update(key("up"))
	}
	view := ansi.Strip(s.View(70))
	for _, want := range []string{"[x] memory-01", "[x] memory-02"} {
		if !strings.Contains(view, want) {
			t.Fatalf("a row scrolled out and back should still be ticked, wanted %q:\n%s", want, view)
		}
	}
	done, result := s.Update(key("enter"))
	if !done {
		t.Fatalf("enter should apply the two ticked rows")
	}
	if got := result.(MultiSelectResult).Indices; len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("applying should take the rows that were ticked before the scroll, got %v", got)
	}
}

// A count that is out of sight is a count that has to be taken on trust, so
// the marker says how many of the rows it is hiding are ticked, and the key
// row goes on stating the total.
func TestMultiSelectWindow_TheMarkerSaysHowManyHiddenRowsAreChecked(t *testing.T) {
	s := NewMultiSelect("Forget which memories?", checkList(20))
	s.MaxLines = 10
	s.Checked[0], s.Checked[1], s.Checked[19] = true, true, true

	top := ansi.Strip(s.View(70))
	if !strings.Contains(top, "↓ 14 more · 1 checked") {
		t.Fatalf("the bottom marker should count the ticked row below it:\n%s", top)
	}
	if !strings.Contains(top, "apply (3)") {
		t.Fatalf("the key row should state the whole count:\n%s", top)
	}

	for i := 0; i < 19; i++ {
		s.View(70)
		s.Update(key("down"))
	}
	bottom := ansi.Strip(s.View(70))
	if !strings.Contains(bottom, "↑ 14 more · 2 checked") {
		t.Fatalf("the top marker should count the two ticked rows above it:\n%s", bottom)
	}
	if !strings.Contains(bottom, "apply (3)") {
		t.Fatalf("the key row should still state the whole count:\n%s", bottom)
	}
}

// Invariant 4 on this card too: the markers count rows, and they sum with
// what is on screen to the whole list. A run hiding nothing ticked says only
// what it hid.
func TestMultiSelectWindow_MarkersCountAndSumToTheList(t *testing.T) {
	s := NewMultiSelect("Forget which memories?", checkList(14))
	s.MaxLines = 10
	top := ansi.Strip(s.View(70))
	if !strings.Contains(top, "↓ 8 more") || strings.Contains(top, "checked") {
		t.Fatalf("nothing is ticked, so the marker should say only what it hid:\n%s", top)
	}
	shown := strings.Count(top, "memory-")
	if shown+8 != 14 {
		t.Fatalf("the marker and the rows on screen should sum to the list: %d shown + 8 hidden:\n%s", shown, top)
	}
}

// The pointer stays on the agent list too, whatever the fan-out did.
func TestAgentListWindow_FollowsTheFocus(t *testing.T) {
	l := &AgentList{Rows: agentRows(12), MaxLines: 12}
	for i := 0; i < len(l.Rows); i++ {
		view := l.View(80)
		if got := focusedRow(view); !strings.Contains(got, l.Rows[i].Name) {
			t.Fatalf("at focus %d the pointer should be on %q, got %q:\n%s", i, l.Rows[i].Name, got, view)
		}
		if h := cardHeight(view); h > l.MaxLines {
			t.Fatalf("at focus %d the card grew to %d rows, past its %d bound:\n%s", i, h, l.MaxLines, view)
		}
		l.Update(key("down"))
	}
}

// The manager's own rule: blocked children never scroll. Opening the list
// because something needs you and then having to hunt for it would undo the
// reason the list is there.
func TestAgentListWindow_BlockedChildrenStayPinnedAboveTheWindow(t *testing.T) {
	l := &AgentList{Rows: agentRows(12), MaxLines: 12}
	for i := 0; i < len(l.Rows); i++ {
		view := ansi.Strip(l.View(80))
		if !strings.Contains(view, "runner-2") {
			t.Fatalf("at focus %d the blocked child scrolled off the card:\n%s", i, view)
		}
		if !strings.Contains(view, "waiting approval") {
			t.Fatalf("at focus %d the blocked child lost the line saying what it waits for:\n%s", i, view)
		}
		if !strings.Contains(view, "orchestrator") {
			t.Fatalf("at focus %d the list lost its head row:\n%s", i, view)
		}
		l.Update(key("down"))
	}
}

// The markers count agents rather than lines: a child carrying a note is two
// rows and one agent.
func TestAgentListWindow_MarkersCountAgentsNotRows(t *testing.T) {
	rows := agentRows(8)
	rows[4].Note = "reading internal/ui/chat/model.go"
	l := &AgentList{Rows: rows, MaxLines: 11}
	view := ansi.Strip(l.View(80))
	shown := 0
	for _, r := range rows {
		if strings.Contains(view, r.Name) {
			shown++
		}
	}
	hidden := 0
	if _, err := fmt.Sscanf(markerLine(view, "↓"), "↓ %d more", &hidden); err != nil {
		t.Fatalf("the card should carry a bottom marker: %v\n%s", err, view)
	}
	if shown+hidden != len(rows) {
		t.Fatalf("the marker and the agents on screen should sum to the list: %d shown + %d hidden of %d:\n%s",
			shown, hidden, len(rows), view)
	}
}

// boundRows is still the last line of defence: a card too short even for what
// is pinned to it overruns nothing.
func TestAgentListWindow_BoundRowsStillHoldsTheHeight(t *testing.T) {
	rows := agentRows(6)
	// Every child blocked is the case the window cannot help with — they are
	// all pinned — so the height contract falls to boundRows.
	for i := range rows {
		rows[i].State = AgentBlocked
		rows[i].Note = "waiting approval: run go test ./..."
	}
	for _, maxLines := range []int{6, 8, 12} {
		l := &AgentList{Rows: rows, MaxLines: maxLines}
		if h := cardHeight(l.View(80)); h > maxLines {
			t.Fatalf("a card of %d rows rendered %d:\n%s", maxLines, h, l.View(80))
		}
	}
	s := NewMultiSelect("Forget which memories?", checkList(20))
	s.MaxLines = 5
	if h := cardHeight(s.View(70)); h > s.MaxLines {
		t.Fatalf("a multi-select of %d rows rendered %d:\n%s", s.MaxLines, h, s.View(70))
	}
}

// markerLine is the overflow marker carrying the given arrow, or "".
func markerLine(view, arrow string) string {
	for _, line := range strings.Split(view, "\n") {
		if trimmed := strings.TrimSpace(strings.Trim(line, "│ ")); strings.HasPrefix(trimmed, arrow) {
			return trimmed
		}
	}
	return ""
}
