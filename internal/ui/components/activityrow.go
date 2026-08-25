package components

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The column grid (DESIGN-TUI.md §6a, normative). Widths are character cells
// and match tokens/terminal.css in the design-system project; nothing in the
// transcript may invent a width. The target is the only field that grows.
const (
	ptrWidth   = 2 // fold state ▾/▸, focus cursor ❯
	railWidth  = 1 // the mutation rail ▎ (§14)
	glyphWidth = 2 // the kind of act, or the state that overrides it
	verbWidth  = 8 // closed vocabulary, left-aligned, space-padded
	durWidth   = 6 // right-aligned; blank under 0.5s, — when it never ran
	leadWidth  = ptrWidth + railWidth + glyphWidth + verbWidth

	// detailIndent is the detail body (§6a: 2 row body / 4 detail body /
	// 6 nested detail); tailIndent is a running command's live tail.
	detailIndent = 4
	tailIndent   = 2
)

// NoDuration is the duration field for a call that never ran — queued or
// denied (§6d).
const NoDuration = "—"

// The closed outcome vocabulary (§6d). Counts (`218 lines`, `3 matches`,
// `+12 −4 · 2 hunks`) are the outcome when there is nothing else to say and
// live in ActivityRow.Counts; everything else is one of these.
const (
	OutcomeOK          = "ok"
	OutcomeRunning     = "running…"
	OutcomeQueued      = "queued"
	OutcomeChecking    = "checking"
	OutcomeDenied      = "denied"
	OutcomeApproved    = "approved"
	OutcomeAutoAllowed = "auto-allowed"
)

// OutcomeExit is the terminal outcome of a shell command.
func OutcomeExit(code int) string { return "exit " + strconv.Itoa(code) }

// OutcomeBy names the decider behind a decision outcome — `denied · you`,
// `approved · you`, `auto-allowed · read-only`. Colour never carries the
// distinction alone (invariant 1), so the word is always there.
func OutcomeBy(outcome, decider string) string {
	if decider == "" {
		return outcome
	}
	return outcome + " · " + decider
}

// ActivityKind selects an activity row's glyph and whether it carries the
// mutation rail (§6b). Five different reads share one glyph on purpose: the
// verb, not the colour, says which read it was, and ⚙ never mutates.
type ActivityKind int

const (
	ActivityTool     ActivityKind = iota // ⚙ read-only tool
	ActivityCommand                      // $ shell command
	ActivityEdit                         // ✎ edit, write, patch, memory
	ActivitySubagent                     // ◇ sub-agent
)

// ActivityState is the row's state. It overrides the kind glyph (§6d) — but
// only for the states that are worth a glyph of their own: a row that simply
// finished keeps the kind glyph, so `$`, `⚙` and `✎` stay visible on the rows
// that succeeded (§6b's mocks over §6d's `ok → ✓` table row).
type ActivityState int

const (
	ActivityDone     ActivityState = iota // finished; the kind glyph stands
	ActivityQueued                        // · accepted, not started
	ActivityRunning                       // ▸ in flight
	ActivityChecking                      // ✦ the classifier is deciding
	ActivityFailed                        // ✗ the call failed
	ActivityDenied                        // ⊘ you said no, or a rule did
)

// ActivityRow is one line of activity on the column grid (§6a): pointer,
// mutation rail, glyph, verb, target, outcome, duration. It is a passive
// transcript renderer — focus mode (§7) owns expansion keys, so the row has
// no Update.
type ActivityRow struct {
	Kind  ActivityKind
	State ActivityState
	// Verb is the closed vocabulary of §6c, padded or clipped to 8 columns.
	Verb string
	// Target is the path, command, query or agent name — the only field that
	// grows, and the only one that clips.
	Target string
	// Outcome and Counts render as one right-aligned field, joined by ` · `.
	// The field never clips: it is the reason to read the row.
	Outcome string
	Counts  string
	// Duration is the 6-column right-aligned field. Callers omit it under
	// 0.5s and set NoDuration for a call that never ran.
	Duration string
	// Detail is the bounded detail body shown when Expanded; failed rows
	// auto-expand with error lines first.
	Detail    []string
	MaxDetail int
	// Tail is a running command's last output line, shown live beneath the
	// row.
	Tail string
	// Keys are the keys the row offers (`/mode why`), rendered in info (12)
	// after the outcome — every key the interface offers is info, so a key in
	// any other colour is not an offer (§10a).
	Keys string
	// ByRule colours a denial del (9) rather than dim (241): `⊘ denied · you`
	// is a preference, `⊘ denied · auto` is a rule (§6d).
	ByRule bool
	// Expanded shows the detail body; Selected draws the focus-mode pointer.
	Expanded bool
	Selected bool
}

// Failed reports whether the row broke, for callers deciding what to
// auto-expand.
func (r ActivityRow) Failed() bool { return r.State == ActivityFailed }

// mutated reports whether the row carries the mutation rail (§14): it wrote
// to disk, ran a command, or was denied. Read-only rows leave the gutter
// blank, and a sub-agent's mirrored row is a status report, not an act — but
// a row that failed keeps a rail whatever it was, so scrolling back finds the
// break without hunting for it.
func (r ActivityRow) mutated() bool {
	switch r.State {
	case ActivityFailed, ActivityDenied:
		return true
	}
	return r.Kind == ActivityCommand || r.Kind == ActivityEdit
}

