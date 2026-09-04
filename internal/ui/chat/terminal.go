package chat

// The window shhh is running in (
// docs/interface/surfaces.md#what-the-tab-says).
//
// Everything else in the product draws inside one rectangle. Four things
// here are about the rectangle's frame instead — what the tab is called,
// what it shows while a turn runs, handing the terminal back, and taking
// the screen back — and they are one file because they are one subject: the
// session's relationship with the terminal it borrowed.
//
// The first two are fields on the View rather than sequences anyone writes.
// Bubble Tea carries a window title and a progress state on the same value
// the screen is, diffs them frame to frame, and clears both when the program
// ends — so the tab cannot end up saying something the model stopped
// believing, which is the failure mode the alt screen and the mouse mode
// already had before they became fields (model.go).
//
// Both are gated on the terminal being one at all and on it not having said
// in advance that it is dumb (caps.Terminal). A pipe gets no title, and a
// TERM=dumb terminal is told nothing: it said there was nothing worth
// sending.
//
// The other two are keys. Ctrl+Z is the chord the shell taught every
// foreground job, and shhh answers it rather than leaving it to a terminal
// in raw mode that will not act on it — except while a turn is in flight,
// where it is refused for the same reason the editor is (editor.go): a
// stopped process is not reading the stream it asked for, and the request
// times out while nobody is there to see it. Ctrl+L is the way back from a
// screen that got written over — a notification, a background job's line,
// the resume from that suspend — and it repaints from what the model already
// holds, so it costs nothing and changes nothing.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// defaultTitle is what a session with no name of its own is called. It is
// the header's fallback and the tab's, from one place, because the two
// naming the same session differently is the whole reason a tab is hard to
// find.
const defaultTitle = "shhh chat"

// windowSegments is how much of the directory the tab carries. Two, because
// a tab is a few characters wide and its job is to tell tabs apart rather
// than to state a path: half the checkouts on a machine end in `src`, and
// none of them share the segment above it.
const windowSegments = 2

// progressFailedHold is how long the tab stays red after a turn breaks. Long
// enough to be seen by someone glancing back at a window they left, short
// enough to still be about the turn that just failed — a progress state that
// outlives its turn is a tab reporting on the session, which is not a
// question the tab can answer.
const progressFailedHold = 3 * time.Second

// progressClearedMsg is the red state's window shutting. It carries the
// sequence for the reason armExpiredMsg does (cancel.go): a tick scheduled
// for a state that has since been replaced must not clear the replacement.
type progressClearedMsg struct{ seq int }

// windowChrome reports whether this terminal is one shhh may say anything to
// outside the rectangle it draws in.
//
// Asked is the there-is-a-terminal half: the capability probe declines to ask
// a pipe anything, so it is also the one place that already knows. Dumb is
// the other half, and it is a different fact from a query that came back
// empty — the terminal said so itself, before being asked.
func (m Model) windowChrome() bool { return m.caps.Asked && !m.caps.Dumb }

// windowTitle is what the terminal's tab is called while this session holds
// it: the command that is running and the directory it is running in, which
// are the two things a reader hunting through eight tabs is actually asking.
//
// A waiting decision moves to the front of it, under the glyph every gated
// state in the product wears. It is the one thing that happens in here that
// the reader has to come back for, and a tab is where they will see it from
// the next window over.
//
// Empty means "say nothing", and Bubble Tea reads it that way both times it
// matters: it clears the title when this is turned off mid-session, and it
// clears it again on the way out. Clearing is the most a program can do —
// there is no reading the old title back to restore it — so what the tab
// shows afterwards is whatever the shell puts there at its next prompt.
func (m Model) windowTitle() string {
	if !m.windowTitleOn || !m.windowChrome() {
		return ""
	}
	name := m.title
	if name == "" {
		name = defaultTitle
	}
	// A sprint takes the name's place while it is working. The session's
	// own name is the same in every window a sprint runs through — it makes
	// one per item — so what tells the reader anything is how far through
	// the set it is and which item it is on (todosprint.go).
	if sprint := m.sprintTitle(); sprint != "" {
		name = sprint
	}
	if m.windowDir != "" {
		name += " · " + m.windowDir
	}
	if m.interruptShowing() {
		// The same ⏸ the mode chip and a held child wear: the session is
		// stopped, waiting on a person. "A decision" is the same set the
		// summons reads (notify.go) — an approval, a plan, a child's routed
		// ask — so the tab and the notification cannot disagree about what
		// the reader is being called back for.
		return "⏸ " + name
	}
	return name
}

