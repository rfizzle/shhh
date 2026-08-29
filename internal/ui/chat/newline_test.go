package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The rule is: any modifier on Enter means a line break rather than a
// send. Under v1 that had to be read out of the raw CSI the terminal sent,
// because v1 had no name for a modified Enter; v2 names it, so the rule is a
// rule about a key again.
func TestNewlineKey_RecognisesEveryModifiedEnter(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
		want bool
	}{
		{"shift+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, true},
		{"ctrl+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}, true},
		{"alt+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}, true},
		{"plain enter", tea.KeyPressMsg{Code: tea.KeyEnter}, false},
		{"another modified key", tea.KeyPressMsg{Code: 'a', Mod: tea.ModShift}, false},
	}
	for _, c := range cases {
		if got := newlineKey(c.msg); got != c.want {
			t.Errorf("%s: newlineKey = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNewlineKey_IgnoresOrdinaryMessages(t *testing.T) {
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.WindowSizeMsg{Width: 80, Height: 24},
		nil,
		"a string",
		[]byte("plain bytes"),
	} {
		if newlineKey(msg) {
			t.Errorf("newlineKey(%#v) = true, want false", msg)
		}
	}
}

// A modified Enter has to reach the draft as a line break rather than as a
// send — the whole point of the rule's first half.
func TestUpdate_ShiftEnterInsertsNewlineWithoutSending(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue("first line")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	next := updated.(Model)

	if !strings.Contains(next.input.Value(), "\n") {
		t.Fatalf("shift+enter did not insert a newline: %q", next.input.Value())
	}
	if next.state != stateInput {
		t.Fatalf("shift+enter started a turn: state = %v", next.state)
	}
	if len(next.transcript) != 0 {
		t.Fatalf("shift+enter sent the draft: %d transcript entries", len(next.transcript))
	}
}

// Plain Enter must still send, or the rewrite has stolen the send key.
func TestUpdate_PlainEnterStillSends(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue("send me")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := updated.(Model)

	if next.input.Value() != "" {
		t.Fatalf("enter left the draft in place: %q", next.input.Value())
	}
	if len(next.transcript) == 0 {
		t.Fatal("enter did not send the draft")
	}
}
