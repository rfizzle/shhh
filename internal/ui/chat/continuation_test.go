package chat

// Trailing backslash: enter continues the line instead of sending it.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestContinuation_TrailingBackslashHoldsTheSend(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue(`foo\`)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := updated.(Model)

	if next.input.Value() != "foo\n" {
		t.Fatalf("expected the draft to become %q, got %q", "foo\n", next.input.Value())
	}
	if next.state != stateInput || len(next.transcript) != 0 {
		t.Fatal("a held send must not start a turn")
	}
}

func TestContinuation_DoubledBackslashSendsOneLiteral(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue(`foo\\`)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := updated.(Model)

	if next.state != stateStreaming {
		t.Fatalf("a doubled backslash should send, got state %d", next.state)
	}
	last := next.transcript[len(next.transcript)-1]
	if last.kind != entryUser || last.text != `foo\` {
		t.Fatalf("expected the message %q, got %+v", `foo\`, last)
	}
}

func TestContinuation_HoldsWhileSteeringToo(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.input.SetValue(`still working on it\`)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := updated.(Model)

	if len(next.steering) != 0 {
		t.Fatalf("a held send must not queue steering, got %v", next.steering)
	}
	if !strings.HasSuffix(next.input.Value(), "\n") {
		t.Fatalf("expected a newline in the draft, got %q", next.input.Value())
	}
}
