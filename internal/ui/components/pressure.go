package components

// The context-pressure card (S-108, DESIGN-TUI.md §17b, and
// ui_kits/cockpit/Edges.html in the shhh Design System project).
//
// The second of the two cards, and for the same reason as the first: at the
// alert threshold the session cannot go on without an answer. Everything
// below it — the warning colour in the vitals and inspector rails — is a
// state you can read past. This is the one you cannot.
//
// It is the only place in the product that itemises token spend, because it
// is the only place where you can act on it: a percentage tells you the
// window is closing, and a breakdown tells you what closed it. The rows come
// from the host's own accounting (S-093), so the card cannot quote a number
// the rails disagree with.
//
// Like the provider card it is a passive renderer plus a key reader; what
// compacting, starting fresh and keeping going actually do belongs to the
// host.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// pressureTitle is the card's own name. It stays fixed while the chip beside
// it carries the numbers, so the title is a thing you recognise rather than a
// string that changes every turn.
const pressureTitle = "Context is nearly full"

// pressureCountGap separates the token count from the category it belongs to.
// Two spaces, so the counts read as a column without a rule drawn under them.
const pressureCountGap = 2

// PressureRow is one category of the occupancy breakdown: what it costs, what
// it is, and — where the accounting can say so honestly — what it is made of.
type PressureRow struct {
	// Tokens is the category's share of the window.
	Tokens int64
	// Label names the category in the card's own words ("tool output"), not
	// in the accounting's field names.
	Label string
	// Detail is the optional clause after the em dash: `6 results`, `14
	// messages`. A category the host cannot characterise has none rather than
	// a fabricated one.
	Detail string
}

// PressureCard is the decision surface a full window earns. It states the
// occupancy, breaks it down, predicts what compaction recovers, and offers
// the three answers — compact, start fresh, keep going.
type PressureCard struct {
	// Pct is the occupancy, 0–100, and Tokens/Window the numbers behind it.
	Pct            int
	Tokens, Window int64
	// Warn and Alert are the host's own thresholds, passed through to the
	// meter so the card, the vitals rail and the inspector rail turn colour
	// at the same two numbers (§10c).
	Warn, Alert int
	// Estimated marks a total the provider never reported, so the card says
	// `~188k` where the rails say `~188k` and neither passes a guess off as a
	// measurement.
	Estimated bool
	// Rows are the categories, largest first; the host drops the empty ones.
	Rows []PressureRow
	// Keeps is what compaction preserves, as a clause: `the plan, the 3
	// changed files and the last 2 turns`.
	Keeps string
	// Drops is what it discards, as a clause: `the older tool output`.
	Drops string
	// Recovers is the prediction, with the share of the window it frees.
	Recovers    int64
	RecoversPct int
	// Continuing is the one sentence about the answer that changes nothing —
	// what keeping going will cost, in words, because the cost is silent
	// otherwise.
	Continuing string
	// Keys are the three offers on the action bar.
	Keys []KeyOffer
}

// Update resolves on any offered key and on esc, which declines. The result
// is the chosen keystroke, or "" for a decline — esc keeps going, which is
// invariant 3 holding even at 94%.
func (c *PressureCard) Update(msg tea.KeyMsg) (done bool, result any) {
	pressed := msg.String()
	if keys.Is(pressed, keys.Select.Cancel) {
		return true, ""
	}
	for _, k := range c.Keys {
		if strings.Trim(k.Key, "[]") == pressed {
			return true, pressed
		}
	}
	return false, nil
}

// meter is the card's bar: the same component, cell count and thresholds the
// inspector rail's CONTEXT block uses, so the two cannot disagree about what
// colour 84% is (S-094).
func (c PressureCard) meter() Meter {
	return Meter{Pct: c.Pct, Cells: MeterCellsRail, Tone: MeterPressure, Warn: c.Warn, Alert: c.Alert}
}

// View renders the card at the given width.
func (c PressureCard) View(width int) string {
	meter := c.meter()
	// The border carries the meter's own colour — bold del at the alert
	// threshold — which is what puts the bar and the numbers on the title
	// rail in one colour without the chips having to be styled through the
	// frame (§10c: the bar and its number turn colour together).
	style := meter.Style()

	rows := []string{meter.Bar()}
	if len(c.Rows) > 0 {
		rows = append(rows, "")
		field := c.countField()
		for _, r := range c.Rows {
			rows = append(rows, c.rowLine(r, field))
		}
	}
	if body := c.prediction(); len(body) > 0 {
		rows = append(rows, "")
		// The prediction is the only prose on the card, and prose that clips
		// loses its verb. Every other row is a field, which can afford to.
		for _, line := range body {
			rows = append(rows, wrapSpans(line, width-cardFrameWidth)...)
		}
	}
	if len(c.Keys) > 0 {
		// None of the three is truncated away: a narrow terminal gets more
		// rows, not fewer answers, and the one that would clip here is the
		// one that keeps going.
		rows = append(rows, cardRule)
		rows = append(rows, wrapOffers(c.Keys, width-cardFrameWidth)...)
	}
	return renderChromeCard(cardChrome{
		title: pressureTitle,
		chips: []string{c.chip()},
		style: &style,
	}, rows, width)
}

