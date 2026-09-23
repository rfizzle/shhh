package chat

// Public progress: the status a long, otherwise silent run is asked for at a
// round boundary, and what the transcript does with the answer
// (docs/interface/surfaces.md#the-progress-checkpoint).
//
// The scheduling is the agent's — when a note is owed, and the words the
// request is made in. What is here is the surface's half: the boundary a
// request may not cross, the record that one was answered, and the rung and
// the bound the answer is drawn at.

import (
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// injectProgressCheckpoint asks for public status only between complete tool
// rounds. An overlay belongs to the reader, so it defers the request rather
// than sending an unseen update behind a card or supporting screen.
func (m *Model) injectProgressCheckpoint() {
	if m.state.isSurface() {
		return
	}
	prompt, ok := m.agent.TakeProgressCheckpoint()
	if !ok {
		return
	}
	m.agent.AppendProgressRequest(prompt)
}

// noteProgressProse records the assistant prose that a tool round led with,
// and reports whether it was the public status the session had asked for. A
// checkpoint response is ordinary assistant text and stays one — its existing
// one-line step title therefore remains the title for the calls following it,
// and nothing about the round protocol changes — while the record keeps only
// that public status happened, never its content.
func (m *Model) noteProgressProse(text string) bool {
	answered := m.agent.NoteProgressProse(text)
	if answered {
		m.signal(observe.SignalProgress, observe.ProgressCheckpoint)
	}
	return answered
}

// markCheckpoint stamps prose that answered a public-status request, and
// retires the checkpoint before it to its first line: the status a run is on
// is the last note it wrote, and the notes before it are how it got there
// (docs/interface/surfaces.md#the-progress-checkpoint).
//
// The retiring happens here, once, rather than being asked at every render.
// Which checkpoint is the current one is a fact about the transcript and not
// about any one entry in it, and a render that had to look forward to answer
// it could not be cached the way every other entry's is (render.go).
func (m *Model) markCheckpoint(e entry) entry {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if !m.transcript[i].checkpoint {
			continue
		}
		if !m.transcript[i].checkpointReplaced {
			m.transcript[i].checkpointReplaced = true
			// That entry renders shorter now, and it may belong to a block
			// both caches have frozen (render.go, focus.go).
			m.invalidateRenderCache()
		}
		break
	}
	e.checkpoint = true
	return e
}

// checkpointBound is how much of the current public status is on screen
// before the rest folds. Three lines, because three is what the request asks
// for — a sentence each for the objective, the evidence and the next action —
// so a note that keeps to its own brief is drawn whole and one that ran on is
// drawn to the length of one that did not.
//
// A note a later checkpoint has replaced keeps one line. It is not history to
// be thrown away — a reader scrolling back is reading exactly this, how the
// run reached where it is — but what it owes them is the sentence saying
// which objective was in hand, not a paragraph restating an objective the
// next note has already restated.
const (
	checkpointBound         = 3
	checkpointReplacedBound = 1
)

// checkpointBlock draws a public status: the model's own words at the rung a
// body under a row is drawn at, on the content column every other entry
// starts on (docs/interface/surfaces.md#the-leading-columns), bounded and
// counted (docs/interface/principles.md#fold-never-hide).
//
// It is not the markdown render an answer gets. A checkpoint is a few
// sentences of prose by construction, and passing it through the document
// renderer would let a note that opened with a heading draw one — at the
// weight a heading is drawn at, which is the one thing this block may not do.
//
// The words are still full weight while they arrive: the streaming render is
// the model's live output and belongs to the moment it is being written in.
// It settles into this block at the same round boundary that puts the calls
// the note was reporting on underneath it (streammd.go).
func (m Model) checkpointBlock(e entry, width int) string {
	body := m.checkpointLines(e.text, width)
	if len(body) == 0 {
		return ""
	}
	shown := body
	if !e.expanded {
		bound := checkpointBound
		if e.checkpointReplaced {
			bound = checkpointReplacedBound
		}
		shown = body[:min(bound, len(body))]
	}
	inner := max(width-components.GridPointerWidth, 1)
	lines := make([]string, 0, len(shown)+1)
	for _, l := range shown {
		lines = append(lines, marginLine(sty.Checkpoint, l, inner, width))
	}
	if more := len(body) - len(shown); more > 0 {
		// The foot every bounded body in the transcript carries, in the
		// columns a detail body starts in: reading mode's [enter] on the row
		// is how the rest is reached, the way it is on a tool result.
		lines = append(lines, components.Clip(strings.Repeat(" ", components.GridDetailIndent)+
			sty.SystemMsg.Render("… "+plural(more, "more line")), width))
	}
	return strings.Join(lines, "\n")
}

// checkpointLines is the status wrapped to the column it is drawn on, which
// is what the bound counts: the number on the fold is the number of lines
// opening it costs, at this width and no other.
func (m Model) checkpointLines(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	inner := max(width-components.GridPointerWidth, 1)
	return strings.Split(strings.TrimRight(m.wordWrap(text, inner), "\n"), "\n")
}

// WithProgressIntervals sets the two public-status clocks for this session.
func (m Model) WithProgressIntervals(calls int, elapsed time.Duration) Model {
	m.agent.SetProgressIntervals(calls, elapsed)
	return m
}
