package chat

// The ! prefix: a draft in bang form rides the /run confirm, and !! keeps
// the output out of the conversation.

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestBangCommand_Forms(t *testing.T) {
	cases := []struct {
		text  string
		cmd   string
		local bool
		ok    bool
	}{
		{"!ls", "ls", false, true},
		{"!!ls", "ls", true, true},
		{"! ls -la", "ls -la", false, true},
		{"!", "", false, false},
		{"!!", "", false, false},
		{"echo !", "", false, false},
		{"plain text", "", false, false},
	}
	for _, c := range cases {
		cmd, local, ok := bangCommand(c.text)
		if cmd != c.cmd || local != c.local || ok != c.ok {
			t.Errorf("bangCommand(%q) = (%q, %v, %v), want (%q, %v, %v)",
				c.text, cmd, local, ok, c.cmd, c.local, c.ok)
		}
	}
}

func TestBang_TakesTheRunConfirmPath(t *testing.T) {
	m := runCapableModel("no blocks here")

	m = sendText(t, m, "!ls")
	if m.state != stateConfirmRun {
		t.Fatalf("expected the /run confirm, got state %d", m.state)
	}
	if m.pendingRun != "ls" || m.pendingRunLocal {
		t.Fatalf("expected pending 'ls' not local, got %q local=%v", m.pendingRun, m.pendingRunLocal)
	}
	view := m.View().Content
	if !strings.Contains(view, "ls") || !strings.Contains(view, "[y/N]") {
		t.Fatal("confirm card should show the command and y/N")
	}
}

func TestBangBang_RunsWithoutFeedingTheModel(t *testing.T) {
	m := runCapableModel("no blocks here")

	m = sendText(t, m, "!!ls")
	if m.state != stateConfirmRun || m.pendingRun != "ls" || !m.pendingRunLocal {
		t.Fatalf("expected a local pending run, got state=%d pending=%q local=%v",
			m.state, m.pendingRun, m.pendingRunLocal)
	}

	before := len(m.Messages())
	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	var done cmdDoneMsg
	found := false
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(cmdDoneMsg); ok {
			done, found = msg, true
		}
	}
	if !found {
		t.Fatal("expected cmdDoneMsg from the run")
	}
	if !done.local {
		t.Fatal("a !! run must carry the local flag")
	}

	updated, _ = m.Update(done)
	m = updated.(Model)
	if len(m.Messages()) != before {
		t.Fatalf("a local run's output must not join the conversation: %d messages, had %d",
			len(m.Messages()), before)
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryCommand || !last.localRun {
		t.Fatalf("expected a local command row, got %+v", last)
	}
	if row := m.renderEntry(last, 76); !strings.Contains(stripANSI(row), "local") {
		t.Fatalf("the row's outcome should say local:\n%s", stripANSI(row))
	}
}

func TestBang_MidSentenceIsALetter(t *testing.T) {
	m := runCapableModel("no blocks here")
	before := len(m.transcript)

	m = sendText(t, m, "echo !")
	if m.state != stateStreaming {
		t.Fatalf("'echo !' should send as a message, got state %d", m.state)
	}
	if len(m.transcript) <= before || m.transcript[len(m.transcript)-1].kind != entryUser {
		t.Fatal("'echo !' should land as a user row")
	}
}

func TestBang_RefusedWhileWorking(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.runFn = func(ctx context.Context, cmd string) (string, int) { return "", 0 }

	m = sendText(t, m, "!ls")
	if m.state != stateStreaming {
		t.Fatalf("a bang mid-turn must not disturb the stream, got state %d", m.state)
	}
	if len(m.steering) != 0 {
		t.Fatalf("a refused bang must not queue as steering, got %v", m.steering)
	}
	notice := m.transcript[len(m.transcript)-1]
	if notice.kind != entrySystem || !strings.Contains(notice.text, "needs the turn to be finished") {
		t.Fatalf("expected the idle-only refusal, got %+v", notice)
	}
}

func TestBang_GutterShowsBangForm(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue("!go test ./...")
	if got := stripANSI(m.promptGutter()); !strings.HasPrefix(got, "!") {
		t.Fatalf("expected the bang gutter, got %q", got)
	}
	m.input.SetValue("plain sentence")
	if got := stripANSI(m.promptGutter()); !strings.HasPrefix(got, "❯") {
		t.Fatalf("expected the idle gutter, got %q", got)
	}
}

func TestBang_NoCompletionMenu(t *testing.T) {
	m := runCapableModel("no blocks here")
	m.input.SetValue("!ls")
	m.syncCompletions()
	if m.completionActive() {
		t.Fatal("the completion menu must not open for a bang draft")
	}
}
