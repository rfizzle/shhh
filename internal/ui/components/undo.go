package components

// The undo confirm (S-100, DESIGN-TUI.md §5 and §16). Taking a turn back
// writes to the workspace, so it asks first — as an inline confirm in the
// input area rather than a card, because the question is one line and the
// answer is a keystroke.
//
// The confirm is where drift is put to the user. A file that changed since
// the turn holds something the record never saw, so the default answer
// leaves it alone and says so; [f] is the deliberate second answer that
// takes it back anyway. As everywhere else, the default is the safe one and
// esc declines.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// undoDriftNames bounds how many drifted paths the confirm lists by name;
// past it the rest are counted. The prompt has to fit in the input area.
const undoDriftNames = 3

// UndoDecision is the answer to the undo confirm.
type UndoDecision int

const (
	// UndoCancel: nothing is written. n, enter and esc all mean this.
	UndoCancel UndoDecision = iota
	// UndoApply: take the turn back, leaving drifted files alone.
	UndoApply
	// UndoForce: take the turn back including the drifted files, discarding
	// what changed since.
	UndoForce
)

// UndoConfirm is the prompt an undo asks through. Restores and Removes are
// what [y] would do — the drifted files are not in them, because [y] does
// not touch a drifted file.
type UndoConfirm struct {
	Turn int64
	// Restores is how many files [y] writes back; Removes how many it
	// deletes, which are the files the turn created.
	Restores, Removes int
	// Drifted names the files that changed since the turn, in plan order.
	Drifted []string
}

// touches is what [y] would act on at all.
func (c UndoConfirm) touches() int { return c.Restores + c.Removes }

// Update resolves on the first decisive key: y undoes, f forces through
// drift, and n, enter, esc and ctrl+c all decline. With nothing for [y] to
// do — every file drifted — y is not bound, so the only ways out are the
// deliberate [f] and declining.
func (c *UndoConfirm) Update(msg tea.KeyMsg) (done bool, result any) {
	switch msg.String() {
	case "y", "Y":
		if c.touches() == 0 {
			return false, nil
		}
		return true, UndoApply
	case "f", "F":
		if len(c.Drifted) == 0 {
			return false, nil
		}
		return true, UndoForce
	case "n", "N", "enter", "esc", "ctrl+c":
		return true, UndoCancel
	}
	return false, nil
}

// effect is what [y] would do, in words: a count per kind, and nothing said
// about a kind with no files in it.
func (c UndoConfirm) effect() string {
	var parts []string
	if c.Restores > 0 {
		parts = append(parts, "restores "+plural(c.Restores, "file"))
	}
	if c.Removes > 0 {
		parts = append(parts, "deletes "+plural(c.Removes, "file")+" it created")
	}
	if len(parts) == 0 {
		return "Nothing is left to restore."
	}
	return "It " + strings.Join(parts, ", ") + "."
}

// driftRows are the drift lines: the count and what the default answer does
// with it, then the paths themselves. The state is a glyph and a sentence,
// never a colour (invariant 1).
func (c UndoConfirm) driftRows(width int) []string {
	if len(c.Drifted) == 0 {
		return nil
	}
	head := fmt.Sprintf("⚠ %s changed since the turn", plural(len(c.Drifted), "file"))
	if c.touches() > 0 {
		head += " — left alone"
	}
	rows := []string{clip(warnStyle.Render(head), width)}
	named := c.Drifted
	if len(named) > undoDriftNames {
		named = named[:undoDriftNames]
	}
	for _, p := range named {
		rows = append(rows, clip("    "+dimStyle.Render(p), width))
	}
	if rest := len(c.Drifted) - len(named); rest > 0 {
		rows = append(rows, clip("    "+dimStyle.Render(fmt.Sprintf("and %d more", rest)), width))
	}
	return rows
}

// headRows are the question and the answers. The answers are the one thing
// that may never be clipped away, so on a terminal too narrow to hold both
// the statement moves to a row of its own rather than pushing them off the
// end.
func (c UndoConfirm) headRows(width int) []string {
	question := fmt.Sprintf("Undo turn %d?", c.Turn)
	keys := headlineStyle.Render(c.defaultKeys())
	if full := bodyStyle.Render(question+" "+c.effect()) + "  " + keys; lipgloss.Width(full) <= width {
		return []string{full}
	}
	return []string{
		clip(bodyStyle.Render(question)+"  "+keys, width),
		clip(dimStyle.Render(c.effect()), width),
	}
}

// View renders the confirm: the question and its default first, the drift
// underneath it, and the keys last.
func (c UndoConfirm) View(width int) string {
	rows := c.headRows(width)
	rows = append(rows, c.driftRows(width)...)
	if len(c.Drifted) > 0 {
		force := fmt.Sprintf("[f] force — take back %s too, discarding what changed",
			plural(len(c.Drifted), "drifted file"))
		rows = append(rows, hintRows([]string{force, "[esc] cancel"}, width)...)
	}
	return strings.Join(rows, "\n")
}

// defaultKeys is the answer set on the head line. Without anything for [y]
// to do it is not offered, so the prompt never shows a key that does nothing.
func (c UndoConfirm) defaultKeys() string {
	if c.touches() == 0 {
		return "[f/N]"
	}
	return "[y/N]"
}
