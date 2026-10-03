package components

// The rail's TOOLS block: where this session's tools came from and what
// standing each source is in. It is a file of its own because a source's
// standing is a small vocabulary of its own, drawn in words the block and
// `shhh mcp` both read.

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// InspectorTools is the TOOLS block: where this session's tools came from
// and which of those sources answered. A tool that silently failed to
// register is otherwise indistinguishable from one the model simply did not
// call, and the difference is the whole reason the block exists.
// See docs/interface/surfaces.md#the-inspector-rail.
type InspectorTools struct {
	Sources []InspectorToolSource
	// Up is how many sources answered, over every source the session has and
	// not only the rows that fit — the heading states it against the same
	// total, and a fold that changed the numerator would make the ratio a lie.
	Up int
	// More is how many sources Sources left out.
	More int
	// MemoryOmitted is how many durable memories the recall budget kept out
	// of the system prompt. It is on this block because it is the same
	// question the block already answers — what did this session actually get
	// — and a memory the session never saw is otherwise indistinguishable
	// from one that was never written.
	MemoryOmitted int
}

// InspectorToolSource is one place tools came from: the built-in toolset, or
// a server the session was told to reach.
type InspectorToolSource struct {
	Name  string
	State ToolSourceState
	// Note is what the state amounts to in the host's own words — a tool
	// count for a source that answered, the reason for one that did not.
	Note string
}

// ToolSourceState is a source's standing, and the block draws it as a glyph
// and a word so a monochrome terminal reads the same as a colour one. It is
// four states rather than the transport's own vocabulary because the reader's
// question is whether the tools are there, and the note beside it says why
// when they are not.
type ToolSourceState int

const (
	// ToolSourceUp: it answered and its tools are in the toolset.
	ToolSourceUp ToolSourceState = iota
	// ToolSourceBlocked: it is configured and something is in the way that
	// only a person can move.
	ToolSourceBlocked
	// ToolSourceOff: it was left out on purpose.
	ToolSourceOff
	// ToolSourceFailed: it was tried and did not answer.
	ToolSourceFailed
)

// toolsBlock is the TOOLS block. It sits under AGENTS because the two answer
// the same question about the session's machinery — who else is working, and
// what this session can reach — and above CONTEXT because those are what the
// work is costing rather than what it is made of.
func (r InspectorRail) toolsBlock(width int) (railBlock, bool) {
	t := r.Tools
	if t == nil || (len(t.Sources) == 0 && t.MemoryOmitted == 0) {
		return railBlock{}, false
	}
	// The ratio is over every source the session has, and a session with none
	// gets no ratio at all: "0 of 0 up" is a fabricated zero, and the block is
	// on screen for the memory row underneath.
	meta := ""
	if total := len(t.Sources) + t.More; total > 0 {
		meta = fmt.Sprintf("%d of %d up", t.Up, total)
	}
	b := railBlock{heading: railHeading("TOOLS", meta, sty.dim, width)}
	for _, s := range t.Sources {
		glyph, word, style := toolSourceTone(s.State)
		left := glyph + " " + sty.body.Render(s.Name)
		right := style.Render(word)
		if s.Note != "" {
			// The name is the row's target and the note its outcome, and the
			// note is what gives way: which server broke is the one word the
			// row exists to say, and a reason cut short still reads as one.
			// The note is cut to what the name and the word leave it, and
			// dropped whole where not even its separator and one cell fit.
			const sep = " · "
			if room := railRoom(width, right, inspectorIndent) - lipgloss.Width(left); room > lipgloss.Width(sep) {
				right += sty.dim.Render(Clip(sep+s.Note, room))
			}
		}
		b.add(railRow(left, right, width, inspectorIndent))
	}
	if t.More > 0 {
		// A count of sources rather than one, so it folds with the first
		// source the rail takes and the block's marker states both numbers,
		// and it opens the block's door as that marker does (railLine.more).
		b.moreRow(indentRow(sty.hint.Render(fmt.Sprintf("… %d more", t.More)), width), t.More)
	}
	if t.MemoryOmitted > 0 {
		// A source row in everything but name: the memory the prompt carries
		// is a place this session's knowledge came from, and this says how
		// much of it did not arrive. The mark is the one the rail already
		// uses for something only a person can move — the way out is to
		// shorten an entry.
		b.add(railRow(sty.accent.Render("⚠")+" "+sty.body.Render("memory"),
			sty.dim.Render(fmt.Sprintf("%d did not fit", t.MemoryOmitted)),
			width, inspectorIndent))
	}
	return b, true
}

// ToolSourceWord is a source's state in the one word the block's glyph stands
// for, so a surface that prints the block in words rather than drawing it says
// the same thing.
func ToolSourceWord(s ToolSourceState) string {
	_, word, _ := toolSourceTone(s)
	return word
}

// toolSourceTone is a source's glyph, its word and the weight the word
// carries. The glyph is the distinction a monochrome terminal reads: the
// same four marks every other surface uses for done, waiting on a person,
// left out and failed.
func toolSourceTone(s ToolSourceState) (string, string, lipgloss.Style) {
	switch s {
	case ToolSourceUp:
		return sty.add.Render("✓"), "up", sty.dim
	case ToolSourceBlocked:
		return sty.accent.Render("⚠"), "blocked", sty.body
	case ToolSourceOff:
		return sty.dim.Render("⊘"), "off", sty.dim
	}
	return sty.err.Render("✗"), "error", sty.body
}
