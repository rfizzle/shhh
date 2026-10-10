package chat

// The key list is looked up and put away
// (docs/interface/surfaces.md#the-key-list): every door opens the same card
// over the session, the draft under it is untouched, and nothing it showed
// is left in the transcript or the conversation.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

var (
	keyListChord = tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl}
	keyListEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

// keyPopupView is the card as the panel draws it, without colour.
func keyPopupView(m Model) string {
	return ansi.Strip(strings.Join(m.keyPopupLines(), "\n"))
}

func TestKeyPopup_OpensOverAHalfWrittenDraftAndLeavesItAsItWas(t *testing.T) {
	m := frameModel(t, 110, 40)
	m.input.SetValue("why does the parser")
	m.input.SetCursorColumn(4)
	history := m.renderHistory()
	messages := len(m.Messages())
	entries := len(*m.entries())

	// esc backs out a level a press: the typed filter first, then the list.
	for _, close := range [][]tea.KeyPressMsg{{keyListEsc, keyListEsc}, {keyListChord}} {
		opened, _ := pressKey(t, m, keyListChord)
		if opened.state != stateKeyPopup || opened.screens.keyPopup() == nil {
			t.Fatalf("the chord did not open the key list, state %d", opened.state)
		}
		if !strings.Contains(keyPopupView(opened), "DRAFT") {
			t.Fatalf("the list does not start at the draft's group:\n%s", keyPopupView(opened))
		}
		opened = typeInto(t, opened, "copy")
		closed := pressKeys(t, opened, close...)
		if closed.state != stateInput || closed.screens.keyPopup() != nil {
			t.Fatalf("%v did not close the key list, state %d", close, closed.state)
		}
		if got := closed.input.Value(); got != "why does the parser" {
			t.Fatalf("closing with %v changed the draft to %q", close, got)
		}
		if got := closed.input.Column(); got != 4 {
			t.Fatalf("closing with %v moved the cursor to %d", close, got)
		}
		if closed.renderHistory() != history || len(*closed.entries()) != entries || len(closed.Messages()) != messages {
			t.Fatalf("the key list left something in the transcript or the conversation")
		}
	}
}

// Every door is the same card: the chord, the start screen's offer (which is
// the chord), and a bare /help. /help with words goes on writing the whole
// help as a row.
func TestKeyPopup_BareHelpOpensItAndHelpWithWordsWritesTheSheet(t *testing.T) {
	m := sendText(t, readyModel(t), "/help")
	if m.state != stateKeyPopup {
		t.Fatalf("bare /help did not open the key list, state %d", m.state)
	}
	if transcriptContains(m, "approval prompts") {
		t.Fatal("bare /help still wrote the sheet into the transcript")
	}

	m = sendText(t, readyModel(t), "/help keys")
	if m.state == stateKeyPopup {
		t.Fatal("/help with words opened the key list instead of writing the sheet")
	}
	if !transcriptContains(m, "approval prompts") {
		t.Fatal("/help with words did not write the sheet")
	}

	start, _ := pressKey(t, startModel(t, startFixture()), keyListChord)
	if start.state != stateKeyPopup {
		t.Fatalf("the start screen's offer did not open the key list, state %d", start.state)
	}
}

// Attached to a child, the keyboard is pointed at the child and the chord
// keeps its textarea meaning, as it always has.
func TestKeyPopup_TheChordDoesNothingWhileAttached(t *testing.T) {
	m := frameModel(t, 110, 40)
	m.attachedTo = "writer-1"
	m, _ = pressKey(t, m, keyListChord)
	if m.state == stateKeyPopup {
		t.Fatal("the chord opened the key list over an attached child")
	}
}

// A running turn is borrowed from, never parked: it is still the state the
// screen goes back to, and streaming under the card.
func TestKeyPopup_ARunningTurnKeepsRunningUnderIt(t *testing.T) {
	m := frameModel(t, 110, 40)
	m.setTurnState(stateStreaming)
	m, _ = pressKey(t, m, keyListChord)
	if m.state != stateKeyPopup || m.turnState() != stateStreaming {
		t.Fatalf("the key list over a running turn: state %d, turn %d", m.state, m.turnState())
	}
	m, _ = pressKey(t, m, keyListEsc)
	if m.state != stateStreaming {
		t.Fatalf("closing did not hand the screen back to the running turn, state %d", m.state)
	}
}

