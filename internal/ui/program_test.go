package ui

// The one-shot's route, run through the real program: the answer streams in,
// the result is drawn against the terminal, and the key that opens the other
// commands reaches the list. The render is the goldens' question; this is
// whether the key and the stream get there
// (internal/ui/chat/program_test.go says the same of the session).

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// watchedGenerate is the one-shot with the frame it last drew noted, so a
// test waits on the screen rather than on the renderer's byte stream.
type watchedGenerate struct {
	GenerateModel
	frame *atomic.Pointer[string]
}

func (w watchedGenerate) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := w.GenerateModel.Update(msg)
	if g, ok := next.(GenerateModel); ok {
		w.GenerateModel = g
		return w, cmd
	}
	return next, cmd
}

func (w watchedGenerate) View() tea.View {
	v := w.GenerateModel.View()
	s := ansi.Strip(v.Content)
	w.frame.Store(&s)
	return v
}

// runOneShot starts the one-shot over one scripted answer at the pane the
// artboard is drawn at, and waits until it has drawn want.
func runOneShot(t *testing.T, answer, want string) (*teatest.TestModel, *atomic.Pointer[string]) {
	t.Helper()
	frame := new(atomic.Pointer[string])
	m := NewGenerateModel(makeEvents(answer), noopCancel, nil, nil, nil, "")
	tm := teatest.NewTestModel(t, watchedGenerate{GenerateModel: m, frame: frame}, teatest.WithInitialTermSize(100, 40))
	t.Cleanup(func() { _ = tm.Quit() })
	waitForFrame(t, frame, want)
	return tm, frame
}

func waitForFrame(t *testing.T, frame *atomic.Pointer[string], want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f := frame.Load(); f != nil && strings.Contains(*f, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	last := ""
	if f := frame.Load(); f != nil {
		last = *f
	}
	t.Fatalf("the one-shot never drew %q; the last frame was:\n%s", want, last)
}

// The answer arrives as a command with its explanation and the count of the
// others, and [a] opens the list of those others.
func TestProgram_TheOneShotOpensItsAlternatives(t *testing.T) {
	tm, frame := runOneShot(t,
		"lsof -nP -iTCP -sTCP:LISTEN | awk '$9 ~ /:[89][0-9]{3}$/'\n"+
			"--- explanation\nlsof lists listening TCP sockets without resolving names.\n"+
			"--- alternatives\nss -lntp 'sport > :8000'\n# linux only\nnetstat -anv -p tcp\n# everywhere",
		"2 others")

	tm.Send(tea.KeyPressMsg{Code: 'a', Text: "a"})
	waitForFrame(t, frame, "netstat -anv -p tcp")
	oneShotEndsOn(t, tm, "Alternatives", "netstat -anv -p tcp")
}

// A command the safe default moves for says why before anything runs.
func TestProgram_TheOneShotNamesADestructiveCommand(t *testing.T) {
	tm, _ := runOneShot(t,
		"find ~/src -name node_modules -type d -prune -exec rm -rf {} +\n"+
			"--- explanation\n-prune stops find from descending into a directory it just deleted.",
		"recursive forced deletion")
	oneShotEndsOn(t, tm, "recursive forced deletion")
}

// oneShotEndsOn quits the program and fails for each phrase the frame it
// ended on does not carry.
func oneShotEndsOn(t *testing.T, tm *teatest.TestModel, wants ...string) {
	t.Helper()
	if err := tm.Quit(); err != nil {
		t.Fatalf("quitting the program: %v", err)
	}
	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(watchedGenerate)
	if !ok {
		t.Fatal("the program did not end on the one-shot")
	}
	frame := ansi.Strip(final.GenerateModel.View().Content)
	for _, want := range wants {
		if !strings.Contains(frame, want) {
			t.Fatalf("the frame the one-shot ended on does not carry %q:\n%s", want, frame)
		}
	}
}
