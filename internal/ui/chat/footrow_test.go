package chat

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
)

// The foot row is a ladder, floor first, and offers drop from the right as
// the width runs out (docs/interface/surfaces.md#the-input-frame). One case
// per width and per idle and working, read off the Frame board's windows 19
// to 26: the row the board draws is the row the frame draws, except that the
// board's 110 and 130 boxes are the full column count and the frame's are the
// terminal less its padding, so each rung that depends on the last four
// columns arrives four columns later (quit idle, commands working).
func TestFrame_TheFootRowLadder(t *testing.T) {
	idle := "[enter] send · [shift+tab] mode · [ctrl+/] commands · [ctrl+]] keys · [ctrl+n] queue · [ctrl+c] ×2 quit"
	working := "[ctrl+p] hold · [ctrl+c] stop the run · [enter] add to this turn · [shift+tab] mode · [ctrl+/] commands · [ctrl+]] keys · [ctrl+n] queue"
	cases := []struct {
		width   int
		working bool
		want    string
	}{
		{60, false, "[enter] send · [shift+tab] mode · [ctrl+]] keys"},
		{80, false, "[enter] send · [shift+tab] mode · [ctrl+/] commands · [ctrl+]] keys"},
		{110, false, strings.TrimSuffix(idle, " · [ctrl+c] ×2 quit")},
		{130, false, idle},
		{60, true, "[ctrl+p] hold · [ctrl+c] stop the run"},
		{80, true, "[ctrl+p] hold · [ctrl+c] stop the run · [enter] add to this turn"},
		{110, true, "[ctrl+p] hold · [ctrl+c] stop the run · [enter] add to this turn · [shift+tab] mode"},
		{130, true, strings.TrimSuffix(working, " · [ctrl+n] queue")},
	}
	for _, c := range cases {
		name := fmt.Sprintf("%d idle", c.width)
		m := frameModel(t, c.width, 40)
		if c.working {
			name = fmt.Sprintf("%d working", c.width)
			m.state = stateStreaming
		}
		if got := footRow(stripANSI(m.renderPromptFrame())); got != c.want {
			t.Errorf("%s: foot row\n got  %q\n want %q", name, got, c.want)
		}
	}
}

// A narrow terminal is not a keyless one: the floor is what each state needs
// at once, and it is on the row at sixty and eighty columns.
func TestFrame_NarrowRowsAreNeverHintless(t *testing.T) {
	for _, width := range []int{60, 80} {
		idle := footRow(stripANSI(frameModel(t, width, 40).renderPromptFrame()))
		for _, want := range []string{"[enter] send", "[ctrl+]] keys"} {
			if !strings.Contains(idle, want) {
				t.Errorf("at %d columns the idle row lost %q: %q", width, want, idle)
			}
		}
		m := frameModel(t, width, 40)
		m.state = stateStreaming
		working := footRow(stripANSI(m.renderPromptFrame()))
		for _, want := range []string{"[ctrl+p] hold", "[ctrl+c] stop the run"} {
			if !strings.Contains(working, want) {
				t.Errorf("at %d columns the working row lost %q: %q", width, want, working)
			}
		}
	}
}

// The editor and the attach chord are not rungs: a rarity does not earn the
// row, and the attach chord is a contextual helper above the prompt.
func TestFrame_TheEditorAndTheAttachChordLeaveTheRow(t *testing.T) {
	for _, width := range []int{60, 80, 110, 130, 200} {
		row := footRow(stripANSI(frameModel(t, width, 40).renderPromptFrame()))
		for _, gone := range []string{"ctrl+g", "ctrl+v", "editor", "attach", "ctrl+d"} {
			if strings.Contains(row, gone) {
				t.Errorf("at %d columns the row still carries %q: %q", width, gone, row)
			}
		}
	}
}

