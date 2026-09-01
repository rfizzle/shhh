package chat

// The think row (docs/interface/surfaces.md#the-think-row): the reasoning a
// round did, folded into one activity row with the rest of the round's acts.
//
// Thinking arrives on its own channel (internal/provider) because it is not
// the answer — a provider that streamed it as a token would print the model's
// private murmur as its reply. Until now the session carried the blocks for
// the next request and showed nothing, so a turn that spent thirty seconds
// reasoning was a spinner and a promise. "See it before it happens" is the
// product's first claim, and what the model thought is the earliest thing
// there is to see.
//
// It is a row rather than a block for the reason everything else is: one
// grid, and the row states what it swallowed — `✻ think   42 lines` — so the
// fold hides the words without hiding that there were words
// (docs/interface/principles.md#fold-never-hide).
//
// The text is the model's own and never travels back from here: what the next
// request replays is the signed block the provider handed over, held by the
// agent. This is only what the screen can show of it, which is why a redacted
// block — thinking the provider took back — contributes no lines and a round
// that produced nothing but redactions gets no row.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// thinkDepth is how much of a think row's body is on screen. It is the diff's
// three depths applied to prose (docs/interface/surfaces.md#the-diff-view):
// the middle one is the common case, because the last thing the model thought
// is usually the part worth reading and a long block would otherwise cost the
// whole screen to glance at.
//
// Like a step's fold it is an *override*, and for the same reason: the
// verbosity has an answer of its own, and a reader who says otherwise has to
// outrank it rather than fight it. Without the auto state, [enter] at high
// verbosity would cycle a row it could never close.
type thinkDepth int

const (
	// thinkAuto is the verbosity's answer: folded, or the tail window where
	// every row is opened to its bounded body.
	thinkAuto thinkDepth = iota
	// thinkClosed is the row alone, counting the lines it swallowed.
	thinkClosed
	// thinkTail is the last maxToolResultLines of the block.
	thinkTail
	// thinkFull is every line of it.
	thinkFull
)

// thinkVerb is the row's verb, and it is closed like every other
// (docs/interface/principles.md#closed-vocabularies).
const thinkVerb = "think"

// thinkLines is the block as the row would show it: wrapped to the detail
// body's width, which is the pane less the indent every detail body carries.
//
// Wrapping is not decoration here. A detail body clips what does not fit,
// which is right for the output of a program — a log line's information is at
// its head — and wrong for prose, and reasoning arrives as prose: one
// paragraph is one physical line, hundreds of characters long. Clipped, an
// opened row would show a sentence and an ellipsis where the fold promised
// forty lines, which is the fold hiding what it said it had
// (docs/interface/principles.md#fold-never-hide).
//
// It is also what the count counts, so the number on a closed row is the
// number of lines opening it costs.
func (m Model) thinkLines(text string, width int) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(m.wordWrap(text, max(width-components.GridDetailIndent, 1)), "\n")
}

