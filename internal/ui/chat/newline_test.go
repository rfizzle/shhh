package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// csiMsg is bubbletea's unrecognised-sequence message: a named byte slice.
// The real type is unexported, so the test stands in a value of the same
// shape — which is exactly what newlineKey recognises.
type csiMsg []byte

func TestNewlineKey_RecognisesBothEncodings(t *testing.T) {
	cases := []struct {
		name string
		seq  string
		want bool
	}{
		{"csi-u shift+enter", "\x1b[13;2u", true},
		{"csi-u ctrl+enter", "\x1b[13;5u", true},
		{"csi-u with event sub-parameters", "\x1b[13;2:1u", true},
		{"csi-u plain enter", "\x1b[13;1u", false},
		{"csi-u another key", "\x1b[97;2u", false},
		{"modifyOtherKeys shift+enter", "\x1b[27;2;13~", true},
		{"modifyOtherKeys plain enter", "\x1b[27;1;13~", false},
		{"modifyOtherKeys another key", "\x1b[27;2;97~", false},
		{"an unrelated sequence", "\x1b[1;2A", false},
		{"truncated", "\x1b[", false},
	}
	for _, c := range cases {
		if got := newlineKey(csiMsg(c.seq)); got != c.want {
			t.Errorf("%s: newlineKey(%q) = %v, want %v", c.name, c.seq, got, c.want)
		}
	}
}

func TestNewlineKey_IgnoresOrdinaryMessages(t *testing.T) {
	for _, msg := range []tea.Msg{
		tea.KeyMsg{Type: tea.KeyEnter},
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
// send — the whole point of S-134's first half.
func TestUpdate_ShiftEnterInsertsNewlineWithoutSending(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue("first line")

	updated, _ := m.Update(csiMsg("\x1b[13;2u"))
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

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)

	if next.input.Value() != "" {
		t.Fatalf("enter left the draft in place: %q", next.input.Value())
	}
	if len(next.transcript) == 0 {
		t.Fatal("enter did not send the draft")
	}
}

func TestRequestEnhancedKeys_AsksAndRestores(t *testing.T) {
	var w strings.Builder
	restore := RequestEnhancedKeys(&w)
	if got := w.String(); got != modifyOtherKeysOn {
		t.Fatalf("asked %q, want %q", got, modifyOtherKeysOn)
	}
	restore()
	if got := w.String(); got != modifyOtherKeysOn+modifyOtherKeysOff {
		t.Fatalf("after restore %q, want the request then the reset", got)
	}
	// A nil writer is the headless case; it must not panic.
	RequestEnhancedKeys(nil)()
}
