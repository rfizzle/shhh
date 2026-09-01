package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func wheelDown() tea.MouseWheelMsg { return tea.MouseWheelMsg{Button: tea.MouseWheelDown} }
func wheelUp() tea.MouseWheelMsg   { return tea.MouseWheelMsg{Button: tea.MouseWheelUp} }

// A fling followed by a keystroke leaves the queue as exactly two things: the
// summed scroll, and the key riding behind it.
func TestWheelFloodCoalescesBeforeKey(t *testing.T) {
	m := New(nil, nil).WithMouse(true)
	f := NewWheelFilter()

	for i := 0; i < 30; i++ {
		if out := f.Filter(m, wheelDown()); out != nil {
			t.Fatalf("notch %d escaped the accumulator: %#v", i, out)
		}
	}
	key := tea.KeyPressMsg{Code: 'x', Text: "x"}
	out, ok := f.Filter(m, key).(coalescedWheelMsg)
	if !ok {
		t.Fatalf("key did not flush the run")
	}
	if out.lines != 30*wheelLines {
		t.Fatalf("lines = %d, want %d", out.lines, 30*wheelLines)
	}
	if got, ok := out.then.(tea.KeyPressMsg); !ok || got.Text != "x" {
		t.Fatalf("key not carried behind the flush: %#v", out.then)
	}
}

// A direction change flushes the old run first, and the probe lands the new
// one, so the two deltas arrive in gesture order.
func TestWheelDirectionChangeFlushesInOrder(t *testing.T) {
	m := New(nil, nil).WithMouse(true)
	f := NewWheelFilter()

	for i := 0; i < 10; i++ {
		f.Filter(m, wheelDown())
	}
	first, ok := f.Filter(m, wheelUp()).(coalescedWheelMsg)
	if !ok || first.lines != 10*wheelLines || first.then != nil {
		t.Fatalf("direction change did not flush the down run: %#v", first)
	}
	for i := 0; i < 4; i++ {
		if out := f.Filter(m, wheelUp()); out != nil {
			t.Fatalf("up notch %d escaped the accumulator: %#v", i, out)
		}
	}
	second, ok := f.Filter(m, wheelProbeMsg{seq: f.seq}).(coalescedWheelMsg)
	if !ok || second.lines != -5*wheelLines {
		t.Fatalf("probe flush = %#v, want %d lines", second, -5*wheelLines)
	}
}

// With reporting off the filter steps aside: every message, wheel included,
// passes through unchanged.
func TestWheelFilterMouseOffIsNoOp(t *testing.T) {
	m := New(nil, nil).WithMouse(false)
	f := NewWheelFilter()

	if out, ok := f.Filter(m, wheelDown()).(tea.MouseWheelMsg); !ok || out.Button != tea.MouseWheelDown {
		t.Fatalf("wheel notch was not passed through: %#v", out)
	}
	key := tea.KeyPressMsg{Code: 'x', Text: "x"}
	if out, ok := f.Filter(m, key).(tea.KeyPressMsg); !ok || out.Text != "x" {
		t.Fatalf("key was not passed through: %#v", out)
	}
}

// A stale probe — one scheduled for a run that already flushed — must not
// flush the run that came after it.
func TestWheelStaleProbeFlushesNothing(t *testing.T) {
	m := New(nil, nil).WithMouse(true)
	f := NewWheelFilter()
	f.SetSend(func(tea.Msg) {})

	f.Filter(m, wheelDown())
	stale := f.seq
	// The probe flushes its run; the next run schedules a new sequence, and
	// a late duplicate of the first probe must not flush it early.
	f.Filter(m, wheelProbeMsg{seq: stale})
	f.Filter(m, wheelDown())
	if out := f.Filter(m, wheelProbeMsg{seq: stale}); out != nil {
		t.Fatalf("stale probe flushed: %#v", out)
	}
	if f.lines != wheelLines {
		t.Fatalf("live run lost: lines = %d", f.lines)
	}
}

// The summed delta scrolls the same surface switch a single notch does.
func TestCoalescedWheelScrollsTranscript(t *testing.T) {
	updated, _ := New(nil, mockStream).WithMouse(true).Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m := updated.(Model)
	for i := 0; i < 60; i++ {
		m.appendEntry(entry{kind: entrySystem, text: "line"})
	}
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	before := m.viewport.YOffset()
	updated, _ = m.Update(coalescedWheelMsg{lines: -9})
	mm := updated.(Model)
	if got := mm.viewport.YOffset(); got != before-9 {
		t.Fatalf("offset = %d, want %d", got, before-9)
	}
	// The message the flush carried is delivered after the scroll.
	updated, _ = mm.Update(coalescedWheelMsg{lines: -3, then: tea.KeyPressMsg{Code: 'h', Text: "h"}})
	mm = updated.(Model)
	if got := mm.input.Value(); got != "h" {
		t.Fatalf("carried key did not reach the draft: %q", got)
	}
}

// A message that is not terminal input — a command result, one of the event
// loop's own control messages — must never ride inside the flush: the loop
// answers those itself, and one wrapped inside another message would reach
// only the model. The run stays accumulated for the probe.
func TestWheelNonInputPassesThroughUnwrapped(t *testing.T) {
	m := New(nil, nil).WithMouse(true)
	f := NewWheelFilter()

	f.Filter(m, wheelDown())
	type controlMsg struct{}
	if out, ok := f.Filter(m, controlMsg{}).(controlMsg); !ok {
		t.Fatalf("control message was not passed through untouched: %#v", out)
	}
	if f.lines != wheelLines {
		t.Fatalf("the run must survive for the probe, lines = %d", f.lines)
	}
	out, ok := f.Filter(m, wheelProbeMsg{seq: f.seq}).(coalescedWheelMsg)
	if !ok || out.lines != wheelLines {
		t.Fatalf("probe did not flush the surviving run: %#v", out)
	}
}
