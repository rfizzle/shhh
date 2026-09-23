package chat

// The turns a rewind takes back are held as a fold beside the branch, out of
// the window, readable and searchable, and [r] on the fold puts them back
// (docs/interface/surfaces.md#the-rewind). The picker's [d] reads what a
// rewind to a row would take back before the row is taken.

import (
	"os"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// threeTurnModel is three finished turns that wrote nothing, so a rewind goes
// straight back without a card.
func threeTurnModel(t *testing.T) Model {
	t.Helper()
	m := newRewindModel(t).WithDB(rewindTestDB(t))
	m = completeExchange(t, m, "first question", "answer one")
	m = completeExchange(t, m, "second question", "answer two")
	return completeExchange(t, m, "third question", "answer three")
}

// rewoundAt is the index of the newest fold in the transcript, or a fatal.
func rewoundAt(t *testing.T, m Model) int {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryRewound {
			return i
		}
	}
	t.Fatal("no rewound fold in the transcript")
	return -1
}

// readerRows counts the rows carrying what the reader typed, which is what
// every reader of the transcript numbers turns by.
func readerRows(m Model) int {
	n := 0
	for _, e := range m.transcript {
		if e.kind == entryUser {
			n++
		}
	}
	return n
}

// focusOn puts reading mode's cursor on the entry at idx.
func focusOn(t *testing.T, m Model, idx int) Model {
	t.Helper()
	next, _ := m.enterFocusMode()
	m = next.(Model)
	m.focusIdx = idx
	return m
}

// The fold holds the turns' own rows, and they are out of the window: the
// conversation the model is sent holds none of it, and nothing that numbers
// turns off the transcript counts them.
func TestRewind_TheTurnsTakenBackFoldOutOfTheWindow(t *testing.T) {
	m := threeTurnModel(t)
	m = sendText(t, m, "/rewind 1")

	f := m.transcript[rewoundAt(t, m)].rewound
	if f.first != 2 || f.last != 3 {
		t.Fatalf("the fold should hold turns 2–3, got %d–%d", f.first, f.last)
	}
	var said []string
	for _, r := range f.rows {
		if !r.outOfWindow {
			t.Fatalf("a rewound row should be out of the window: %+v", r)
		}
		said = append(said, r.text)
	}
	if got := strings.Join(said, "|"); !strings.Contains(got, "second question") ||
		!strings.Contains(got, "answer three") {
		t.Fatalf("the fold should hold the rows the turns left, got %q", got)
	}
	for _, msg := range m.Messages() {
		if strings.Contains(msg.Content, "answer two") || strings.Contains(msg.Content, "third question") {
			t.Fatalf("a rewound turn must never be sent again, found %q", msg.Content)
		}
	}
	if got := readerRows(m); got != len(m.checkpoints) {
		t.Fatalf("the transcript's reader rows (%d) should number the checkpoints (%d)", got, len(m.checkpoints))
	}

	closed := stripANSI(m.renderHistory())
	if !strings.Contains(closed, "▸ turns 2–3 · rewound · 2 turns, nothing written") ||
		!strings.Contains(closed, "read them") {
		t.Fatalf("the fold row should say what it holds and how to open it:\n%s", closed)
	}
	if strings.Contains(closed, "answer two") {
		t.Fatal("a closed fold draws none of its rows")
	}
	idx := rewoundAt(t, m)
	m.transcript[idx].expanded = true
	m.invalidateRenderCache()
	if open := stripANSI(m.renderHistory()); !strings.Contains(open, "answer two") ||
		!strings.Contains(open, "▾ turns 2–3") {
		t.Fatalf("an open fold draws the turns it holds:\n%s", open)
	}
}

