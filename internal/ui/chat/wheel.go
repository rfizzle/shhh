package chat

// Wheel input at the program boundary.
//
// A trackpad flings the wheel at hundreds of notches a second, and each notch
// used to be its own message: its own Update, its own three-line scroll, its
// own frame. The scrolling itself stayed correct — the transcript moved the
// same distance — but every keystroke typed behind the fling waited in the
// queue while the frames drained, which is the one latency a terminal user
// actually feels.
//
// So the notches are merged before they reach the queue's consumer, at
// tea.WithFilter — the same boundary Crush coalesces at. Consecutive notches
// in one direction accumulate into a single message carrying the summed
// distance; the sum is flushed by the first thing that is not another notch
// the same way — a direction change, any other terminal input (which rides
// out behind the flushed distance, so a key pressed mid-fling lands after
// the scroll it followed and never after every notch), or a probe the filter
// schedules so a fling with nothing behind it still lands. The model scrolls
// the sum through the same per-surface switch a single notch used, so the
// transcript, the full-screen diff and the review read identically.

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// wheelProbeDelay is how long the filter waits for the next notch before
// flushing what it holds: one frame at 60Hz, the cadence Crush samples wheel
// input at. Shorter would forward almost every notch alone; longer would make
// a slow deliberate scroll feel detached from the finger.
const wheelProbeDelay = 16 * time.Millisecond

// coalescedWheelMsg is a run of same-direction wheel notches as one message.
type coalescedWheelMsg struct {
	// lines is the summed scroll distance in transcript rows, negative up.
	lines int
	// then is the message that flushed the run, delivered after the scroll it
	// queued behind. Nil when the run was flushed by the probe or a direction
	// change.
	then tea.Msg
}

// wheelProbeMsg asks the filter to flush an accumulation the message stream
// has gone quiet under. The sequence number keeps a probe scheduled for an
// old run from flushing a new one.
type wheelProbeMsg struct{ seq int }

// WheelFilter merges wheel floods at the program's message boundary. It is
// handed to tea.WithFilter, so every field is touched only from the
// program's own message loop; the probe goroutine calls send and nothing
// else.
type WheelFilter struct {
	// send enqueues a probe behind whatever the terminal has already queued.
	// It is the program's own Send, attached once the program exists.
	send func(tea.Msg)
	// lines is the run accumulated so far, negative up; 0 means no run.
	lines int
	// probing is whether a probe for the current run is already in flight.
	probing bool
	seq     int
}

// NewWheelFilter returns a filter with nothing accumulated.
func NewWheelFilter() *WheelFilter { return &WheelFilter{} }

// SetSend attaches the program's Send, which the filter cannot have at
// construction time because the filter is an option the program is built
// with. Call it after tea.NewProgram and before Run.
func (f *WheelFilter) SetSend(send func(tea.Msg)) { f.send = send }

// Filter is the tea.WithFilter hook. Wheel notches accumulate and return nil
// — dropped from the queue — until something flushes them; everything else
// passes through, prefixed by the flush it triggered.
func (f *WheelFilter) Filter(model tea.Model, msg tea.Msg) tea.Msg {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		// With reporting off the wheel event should behave exactly as it
		// does today — reach the model and be ignored there — so the filter
		// steps aside rather than duplicating the model's answer.
		if m, ok := model.(Model); ok && !m.mouseOn {
			return msg
		}
		var delta int
		switch msg.Button {
		case tea.MouseWheelUp:
			delta = -wheelLines
		case tea.MouseWheelDown:
			delta = wheelLines
		default:
			// Horizontal wheel: nothing on the surface scrolls sideways by
			// wheel, so it passes through like any other message, flushing
			// the vertical run ahead of it.
			return f.flushBefore(msg)
		}
		if f.lines != 0 && (f.lines < 0) != (delta < 0) {
			// A direction change is a new gesture: the old run lands first,
			// whole, and the new one starts accumulating behind it.
			flushed := coalescedWheelMsg{lines: f.lines}
			f.lines = delta
			f.probe()
			return flushed
		}
		f.lines += delta
		f.probe()
		return nil
	case wheelProbeMsg:
		if msg.seq != f.seq {
			return nil
		}
		f.probing = false
		if f.lines == 0 {
			return nil
		}
		flushed := coalescedWheelMsg{lines: f.lines}
		f.lines = 0
		return flushed
	}
	return f.flushBefore(msg)
}

// flushBefore hands msg on, carrying any accumulated run in front of it so
// the two arrive in the order the hands produced them.
//
// Only terminal input rides inside the flush. Everything else on the channel
// — command results, and above all the event loop's own control messages
// (a BatchMsg, a QuitMsg, the renderer's WindowSizeMsg) — is answered by the
// loop itself before the model ever sees it, and a control message wrapped
// inside the flush would reach only the model: the batch's commands would
// silently never run, the quit would never quit. Those pass through
// untouched and the run waits for its probe; ordering against anything that
// is not a keystroke carries no meaning for a scroll.
func (f *WheelFilter) flushBefore(msg tea.Msg) tea.Msg {
	if f.lines == 0 {
		return msg
	}
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg, tea.PasteMsg:
		flushed := coalescedWheelMsg{lines: f.lines, then: msg}
		f.lines = 0
		return flushed
	}
	return msg
}

// probe schedules the flush that ends a run nothing else interrupts. It goes
// through the program's queue rather than firing directly, so every notch
// already queued ahead of it joins the run first; the AfterFunc keeps the
// send off the message loop's own goroutine, which Send would block.
func (f *WheelFilter) probe() {
	if f.probing || f.send == nil {
		return
	}
	f.probing = true
	f.seq++
	seq, send := f.seq, f.send
	time.AfterFunc(wheelProbeDelay, func() { send(wheelProbeMsg{seq: seq}) })
}