// The attach label sits on the top rail at the right, muted, only while the
// clipboard holds something to attach: the hermetic tier seeds the answer the
// read would have given (the clipboard tier proves the read), and the rail
// carries nothing without it.
func TestFrame_AttachIsOfferedOnlyWithSomethingToAttach(t *testing.T) {
	m := frameModel(t, 110, 40)
	if top := frameTopRail(stripANSI(m.renderPromptFrame())); strings.Contains(top, "attach") {
		t.Fatalf("an empty offer draws no label: %q", top)
	}
	seeded, _ := m.Update(clipboardOfferMsg{name: "clipboard.png"})
	m = seeded.(Model)
	view := stripANSI(m.renderPromptFrame())
	top := frameTopRail(view)
	if !strings.Contains(top, "[ctrl+v] attach clipboard.png ─╮") {
		t.Fatalf("the label belongs at the top rail's right: %q", top)
	}
	if strings.Contains(footRow(view), "attach") {
		t.Fatalf("the label is never a slot on the foot row: %q", footRow(view))
	}
	// At a width too short for the name the name goes first, then the label.
	narrow := frameModel(t, 30, 40)
	narrowSeeded, _ := narrow.Update(clipboardOfferMsg{name: "a-very-long-screenshot-name.png"})
	narrow = narrowSeeded.(Model)
	if top := frameTopRail(stripANSI(narrow.renderPromptFrame())); strings.Contains(top, "…") || strings.Contains(top, "screenshot") {
		t.Fatalf("a label that does not fit is shed whole, never cut: %q", top)
	}
	// Working, the draft is not what is offered the clipboard.
	m.state = stateStreaming
	if top := frameTopRail(stripANSI(m.renderPromptFrame())); strings.Contains(top, "attach") {
		t.Fatalf("a working frame offers no attach: %q", top)
	}
}

// The clipboard is read when the frame goes idle and when the window comes
// back, and at no other moment: not on a tick, not on a key, and not at all
// without a reader. The rule is a transition, so it is asked of the model
// before against the model after, and only the command it returns is run.
func TestFrame_TheClipboardIsReadOnIdleAndOnFocusOnly(t *testing.T) {
	reads := 0
	read := func() (attachment.Clipboard, error) {
		reads++
		return attachment.Clipboard{Attachments: []provider.Attachment{{Name: "shot.png"}}}, nil
	}
	asked := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		if msg, ok := cmd().(clipboardOfferMsg); !ok || msg.name != "shot.png" {
			t.Fatalf("the read should name what the clipboard holds, got %#v", msg)
		}
		return true
	}
	step := func(m Model, msg tea.Msg) Model {
		next, _ := m.Update(msg)
		return next.(Model)
	}

	up := New(nil, mockStream, Wiring{ClipboardRead: read})
	m := step(up, tea.WindowSizeMsg{Width: 110, Height: 40})
	if !asked(m.attachOfferCmd(up)) || reads != 1 {
		t.Fatalf("the screen coming up should read the clipboard once, read %d", reads)
	}

	reads = 0
	for _, msg := range []tea.Msg{spinner.TickMsg{}, tea.KeyPressMsg{Code: 'x', Text: "x"}} {
		if asked(step(m, msg).attachOfferCmd(m)) || reads != 0 {
			t.Fatalf("a %T never reads the clipboard, read %d", msg, reads)
		}
	}

	blurred := step(m, tea.BlurMsg{})
	if blurred.attachOfferCmd(m) != nil {
		t.Fatal("losing focus reads nothing")
	}
	if !asked(step(blurred, tea.FocusMsg{}).attachOfferCmd(blurred)) || reads != 1 {
		t.Fatalf("regaining focus should read the clipboard once, read %d", reads)
	}

	reads = 0
	working := m
	working.state = stateStreaming
	if working.attachOfferCmd(m) != nil {
		t.Fatal("a turn starting reads nothing")
	}
	if !asked(m.attachOfferCmd(working)) || reads != 1 {
		t.Fatalf("the frame going idle should read the clipboard once, read %d", reads)
	}

	// A frame with no reader asks nothing, which is the whole of the hermetic
	// tier's promise.
	bare := frameModel(t, 110, 40)
	bareBlurred := step(bare, tea.BlurMsg{})
	if step(bareBlurred, tea.FocusMsg{}).attachOfferCmd(bareBlurred) != nil {
		t.Fatal("a frame given no reader asked for the clipboard")
	}
}
