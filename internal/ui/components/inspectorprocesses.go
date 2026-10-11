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
	b := railBlock{heading: railHeading(RailProcesses, meta, sty.dim, width), class: railStanding}
	if running > 0 || fresh > 0 {
		b.class = railAsks
	}
	for _, p := range r.Processes {
		glyph, style := processTone(p)
		right := style.Render(p.Word) + sty.dim.Render(" · "+FormatElapsed(p.Age))
		if p.Container {
			right += sty.dim.Render(" · container")
		}
		b.add(railRow(glyph+" "+sty.body.Render(p.Name), right, width, inspectorIndent))
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
		left := glyph + " " + sty.body.Render(what)
		b.add(railRow(left, right, width, inspectorIndent))
	}
	return b, true
}