func TestKeyPopup_TypingFiltersByKeyOrWordsAndTheGroupsKeepTheirOrder(t *testing.T) {
	m, _ := pressKey(t, frameModel(t, 110, 40), keyListChord)
	m = typeInto(t, m, "ctrl+]")
	view := keyPopupView(m)
	if !strings.Contains(view, "[ctrl+]]") || !strings.Contains(view, "DRAFT") {
		t.Fatalf("filtering by the key did not find it:\n%s", view)
	}
	if strings.Contains(view, "[ctrl+n]") {
		t.Fatalf("the filter kept a key it does not match:\n%s", view)
	}

	m, _ = pressKey(t, m, keyListEsc)
	if m.state != stateKeyPopup {
		t.Fatal("esc over a typed filter closed the list instead of clearing it")
	}
	m = typeInto(t, m, "delete")
	view = keyPopupView(m)
	if !strings.Contains(view, "QUEUE") || !strings.Contains(view, "delete") {
		t.Fatalf("filtering by the words did not find the queue's key:\n%s", view)
	}
	if !strings.Contains(view, "of") || !strings.Contains(view, "keys") {
		t.Fatalf("the title rail does not count the matches:\n%s", view)
	}

	m = typeInto(t, m, "zzz")
	if view := keyPopupView(m); !strings.Contains(view, "no matches") {
		t.Fatalf("a query that matches nothing does not say so:\n%s", view)
	}
	// A query line emptied back out keeps the list a query line.
	for range len("deletezzz") {
		m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	if p := m.screens.keyPopup(); p == nil || !p.card.Filtering {
		t.Fatal("emptying the query closed the list's query line")
	}
	m, _ = pressKey(t, m, keyListEsc)
	if m.state != stateInput || m.screens.keyPopup() != nil {
		t.Fatalf("esc on an empty query should leave the list, state %d", m.state)
	}
}

func TestKeyPopup_PagesAndJumpsToTheEnds(t *testing.T) {
	m, _ := pressKey(t, frameModel(t, 110, 40), keyListChord)
	p := m.screens.keyPopup()
	first := p.card.Focus
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if p.card.Focus <= first {
		t.Fatal("↓ did not move the pointer")
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if paged := p.card.Focus; paged-first < p.page() {
		t.Fatalf("pgdn moved %d rows, want at least a page of %d", paged-first, p.page())
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if p.card.Focus != p.last() {
		t.Fatalf("end left the pointer on %d, want the last key %d", p.card.Focus, p.last())
	}
	last := p.all[len(p.all)-1]
	if view := keyPopupView(m); !strings.Contains(view, strings.ToUpper(last.group)) || !strings.Contains(view, last.words) {
		t.Fatalf("end did not bring the last group into view:\n%s", view)
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	if p.card.Focus != first {
		t.Fatalf("home left the pointer on %d, want %d", p.card.Focus, first)
	}
	if m.state != stateKeyPopup {
		t.Fatal("moving closed the list")
	}
}

// The list is read from the register when it opens, so a key a
// keybindings.toml moved is listed at the keystrokes it answers.
func TestKeyPopup_ListsAMovedKeyAtItsBoundKeystrokes(t *testing.T) {
	was := keys.Draft
	t.Cleanup(func() { keys.Draft = was; _ = keys.Load() })
	path := filepath.Join(t.TempDir(), "keybindings.toml")
	if err := os.WriteFile(path, []byte("[draft]\neditor = [\"f9\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := keys.Load(path); err != nil {
		t.Fatalf("the keymap was refused: %v", err)
	}
	m, _ := pressKey(t, frameModel(t, 110, 40), keyListChord)
	m = typeInto(t, m, "EDITOR")
	view := keyPopupView(m)
	if !strings.Contains(view, "[f9]") || strings.Contains(view, "[ctrl+g]") {
		t.Fatalf("the moved key is not listed as it is bound:\n%s", view)
	}
}
