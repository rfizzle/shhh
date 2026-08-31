package caps

// The probe against the terminals it will meet (
// docs/architecture.md#only-one-place-speaks-to-the-terminal).
//
// Two halves are worth asserting. The questions: that they go out at all,
// that the three risky ones are held back exactly where the rule says, and
// that a terminal with nothing on the other end is not written to. And the
// answers: that each reply lands on the field it is about, that a yes is
// never taken back, and that the OSC 99 reply is read the way its own query
// wrote it.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// withProfile settles the profile the probe reads to decide whether there is
// a terminal to ask, and puts it back afterwards.
func withProfile(t *testing.T, p colorprofile.Profile) {
	t.Helper()
	prev := components.Profile()
	components.SetProfile(p)
	t.Cleanup(func() { components.SetProfile(prev) })
}

// raw runs the command and returns the bytes it would write.
func raw(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a query command, got nil")
	}
	msg, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("expected a tea.RawMsg, got %T", cmd())
	}
	s, ok := msg.Msg.(string)
	if !ok {
		t.Fatalf("expected the raw message to carry a string, got %T", msg.Msg)
	}
	return s
}

func TestQuery_AsksTheWholeSetOnAnOrdinaryTerminal(t *testing.T) {
	withProfile(t, colorprofile.ANSI256)
	var term Terminal
	seq := raw(t, term.Query([]string{"TERM=xterm-256color"}))

	for _, want := range []struct{ name, seq string }{
		{"primary device attributes", ansi.RequestPrimaryDeviceAttributes},
		{"focus mode report", ansi.RequestModeFocusEvent},
		{"desktop notifications", notifyQuery()},
		{"name and version", ansi.RequestNameVersion},
		{"window size in pixels", ansi.WindowOp(14)},
	} {
		if !strings.Contains(seq, want.seq) {
			t.Errorf("the probe did not ask for %s", want.name)
		}
	}
	if !strings.Contains(seq, "a=q") {
		t.Error("the probe did not ask the kitty graphics question")
	}
	if !term.Asked {
		t.Error("Asked should be true once the questions have gone out")
	}
	if term.Held != "" {
		t.Errorf("nothing should be held back on an ordinary terminal, got %q", term.Held)
	}
}

// A dumb terminal is the one answer that arrives without being asked, and it
// is read from the environment the probe was handed — over ssh that is the
// client's terminal rather than this machine's.
func TestQuery_RecordsADumbTerminal(t *testing.T) {
	withProfile(t, colorprofile.ANSI256)
	var term Terminal
	term.Query([]string{"TERM=dumb"})
	if !term.Dumb {
		t.Error("TERM=dumb was not recorded")
	}

	var ordinary Terminal
	ordinary.Query([]string{"TERM=xterm-256color"})
	if ordinary.Dumb {
		t.Error("an ordinary terminal was called dumb")
	}
}

func TestQuery_SaysNothingWithNoTerminalToSayItTo(t *testing.T) {
	withProfile(t, colorprofile.NoTTY)
	var term Terminal
	if cmd := term.Query([]string{"TERM=xterm-256color"}); cmd != nil {
		t.Fatal("a query sequence would be garbage in a pipe; the probe must not write one")
	}
	if term.Asked {
		t.Error("nothing was asked, so Asked must stay false")
	}
	if term.Held == "" {
		t.Error("the readout needs a reason to print, not a silent false")
	}
}

func TestQuery_HoldsTheRiskyThreeWhereTheRuleSays(t *testing.T) {
	withProfile(t, colorprofile.ANSI256)
	// The safe three always go out; only the ones a terminal can print
	// instead of answering are held.
	safe := []string{ansi.RequestPrimaryDeviceAttributes, ansi.RequestModeFocusEvent, notifyQuery()}

	for _, tc := range []struct {
		name string
		env  []string
		held bool
	}{
		{"an ordinary terminal", []string{"TERM=xterm-256color"}, false},
		{"Apple Terminal", []string{"TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal"}, true},
		{"over ssh", []string{"TERM=xterm-256color", "SSH_TTY=/dev/pts/3"}, true},
		{"over ssh into a terminal that answers", []string{"TERM=xterm-ghostty", "SSH_TTY=/dev/pts/3"}, false},
		{"Apple Terminal wins over a terminal that answers", []string{"TERM=xterm-kitty", "TERM_PROGRAM=Apple_Terminal"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var term Terminal
			seq := raw(t, term.Query(tc.env))
			for _, s := range safe {
				if !strings.Contains(seq, s) {
					t.Error("a safe query was held back; only the three risky ones are conditional")
				}
			}
			asked := strings.Contains(seq, ansi.RequestNameVersion)
			if asked == tc.held {
				t.Errorf("held = %v, want %v (probe wrote %q)", !asked, tc.held, seq)
			}
			if (term.Held != "") != tc.held {
				t.Errorf("Held = %q, want held = %v", term.Held, tc.held)
			}
		})
	}
}

func TestQuery_WrapsTheKittyQuestionForTmux(t *testing.T) {
	withProfile(t, colorprofile.ANSI256)
	var plain, inTmux Terminal
	bare := raw(t, plain.Query([]string{"TERM=xterm-256color"}))
	wrapped := raw(t, inTmux.Query([]string{"TERM=xterm-256color", "TMUX=/tmp/tmux-1000/default,42,0"}))

	if strings.Contains(bare, "\x1bPtmux;") {
		t.Error("a passthrough wrapper outside tmux would be printed as text")
	}
	if !strings.Contains(wrapped, "\x1bPtmux;") {
		t.Error("inside tmux the kitty question has to be told it is passing through, or tmux eats it")
	}
	// Only the graphics question is wrapped: the rest are sequences tmux
	// already forwards.
	if !strings.Contains(wrapped, ansi.RequestPrimaryDeviceAttributes) {
		t.Error("the safe queries should go out unwrapped inside tmux too")
	}
}