// [r] undoes the rewind: the conversation, the checkpoints and the reader's
// rows are what they were before it, and the frame no longer says the session
// stands at an earlier turn.
func TestRewind_ReapplyPutsBackExactlyWhatTheRewindTook(t *testing.T) {
	m := threeTurnModel(t)
	wantMsgs := append([]provider.Message(nil), m.Messages()...)
	wantCps := append([]checkpoint(nil), m.checkpoints...)

	m = sendText(t, m, "/rewind 1")
	idx := rewoundAt(t, m)
	m = focusOn(t, m, idx)
	if offers := m.readingRowOffers(); len(offers) != 1 || !strings.Contains(offers[0].Label, "reapply") {
		t.Fatalf("the fold should offer the reapply under the cursor, got %+v", offers)
	}
	m = press(t, m, keys.Shown(keys.Row.Retry))

	if got := m.Messages(); !reflect.DeepEqual(got, wantMsgs) {
		t.Fatalf("the conversation should be what it was before the rewind:\n got %+v\nwant %+v", got, wantMsgs)
	}
	if len(m.checkpoints) != len(wantCps) {
		t.Fatalf("the checkpoints should come back, got %d want %d", len(m.checkpoints), len(wantCps))
	}
	for i, cp := range m.checkpoints {
		if cp.index != wantCps[i].index || cp.preview != wantCps[i].preview || cp.turn != wantCps[i].turn {
			t.Fatalf("checkpoint %d moved: got %+v want %+v", i, cp, wantCps[i])
		}
		if m.Messages()[cp.index].Content != cp.preview {
			t.Fatalf("checkpoint %d no longer points at its turn's message", i)
		}
	}
	if got := readerRows(m); got != len(m.checkpoints) {
		t.Fatalf("the reader rows (%d) should number the checkpoints again (%d)", got, len(m.checkpoints))
	}
	if m.rewoundTo != nil {
		t.Fatal("a reapplied rewind leaves the session at its latest turn")
	}
	f := m.transcript[idx].rewound
	if !f.reapplied || len(f.rows) != 0 {
		t.Fatal("the fold should say it was reapplied and hold nothing now")
	}
	if !strings.Contains(stripANSI(m.renderHistory()), "turns 2–3 · rewound, then reapplied below") {
		t.Fatal("the fold should stay as the record that the turns were once taken back")
	}
	// And a second rewind to the same turn takes back the same turns.
	m = press(t, m, "esc")
	m = sendText(t, m, "/rewind 2")
	if f := m.transcript[rewoundAt(t, m)].rewound; f.first != 3 || f.last != 3 {
		t.Fatalf("the reapplied turns should be rewindable again, got %d–%d", f.first, f.last)
	}
}

// A rewind that put the files back is undone in both halves: the reapply
// takes back the turn the restore landed as, through the undo confirm.
func TestRewind_ReapplyPutsTheFilesBackThroughTheConfirm(t *testing.T) {
	m, kept, added := rewindOfferModel(t)
	m = sendText(t, m, "/rewind 1")
	m = press(t, m, keys.Shown(keys.Rewind.Both))
	m = press(t, m, "y")
	if got, _ := os.ReadFile(kept); string(got) != "two\n" {
		t.Fatalf("the rewind should have put kept.go back, got %q", got)
	}
	f := m.transcript[rewoundAt(t, m)].rewound
	if f.restored == 0 {
		t.Fatal("the fold should know which turn the file half landed as")
	}

	m = focusOn(t, m, rewoundAt(t, m))
	m = press(t, m, keys.Shown(keys.Row.Retry))
	if m.state != stateUndoConfirm {
		t.Fatalf("the file half is put back through the undo confirm, got state %v", m.state)
	}
	m = press(t, m, "y")
	if got, _ := os.ReadFile(kept); string(got) != "three\n" {
		t.Fatalf("the reapply should leave kept.go as turn 2 did, got %q", got)
	}
	if got, err := os.ReadFile(added); err != nil || string(got) != "new\n" {
		t.Fatalf("the reapply should bring back the file turn 2 wrote, got %q (%v)", got, err)
	}
}

// A fold can only be put back onto the conversation it was cut from. Once
// the session has moved on it stays readable and offers nothing, and [r] is
// a letter again.
func TestRewind_AFoldIsSpentOnceTheSessionMovesOn(t *testing.T) {
	m := threeTurnModel(t)
	m = sendText(t, m, "/rewind 1")
	m = completeExchange(t, m, "a different second question", "a different answer")

	idx := rewoundAt(t, m)
	if offers := m.rewoundOffers(m.transcript[idx]); len(offers) != 0 {
		t.Fatalf("a spent fold offers nothing, got %+v", offers)
	}
	m = focusOn(t, m, idx)
	before := len(m.Messages())
	if _, _, claimed := m.rowKey(keys.Shown(keys.Row.Retry)); claimed {
		t.Fatal("a spent fold must not claim [r]")
	}
	if len(m.Messages()) != before {
		t.Fatal("nothing should have been put back")
	}
	if !expandable(m.transcript[idx]) {
		t.Fatal("a spent fold is still something to read")
	}
}

