package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

var questionMark = tea.KeyPressMsg{Code: '?', Text: "?"}

// `?` on a card holding the keyboard puts the card's own register on the
// pane, and the same key takes the reader back to the card with the decision
// still waiting on it
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func TestKeyList_ACardAnswersQuestionMarkWithItsRegister(t *testing.T) {
	m := notedCardModel(t)
	if !m.decisionGated() && !m.decisionHeld {
		t.Fatal("the fixture's card does not hold the keyboard")
	}
	m = pressOn(t, m, questionMark)
	if m.state != stateKeyList || m.keyList == nil {
		t.Fatalf("? on the card did not open the key list (state %d)", m.state)
	}
	if got := m.keyList.screen.Surface; got != "the approval card and the /run confirm" {
		t.Errorf("the list is about %q, want the approval card's register", got)
	}
	view := ansi.Strip(strings.Join(m.keyListLines(100, 60), "\n"))
	for _, want := range []string{"[" + keys.Shown(keys.Decision.Allow) + "]", "[" + keys.Shown(keys.Decision.Deny) + "]", "glyphs", "⚙", "⊘"} {
		if !strings.Contains(view, want) {
			t.Errorf("the card's key list is missing %q:\n%s", want, view)
		}
	}
	m = pressOn(t, m, questionMark)
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("? again did not go back to the card with its decision waiting (state %d)", m.state)
	}
}

// While a field on the card has the keyboard, `?` is a character in it.
func TestKeyList_AFieldOnTheCardKeepsQuestionMarkAsText(t *testing.T) {
	m := pressOn(t, notedCardModel(t), tea.KeyPressMsg{Code: 'N', Text: "N"})
	if m.decisionNote == nil {
		t.Fatal("the shifted deny should open the note field")
	}
	m = pressOn(t, m, questionMark)
	if m.keyList != nil {
		t.Fatal("? opened the key list from inside the note field")
	}
	if !strings.Contains(m.decisionNote.field.Value(), "?") {
		t.Errorf("? did not land in the note: %q", m.decisionNote.field.Value())
	}
}

// Every register row a card names for `?` is a row of the register: a name
// that matched none would open a list with no keys on it.
func TestKeyList_EveryCardNamesARegisterRow(t *testing.T) {
	names := []string{
		"reading mode", "a yes-or-no question", "the question card", "the agent manager",
		"the approval card's queue list", "the approval card's grant list",
	}
	overlayOnce.Do(func() { overlayTable = buildOverlays() })
	for _, o := range append([]*mode{agentListMode(), childAskMode(nil)}, values(overlayTable)...) {
		if o.keyList == nil {
			continue
		}
		if name := o.keyList(Model{}); name != "" {
			names = append(names, name)
		}
	}
	for _, name := range names {
		if len(registerOffers(name)) == 0 {
			t.Errorf("%q names no row of the register", name)
		}
	}
}

func values(table map[state]*mode) []*mode {
	out := make([]*mode, 0, len(table))
	for _, o := range table {
		out = append(out, o)
	}
	return out
}
