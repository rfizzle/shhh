package chat

// One code block copied by itself (docs/interface/surfaces.md#reading-mode).
// A reply can carry a handful of fenced blocks and the reader wants one of
// them: /copy code takes the last reply's by number or from a numbered card,
// and reading mode's [c] takes them from whichever reply the cursor is on.
//
// What reaches the clipboard is the block as the message wrote it — its body
// from the markdown source, never the rows the renderer drew: no indent, no
// fold, tabs kept as tabs, and no fence lines or language tag. The copy
// rides the shared clipboard path (copyText) and fails the way /copy, [y]
// and the drag do (copyFailure).

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// copyBlock copies block n, counted from 1, of an assistant message's
// markdown source, and says so where the reader is: in reading mode as the
// rail's caption, the way [y] says it, and from the draft as a notice, which
// has no rail to caption on. A number the message has no block for answers
// with the range, the way /run does.
//
// It is the one copy every route to a single block takes — the number, the
// card, the reading key, and a pointer on the block itself — so the words a
// copy is confirmed in and the words it fails in cannot drift apart.
func (m Model) copyBlock(src string, n int) (tea.Model, tea.Cmd) {
	blocks := extractCodeBlockInfo(src)
	if n < 1 || n > len(blocks) {
		return m.copyBlockNotice(fmt.Sprintf("usage: /copy code [1-%d]", len(blocks)))
	}
	b := blocks[n-1]
	res, write := m.copyText(b.body)
	if note := copyFailure(res); note != "" {
		return m.copyBlockNotice(note)
	}
	what := fmt.Sprintf("copied block %d · %s · %s", n, blockWord(b.lang), plural(blockLines(b.body), "line"))
	if m.state == stateFocus {
		// The caption stands as soon as the write leaves, as [y]'s does:
		// a write to the terminal has no reply to wait for.
		m.readingCopied = "✓ " + what
		return m, write
	}
	next, cmd := m.systemNotice(what)
	return next, tea.Batch(cmd, write)
}

// copyBlockNotice lands a line in the transcript the reader is looking at:
// the one being read, cursor gutter kept, or the session's from the draft.
func (m Model) copyBlockNotice(text string) (tea.Model, tea.Cmd) {
	if m.state == stateFocus {
		return m.focusNotice(text)
	}
	return m.systemNotice(text)
}

// copyCode is /copy code over the last reply's source. A number copies that
// block; `all` joins every block, which is what bare /copy code did before a
// block could be taken by itself, so nothing that worked is taken away; and
// bare, one block is copied as it always was while several open the card.
func (m Model) copyCode(src string, args []string) (tea.Model, tea.Cmd) {
	blocks := extractCodeBlockInfo(src)
	if len(blocks) == 0 {
		return m.systemNotice("no code blocks in the last response")
	}
	switch {
	case len(args) > 0 && args[0] == "all":
		bodies := make([]string, len(blocks))
		for i, b := range blocks {
			bodies[i] = b.body
		}
		return m.copyWhole(strings.Join(bodies, "\n"), "code")
	case len(args) > 0:
		// Anything but a number in range is answered with the range; zero
		// is out of it, so a word that is not one falls there too.
		n, _ := strconv.Atoi(args[0])
		return m.copyBlock(src, n)
	case len(blocks) == 1:
		return m.copyWhole(blocks[0].body, "code")
	}
	return m.openCopyPick(src, blocks)
}

// copyWhole is /copy's own confirmation, for what it copied before a block
// could be taken by number: the whole reply, or every block joined.
func (m Model) copyWhole(text, what string) (tea.Model, tea.Cmd) {
	res, write := m.copyText(text)
	if note := copyFailure(res); note != "" {
		return m.systemNotice(note)
	}
	next, cmd := m.systemNotice("copied last " + what + " to clipboard")
	return next, tea.Batch(cmd, write)
}

// openCopyPick opens the numbered card over a message's blocks — the card
// /run opens, asking a different question of the same rows. Taking a row
// copies that block; esc keeps nothing, and the clipboard is left as it was.
func (m Model) openCopyPick(src string, blocks []codeBlock) (tea.Model, tea.Cmd) {
	return m.openBlockPick("copy which block?", "keep nothing", blocks, func(m *Model, idx int) (string, tea.Cmd) {
		next, cmd := m.copyBlock(src, idx+1)
		*m = next.(Model)
		return "", cmd
	})
}

// focusedBlocks is the reply under reading mode's cursor and the fenced
// blocks in it: none on any other row, and none on a card. It is the one fact both the bar's offer of [c] and the key itself
// read, so the bar never offers the key on a row it would hand back from.
func (m Model) focusedBlocks() (string, []codeBlock) {
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return "", nil
	}
	if _, ok := m.cardTakesKey(es, m.focusIdx); ok {
		return "", nil
	}
	e := es[m.focusIdx]
	if e.kind != entryAssistant {
		return "", nil
	}
	return e.text, extractCodeBlockInfo(e.text)
}

// copyFocusedBlock answers [c]: the reply's one block is copied at once,
// and several open the card over that reply's blocks — not the last
// reply's, which is /copy code's. A row with no block hands the letter back
// to the draft, the way [y] does on a row with nothing to copy.
func (m Model) copyFocusedBlock(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	src, blocks := m.focusedBlocks()
	switch len(blocks) {
	case 0:
		return m.returnToInput(msg)
	case 1:
		return m.copyBlock(src, 1)
	}
	return m.openCopyPick(src, blocks)
}
