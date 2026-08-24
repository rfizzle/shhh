package components

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/diff"
)

// ApprovalVariant selects which body the approval card renders
// (DESIGN-TUI.md §2): a command, a file edit diff, or a generic tool summary.
type ApprovalVariant int

const (
	ApprovalCommand ApprovalVariant = iota
	ApprovalEdit
	ApprovalGeneric
)

// ApprovalDecision is the card's Update result once a decision key is
// pressed.
type ApprovalDecision int

const (
	// ApprovalApprove runs the pending action (y / enter).
	ApprovalApprove ApprovalDecision = iota
	// ApprovalDeny declines it (n / esc / ctrl+c) — esc never destroys.
	ApprovalDeny
	// ApprovalAlways approves and auto-allows the category for the session
	// (a, only when AllowAlways is set).
	ApprovalAlways
	// ApprovalFullDiff opens the full-screen diff view (d, only when
	// FullDiff is set); the host returns to the card afterwards (S-074).
	ApprovalFullDiff
)

// ApprovalCard is the single surface for every approval-gated action. One
// container, three body variants.
type ApprovalCard struct {
	Variant ApprovalVariant
	// Title is the border title, e.g. "Approve command"; QueuePos ("2 of 5")
	// is appended when set.
	Title    string
	QueuePos string
	// Headline is the first body row, e.g. "Assistant wants to run: go test".
	Headline string
	// Warnings are safety.Check risks, rendered as ⚠ rows; when present the
	// caller must not set AllowAlways (flagged actions are never
	// blanket-approved).
	Warnings []string
	// Containment describes the process-containment state, rendered as a ⛨
	// row when set.
	Containment string
	// Hunks is the edit variant's diff body; Syntax highlights its lines
	// (S-074).
	Hunks  []diff.Hunk
	Syntax Syntax
	// FullDiff offers [d] to open the diff full screen (S-074).
	FullDiff bool
	// Summary is the generic variant's one-line description.
	Summary string
	// Question is the decision prompt, e.g. "Run this command?".
	Question string
	// AllowAlways offers [a] with AlwaysHint describing the session grant.
	AllowAlways bool
	AlwaysHint  string
	// ExtraHints are additional key hints the host handles itself (e.g.
	// "g: attach to writer-1" on a routed child approval, S-077).
	ExtraHints []string
	// MaxLines bounds the card's total height, frame included; the diff body
	// shrinks to fit. 0 means unbounded.
	MaxLines int
}

// Update maps decision keys, preserving the chat confirm prompt's y/n/esc
// semantics. Unrecognized keys — including [a] when AllowAlways is off —
// leave the card waiting.
func (c *ApprovalCard) Update(msg tea.KeyMsg) (done bool, result any) {
	switch msg.String() {
	case "y", "Y", "enter":
		return true, ApprovalApprove
	case "a", "A":
		if c.AllowAlways {
			return true, ApprovalAlways
		}
	case "d", "D":
		if c.FullDiff {
			return true, ApprovalFullDiff
		}
	case "n", "N", "esc", "ctrl+c":
		return true, ApprovalDeny
	}
	return false, nil
}

// View renders the card at the given width, bounded to MaxLines rows.
func (c *ApprovalCard) View(width int) string {
	inner := width - cardFrameWidth
	rows := []string{headlineStyle.Render(c.Headline)}
	for _, w := range c.Warnings {
		rows = append(rows, warnStyle.Render("⚠ "+w))
	}
	if c.Containment != "" {
		rows = append(rows, shieldStyle.Render("⛨ "+c.Containment))
	}

	hint := c.Question + " [y/N]"
	if c.AllowAlways {
		hint = c.Question + " [y/n/a]"
		if c.AlwaysHint != "" {
			hint += "  (" + c.AlwaysHint + ")"
		}
	}
	if c.FullDiff {
		hint += "  (d: full diff)"
	}
	hints := hintRows(append([]string{hint}, c.ExtraHints...), width)

	switch c.Variant {
	case ApprovalEdit:
		adds, dels := diff.Stats(c.Hunks)
		stats := dimStyle.Render(fmt.Sprintf("+%d −%d · %s", adds, dels, plural(len(c.Hunks), "hunk")))
		// Frame (2) plus the fixed rows bound how much diff fits.
		budget := 0
		if c.MaxLines > 0 {
			budget = max(c.MaxLines-2-len(rows)-len(hints)-1, 1)
		}
		rows = append(rows, UnifiedLines(c.Hunks, inner,
			UnifiedOpts{LineNumbers: true, Emphasis: true, MaxLines: budget, Syntax: c.Syntax})...)
		rows = append(rows, stats)
	case ApprovalGeneric:
		if c.Summary != "" && c.Summary != c.Headline {
			rows = append(rows, dimStyle.Render(clip(c.Summary, inner)))
		}
	}
	rows = append(rows, hints...)

	title := c.Title
	if c.QueuePos != "" {
		title += " (" + c.QueuePos + ")"
	}
	return renderCard(title, rows, width)
}

// plural renders "1 hunk" / "3 hunks".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