func TestUpdate_EachReplyLandsOnItsOwnField(t *testing.T) {
	var term Terminal
	term.Update(uv.PrimaryDeviceAttributesEvent{62, 4, 22})
	term.Update(uv.PixelSizeEvent{Width: 1512, Height: 950})
	term.Update(uv.KittyGraphicsEvent{})
	term.Update(tea.TerminalVersionMsg{Name: "ghostty 1.2.0"})
	term.Update(tea.ModeReportMsg{Mode: ansi.ModeFocusEvent, Value: ansi.ModeReset})
	term.Update(uv.UnknownOscEvent(notifyReply("title,body")))

	if !term.Sixel {
		t.Error("attribute 4 is sixel")
	}
	if term.PixelWidth != 1512 || term.PixelHeight != 950 {
		t.Errorf("pixels = %d×%d, want 1512×950", term.PixelWidth, term.PixelHeight)
	}
	if !term.Kitty {
		t.Error("answering the graphics query at all is the answer")
	}
	if term.Name != "ghostty 1.2.0" {
		t.Errorf("Name = %q", term.Name)
	}
	if !term.FocusEvents {
		t.Error("a mode that reports as reset is still a mode the terminal knows")
	}
	if !term.Notifications {
		t.Error("an OSC 99 reply listing title is notification support")
	}
	if !term.Graphics() {
		t.Error("either protocol is a picture")
	}
}

func TestUpdate_NeverTakesAYesBack(t *testing.T) {
	var term Terminal
	term.Update(uv.PrimaryDeviceAttributesEvent{62, 4})
	term.Update(tea.ModeReportMsg{Mode: ansi.ModeFocusEvent, Value: ansi.ModeSet})
	// A second reply from later in the session — a resize, a tmux
	// re-attach — that says nothing about sixel or focus.
	term.Update(uv.PrimaryDeviceAttributesEvent{62, 22})
	term.Update(tea.ModeReportMsg{Mode: ansi.ModeFocusEvent, Value: ansi.ModeNotRecognized})

	if !term.Sixel {
		t.Error("a later reply that omits attribute 4 is about something else, not a retraction")
	}
	if !term.FocusEvents {
		t.Error("focus support does not come and go within a session")
	}
}

func TestUpdate_IgnoresEverythingElse(t *testing.T) {
	var term Terminal
	before := term
	term.Update(tea.KeyPressMsg{})
	term.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	term.Update(tea.ModeReportMsg{Mode: ansi.ModeBracketedPaste, Value: ansi.ModeSet})
	if term != before {
		t.Errorf("the probe read a message that was not addressed to it: %+v", term)
	}
}

func TestNotifySupported_OnlyItsOwnAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
		want bool
	}{
		{"this query's answer", notifyReply("title,body"), true},
		{"title only", notifyReply("title"), true},
		{"a terminal that cannot do titles", notifyReply("body"), false},
		{"another program's OSC 99", "\x1b]99;i=someone-else:p=?;p=title\x07", false},
		{"an OSC 99 that is not a query answer", "\x1b]99;i=" + notifyQueryID + ";p=title\x07", false},
		{"a different OSC entirely", "\x1b]777;notify;hello\x07", false},
		{"not an OSC at all", "\x1b[?1004;1$y", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := notifySupported(tc.seq); got != tc.want {
				t.Errorf("notifySupported(%q) = %v, want %v", tc.seq, got, tc.want)
			}
		})
	}
}

// TestOscBody_ReadsEveryFramingUvCanHandOver — uv hands the sequence over
// whole, introducer and terminator included, and which of each it used is the
// terminal's choice rather than ours.
func TestOscBody_ReadsEveryFramingUvCanHandOver(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
		ok   bool
	}{
		{"ESC ] with BEL", "\x1b]99;a;b\x07", true},
		{"ESC ] with ST", "\x1b]99;a;b\x1b\\", true},
		{"C1 OSC with C1 ST", "\x9d99;a;b\x9c", true},
		{"no command number", "\x1b];a\x07", false},
		{"a command that is not a number", "\x1b]9x;a\x07", false},
		{"no separator", "\x1b]99\x07", false},
		{"not an OSC", "hello", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, body, ok := oscBody(tc.seq)
			if ok != tc.ok {
				t.Fatalf("oscBody(%q) ok = %v, want %v", tc.seq, ok, tc.ok)
			}
			if ok && (cmd != 99 || body != "a;b") {
				t.Errorf("oscBody(%q) = %d, %q", tc.seq, cmd, body)
			}
		})
	}
}

func TestCellSize_NeedsBothHalves(t *testing.T) {
	full := Terminal{PixelWidth: 1512, PixelHeight: 950}
	if w, h := full.CellSize(168, 50); w != 9 || h != 19 {
		t.Errorf("CellSize = %d×%d, want 9×19", w, h)
	}
	// A terminal that was never asked, and a window nobody has measured
	// yet, both give nothing rather than a division by zero.
	if w, h := (Terminal{}).CellSize(168, 50); w != 0 || h != 0 {
		t.Errorf("an unasked terminal has no cell size, got %d×%d", w, h)
	}
	if w, h := full.CellSize(0, 0); w != 0 || h != 0 {
		t.Errorf("a window with no columns has no cell size, got %d×%d", w, h)
	}
}

// notifyReply is a terminal answering this package's own OSC 99 query with
// the list of parts it supports.
func notifyReply(parts string) string {
	return ansi.DesktopNotification("p="+parts, "i="+notifyQueryID, "p=?")
}