// sessionDir is the directory the tab carries, resolved once when the model
// is built. Nothing in a session changes it — shhh never chdirs, and a
// working scope is a set of grants rather than a place to stand — so asking
// the operating system again on every frame would be re-answering a question
// that was settled when the process started.
func sessionDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return tabDir(dir)
}

// tabDir is the working directory as the tab states it: its last couple of
// segments, and nothing else. The root is itself, because a session in `/`
// has no segments to take.
//
// Control characters come out first, and that is not tidying. Every other
// string shhh puts on the wire is its own words; this one is a filename, and
// a directory with a BEL or an ESC in it would end the title sequence early
// and leave the rest of the path being read by the terminal as instructions.
// It is the same reason a notification's text is stripped before it goes out
// (caps/notify.go).
func tabDir(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(plainPath(dir)))
	trimmed := strings.Trim(clean, "/")
	if trimmed == "" {
		return clean
	}
	segs := strings.Split(trimmed, "/")
	if len(segs) > windowSegments {
		segs = segs[len(segs)-windowSegments:]
	}
	return strings.Join(segs, "/")
}

// plainPath drops the control characters a path may legally contain, so
// nothing a directory is named can steer the terminal.
func plainPath(dir string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, dir)
}

// progressBar is the tab's own activity light: the indeterminate state while
// the session is working, the error state for a moment after a turn breaks,
// and nothing at all the rest of the time.
//
// It rides the notification switch rather than the title's, because it makes
// the same promise: shhh will get your attention while you are looking
// somewhere else. The title is identity and is useful whether or not anyone
// is watching; this is a summons with no words, and a reader who turned the
// summons off turned this off with it.
//
// There is no percentage to report and there will not be one. A turn does not
// know how much of itself is left, and a bar that guesses is a bar that lies
// — the indeterminate state is the honest shape of "something is happening".
//
// The light follows the frame, which attached is the child's work rather than
// the orchestrator's; the red follows the session's own turn (progressCmd),
// because turnOutcome is the session's and a child's failure is reported on
// the child's row. So the two read different questions on purpose: is
// anything happening here, and did the thing this session was doing break.
func (m Model) progressBar() *tea.ProgressBar {
	if !m.notifyOn || !m.windowChrome() {
		return nil
	}
	switch {
	case m.working() || m.frameWorking():
		return tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	case m.progressFailed:
		return tea.NewProgressBar(tea.ProgressBarError, 100)
	}
	return nil
}

// progressCmd keeps the error state's brief life. Like the notification and
// the closing summary it is derived in the Update tail from the model before
// against the model after (model.go): "the turn just broke" is a fact about a
// transition, and every path that can break one would otherwise have to
// remember to say so.
//
// A turn starting clears it rather than waiting for its tick, so a session
// that failed and was asked again does not go back to red when the second
// turn succeeds inside the window the first one opened.
//
// The edge is a turn's, not the working state's, and the difference is the
// bug it was written with: turnOutcome belongs to the last turn and outlives
// it, while a !bang, a /run, an approved tool and a compaction all reach a
// working state without opening one. Read off the working state alone, the
// next command after a failed turn inherits the failure and reddens the tab
// for succeeding. So the same fact the summons reads is read here — a turn
// was open and has just closed (notify.go).
func (m *Model) progressCmd(prev Model) tea.Cmd {
	switch {
	case m.working() && !prev.working():
		m.progressFailed = false
		m.progressSeq++
	case prev.turnOpen && !m.turnOpen && !m.working() && m.turnOutcome == components.TurnFailed:
		m.progressFailed = true
		m.progressSeq++
		seq := m.progressSeq
		return tea.Tick(progressFailedHold, func(time.Time) tea.Msg {
			return progressClearedMsg{seq: seq}
		})
	}
	return nil
}

// suspend hands the terminal back to the shell, or says why it will not.
func (m Model) suspend() (tea.Model, tea.Cmd) {
	if reason, refused := m.suspendRefusal(); refused {
		return m.surfaceNotice(reason)
	}
	// Bubble Tea puts the terminal back the way it found it, stops itself,
	// and restores the alt screen, the mouse mode and the focus reporting
	// from the last View when the shell brings it back — which is the same
	// argument for those being fields rather than commands.
	return m, tea.Suspend
}

