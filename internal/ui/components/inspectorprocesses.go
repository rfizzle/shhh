package components

// The rail's PROCESSES and CALLS blocks: what the session left running. They
// stand beside AGENTS because they answer the same question — what else is
// working — and both open the process manager, where any of it can be opened
// and stopped (docs/interface/surfaces.md#the-process-manager).

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
)

// longCall is how long a call runs before the rail holds its row through a
// collapse. A flat 30 s, not a fraction of the command ceiling
// (command_timeout_seconds): the ceiling is a command's, and the calls on the
// rail include fetches and searches that have no ceiling of that size.
const longCall = 30 * time.Second

// InspectorProcess is one process the session started, as the rail draws it.
type InspectorProcess struct {
	Name string
	// Word is its state in the manager's words: running, exited 0,
	// exited 1, stopped, killed.
	Word    string
	Running bool
	// Failed says it ended by itself and not well, and Fresh that it did so
	// since the last turn started: news the reader has not had yet.
	Failed bool
	Fresh  bool
	// Ended says it ended by itself well; a process neither running, failed
	// nor ended was stopped or killed.
	Ended bool
	// Age is its uptime, or how long it ran.
	Age       time.Duration
	Container bool
}

// InspectorCall is one call in flight, as the rail draws it.
type InspectorCall struct {
	Name string
	// Summary is its arguments in a line, in the receipt's words.
	Summary string
	Age     time.Duration
	// Stopped says a person stopped it and the round has not yet ended.
	Stopped bool
}

// processesBlock is PROCESSES: one row per process the session started. A
// session that started none draws no block.
//
// A process running, or one that failed since the last turn began, is something
// the reader may want to stop or look at now, so the block asks; once every
// process has ended well it is history read before the next send.
func (r InspectorRail) processesBlock(width int) (railBlock, bool) {
	if len(r.Processes) == 0 {
		return railBlock{}, false
	}
	running, failed, fresh := 0, 0, 0
	for _, p := range r.Processes {
		switch {
		case p.Running:
			running++
		case p.Failed:
			failed++
			if p.Fresh {
				fresh++
			}
		}
	}
	meta := fmt.Sprintf("%d running", running)
	if running == 0 {
		meta = fmt.Sprintf("%d ended", len(r.Processes))
	}
	if failed > 0 {
		meta += fmt.Sprintf(" · %d failed", failed)
	}
	// Collapsed it states the one figure its rows would: how many run, or how
	// many have exited and how many of those failed.
	collapsed := fmt.Sprintf("%d running", running)
	if running == 0 {
		collapsed = fmt.Sprintf("%d exited", len(r.Processes))
	}
	if failed > 0 {
		collapsed += fmt.Sprintf(" · %d failed", failed)
	}
	b := railBlock{heading: railHeading(RailProcesses, meta, sty.dim, width), class: railStanding,
		collapsedHeading: railHeading(RailProcesses, collapsed, sty.dim, width)}
	if running > 0 || fresh > 0 {
		b.class = railAsks
	}
	for _, p := range r.Processes {
		glyph, style := processTone(p)
		right := style.Render(p.Word) + sty.dim.Render(" · "+FormatElapsed(p.Age))
		if p.Container {
			right += sty.dim.Render(" · container")
		}
		row := railRow(glyph+" "+sty.body.Render(p.Name), right, width, inspectorIndent)
		// A process that died since the turn began is news the rail keeps
		// through a collapse, as a blocked child's row is kept.
		if p.Failed && p.Fresh {
			b.pin(row)
			continue
		}
		b.add(row)
	}
	return b, true
}

// processTone is a process's mark and the weight its word carries: the
// manager's marks, so the rail and the screen read one state one way.
func processTone(p InspectorProcess) (string, lipgloss.Style) {
	switch {
	case p.Running:
		return sty.spinText.Render("▸"), sty.dim
	case p.Failed:
		return sty.err.Render("✗"), sty.body
	case p.Ended:
		return sty.add.Render("✓"), sty.dim
	}
	return sty.dim.Render("⊘"), sty.dim
}

// callsBlock is CALLS: the calls of the round in flight, one row each, drawn
// only while a round has any. It is not the TOOLS block, which says where the
// session's tools came from; this says which of them are running now.
func (r InspectorRail) callsBlock(width int) (railBlock, bool) {
	if len(r.Calls) == 0 {
		return railBlock{}, false
	}
	running := 0
	for _, c := range r.Calls {
		if !c.Stopped {
			running++
		}
	}
	meta := sty.spinText.Render("▸") + sty.dim.Render(fmt.Sprintf(" %d running", running))
	if running == 0 {
		meta = sty.dim.Render(fmt.Sprintf("%d stopped", len(r.Calls)))
	}
	b := railBlock{heading: railHeading(RailCalls, meta, sty.dim, width), class: railLive}
	if running > 0 {
		b.class = railAsks
		// Collapsed it states the count and the longest wait: `▸ 3 calls · 41s`.
		var longest time.Duration
		for _, c := range r.Calls {
			if !c.Stopped && c.Age > longest {
				longest = c.Age
			}
		}
		noun := "calls"
		if running == 1 {
			noun = "call"
		}
		b.collapsedHeading = railHeading(RailCalls, sty.spinText.Render("▸")+
			sty.dim.Render(fmt.Sprintf(" %d %s · %s", running, noun, FormatElapsed(longest))), sty.dim, width)
	}
	for _, c := range r.Calls {
		glyph, right := sty.spinText.Render("▸"), sty.dim.Render(FormatElapsed(c.Age))
		if c.Stopped {
			glyph, right = sty.dim.Render("⊘"), sty.dim.Render("stopped by you")
		}
		// The receipt's title says what the call does in its own verb, which
		// is the tool's name said as an act; the bare name is the fallback.
		what := c.Summary
		if what == "" {
			what = c.Name
		}
		row := railRow(glyph+" "+sty.body.Render(what), right, width, inspectorIndent)
		// A call that has run past longCall keeps its row through a collapse,
		// so the block stays open in place for the call the reader may want
		// to stop.
		if !c.Stopped && c.Age >= longCall {
			b.pin(row)
			continue
		}
		b.add(row)
	}
	return b, true
}