// lineCounts is a folded prose row's outcome field: what the fold swallowed.
// Lines rather than tokens, because a line is a thing the reader can count
// back once the row is open and a token is not. The think row and the summary
// row share it, so the two folds over prose count in the same units.
func lineCounts(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

// reasoningText is the readable half of a round's reasoning blocks. A
// redacted block has none — the provider kept the words and handed back an
// opaque payload — so it contributes nothing rather than an empty line.
func reasoningText(blocks []provider.ReasoningBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// showThink reports whether think rows are drawn at all. Low verbosity is
// "step headers only" and this row is the first thing that means: it is the
// one row that reports no act at all, so it is the first to go.
func (m Model) showThink() bool { return m.verbosity != verbosityLow }

// thinkDepthOf is the depth a row renders at: the reader's own where they
// have given one, and the verbosity's otherwise — high opens every row to its
// bounded body, and this row's bounded body is the tail window.
func (m Model) thinkDepthOf(e entry) thinkDepth {
	if e.thinkDepth != thinkAuto {
		return e.thinkDepth
	}
	if m.verbosity == verbosityHigh {
		return thinkTail
	}
	return thinkClosed
}

// thinkOpened is the depth a closed row opens to: the tail window, or the
// whole block when it already fits under the cap. A tail step that showed the
// same lines the next press would show is a press that changes nothing.
func (m Model) thinkOpened(text string, width int) thinkDepth {
	if len(m.thinkLines(text, width)) <= maxToolResultLines {
		return thinkFull
	}
	return thinkTail
}

// thinkRowFor builds the row for a reasoning entry at the width it will be
// drawn at. It streams: the count grows as the block arrives, and the row
// spins while it does, off the session's one frame like every other moving
// row (spin.go).
func (m Model) thinkRowFor(e entry, width int) components.ActivityRow {
	lines := m.thinkLines(e.text, width)
	row := components.ActivityRow{
		Kind:   components.ActivityThink,
		Verb:   thinkVerb,
		Counts: lineCounts(len(lines)),
		Frame:  m.spinFrame,
	}
	if e.thinkStreaming {
		row.State = components.ActivityRunning
		row.Outcome = components.OutcomeRunning
		// A row left mid-thought by a cancelled turn keeps the still `▸`
		// rather than standing on one braille frame, which reads as a hang.
		row.Spin = m.spinnerWanted()
	}
	switch depth := m.thinkDepthOf(e); depth {
	case thinkTail, thinkFull:
		row.Expanded = true
		if depth == thinkTail {
			// The tail window is sliced here rather than handed to MaxDetail,
			// which bounds a body from the top: the interesting end of a
			// thought is the end it stopped at.
			lines = lines[max(len(lines)-maxToolResultLines, 0):]
		}
		row.Detail = lines
	default:
		// A closed row says how it opens. The folded group row and the
		// collapsed diff row say it the same way, and a fold nobody knows is
		// a fold is a row that looks like it had nothing to show.
		if len(lines) > 0 {
			row.Keys = components.GroupExpandKey
		}
	}
	return row
}

// appendThinking adds arriving reasoning text to the round's think row,
// starting one if the round has none yet. The row is the round's, not the
// block's: a provider that thinks in three blocks around two tool calls still
// produces one row per round, because a round is what the reader is watching.
func (m *Model) appendThinking(text string) {
	if text == "" {
		return
	}
	if m.compacting {
		// A compaction is housekeeping, not a turn: its summary replaces the
		// transcript on success and is discarded on cancel, so a row about
		// how it was written would either vanish or outlive what it
		// described (context.go).
		return
	}
	if m.thinkIdx > 0 {
		m.transcript[m.thinkIdx-1].text += text
		return
	}
	m.appendEntry(entry{kind: entryThink, text: text, thinkStreaming: true})
	m.thinkIdx = len(m.transcript)
}

// settleThink stops the round's row spinning: the thinking is over, whether
// because the answer has started, the round ended, or the turn was cancelled.
func (m *Model) settleThink() {
	if m.thinkIdx > 0 && m.thinkIdx <= len(m.transcript) {
		m.transcript[m.thinkIdx-1].thinkStreaming = false
	}
}

// recordReasoning is the same row from the terminal event, for a provider
// that delivers its thinking whole at the end of the round rather than as it
// is written. A round that already has a row keeps it — the streamed text and
// the finished blocks are the same words, and appending both would say
// everything twice.
func (m *Model) recordReasoning(blocks []provider.ReasoningBlock) {
	if m.thinkIdx == 0 {
		m.appendThinking(reasoningText(blocks))
	}
	// Nothing more is coming either way: the event that carried these blocks
	// is the one that ended the round.
	m.settleThink()
}

// cycleThink is [enter] on a think row: closed → tail → full → closed,
// starting from the depth on screen rather than the one recorded, so the
// first press always changes something.
func (m *Model) cycleThink(idx int) {
	es := *m.entries()
	if idx < 0 || idx >= len(es) {
		return
	}
	switch m.thinkDepthOf(es[idx]) {
	case thinkTail:
		es[idx].thinkDepth = thinkFull
	case thinkFull:
		es[idx].thinkDepth = thinkClosed
	default:
		es[idx].thinkDepth = m.thinkOpened(es[idx].text, m.transcriptWidth())
	}
}
