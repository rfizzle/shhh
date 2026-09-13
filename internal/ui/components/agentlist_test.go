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

// TestAgentListOffersWhatTheListCanDo: answering in place and killing every
// child are offers about the list, so they stand wherever the pointer is
// standing — an offer a reader has to go hunting for with the pointer is
// indistinguishable from an offer that is not there. Retry is the row's, and
// stays with it.
func TestAgentListOffersWhatTheListCanDo(t *testing.T) {
	rows := append(managerRows(), AgentRow{State: AgentOffer, Name: "draft a new profile"})
	for focus := range rows {
		view := ansi.Strip((&AgentList{Rows: rows, Focus: focus}).View(96))
		for _, want := range []string{"[a] answer without attaching", "[K] kill all", "[esc] back to the turn"} {
			if !strings.Contains(view, want) {
				t.Fatalf("focus %d missing %q:\n%s", focus, want, view)
			}
		}
		if got := strings.Contains(view, "[x] cancel"); got != (focus != 4) {
			t.Fatalf("focus %d offers cancel=%v, want %v:\n%s", focus, got, focus != 4, view)
		}
		if got := strings.Contains(view, "[r] retry"); got != (focus == 3) {
			t.Fatalf("focus %d offers retry=%v, want %v:\n%s", focus, got, focus == 3, view)
		}
	}
}

// The two list-wide offers are still offers, so neither is drawn where it
// would do nothing: nothing blocked, no answer to give; one child left, no
// second one for kill-all to reach.
func TestAgentListDropsTheOffersTheListCannotMake(t *testing.T) {
	p := func(v AgentProgress) *AgentProgress { return &v }
	rows := []AgentRow{
		{State: AgentCurrent, Name: "orchestrator", Status: "round 7"},
		{State: AgentRunning, Name: "writer-1", Task: "docs/loop.md",
			Progress: p(AgentProgress{State: FanoutRunning, Step: 2, Steps: 5})},
		{State: AgentDone, Name: "reader-3", Task: "survey",
			Progress: p(AgentProgress{State: FanoutDone})},
	}
	view := ansi.Strip((&AgentList{Rows: rows, Focus: 1}).View(96))
	for _, gone := range []string{"[a] answer", "[K] kill all"} {
		if strings.Contains(view, gone) {
			t.Fatalf("one running child and nothing blocked must not offer %q:\n%s", gone, view)
		}
	}
	if _, res := (&AgentList{Rows: rows, Focus: 1}).Update(agentKey("K")); res.Action != AgentNone {
		t.Fatalf("[K] with one live child = %#v, want nothing", res)
	}
}

// A manager row joins the child's name to its task with the separator every
// row in the product joins two facts with, which is what the lane above it in
// the transcript joins the same two with.
func TestAgentRowJoinsNameAndTaskWithTheSeparator(t *testing.T) {
	view := ansi.Strip((&AgentList{Rows: managerRows()}).View(96))
	if !strings.Contains(view, "writer-1 · docs/loop.md") {
		t.Fatalf("the row should read `writer-1 · docs/loop.md`:\n%s", view)
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
	// [a] away from the blocked row answers the blocked row: the key is the
	// list's, and blocked children sort to the top, so the one it reaches is
	// the one the manager was opened for.
	if _, result := (&AgentList{Rows: rows, Focus: 2}).Update(agentKey("a")); result.Action != AgentAnswer || result.Index != 1 {
		t.Fatalf("[a] from the running row = %#v, want AgentAnswer on row 1", result)
	}
	if _, result := (&AgentList{Rows: rows, Focus: 1}).Update(agentKey("r")); result.Action != AgentNone {
		t.Fatalf("[r] on a row that cannot be retried = %#v, want nothing", result)
	}
}

// [K] is about the list, so it carries no row: a host reading an index off it
// would kill whichever child the pointer happened to rest on as well as all
// of them.
func TestAgentListKillAllCarriesNoRow(t *testing.T) {
	l := &AgentList{Rows: managerRows(), Focus: 2}
	done, result := l.Update(agentKey("K"))
	if done || result.Action != AgentKillAll || result.Index != -1 {
		t.Fatalf("[K] = %#v (done=%v), want AgentKillAll with no index and the list open", result, done)
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

// nestedManagerRows is the list a host hands over once a child has
// delegated: the group holding the request has floated whole, so the
// grandchild waiting on an answer sits directly under the row it belongs to.
func nestedManagerRows() []AgentRow {
	p := func(v AgentProgress) *AgentProgress { return &v }
	return []AgentRow{
		{State: AgentCurrent, Name: "orchestrator", Status: "round 7", Spend: "$0.12"},
		{State: AgentRunning, Name: "writer-1", Task: "docs/loop.md", Depth: 1,
			Progress: p(AgentProgress{State: FanoutRunning, Step: 2, Steps: 5, Tools: 6, Spend: "$0.02"})},
		{State: AgentBlocked, Name: "reviewer-1a", Task: "read the change", Depth: 2, Answerable: true,
			Progress: p(AgentProgress{State: FanoutBlocked, Tools: 2, Spend: "$0.01"}),
			Note:     "waiting approval: read internal/agent/loop.go"},
		{State: AgentDone, Name: "reader-2", Task: "survey internal/ui", Depth: 1,
			Progress: p(AgentProgress{State: FanoutDone, Tools: 8, Spend: "$0.02"})},
	}
}

// TestAgentRowDrawsADescendantUnderItsParent: a row a child spawned takes the
// column before its glyph for the corner, which is where the rail's map puts
// the same corner on the same agent, and its note moves in with it.
func TestAgentRowDrawsADescendantUnderItsParent(t *testing.T) {
	view := ansi.Strip((&AgentList{Rows: nestedManagerRows()}).View(96))
	var row, note string
	for _, line := range strings.Split(view, "\n") {
		switch {
		case strings.Contains(line, "reviewer-1a"):
			row = line
		case strings.Contains(line, "waiting approval"):
			note = line
		}
	}
	if !strings.Contains(row, "└⚠ reviewer-1a") {
		t.Fatalf("a row a child spawned should draw behind the corner: %q", row)
	}
	// Under its own name rather than under a sibling of its parent's: the
	// line belongs to the row above it, and the row moved. In columns, not
	// bytes — the corner and the state mark are three bytes each.
	column := func(line, text string) int {
		return len([]rune(line[:strings.Index(line, text)]))
	}
	if a, b := column(row, "reviewer-1a"), column(note, "waiting"); a != b {
		t.Fatalf("the note starts at column %d and the name at %d: %q under %q", b, a, note, row)
	}
}

// TestAgentListPinsTheWholeGroup: a request under a nested row is pinned
// above the window with the row it hangs off, because half a group above the
// fold is a corner under nothing.
func TestAgentListPinsTheWholeGroup(t *testing.T) {
	l := &AgentList{Rows: nestedManagerRows()}
	pinned, scrolling := l.split()
	if len(pinned) != 3 {
		t.Fatalf("pinned %v, want the orchestrator, the parent and the request under it", pinned)
	}
	if len(scrolling) != 1 || scrolling[0] != 3 {
		t.Fatalf("scrolling %v, want only the settled child", scrolling)
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