// The search counts what a closed fold holds, says so on the fold's row, and
// opens it onto the match.
func TestRewind_TheFoldIsSearched(t *testing.T) {
	m := threeTurnModel(t)
	m = sendText(t, m, "/rewind 1")
	idx := rewoundAt(t, m)
	m = focusOn(t, m, idx)

	if visible := m.searchTranscript("answer three"); visible != 0 {
		t.Fatalf("the fixture wants nothing on screen to match, the pane found %d", visible)
	}
	if _, total := m.searchPosition(); total != 1 {
		t.Fatalf("the session holds one occurrence behind the fold, the count says %d", total)
	}
	if row := stripANSI(m.renderEntry(m.transcript[idx], 110)); !strings.Contains(row, "1 match inside") {
		t.Fatalf("the fold row should count the match behind it:\n%s", row)
	}
	next, opened := m.openFoldToMatch()
	if !opened || !next.transcript[idx].expanded {
		t.Fatal("enter on the fold should open it onto the match")
	}
	if !next.clearSearchFolds() || next.transcript[idx].expanded {
		t.Fatal("clearing the query folds back what the search opened")
	}
}

// [d] on the picker opens what a rewind to the row would take back, full
// screen, and esc comes back to the picker as it was left.
func TestRewindPicker_TheDiffKeyOpensTheRunAndComesBack(t *testing.T) {
	m, _, _ := rewindOfferModel(t)
	m = sendText(t, m, "/rewind")
	// The card opens as a search; its letters are text until the row closes.
	m = press(t, m, "d")
	if m.state != statePick || m.picker.Query != "d" {
		t.Fatalf("a letter typed into the query is text, got state %v query %q", m.state, m.picker.Query)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.picker.Filtering {
		t.Fatal("the fixture wants the query row closed")
	}
	// Down to turn 1: a rewind there takes turn 2 back.
	m = press(t, m, "down")
	focus := m.picker.Focus
	m = press(t, m, keys.Shown(keys.Rewind.Diff))
	if m.state != stateDiffFull || m.fullDiff == nil {
		t.Fatalf("the key should open the run's diff full screen, got state %v", m.state)
	}
	if m.fullDiff.Path != "turn 2" || len(m.fullDiff.Files) != 2 {
		t.Fatalf("the diff should be turn 2's two files, got %q with %d", m.fullDiff.Path, len(m.fullDiff.Files))
	}
	m = press(t, m, "esc")
	if m.state != statePick || m.picker == nil || m.picker.Focus != focus {
		t.Fatalf("esc should come back to the picker as it was left, got state %v", m.state)
	}
	// The latest turn has nothing after it, and the card says so.
	m = press(t, m, "up")
	m = press(t, m, keys.Shown(keys.Rewind.Diff))
	if m.state != statePick || !strings.Contains(m.picker.Warning, "nothing after it") {
		t.Fatalf("the latest turn should say why there is nothing to show, got %q", m.picker.Warning)
	}
}

// TestGolden_RewindFold captures the fold in each of its states: closed and
// offering the reapply, open onto the turns it holds, spent once the session
// moved on, and the record a reapply leaves.
func TestGolden_RewindFold(t *testing.T) {
	captureGolden(t, "rewind-fold", "the turns a rewind took back", goldenWidths,
		func(width int) []golden.Panel {
			fold := func() (Model, int) {
				m := threeTurnModel(t)
				m.width, m.height = width, 40
				m.syncInputWidth()
				m = sendText(t, m, "/rewind 1")
				f := m.transcript[rewoundAt(t, m)].rewound
				// The files a real session would have counted, so the
				// clause reads as it does over a turn that wrote.
				f.files, f.filesKnown = 3, true
				return m, rewoundAt(t, m)
			}
			view := func(m Model, idx int, live bool) string {
				m.invalidateRenderCache()
				return strings.TrimRight(m.renderEntryKeys(m.transcript[idx], width, live), "\n")
			}
			m, idx := fold()
			waiting := view(m, idx, false)
			cursor := view(m, idx, true)
			m.transcript[idx].expanded = true
			open := view(m, idx, false)
			m.transcript[idx].expanded = false
			m.transcript[idx].rewound.spent = true
			spent := view(m, idx, false)
			r, ridx := fold()
			r = focusOn(t, r, ridx)
			r = press(t, r, keys.Shown(keys.Row.Retry))
			return []golden.Panel{
				{Label: "closed · the reapply reached from the draft", View: waiting},
				{Label: "closed · reading mode's cursor on it", View: cursor},
				{Label: "open · the turns it holds, out of the window", View: open},
				{Label: "spent · the session moved on, so nothing to put back", View: spent},
				{Label: "reapplied · the record left behind", View: view(r, ridx, false)},
			}
		})
}
