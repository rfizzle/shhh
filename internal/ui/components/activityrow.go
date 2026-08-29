package components

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The column grid (docs/interface/principles.md#one-grid, normative). Widths
// are character cells and match tokens/terminal.css in the design-system
// project; nothing in the transcript may invent a width. The target is the
// only field that grows.
const (
	ptrWidth   = 2 // fold state ▾/▸, focus cursor ❯
	railWidth  = 1 // the mutation rail ▎
	glyphWidth = 2 // the kind of act, or the state that overrides it
	verbWidth  = 8 // closed vocabulary, left-aligned, space-padded
	durWidth   = 6 // right-aligned; blank under 0.5s, — when it never ran
	leadWidth  = ptrWidth + railWidth + glyphWidth + verbWidth

	// detailIndent is the detail body (2 row body / 4 detail body / 6 nested
	// detail); tailIndent is a running command's live tail.
	detailIndent = 4
	tailIndent   = 2
)

// NoDuration is the duration field for a call that never ran — queued or
// denied.
const NoDuration = "—"

// The closed outcome vocabulary
// (docs/interface/principles.md#closed-vocabularies). Counts (`218 lines`, `3
// matches`, `+12 −4 · 2 hunks`) are the outcome when there is nothing else to
// say and live in ActivityRow.Counts; everything else is one of these.
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
// mutation rail. Five different reads share one glyph on purpose: the
// verb, not the colour, says which read it was, and ⚙ never mutates.
type ActivityKind int

const (
	ActivityTool     ActivityKind = iota // ⚙ read-only tool
	ActivityCommand                      // $ shell command
	ActivityEdit                         // ✎ edit, write, patch, memory
	ActivitySubagent                     // ◇ sub-agent
)

// ActivityState is the row's state. It overrides the kind glyph — but only
// for the states that are worth a glyph of their own: a row that simply
// finished keeps the kind glyph, so `$`, `⚙` and `✎` stay visible on the rows
// that succeeded: the kind glyphs win over the outcome table's `ok → ✓` row.
type ActivityState int

const (
	ActivityDone     ActivityState = iota // finished; the kind glyph stands
	ActivityQueued                        // · accepted, not started
	ActivityRunning                       // ▸ in flight
	ActivityChecking                      // ✦ the classifier is deciding
	ActivityFailed                        // ✗ the call failed
	ActivityDenied                        // ⊘ you said no, or a rule did
)

// ActivityRow is one line of activity on the column grid: pointer, mutation
// rail, glyph, verb, target, outcome, duration. It is a passive transcript
// renderer — reading mode (docs/interface/surfaces.md#reading-mode) owns the
// expansion keys, so the row has no Update.
type ActivityRow struct {
	Kind  ActivityKind
	State ActivityState
	// Verb is the closed verb vocabulary, padded or clipped to 8 columns.
	Verb string
	// Target is the path, command, query or agent name — the only field that
	// grows, and the only one that clips.
	Target string
	// Outcome and Counts render as one right-aligned field, joined by ` · `. The
	// field never clips: it is the reason to read the row.
	Outcome string
	Counts  string
	// Duration is the 6-column right-aligned field. Callers omit it under 0.5s
	// and set NoDuration for a call that never ran.
	Duration string
	// Detail is the bounded detail body shown when Expanded; failed rows
	// auto-expand with error lines first.
	Detail    []string
	MaxDetail int
	// Tail is a running command's last output line, shown live beneath the row.
	Tail string
	// Keys are the keys the row offers (`/mode why`), rendered in info (12)
	// after the outcome — every key the interface offers is info, so a key in
	// any other colour is not an offer.
	Keys string
	// ByRule colours a denial del (9) rather than dim (241): `⊘ denied · you`
	// is a preference, `⊘ denied · auto` is a rule.
	ByRule bool
	// Expanded shows the detail body; Selected draws the focus-mode pointer.
	Expanded bool
	Selected bool
	// Spin says the host is ticking, and Frame is the frame it is on — the same
	// frame the status line and the frame header are drawing, from the
	// one tick source. A running row is `▸` in a still image and the spinner
	// while it animates, which is what the outcome table says; a host that does
	// not tick leaves Spin false rather than freezing a braille glyph on screen,
	// because a stopped spinner reads as a hang.
	Spin  bool
	Frame int
}

// Failed reports whether the row broke, for callers deciding what to
// auto-expand.
func (r ActivityRow) Failed() bool { return r.State == ActivityFailed }

// mutated reports whether the row carries the mutation rail
// (docs/interface/principles.md#weight-tracks-risk): it wrote to disk, ran a
// command, or was denied. Read-only rows leave the gutter blank, and a
// sub-agent's mirrored row is a status report, not an act — but a row that
// failed keeps a rail whatever it was, so scrolling back finds the break
// without hunting for it.
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
		return sty.Del.Render("▎")
	}
	return sty.Accent.Render("▎")
}

