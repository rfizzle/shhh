package components

// The sprint tab against its own rules: when the tab is there at all, what
// the board's head states, and what each of the plan card's five keys does.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// A project with no sprint has no sprint tab, so the tab key steps straight
// to the archive: a tab that opens on "there is no sprint" is a place the
// reader learns to stop pressing.
func TestSprintTab_IsAbsentWithoutASprint(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("tab"))
	if !b.archived() {
		t.Fatalf("tab = %d; with no board the key goes to the archive", b.tab)
	}
	b.Board = goldenSprintBoard()
	b.tab = backlogTabItems
	b.Update(key("tab"))
	if !b.sprinting() {
		t.Fatalf("tab = %d; with a board the key goes to the sprint", b.tab)
	}
	b.Update(key("tab"))
	if !b.archived() {
		t.Fatalf("tab = %d; the archive comes after the sprint", b.tab)
	}
}

// A sprint that closed under the reader steps them back to the backlog
// rather than leaving them on a tab that is no longer there.
func TestSprintTab_StepsBackWhenTheSprintGoes(t *testing.T) {
	b := goldenSprintScreen(goldenSprintBoard())
	b.Board = nil
	b.View(110)
	if b.tab != backlogTabItems {
		t.Fatalf("tab = %d after the sprint closed", b.tab)
	}
}

// The head states what the set is for, how far through it is, what it has
// cost and what comes next — the four facts a board is opened to answer.
func TestSprintBoard_HeadStatesTheSet(t *testing.T) {
	view := ansi.Strip(goldenSprintScreen(goldenSprintBoard()).View(110))
	for _, want := range []string{
		"backlog · sprint · the cockpit sprint",
		"Put the backlog, the run and the sprint on screen",
		"1 of 4 done", "9 turns · $1.42", "next · prose-renderer",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the board never says %q:\n%s", want, view)
		}
	}
}

// The row's state field is the sprint's reading of the slug, not the item's
// status in the backlog: the one in flight says which stage it is at, and a
// slug the backlog dropped says so.
func TestSprintBoard_RowsCarryTheSetsOwnReading(t *testing.T) {
	view := ansi.Strip(goldenSprintScreen(goldenSprintBoard()).View(130))
	for _, want := range []string{"rail-todo-block", "implement", "cache-warm", "dropped from the backlog"} {
		if !strings.Contains(view, want) {
			t.Errorf("the set's rows never say %q:\n%s", want, view)
		}
	}
}

// A set that stopped names the block and the item that wrote it, on the
// board rather than only in the transcript of the session that hit it.
func TestSprintBoard_ShowsTheBlockThatStoppedIt(t *testing.T) {
	board := goldenSprintBoard()
	board.Stopped = "sprint-file blocked — waiting on a decision"
	view := ansi.Strip(goldenSprintScreen(board).View(110))
	if !strings.Contains(view, "sprint-file blocked — waiting on a decision") {
		t.Errorf("the block is not on the board:\n%s", view)
	}
}

// A closed sprint's last row is the page it wrote, whole: a URL is the one
// field that must never be clipped into something nobody can paste.
func TestSprintBoard_ClosedOffersItsPage(t *testing.T) {
	board := &SprintBoard{Name: "caching", Closed: true, Report: "http://127.0.0.1:8731/r/rp-0123456789abcdef"}
	view := ansi.Strip(goldenSprintScreen(board).View(110))
	if !strings.Contains(view, "→ http://127.0.0.1:8731/r/rp-0123456789abcdef") {
		t.Errorf("the closed board never offers its page:\n%s", view)
	}
}

// planScreen is a screen with a proposal on its sprint tab.
func planScreen() *BacklogScreen {
	b := &BacklogScreen{
		Rows: goldenBacklogRows(), MaxLines: 24,
		Plan: &SprintPlan{
			Budget: "S=2 M=1",
			Rows: []SprintPlanRow{
				{Slug: "one", Title: "First", Note: "high · S"},
				{Slug: "two", Title: "Second", Note: "medium · M"},
				{Slug: "three", Title: "Third", Note: "low · S"},
			},
		},
	}
	b.Priority, b.Fields = goldenBacklogFields()
	return b
}