// railCell renders gutter column 3: accent for a mutation, del for a break.
func (r ActivityRow) railCell() string {
	if !r.mutated() {
		return strings.Repeat(" ", railWidth)
	}
	if r.State == ActivityFailed {
		return delStyle.Render("▎")
	}
	return accentStyle.Render("▎")
}

// pointer renders gutter columns 1–2: the focus cursor today, fold state once
// steps land (S-090).
func (r ActivityRow) pointer() string {
	if r.Selected {
		return focusRowStyle.Render("❯") + " "
	}
	return strings.Repeat(" ", ptrWidth)
}

// glyph renders the 2-column glyph field: the state where it overrides, the
// kind of act otherwise.
func (r ActivityRow) glyph() string {
	var g string
	switch r.State {
	case ActivityQueued:
		g = dimStyle.Render("·")
	case ActivityRunning:
		g = spinTextStyle.Render("▸")
	case ActivityChecking:
		g = spinTextStyle.Render("✦")
	case ActivityFailed:
		g = errStyle.Render("✗")
	case ActivityDenied:
		if r.ByRule {
			g = delStyle.Render("⊘")
		} else {
			g = dimStyle.Render("⊘")
		}
	default:
		switch r.Kind {
		case ActivityCommand:
			g = accentStyle.Render("$")
		case ActivityEdit:
			g = accentStyle.Render("✎")
		case ActivitySubagent:
			g = infoStyle.Render("◇")
		default:
			g = accentStyle.Render("⚙")
		}
	}
	return g + " "
}

// verbField pads the verb to its 8 columns; an over-long verb clips, which is
// the signal that the §6c table is stale.
func (r ActivityRow) verbField() string {
	v := clip(r.Verb, verbWidth)
	return v + strings.Repeat(" ", verbWidth-lipgloss.Width(v))
}

// outcomeField joins outcome and counts into the one right-aligned field.
func (r ActivityRow) outcomeField() string {
	var parts []string
	if r.Outcome != "" {
		style := dimStyle
		switch {
		case r.State == ActivityFailed, r.State == ActivityDenied && r.ByRule:
			style = delStyle
		}
		parts = append(parts, style.Render(r.Outcome))
	}
	if r.Counts != "" {
		parts = append(parts, dimmerStyle.Render(r.Counts))
	}
	if r.Keys != "" {
		parts = append(parts, infoStyle.Render(r.Keys))
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

// durationField right-aligns the duration in its 6 columns. The field is
// reserved even when blank so outcomes line up down the transcript; the
// trailing blank is trimmed off the rendered line.
func (r ActivityRow) durationField() string {
	d := clip(r.Duration, durWidth)
	pad := durWidth - lipgloss.Width(d)
	if pad < 0 {
		pad = 0
	}
	if d == "" {
		return strings.Repeat(" ", durWidth)
	}
	return strings.Repeat(" ", pad) + dimmerStyle.Render(d)
}

// View renders the row (plus tail and detail lines) at the given width.
func (r ActivityRow) View(width int) string {
	lead := r.pointer() + r.railCell() + r.glyph() + r.verbField()
	outcome := r.outcomeField()
	outW := lipgloss.Width(outcome)

	// The target grows into whatever the fixed fields leave, and clips with …
	// so the outcome never has to.
	sep := 0
	if outW > 0 {
		sep = 2
	}
	target := clip(r.Target, width-leadWidth-durWidth-outW-sep)
	targetW := lipgloss.Width(target)

	pad := width - leadWidth - targetW - outW - durWidth
	if pad < sep {
		pad = sep
	}
	row := lead + dimStyle.Render(target) + strings.Repeat(" ", pad) + outcome + r.durationField()
	lines := []string{strings.TrimRight(row, " ")}

	if r.State == ActivityRunning && r.Tail != "" {
		lines = append(lines, indented(r.Tail, tailIndent, width))
	}
	// Failed rows auto-expand to their bounded detail; successful rows stay
	// collapsed until focus mode expands them.
	if (r.Expanded || r.State == ActivityFailed) && len(r.Detail) > 0 {
		detail := r.Detail
		if r.MaxDetail > 0 && len(detail) > r.MaxDetail {
			detail = detail[:r.MaxDetail]
		}
		for _, d := range detail {
			lines = append(lines, indented(d, detailIndent, width))
		}
	}
	return strings.Join(lines, "\n")
}

// indented renders one detail line at the given indent in dimmer (245).
// Detail bodies indent, they do not re-grid (§6a).
func indented(s string, indent, width int) string {
	return strings.Repeat(" ", indent) + dimmerStyle.Render(clip(s, max(width-indent, 1)))
}

// The step outline (§13) draws its headers on this same grid but lives in
// internal/ui/chat, because it groups history rather than rendering a widget
// (§11). These are the fields it needs; the widths stay declared here so a
// grid change remains a one-line change.
const (
	// GridPointerWidth is the fold-state/focus column.
	GridPointerWidth = ptrWidth
	// GridVerbColumn is where a row's verb — and a step's title — starts.
	GridVerbColumn = ptrWidth + railWidth + glyphWidth
	// GridDurationWidth is the right-aligned duration field.
	GridDurationWidth = durWidth
)