// pointer renders gutter columns 1–2: the focus cursor today, fold state once
// steps land (S-090).
func (r ActivityRow) pointer() string {
	if r.Selected {
		// The pointer is a glyph in its own column, not part of the highlight
		// behind the row.
		return sty.FocusPointer.Render("❯") + " "
	}
	return strings.Repeat(" ", ptrWidth)
}

// runningGlyph is `▸` where the row is a still image and the host's current
// spinner frame where it is animating. The frame is passed in rather than
// counted here, so this row, the turn status line and the frame header show
// the same one.
func (r ActivityRow) runningGlyph() string {
	if !r.Spin {
		return "▸"
	}
	return Spinner{Frame: r.Frame}.Glyph()
}

// glyph renders the 2-column glyph field: the state where it overrides, the
// kind of act otherwise.
func (r ActivityRow) glyph() string {
	var g string
	switch r.State {
	case ActivityQueued:
		g = sty.Dim.Render("·")
	case ActivityRunning:
		g = sty.SpinText.Render(r.runningGlyph())
	case ActivityChecking:
		g = sty.SpinText.Render("✦")
	case ActivityFailed:
		g = sty.Err.Render("✗")
	case ActivityDenied:
		if r.ByRule {
			g = sty.Del.Render("⊘")
		} else {
			g = sty.Dim.Render("⊘")
		}
	default:
		switch r.Kind {
		case ActivityCommand:
			g = sty.Accent.Render("$")
		case ActivityEdit:
			g = sty.Accent.Render("✎")
		case ActivitySubagent:
			g = sty.Info.Render("◇")
		default:
			g = sty.Accent.Render("⚙")
		}
	}
	return g + " "
}

// verbField pads the verb to its 8 columns; an over-long verb clips, which is
// the signal that the verb table is stale. Recovery rows share it, which is
// what puts `model` in the same column as `read`.
func verbField(verb string) string {
	v := clip(verb, verbWidth)
	return v + strings.Repeat(" ", verbWidth-lipgloss.Width(v))
}

// outcomeField joins outcome and counts into the one right-aligned field.
func (r ActivityRow) outcomeField() string {
	var parts []string
	if r.Outcome != "" {
		style := sty.Dim
		switch {
		case r.State == ActivityFailed, r.State == ActivityDenied && r.ByRule:
			style = sty.Del
		}
		parts = append(parts, style.Render(r.Outcome))
	}
	if r.Counts != "" {
		parts = append(parts, sty.Dimmer.Render(r.Counts))
	}
	if r.Keys != "" {
		parts = append(parts, sty.Info.Render(r.Keys))
	}
	return strings.Join(parts, sty.Dim.Render(" · "))
}

// durationField right-aligns the duration in its 6 columns. The field is
// reserved even when blank so outcomes line up down the transcript; the
// trailing blank is trimmed off the rendered line.
func durationField(d string) string {
	d = clip(d, durWidth)
	pad := durWidth - lipgloss.Width(d)
	if pad < 0 {
		pad = 0
	}
	if d == "" {
		return strings.Repeat(" ", durWidth)
	}
	return strings.Repeat(" ", pad) + sty.Dimmer.Render(d)
}

// gridLine assembles one line on the grid: a lead already padded to
// leadWidth, then the target, the outcome field and the duration. The target
// grows into whatever the fixed fields leave and clips with … so the outcome
// never has to — it is the reason to read the line. Both the activity row and
// the folded group row are this shape, which is why they line up.
func gridLine(lead, target, outcome, duration string, width int) string {
	return gridLineWith(lead, target, func(s string) string { return sty.Dim.Render(s) }, outcome, duration, width)
}

