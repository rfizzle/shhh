package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
)

var errTest = errors.New("test error")

func drainStream(m GenerateModel, events int) GenerateModel {
	for i := 0; i < events; i++ {
		cmd := m.stream.waitForEvent()
		model, _ := m.Update(cmd())
		m = model.(GenerateModel)
	}
	return m
}

func TestGenerate_StartsInStreamingPhase(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel)

	if m.Phase() != phaseStreaming {
		t.Errorf("expected phaseStreaming, got %v", m.Phase())
	}
}

func TestGenerate_TransitionsToActionOnDone(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel)

	// token + done
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction, got %v", m.Phase())
	}
}

func TestGenerate_ActionBarAppearsInView(t *testing.T) {
	events := makeEvents("echo hello")
	m := NewGenerateModel(events, noopCancel)
	m = drainStream(m, 2)

	view := m.View()
	if !strings.Contains(view, "Run") {
		t.Error("expected action bar visible after stream completes")
	}
	if !strings.Contains(view, "echo hello") {
		t.Error("expected command still visible after stream completes")
	}
}

func TestGenerate_SelectRunReturnsResult(t *testing.T) {
	events := makeEvents("rm -rf /tmp/test")
	m := NewGenerateModel(events, noopCancel)
	m = drainStream(m, 2)

	// Press 'r'
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = model.(GenerateModel)

	if m.Phase() != phaseDone {
		t.Errorf("expected phaseDone, got %v", m.Phase())
	}
	r := m.Result()
	if r.Action != ActionRun {
		t.Errorf("expected ActionRun, got %v", r.Action)
	}
	if r.Command != "rm -rf /tmp/test" {
		t.Errorf("expected command 'rm -rf /tmp/test', got %q", r.Command)
	}

	// Should emit tea.Quit
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestGenerate_SelectCopyReturnsResult(t *testing.T) {
	events := makeEvents("docker ps")
	m := NewGenerateModel(events, noopCancel)
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = model.(GenerateModel)

	r := m.Result()
	if r.Action != ActionCopy {
		t.Errorf("expected ActionCopy, got %v", r.Action)
	}
	if r.Command != "docker ps" {
		t.Errorf("expected 'docker ps', got %q", r.Command)
	}
}

func TestGenerate_SelectCancelReturnsResult(t *testing.T) {
	events := makeEvents("whoami")
	m := NewGenerateModel(events, noopCancel)
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = model.(GenerateModel)

	r := m.Result()
	if r.Action != ActionCancel {
		t.Errorf("expected ActionCancel, got %v", r.Action)
	}
}

func TestGenerate_CancelDuringStreamQuitsImmediately(t *testing.T) {
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{Token: "partial"}
	cancel, called := testCancel()
	m := NewGenerateModel(ch, cancel)

	// Receive token
	cmd := m.stream.waitForEvent()
	model, _ := m.Update(cmd())
	m = model.(GenerateModel)

	// Press Esc during stream
	model, quitCmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = model.(GenerateModel)

	if m.Phase() != phaseDone {
		t.Errorf("expected phaseDone after cancel, got %v", m.Phase())
	}
	if !m.Result().Cancelled {
		t.Error("expected result.Cancelled to be true")
	}
	if !*called {
		t.Error("expected cancel to be called")
	}

	msg := quitCmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
	close(ch)
}

func TestGenerate_ErrorDuringStreamQuitsImmediately(t *testing.T) {
	events := makeErrorEvents(errTest)
	m := NewGenerateModel(events, noopCancel)

	cmd := m.stream.waitForEvent()
	model, quitCmd := m.Update(cmd())
	m = model.(GenerateModel)

	if m.Phase() != phaseDone {
		t.Errorf("expected phaseDone after error, got %v", m.Phase())
	}
	if m.Result().Err == nil {
		t.Error("expected error in result")
	}

	msg := quitCmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestGenerate_StripsMarkdownBeforeActionBar(t *testing.T) {
	events := makeEvents("```bash\nfind . -name '*.log'\n```")
	m := NewGenerateModel(events, noopCancel)
	m = drainStream(m, 2)

	// Command should be stripped by the time action bar appears
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = model.(GenerateModel)

	if m.Result().Command != "find . -name '*.log'" {
		t.Errorf("expected stripped command, got %q", m.Result().Command)
	}
}

func TestGenerate_NavigateThenEnter(t *testing.T) {
	events := makeEvents("pwd")
	m := NewGenerateModel(events, noopCancel)
	m = drainStream(m, 2)

	// Navigate right to Copy, then Enter
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = model.(GenerateModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	if m.Result().Action != ActionCopy {
		t.Errorf("expected ActionCopy after nav+enter, got %v", m.Result().Action)
	}
}