// suspendRefusal is whether the chord is refused here, and why.
//
// It is editorRefusal's list for editorRefusal's reason, one step further:
// the editor at least leaves shhh's process running to notice things while
// it is away. A stopped process notices nothing. The stream it asked for
// backs up in a socket nobody is reading, the provider gives up on it, and
// the reader who typed fg gets a turn that failed while they were in another
// window — so the chord is refused with a notice and the session goes on
// running.
//
// frameWorking rather than working alone: attached, the turn that must not
// be stopped is the child's, and the orchestrator's own state says nothing
// about it. The third branch cannot be reached through the keyboard — every
// surface that takes it is routed before the draft's keys are — and is here
// for editorRefusal's reason, that a guard which fails closed costs nothing.
func (m Model) suspendRefusal() (string, bool) {
	switch {
	case m.interruptShowing():
		return "a decision is waiting — answer it first, then suspend", true
	case m.working() || m.frameWorking():
		return "not while the turn is running — a stopped shhh is not reading the stream, and the request times out", true
	case !m.inputLive():
		return "the draft does not have the keyboard", true
	}
	return "", false
}

// resumed is the session coming back from a suspend. Everything the terminal
// needs told again is on the View and Bubble Tea has already sent it; the one
// thing that is not is the alternate-scroll suppression, which belongs to
// whoever started the program rather than to a frame (altscroll.go).
func (m Model) resumed() (tea.Model, tea.Cmd) {
	return m, resumeAlternateScroll()
}

// redraw repaints every rectangle from what the model already holds. It is
// the recovery from a screen something else wrote on — a notification, a
// background job's line, the shell's own output around a suspend — and it
// touches nothing: the draft, the history and any live selection are the
// same afterwards, because the screen was never where they lived.
func (m Model) redraw() (tea.Model, tea.Cmd) {
	return m, tea.ClearScreen
}

// windowStatus describes the tab's title for /ui, naming the reason there is
// nothing on it when there is one — the same courtesy notifyStatus pays,
// because a setting that is on and invisible is the case worth explaining.
func (m Model) windowStatus() string {
	if !m.windowTitleOn {
		return "off"
	}
	switch {
	case !m.caps.Asked:
		return "on, but there is no terminal to name"
	case m.caps.Dumb:
		return "on, but TERM says this terminal is a dumb one — nothing is sent"
	}
	return "on (" + m.windowTitle() + ")"
}

// windowCommand handles /ui window: what the terminal's tab is called. It is
// separate from /ui title, which is the session's own generated name
// (title.go) — one is what the reader's window manager shows, the other is
// what the saved conversation is called, and a single switch for both would
// turn off half of what whoever flipped it meant.
func (m *Model) windowCommand(parts []string) string {
	if len(parts) == 2 {
		return "Window title: " + m.windowStatus() +
			".\nUsage: /ui window <on|off> — on, the terminal's tab says which shhh this is and marks a waiting decision; off, the tab keeps whatever your terminal puts there."
	}
	if len(parts) != 3 {
		return "Usage: /ui window <on|off>"
	}
	var on bool
	switch parts[2] {
	case "on", "true", "yes":
		on = true
	case "off", "false", "no":
		on = false
	default:
		return fmt.Sprintf("Error: unknown window setting %q (on, off)", parts[2])
	}
	if on == m.windowTitleOn {
		return "The window title is already " + m.windowStatus() + "."
	}
	return m.setWindowTitleOn(on)
}

// setWindowTitleOn flips the title and persists the answer, so the choice
// outlives the process that made it — the bargain /ui mouse and /ui notify
// both make. A session with no writer still flips, and says only what it
// could not do.
func (m *Model) setWindowTitleOn(on bool) string {
	m.windowTitleOn = on
	note := windowNote(on)
	if m.writeConfig == nil {
		return note + "\nThis session cannot write the config file, so it is for this session only."
	}
	if err := m.writeConfig("appearance.window_title", strconv.FormatBool(on)); err != nil {
		return note + "\nIt could not be saved: " + err.Error()
	}
	return note + " Saved — new sessions start this way."
}

// windowNote says what the new state does, because "on" is not self-evident
// from a switch called window: what it buys is a tab you can find, and what
// it costs is a tab title you chose yourself.
func windowNote(on bool) string {
	if on {
		return "Window title on — the terminal's tab says which shhh this is: the command, the directory it is running in, and ⏸ while a decision is waiting."
	}
	return "Window title off — the tab keeps whatever your terminal puts there."
}

// WithWindowTitle sets whether the session names the terminal's tab
// (appearance.window_title). Hosts that do not call it name it, which is the
// default the config resolves to.
func (m Model) WithWindowTitle(on bool) Model {
	m.windowTitleOn = on
	return m
}