// gridLineWith is gridLine with the target's painting under the caller's
// control. A recovery row leads its target with the model in body text and
// dims only the class behind it, which one style over the whole field cannot
// express; paint is handed the already-clipped text so the column arithmetic
// stays in one place.
func gridLineWith(lead, target string, paint func(string) string, outcome, duration string, width int) string {
	outW := lipgloss.Width(outcome)
	sep := 0
	if outW > 0 {
		sep = 2
	}
	target = clip(target, width-leadWidth-durWidth-outW-sep)
	pad := width - leadWidth - lipgloss.Width(target) - outW - durWidth
	if pad < sep {
		pad = sep
	}
	line := lead + paint(target) + strings.Repeat(" ", pad) + outcome + durationField(duration)
	return strings.TrimRight(line, " ")
}

// View renders the row (plus tail and detail lines) at the given width.
func (r ActivityRow) View(width int) string {
	lead := r.pointer() + r.railCell() + r.glyph() + verbField(r.Verb)
	first := gridLine(lead, r.Target, r.outcomeField(), r.Duration, width)
	if r.Selected {
		// The reading cursor lights the row it is on: the background runs the row's
		// width and its words go bright, while the rail and the glyph keep the
		// colours that say what the row did. The pointer stays outside it.
		first = LitRow(first, ptrWidth, width)
	}
	lines := []string{first}

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
// Detail bodies indent, they do not re-grid.
//
// It is also the one door foreign bytes come through — a command's output, a
// live tail, a provider's error body — so the line is re-painted into the
// palette before it is measured. A line shhh wrote itself has nothing to
// re-paint and comes back unchanged; the clip runs afterwards because a
// cursor sequence the terminal would never have shown still counts against
// ansi.Truncate's width.
func indented(s string, indent, width int) string {
	pad, inner := strings.Repeat(" ", indent), max(width-indent, 1)
	if painted, ok := repaint(s, Palette.Dimmer); ok {
		// A re-painted line already carries the ground, run by run; a second
		// wrapper around it would only spend bytes the first reset throws away.
		return pad + clip(painted, inner)
	}
	return pad + sty.Dimmer.Render(clip(s, inner))
}

// GroupExpandKey is what a folded group row says opens it. It is drawn in the
// hint treatment rather than in info, the way the collapsed diff row's has
// always been: enter belongs to the draft below until reading mode takes the
// keyboard, so on a transcript row this is a label for what the row does
// under the cursor, not an offer standing open
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
var GroupExpandKey = keys.Bracket(keys.Reading.Expand) + " " + keys.Words(keys.Reading.Expand)

// ActivityGroup is the folded group row: the one line a run of consecutive
// read-only calls collapses into at normal verbosity. It folds, it never
// hides (invariant 4) — the label states what it swallowed and the duration
// states what that cost, so nothing is dropped to save space.
//
// It sits on the same grid as a row, shifted by one field: the fold state
// takes the glyph column and the kind glyph ⚙ takes the verb column, so the
// line reads as chrome about rows rather than as a call of its own.
type ActivityGroup struct {
	// Label counts the swallowed rows by kind, e.g. "6 reads · 2 searches".
	Label string
	// Duration is the summed 6-column field, blank under 0.5s like a row's.
	Duration string
}

// View renders the group row at the given width.
func (g ActivityGroup) View(width int) string {
	lead := strings.Repeat(" ", ptrWidth+railWidth) +
		sty.Dim.Render("▸") + " " +
		sty.Dim.Render("⚙") + strings.Repeat(" ", verbWidth-1)
	return gridLine(lead, g.Label, sty.Hint.Render(GroupExpandKey), g.Duration, width)
}

// The step outline draws its headers on this same grid but lives in
// internal/ui/chat, because it groups history rather than rendering a widget
// (see AGENTS.md). These are the fields it needs; the widths stay declared
// here so a grid change remains a one-line change.
const (
	// GridPointerWidth is the fold-state/focus column.
	GridPointerWidth = ptrWidth
	// GridVerbColumn is where a row's verb — and a step's title — starts.
	GridVerbColumn = ptrWidth + railWidth + glyphWidth
	// GridDurationWidth is the right-aligned duration field.
	GridDurationWidth = durWidth
)
