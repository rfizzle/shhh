package chat

// Reading mode's [y] (docs/interface/surfaces.md#reading-mode): one key that
// copies the focused row's content, shaped by what the row is. Getting a
// command's output onto the clipboard used to require mouse reporting and a
// drag over however many screens the output ran; the cursor already stands
// on the row, and the row already knows what it holds.
//
// What is copied is the row's *content*, never its rendering: an assistant
// message as its markdown source, a command as `$ cmd` over its output, an
// edit as the unified diff, a read as what the read returned, a folded group
// as each member in order. ANSI is stripped the way the drag-selection strip
// does it — what a program painted is not part of what it said.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
)

// copyFocusedRow answers [y]: the focused row's content goes to the
// clipboard through the same path /copy and the drag selection use, and the
// reading rail captions what was caught. A row with nothing to copy hands
// the letter back to the draft, the way [-] does with nothing open.
func (m Model) copyFocusedRow(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	es := *m.entries()
	text, what := m.rowCopyText(es, m.focusIdx)
	if text == "" {
		return m.returnToInput(msg)
	}
	// The failures land in the transcript, where /copy's and the drag
	// selection's already go: a missing clipboard tool is a fact about the
	// machine the reader has to act on, not a caption.
	if m.copyFn == nil {
		return m.focusNotice("Copying is not available in this session.")
	}
	if res := m.copyFn(text); res.Warning != "" {
		return m.focusNotice(res.Warning)
	}
	n := strings.Count(text, "\n") + 1
	noun := "lines"
	if n == 1 {
		noun = "line"
	}
	m.readingCopied = fmt.Sprintf("✂ copied %s · %d %s", what, n, noun)
	return m, nil
}

// focusNotice is systemNotice with reading mode's render: the row lands in
// the transcript being read — an attached child's feed included, the way
// enterFocusMode's own notice does — and the cursor gutter stays on screen.
func (m Model) focusNotice(text string) (tea.Model, tea.Cmd) {
	if m.attachedTo != "" {
		m.noteChild(m.attachedTo, text)
	} else {
		m.appendEntry(entry{kind: entrySystem, text: text})
	}
	m.refreshFocusView()
	return m, nil
}

// rowCopyText is the focused row's content and the word the caption names it
// by. Empty means the row holds nothing a clipboard could carry.
func (m Model) rowCopyText(es []entry, idx int) (text, what string) {
	if idx < 0 || idx >= len(es) {
		return "", ""
	}
	if _, ok := m.stepBlockAt(es, idx); ok {
		// A step header is chrome about rows, not content of its own.
		return "", ""
	}
	if span := m.foldedGroupSpan(es, idx); span > 1 {
		// A folded group copies each member in order — the fold hides the
		// rows, never what they returned
		// (docs/interface/principles.md#fold-never-hide).
		var parts []string
		for _, member := range es[idx : idx+span] {
			if lines := outputLines(member); len(lines) > 0 {
				parts = append(parts, ansi.Strip(strings.Join(lines, "\n")))
			}
		}
		return strings.Join(parts, "\n"), fmt.Sprintf("%d rows", span)
	}
	e := es[idx]
	switch e.kind {
	case entryAssistant:
		// The markdown source, not the rendered form: what the model said is
		// the content, the glamour layout is this terminal's.
		return e.text, "response"
	case entryThink:
		return e.text, "thinking"
	case entryCommand:
		out := ansi.Strip(strings.TrimRight(e.toolResult, "\n"))
		if out == "" {
			return "$ " + e.text, "command"
		}
		return "$ " + e.text + "\n" + out, "command"
	case entryDiff:
		if e.diff == nil || len(e.diff.Hunks) == 0 {
			return "", ""
		}
		return plainUnified(e.diff.Hunks), "diff"
	case entryTool:
		lines := outputLines(e)
		if len(lines) == 0 {
			return "", ""
		}
		return ansi.Strip(strings.Join(lines, "\n")), activityVerb(e.toolName) + " result"
	}
	return "", ""
}

// focusedCopyable reports whether [y] has anything to act on, without
// building the text: it is what the hint bar offers the key by, so the bar
// and the dispatch read the same facts.
func (m Model) focusedCopyable() bool {
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return false
	}
	if _, ok := m.stepBlockAt(es, m.focusIdx); ok {
		return false
	}
	if m.foldedGroupSpan(es, m.focusIdx) > 1 {
		return true
	}
	e := es[m.focusIdx]
	switch e.kind {
	case entryAssistant, entryThink:
		return strings.TrimSpace(e.text) != ""
	case entryCommand:
		return true
	case entryDiff:
		return e.diff != nil && len(e.diff.Hunks) > 0
	case entryTool:
		return len(outputLines(e)) > 0
	}
	return false
}

// foldedGroupSpan is how many rows the folded group at idx swallows, or 0
// where idx does not head a folded group right now. An open run's anchor is
// an ordinary row — its members are on screen with cursors of their own.
func (m Model) foldedGroupSpan(es []entry, idx int) int {
	for _, blk := range m.blocksOf(es) {
		if blk.step == nil || blk.step.queued() {
			continue
		}
		for _, s := range m.stepSlots(es, blk.step) {
			if s.idx == idx && s.group {
				return s.span
			}
		}
	}
	return 0
}

// plainUnified is hunks as clipboard text: the unified diff with no colour
// and no line-number gutter, which is the form every tool that eats a diff
// expects.
func plainUnified(hunks []diff.Hunk) string {
	var b strings.Builder
	for _, h := range hunks {
		b.WriteString(h.Header())
		b.WriteByte('\n')
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.Add:
				b.WriteByte('+')
			case diff.Del:
				b.WriteByte('-')
			default:
				b.WriteByte(' ')
			}
			b.WriteString(l.Text)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
