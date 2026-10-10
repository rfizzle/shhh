package chat

// Suspending says how to come back (docs/interface/surfaces.md#the-input-frame).
//
// A session on the alternate screen leaves nothing behind when it goes, which
// is the reason the exit banner exists, and a suspend goes the same way: the
// screen is gone, the shell's prompt is back, and nothing says shhh is
// stopped rather than crashed. So one line goes to the shell's screen first.
//
// Bubble Tea's own suspend runs release, stop and restore in one call with no
// hook between them, and a Println while the alternate screen is up is drawn
// on it and lost. The line has to go out after the terminal is released and
// before the stop, so the host does the three steps itself through the
// public ReleaseTerminal and RestoreTerminal and answers with the ResumeMsg
// Bubble Tea would have sent.
//
// The public release passes reset=false where Bubble Tea's own passes true
// (v2.0.9, tty.go). Reset only replaces the renderer's cell writer with a
// fresh inline one; the release has already left the alternate screen through
// the renderer's close, and the restore re-enters it with a full erase, so
// the frame is painted whole either way.
//
// It is a filter on the program's message loop and not a SIGTSTP handler:
// ctrl+z is not a signal here, since the terminal is in raw mode and shhh
// reads the byte (terminal.go). A handler would take away the default stop
// and Bubble Tea would wait for a SIGCONT nobody sends.

import (
	tea "charm.land/bubbletea/v2"
)

// SuspendNote is the line the shell sees while shhh is stopped. It names fg
// and nothing else: a backgrounded full-screen program loses the terminal and
// stops again at its first read of it, so offering bg offers a trap.
const SuspendNote = "shhh suspended · fg brings it back"

// Suspender takes the program's SuspendMsg and does what Bubble Tea would,
// with the note written between the release and the stop. Its steps are
// fields so a test can hold the order without a signal.
type Suspender struct {
	// Release gives the terminal back: the alternate screen left, the mode
	// restored. It is the program's ReleaseTerminal.
	Release func() error
	// Note writes SuspendNote to the shell's screen.
	Note func()
	// Stop stops the process and returns once it is continued. Nil where the
	// platform cannot stop it; the message then passes through untouched and
	// Bubble Tea does what it does there.
	Stop func()
	// Restore takes the terminal back and repaints. It is the program's
	// RestoreTerminal.
	Restore func() error
}

// Filter is the tea.WithFilter hook. Only a SuspendMsg is touched; it is
// replaced by the ResumeMsg that follows the continue, so Bubble Tea does not
// suspend a second time.
func (s Suspender) Filter(_ tea.Model, msg tea.Msg) tea.Msg {
	if _, ok := msg.(tea.SuspendMsg); !ok || s.Stop == nil {
		return msg
	}
	if err := s.Release(); err != nil {
		// Bubble Tea aborts the suspend when it cannot release input, and
		// so does this: nothing is written to a terminal still in raw mode.
		return nil
	}
	s.Note()
	s.Stop()
	_ = s.Restore()
	return tea.ResumeMsg{}
}