// chip is the occupancy on the title rail: the percentage, then the two
// numbers it came from. It is one chip rather than three because the three
// are one fact, and a fact should not be dropped a third at a time as the
// terminal narrows.
func (c PressureCard) chip() string {
	tokens := formatTokens(c.Tokens)
	if c.Estimated {
		tokens = "~" + tokens
	}
	return fmt.Sprintf("%d%% · %s / %s", min(max(c.Pct, 0), 100), tokens, formatTokens(c.Window))
}

// countField is the width the token counts are right-aligned in, measured
// from the counts themselves: `188k` and `2k` line up on their last
// character, so the column reads as a column at any scale.
func (c PressureCard) countField() int {
	w := 0
	for _, r := range c.Rows {
		if n := len(formatTokens(r.Tokens)); n > w {
			w = n
		}
	}
	return w
}

// rowLine is one category: its count in status, its name in body text, and
// its detail dim behind an em dash.
func (c PressureCard) rowLine(r PressureRow, field int) string {
	count := formatTokens(r.Tokens)
	if pad := field - len(count); pad > 0 {
		count = strings.Repeat(" ", pad) + count
	}
	line := sty.Status.Render(count) + strings.Repeat(" ", pressureCountGap) + sty.Body.Render(r.Label)
	if r.Detail != "" {
		line += sty.Dim.Render(" — " + r.Detail)
	}
	return line
}

// styledSpan is a run of words in one style. The prediction is built from
// them rather than from finished strings so that wrapping stays a layout
// decision: a line broken mid-clause re-opens every style it was carrying,
// where a wrapped ANSI string leaves the second half painted by whatever came
// before it.
type styledSpan struct {
	text  string
	style lipgloss.Style
}

// prediction is the sentences under the breakdown: what compacting keeps and
// drops with what it recovers, then what keeping going costs. The recovery
// clause is the one thing on the card in add, because it is the only number
// on it that is good news.
func (c PressureCard) prediction() [][]styledSpan {
	var rows [][]styledSpan
	if c.Keeps != "" {
		rows = append(rows, []styledSpan{{"compacting keeps " + c.Keeps, sty.Dim}})
	}
	if c.Drops != "" || c.Recovers > 0 {
		lead := "and drops " + c.Drops
		switch {
		case c.Drops == "":
			lead = "compacting frees the rest of the conversation"
		case len(rows) == 0:
			lead = "compacting drops " + c.Drops
		}
		row := []styledSpan{{lead, sty.Dim}}
		if c.Recovers > 0 {
			row = append(row,
				styledSpan{"—", sty.Dim},
				styledSpan{fmt.Sprintf("recovers about %s (%d%%)", formatTokens(c.Recovers), c.RecoversPct), sty.Add})
		}
		rows = append(rows, row)
	}
	if c.Continuing != "" {
		rows = append(rows, []styledSpan{{c.Continuing, sty.Dim}})
	}
	return rows
}

// wrapSpans lays a run of styled spans out over lines no wider than inner,
// breaking between words and re-emitting each span's style on every line it
// occupies. A word wider than the line is left to the frame to clip: a break
// inside one would be a hyphen the interface invented.
func wrapSpans(spans []styledSpan, inner int) []string {
	if inner <= 0 {
		return nil
	}
	var lines []string
	var line, pending strings.Builder
	var style lipgloss.Style
	width := 0

	// One styling pass per span per line, rather than per word: the wrap is
	// invisible in the ANSI as well as on the screen.
	closeSpan := func() {
		if pending.Len() > 0 {
			line.WriteString(style.Render(pending.String()))
			pending.Reset()
		}
	}
	breakLine := func() {
		closeSpan()
		if line.Len() > 0 {
			lines = append(lines, line.String())
			line.Reset()
		}
		width = 0
	}

	for _, sp := range spans {
		style = sp.style
		for _, word := range strings.Fields(sp.text) {
			w := lipgloss.Width(word)
			switch {
			case width == 0:
				pending.WriteString(word)
				width = w
			case width+1+w <= inner:
				pending.WriteString(" " + word)
				width += 1 + w
			default:
				breakLine()
				style = sp.style
				pending.WriteString(word)
				width = w
			}
		}
		closeSpan()
	}
	breakLine()
	return lines
}
