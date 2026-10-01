package chat

// Thinking (docs/interface/surfaces.md#the-think-row): the reasoning a round
// did, drawn as the model's own prose on bare screen — dimmer than the
// answer and slanted, at the body column a card's sentence starts on, with
// no glyph, no band, no count and no fold.
//
// Thinking arrives on its own channel (internal/provider) because it is not
// the answer — a provider that streamed it as a token would print the model's
// private murmur as its reply. "See it before it happens" is the product's
// first claim, and what the model thought is the earliest thing there is to
// see.
//
// It was a row once, `✻ think   42 lines`, folded through three depths. It is
// prose now because every other thing the model writes is prose and a step
// is the one thing drawn as a card: the fold put a key between the reader and
// the one passage that says why the next card exists, and what bounds a long
// thought now is the density rung rather than a fold
// (docs/interface/departures.md#thinking-is-prose-and-the-rung-bounds-it).
//
// The text is the model's own and never travels back from here: what the next
// request replays is the signed block the provider handed over, held by the
// agent. This is only what the screen can show of it, which is why a redacted
// block — thinking the provider took back — contributes no lines and a round
// that produced nothing but redactions gets no passage.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// lineCounts is a folded prose row's outcome field: what the fold swallowed.
// Lines rather than tokens, because a line is a thing the reader can count
// back once the row is open and a token is not.
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

// showThink reports whether thinking is drawn at all, and it is the one
// place the density rung decides that: a thought reports no act, so it is
// the first thing the least dense rung drops
// (docs/interface/principles.md#density-is-one-ladder). An entry that
// renders to nothing is not a unit, so nothing downstream — spacing, line
// mapping, the reading cursor — has to know it was skipped.
func (m Model) showThink() bool { return m.density(verbosityNormal) }

// thinkBlock draws a round's reasoning: every line of it, wrapped at the body
// column, in the treatment model output nobody asked a question for is drawn
// in. It wraps rather than clips, because a thought is prose, where a
// paragraph is one line hundreds of characters long and cutting it keeps a
// sentence and loses the thought.
func (m Model) thinkBlock(text string, width int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	indent := strings.Repeat(" ", components.GridDetailIndent)
	inner := max(width-components.GridDetailIndent, 1)
	lines := strings.Split(m.wordWrap(text, inner), "\n")
	for i, l := range lines {
		lines[i] = components.Clip(indent+sty.Thinking.Render(components.Clip(l, inner)), width)
	}
	return strings.Join(lines, "\n")
}

// appendThinking adds arriving reasoning text to the round's thought,
// starting one if the round has none yet. The entry is the round's, not the
// block's: a provider that thinks in three blocks around two tool calls still
// produces one passage per round, because a round is what the reader is
// watching.
func (m *Model) appendThinking(text string) {
	if text == "" {
		return
	}
	if m.compacting {
		// A compaction is housekeeping, not a turn: its summary replaces the
		// transcript on success and is discarded on cancel, so a passage
		// about how it was written would either vanish or outlive what it
		// described (context.go).
		return
	}
	if m.thinkIdx > 0 {
		m.transcript[m.thinkIdx-1].text += text
		return
	}
	m.appendEntry(entry{kind: entryThink, text: text})
	m.thinkIdx = len(m.transcript)
}

// recordReasoning is the same passage from the terminal event, for a
// provider that delivers its thinking whole at the end of the round rather
// than as it is written. A round that already has one keeps it — the
// streamed text and the finished blocks are the same words, and appending
// both would say everything twice.
func (m *Model) recordReasoning(blocks []provider.ReasoningBlock) {
	if m.thinkIdx == 0 {
		m.appendThinking(reasoningText(blocks))
	}
}
