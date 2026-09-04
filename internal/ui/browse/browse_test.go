package browse

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func press(t *testing.T, m Model, s string) Model {
	t.Helper()
	var msg tea.KeyPressMsg
	switch s {
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	default:
		msg = tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

type fakeStore struct {
	deleted []string
	renamed [][2]string
	err     error
}

func (f *fakeStore) model() Model {
	m := New([]Item{
		{ID: "alpha", Title: "alpha", Preview: "1 turns", Deleting: "and its 2 branches"},
		{ID: "beta", Title: "beta", Preview: "3 turns"},
	}, []ActionDef{{Label: "Open", Shortcut: "o"}}).WithOps(Ops{
		Delete: func(id string) error {
			if f.err != nil {
				return f.err
			}
			f.deleted = append(f.deleted, id)
			return nil
		},
		Rename: func(id, name string) error {
			if f.err != nil {
				return f.err
			}
			f.renamed = append(f.renamed, [2]string{id, name})
			return nil
		},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(Model)
}

func TestBrowse_DeleteAsksAndDefaultsToNo(t *testing.T) {
	store := &fakeStore{}
	m := store.model()

	m = press(t, m, "x")
	if m.confirm == nil || !strings.Contains(m.confirm.Prompt, `"alpha" and its 2 branches`) {
		t.Fatalf("x should arm a confirm naming the chat and its branches, got %+v", m.confirm)
	}
	if screen := m.screen(); !strings.Contains(screen, "[y/N]") {
		t.Fatalf("the confirm should be on screen, got:\n%s", screen)
	}
	m = press(t, m, "enter")
	if m.confirm != nil || len(store.deleted) != 0 {
		t.Fatalf("enter is No, deleted=%v", store.deleted)
	}
	m = press(t, m, "x")
	m = press(t, m, "y")
	if len(store.deleted) != 1 || store.deleted[0] != "alpha" {
		t.Fatalf("y should delete alpha, got %v", store.deleted)
	}
	if len(m.items) != 1 || m.items[0].ID != "beta" {
		t.Fatalf("the list should drop the row, got %+v", m.items)
	}
	if !strings.Contains(m.screen(), `Deleted "alpha"`) {
		t.Fatal("the list should say what it did")
	}
}

func TestBrowse_RenameCommitsOnEnterAndKeepsOnEsc(t *testing.T) {
	store := &fakeStore{}
	m := store.model()

	m = press(t, m, "r")
	if !m.renaming || m.rename.Value() != "alpha" {
		t.Fatalf("r should open the rename row prefilled, got renaming=%v value=%q", m.renaming, m.rename.Value())
	}
	m = press(t, m, "2")
	m = press(t, m, "esc")
	if m.renaming || len(store.renamed) != 0 || m.items[0].Title != "alpha" {
		t.Fatalf("esc keeps the name, got %+v", store.renamed)
	}

	m = press(t, m, "r")
	m = press(t, m, "2")
	m = press(t, m, "enter")
	if len(store.renamed) != 1 || store.renamed[0] != [2]string{"alpha", "alpha2"} {
		t.Fatalf("enter should rename alpha to alpha2, got %v", store.renamed)
	}
	if m.items[0].ID != "alpha2" || m.items[0].Title != "alpha2" {
		t.Fatalf("the row should carry the new name, got %+v", m.items[0])
	}
}

func TestBrowse_RenameOfAShortNameLeavesTheLabelsAlone(t *testing.T) {
	m := New([]Item{{ID: "a", Title: "a", Detail: "Name:     a\nTurns:    1"}}, nil).WithOps(Ops{
		Delete: func(string) error { return nil },
		Rename: func(string, string) error { return nil },
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m = press(t, m, "r")
	m = press(t, m, "b")
	m = press(t, m, "enter")
	if m.items[0].Detail != "Name:     ab\nTurns:    1" {
		t.Fatalf("only the name under its label changes, got %q", m.items[0].Detail)
	}
}

func TestBrowse_RefusedOpsAreReportedNotHidden(t *testing.T) {
	store := &fakeStore{err: errors.New("already exists")}
	m := store.model()
	m = press(t, m, "r")
	m = press(t, m, "2")
	m = press(t, m, "enter")
	if !strings.Contains(m.screen(), "Could not rename: already exists") {
		t.Fatalf("a refused rename should say why, got:\n%s", m.screen())
	}
	if m.items[0].Title != "alpha" {
		t.Fatal("a refused rename changes nothing")
	}
}

func TestBrowse_HintsNameOnlyTheKeysOffered(t *testing.T) {
	plain := New([]Item{{ID: "a", Title: "a"}}, nil)
	updated, _ := plain.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain = updated.(Model)
	if screen := plain.screen(); strings.Contains(screen, "x delete") || strings.Contains(screen, "r rename") {
		t.Fatalf("a list without ops offers no housekeeping keys, got:\n%s", screen)
	}
	if plain = press(t, plain, "x"); plain.confirm != nil {
		t.Fatal("x on a list without ops does nothing")
	}
	withOps := (&fakeStore{}).model()
	if screen := withOps.screen(); !strings.Contains(screen, "x delete") || !strings.Contains(screen, "r rename") {
		t.Fatalf("a list with ops offers them, got:\n%s", screen)
	}
}

// A row the host refused answers where it stands. Ending the list to report
// the refusal would take away the row the reader is on, along with every
// other one, which is the opposite of what a refusal that leaves the item
// alone is for.
func TestBrowse_ARefusedItemSaysSoAndKeepsTheList(t *testing.T) {
	store := &fakeStore{}
	m := store.model()
	m.items[0].Refused = `"alpha" is open in another session — its conversation is still being written there.`

	m = press(t, m, "enter")
	m = press(t, m, "enter")
	if m.Result != nil {
		t.Fatalf("a refused row must not be taken, got %+v", m.Result)
	}
	if screen := m.screen(); !strings.Contains(screen, "still being written there") {
		t.Fatalf("the refusal should be on the pane it was met on, got:\n%s", screen)
	}
	if m = press(t, m, "o"); m.Result != nil {
		t.Fatalf("the action's own key is refused too, got %+v", m.Result)
	}

	// The row is still a row: the housekeeping keys reach it from the list.
	m = press(t, m, "esc")
	m = press(t, m, "x")
	m = press(t, m, "y")
	if len(store.deleted) != 1 || store.deleted[0] != "alpha" {
		t.Fatalf("a refused row can still be deleted, got %v", store.deleted)
	}
}

// The refusal is per item, not a mode: the row beside it opens.
func TestBrowse_AnItemWithNoRefusalIsTaken(t *testing.T) {
	m := (&fakeStore{}).model()
	m.items[0].Refused = "not this one"

	m = press(t, m, "j")
	m = press(t, m, "enter")
	m = press(t, m, "enter")
	if m.Result == nil || m.Result.Item.ID != "beta" {
		t.Fatalf("the unrefused row should open, got %+v", m.Result)
	}
}

// The detail pane is the card every other detail in the product is drawn in,
// and its body is measured against the frame rather than past it.
func TestBrowse_TheDetailIsDrawnInTheSharedFrame(t *testing.T) {
	long := strings.Repeat("x", 200)
	m := New([]Item{{ID: "alpha", Title: "alpha", Detail: "Name:     alpha\n" + long}}, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = press(t, updated.(Model), "enter")

	lines := strings.Split(m.screen(), "\n")
	if !strings.HasPrefix(lines[0], "┌─ alpha ") {
		t.Fatalf("the title is not in the frame's top border: %q", lines[0])
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 60 {
			t.Fatalf("a row ran past the terminal: %q", line)
		}
	}
}

// The browser scrolls on the window every long list in the product uses, and
// counts what it is hiding: a list that dropped rows off either end without
// saying so would be the one list here that hides rather than folds.
func TestBrowse_WindowsAndCountsWhatItHides(t *testing.T) {
	items := make([]Item, 40)
	for i := range items {
		items[i] = Item{ID: string(rune('a' + i%26)), Title: strings.Repeat("chat", 1) + " " + string(rune('a'+i%26)) + string(rune('0'+i/10))}
	}
	m := New(items, []ActionDef{{Label: "Open", Shortcut: "o"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	m = updated.(Model)

	view := m.screen()
	if !strings.Contains(view, "more") {
		t.Fatalf("forty items in twelve rows should say how many are below:\n%s", view)
	}

	// Walking the pointer down moves the window with it, and once it has
	// moved the marker above says how much went.
	for range 20 {
		m = press(t, m, "j")
	}
	view = m.screen()
	if !strings.Contains(view, "↑") {
		t.Fatalf("a window that has scrolled says what is above it:\n%s", view)
	}
	if !strings.Contains(view, items[m.list.Focus].Title) {
		t.Fatalf("the pointer must stay inside the window:\n%s", view)
	}
}