// The card holds the keyboard: j/k move where the list under it moves on
// the arrows alone, space drops and restores, and enter hands back what is
// left in the order it is drawn.
func TestSprintPlan_KeysAnswerTheCard(t *testing.T) {
	b := planScreen()
	b.Update(key("j"))
	if b.Plan.focus != 1 {
		t.Fatalf("j left the pointer at %d", b.Plan.focus)
	}
	b.Update(key(" "))
	if !b.Plan.Rows[1].Dropped {
		t.Fatal("space did not drop the row")
	}
	if got := strings.Join(b.Plan.Kept(), ","); got != "one,three" {
		t.Fatalf("kept = %q", got)
	}
	b.Update(key(" "))
	if b.Plan.Rows[1].Dropped {
		t.Fatal("space did not put the row back")
	}
	b.Update(key("k"))
	if b.Plan.focus != 0 {
		t.Fatalf("k left the pointer at %d", b.Plan.focus)
	}
	b.Plan.Rows[0].Dropped = true
	_, res := b.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Do == nil || res.Do.Act != BacklogSprintTake {
		t.Fatalf("enter = %+v", res.Do)
	}
	if got := strings.Join(res.Do.Slugs, ","); got != "two,three" {
		t.Fatalf("enter handed back %q, not what was left in order", got)
	}
}

// Nothing on the card is written until it is taken, and the ways out say
// which of the two they are.
func TestSprintPlan_TheWaysOut(t *testing.T) {
	b := planScreen()
	_, res := b.Update(key("g"))
	if res.Do == nil || res.Do.Act != BacklogSprintGoal {
		t.Fatalf("g = %+v", res.Do)
	}
	done, res := b.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if done || res.Do == nil || res.Do.Act != BacklogSprintCancel {
		t.Fatalf("esc = %v %+v; it answers the card rather than closing the screen", done, res.Do)
	}
}

// An empty set would write a sprint that scopes the ready list to nothing,
// so the card refuses and says which key puts a row back.
func TestSprintPlan_RefusesAnEmptySet(t *testing.T) {
	b := planScreen()
	for i := range b.Plan.Rows {
		b.Plan.Rows[i].Dropped = true
	}
	_, res := b.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Do != nil {
		t.Fatalf("enter over an empty set = %+v", res.Do)
	}
	if !strings.Contains(b.Notice, "nothing is left in the set") {
		t.Fatalf("notice = %q", b.Notice)
	}
}

// While the card is up the screen's own row letters are not live, so none
// of them is offered: a key that cannot act is not an offer.
func TestSprintPlan_OffersOnlyItsOwnKeys(t *testing.T) {
	view := ansi.Strip(planScreen().View(110))
	for _, want := range []string{"[↑↓/jk] move", "[space] drop it, or put it back", "[enter] write the sprint", "[esc] write nothing"} {
		if !strings.Contains(view, want) {
			t.Errorf("the card never offers %q:\n%s", want, view)
		}
	}
	for _, gone := range []string{"[R] run it", "[x] drop it", "[tab] what shipped"} {
		if strings.Contains(view, gone) {
			t.Errorf("the card still offers %q, which it does not answer:\n%s", gone, view)
		}
	}
}

// The progress meter is a ratio or it is nothing: a bar drawn against a
// total of nothing would say a sprint with no items is finished.
func TestSprintMeter_NeedsATotal(t *testing.T) {
	if _, ok := SprintMeter(0, 0, 8); ok {
		t.Fatal("a set of nothing drew a meter")
	}
	m, ok := SprintMeter(3, 7, 8)
	if !ok || m.Text != "3 of 7 done" {
		t.Fatalf("meter = %+v %v", m, ok)
	}
}
