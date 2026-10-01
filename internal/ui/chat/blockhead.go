package chat

// A code block's heading row in the transcript
// (docs/interface/surfaces.md#the-activity-row). The renderer heads every
// closed fence with a row naming its language, one row above the block at
// its indent; this file is what the pointer makes of that row.
//
// Two things. A click on a reply's heading copies that block, through the
// one handler every route to a single block takes (copyblock.go), so the
// pointer cannot copy different text or fail in different words than the
// key. And a drag that crosses a heading leaves it out: the heading names
// the block and is no part of it, and a paste that opened with `go` would
// not compile.
//
// The rows are found by walking the units the way the render emits them
// (unitAtLine), because the render is strings and keeps no record of which
// row is which; the renderer's own report of where each block landed
// (markdown.Layout) is then read against the one entry the walk found.

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/ui/markdown"
)

// blockHeading is a heading row: the entry whose render drew it, and the
// block's number in that entry's source, counted from 1 the way copyBlock
// counts. idx is -1 for the answer still arriving, which is no entry yet.
type blockHeading struct {
	idx, n int
}

// entryFences is where an entry's fenced blocks landed, relative to the
// entry's first line, or nil for an entry the markdown renderer does not
// draw. A reply and a sent message are both rendered as markdown; the rows
// a step header or a checkpoint draws for an assistant entry are not.
func (m Model) entryFences(e entry, width int) []markdown.Fence {
	switch {
	case e.kind == entryAssistant && !e.checkpoint:
		_, fences := markdown.Layout(e.text, mdOptions(width))
		return fences
	case e.kind == entryUser:
		o := mdOptions(width)
		o.Prose = markdown.ProseBright
		_, fences := markdown.Layout(e.text, o)
		return fences
	}
	return nil
}

// blockHeadings reports every heading row the transcript draws between
// lines from and to, inclusive, keyed by line. Only the entries the range
// touches are laid out again; the walk itself is the one unitAtLine makes.
func (m Model) blockHeadings(from, to int) map[int]blockHeading {
	heads := map[int]blockHeading{}
	es := *m.entries()
	focus := m.gutterShowing()
	width := m.transcriptWidth()
	at := 0
	var prev entry
	havePrev := false
	for _, u := range m.transcriptUnits(es, width, focus, m.focusIdx) {
		if havePrev {
			at += strings.Count(separatorBefore(prev, u.sepBefore), "\n")
		}
		n := strings.Count(u.text, "\n")
		if at <= to && at+n > from && u.idx >= 0 && u.idx < len(es) {
			for _, f := range m.entryFences(es[u.idx], m.unitWidth(es[u.idx], width, focus)) {
				// A step's header is a unit of its title entry, and draws
				// nothing the layout of that entry's text describes. A title
				// is one line, so it never holds a fence, but the guard is
				// what keeps a header row from being read as a heading.
				if f.Heading < n {
					heads[at+f.Heading] = blockHeading{idx: u.idx, n: f.Index + 1}
				}
			}
		}
		at += n
		prev, havePrev = u.sepAfter, true
	}
	// The answer still arriving follows the units in the plain feed, and
	// its glued render is the whole message's render byte for byte
	// (streammd.go), so the whole message's layout is where its blocks are.
	if !focus && m.answerIsArriving() {
		if havePrev {
			at += strings.Count(separatorBefore(prev, entry{kind: entryAssistant}), "\n")
		}
		_, fences := markdown.Layout(m.streaming, mdOptions(width))
		for _, f := range fences {
			if line := at + f.Heading; line >= from && line <= to {
				heads[line] = blockHeading{idx: -1, n: f.Index + 1}
			}
		}
	}
	for line := range heads {
		if line < from || line > to {
			delete(heads, line)
		}
	}
	return heads
}

// clickBlockHeading answers a click on a reply's heading row by copying the
// block it heads — reading mode's [c] reached from the pointer, which is
// what makes the row a target: it names exactly one block, and that block
// already has a key (click.go).
//
// Only a reply's heading is one. A sent message's blocks are headed the
// same, since the renderer draws a block one way, but no key copies one of
// them, and a target only the pointer reaches is half the readers'; the
// answer still arriving is no row yet. A click anywhere else in a block is
// not a target either: the code is a selection surface, and a click on it
// is reading, not an act.
//
// It never takes the keyboard. From the draft the copy is confirmed as a
// notice and the draft keeps every character; in reading mode the cursor
// goes to the reply first, as clickRow puts it on the row it opens, so the
// caption and the bar's offer are about the row the reader pointed at.
func (m Model) clickBlockHeading(line int) (tea.Model, tea.Cmd, bool) {
	idx, offset, ok := m.unitAtLine(line)
	es := *m.entries()
	if !ok || idx < 0 || idx >= len(es) {
		return m, nil, false
	}
	e := es[idx]
	if e.kind != entryAssistant || e.checkpoint {
		return m, nil, false
	}
	n := 0
	for _, f := range m.entryFences(e, m.unitWidth(e, m.transcriptWidth(), m.gutterShowing())) {
		if f.Heading == offset {
			n = f.Index + 1
		}
	}
	if n == 0 {
		return m, nil, false
	}
	if m.state == stateFocus {
		m.focusIdx = idx
		m.refreshFocusView()
	}
	next, cmd := m.copyBlock(e.text, n)
	return next, cmd, true
}
