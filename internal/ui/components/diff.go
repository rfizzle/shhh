package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/diff"
)

// DiffMode selects which of the diff viewer's three renderings View produces
// (DESIGN-TUI.md §3).
type DiffMode int

const (
	// DiffCollapsed is the one-row transcript form: path, counts, expand hint.
	DiffCollapsed DiffMode = iota
	// DiffExpanded is the in-transcript unified view, bounded to MaxLines.
	DiffExpanded
	// DiffFull is the full-screen scrollable view; side-by-side on wide
	// terminals.
	DiffFull
)

// sideBySideMinWidth is the terminal width at which the full-screen view
// switches to side-by-side automatically.
const sideBySideMinWidth = 120

// DiffView renders one file's hunks in collapsed, expanded, or full-screen
// form. It is a plain-state component: the host owns it and routes keys to
// Update while it is focused.
type DiffView struct {
	Path  string
	Verb  string // row glyph verb, e.g. "edit", "write"
	Hunks []diff.Hunk
	Mode  DiffMode
	// MaxLines bounds the expanded view's body (0 = unbounded).
	MaxLines int
	// Height is the full-screen view's row budget, including header and
	// footer.
	Height     int
	SideBySide bool
	// Offset is the first visible body row of the full-screen view.
	Offset int
}

// Update handles keys while the viewer is focused. done reports that the
// viewer was dismissed (esc from the collapsed or expanded form); result is
// always nil. Esc from full screen steps back to the expanded view — esc
// never destroys.
func (d *DiffView) Update(msg tea.KeyMsg) (done bool, result any) {
	switch msg.String() {
	case "enter":
		// [enter] expand · [enter] full view · [enter again] collapse (§3b).
		switch d.Mode {
		case DiffCollapsed:
			d.Mode = DiffExpanded
		case DiffExpanded:
			d.Mode = DiffFull
			d.Offset = 0
		default:
			d.Mode = DiffCollapsed
		}
		return false, nil
	case "esc":
		if d.Mode == DiffFull {
			d.Mode = DiffExpanded
			return false, nil
		}
		return true, nil
	}
	if d.Mode != DiffFull {
		return false, nil
	}
	switch msg.String() {
	case "j", "down":
		d.scrollTo(d.Offset + 1)
	case "k", "up":
		d.scrollTo(d.Offset - 1)
	case "n":
		d.jumpHunk(1)
	case "p":
		d.jumpHunk(-1)
	case "s":
		d.SideBySide = !d.SideBySide
	}
	return false, nil
}

// View renders the current mode at the given width.
func (d *DiffView) View(width int) string {
	switch d.Mode {
	case DiffExpanded:
		return strings.Join(d.ExpandedLines(width), "\n")
	case DiffFull:
		return d.fullView(width)
	default:
		return d.RowView(width)
	}
}

// statsLabel is the "+N −M · H hunks" summary present in every form.
func (d *DiffView) statsLabel() string {
	adds, dels := diff.Stats(d.Hunks)
	label := fmt.Sprintf("+%d −%d", adds, dels)
	if n := len(d.Hunks); n == 1 {
		label += " · 1 hunk"
	} else {
		label += fmt.Sprintf(" · %d hunks", n)
	}
	return label
}

// RowView is the collapsed one-row transcript form (§3a).
func (d *DiffView) RowView(width int) string {
	verb := d.Verb
	if verb == "" {
		verb = "edit"
	}
	left := accentStyle.Render("✎ "+verb) + " " + d.Path
	right := dimStyle.Render(d.statsLabel()) + "   " + hintStyle.Render("[enter] expand")
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return clip(left+"  "+right, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// UnifiedOpts controls the unified rendering.
type UnifiedOpts struct {
	// LineNumbers prefixes each line with its old/new line number.
	LineNumbers bool
	// Emphasis applies the intraline background tint to changed spans.
	Emphasis bool
	// MaxLines bounds the output; the last row becomes a truncation notice
	// when lines were dropped. 0 means unbounded.
	MaxLines int
}

// UnifiedLines renders hunks as colored unified-diff rows. An empty diff
// renders a single "(no changes)" notice.
func UnifiedLines(hunks []diff.Hunk, width int, opts UnifiedOpts) []string {
	if len(hunks) == 0 {
		return []string{hintStyle.Render("(no changes)")}
	}
	numWidth := 0
	if opts.LineNumbers {
		last := hunks[len(hunks)-1]
		numWidth = len(fmt.Sprintf("%d", max(last.OldStart+last.OldCount, last.NewStart+last.NewCount)))
	}
	var rows []string
	for _, h := range hunks {
		rows = append(rows, hunkStyle.Render(clip(h.Header(), width)))
		for _, l := range h.Lines {
			rows = append(rows, renderUnifiedLine(l, width, numWidth, opts.Emphasis))
		}
	}
	if opts.MaxLines > 0 && len(rows) > opts.MaxLines {
		keep := max(opts.MaxLines-1, 1)
		extra := len(rows) - keep
		rows = append(rows[:keep:keep], hintStyle.Render(fmt.Sprintf("… (+%d more diff lines)", extra)))
	}
	return rows
}

// ExpandedLines is the bounded in-transcript unified view (§3b).
func (d *DiffView) ExpandedLines(width int) []string {
	head := accentStyle.Render("✎ ") + d.Path
	gap := width - lipgloss.Width(head) - lipgloss.Width(d.statsLabel())
	if gap > 1 {
		head += strings.Repeat(" ", gap) + dimStyle.Render(d.statsLabel())
	}
	body := max(d.MaxLines-1, 1)
	if d.MaxLines == 0 {
		body = 0
	}
	return append([]string{head},
		UnifiedLines(d.Hunks, width, UnifiedOpts{LineNumbers: true, Emphasis: true, MaxLines: body})...)
}

// renderUnifiedLine renders one diff line: marker, optional line number, text
// with tab expansion and optional intraline emphasis.
func renderUnifiedLine(l diff.Line, width, numWidth int, emphasis bool) string {
	marker := " "
	style, emphStyle := contextStyle, contextStyle
	switch l.Kind {
	case diff.Add:
		marker, style, emphStyle = "+", addStyle, addEmphStyle
	case diff.Del:
		marker, style, emphStyle = "-", delStyle, delEmphStyle
	}

	prefix := marker
	if numWidth > 0 {
		no := l.NewNo
		if l.Kind == diff.Del {
			no = l.OldNo
		}
		prefix = fmt.Sprintf("%s %*d  ", marker, numWidth, no)
	}

	text := strings.ReplaceAll(l.Text, "\t", "    ")
	avail := width - lipgloss.Width(prefix)
	if !emphasis || len(l.Emph) == 0 {
		return style.Render(clip(prefix+text, width))
	}

	// Clip on the raw runes first, then apply the emphasis span within the
	// visible part.
	r := []rune(text)
	clipped := false
	if avail > 0 && len(r) > avail {
		r = r[:avail-1]
		clipped = true
	}
	span := l.Emph[0]
	s, e := min(span.Start, len(r)), min(span.End, len(r))
	// Tab expansion shifts offsets; only lines without tabs keep exact spans.
	if strings.ContainsRune(l.Text, '\t') {
		s, e = 0, 0
	}
	var b strings.Builder
	b.WriteString(style.Render(prefix + string(r[:s])))
	if e > s {
		b.WriteString(emphStyle.Render(string(r[s:e])))
	}
	b.WriteString(style.Render(string(r[e:])))
	if clipped {
		b.WriteString(style.Render("…"))
	}
	return b.String()
}

// fullView is the full-screen rendering (§3c): header, scrollable body,
// footer hint. Side-by-side when toggled or the terminal is wide enough.
func (d *DiffView) fullView(width int) string {
	header := padRight(" "+d.Path, max(0, width-lipgloss.Width(d.statsLabel()))) + dimStyle.Render(d.statsLabel())
	footer := hintStyle.Render("diff · j/k scroll · n/p hunk · s side-by-side · esc back")

	var body []string
	if d.sideBySideActive(width) {
		body = d.sideBySideLines(width)
	} else {
		body = UnifiedLines(d.Hunks, width, UnifiedOpts{LineNumbers: true, Emphasis: true})
	}

	rows := d.bodyHeight()
	d.Offset = clampOffset(d.Offset, len(body), rows)
	end := min(d.Offset+rows, len(body))
	visible := body[d.Offset:end]

	out := append([]string{header}, visible...)
	for len(out) < rows+1 {
		out = append(out, "")
	}
	return strings.Join(append(out, footer), "\n")
}

// bodyHeight is how many body rows the full-screen view shows.
func (d *DiffView) bodyHeight() int {
	return max(d.Height-2, 1) // header + footer
}

func (d *DiffView) sideBySideActive(width int) bool {
	return d.SideBySide || width >= sideBySideMinWidth
}

// scrollTo clamps and applies a new full-screen offset.
func (d *DiffView) scrollTo(offset int) {
	d.Offset = clampOffset(offset, d.fullBodyLen(), d.bodyHeight())
}

func clampOffset(offset, total, visible int) int {
	return max(0, min(offset, total-visible))
}

// fullBodyLen is the total body row count of the current full-screen layout,
// computed at a nominal width (row counts don't depend on width).
func (d *DiffView) fullBodyLen() int {
	if d.SideBySide {
		return len(d.sideBySideLines(sideBySideMinWidth))
	}
	n := 0
	for _, h := range d.Hunks {
		n += 1 + len(h.Lines)
	}
	return n
}

// jumpHunk scrolls to the start of the next (+1) or previous (-1) hunk.
func (d *DiffView) jumpHunk(dir int) {
	starts := make([]int, 0, len(d.Hunks))
	row := 0
	for _, h := range d.Hunks {
		starts = append(starts, row)
		row += 1 + len(h.Lines)
	}
	if d.SideBySide {
		// Side-by-side rows don't map 1:1 to unified rows; rebuild the starts
		// from the paired layout.
		starts = starts[:0]
		row = 0
		for _, h := range d.Hunks {
			starts = append(starts, row)
			row += 1 + len(pairHunkRows(h))
		}
	}
	if dir > 0 {
		for _, s := range starts {
			if s > d.Offset {
				d.scrollTo(s)
				return
			}
		}
		return
	}
	for i := len(starts) - 1; i >= 0; i-- {
		if starts[i] < d.Offset {
			d.scrollTo(starts[i])
			return
		}
	}
	d.scrollTo(0)
}

// pairedRow is one side-by-side row: the old-side and new-side lines, either
// of which may be absent.
type pairedRow struct {
	old, new *diff.Line
}

// pairHunkRows aligns a hunk's lines into side-by-side rows: context spans
// both panes, and each deletion run pairs index-wise with the addition run
// that follows it.
func pairHunkRows(h diff.Hunk) []pairedRow {
	var rows []pairedRow
	lines := h.Lines
	i := 0
	for i < len(lines) {
		switch lines[i].Kind {
		case diff.Context:
			rows = append(rows, pairedRow{old: &lines[i], new: &lines[i]})
			i++
		default:
			ds := i
			for i < len(lines) && lines[i].Kind == diff.Del {
				i++
			}
			as := i
			for i < len(lines) && lines[i].Kind == diff.Add {
				i++
			}
			dels, adds := lines[ds:as], lines[as:i]
			for k := 0; k < max(len(dels), len(adds)); k++ {
				var row pairedRow
				if k < len(dels) {
					row.old = &dels[k]
				}
				if k < len(adds) {
					row.new = &adds[k]
				}
				rows = append(rows, row)
			}
		}
	}
	return rows
}

// sideBySideLines renders all hunks as two panes separated by a divider;
// truncated cells end with … (§3c).
func (d *DiffView) sideBySideLines(width int) []string {
	pane := max((width-3)/2, 8)
	divider := dimStyle.Render(" │ ")
	var out []string
	for _, h := range d.Hunks {
		out = append(out, hunkStyle.Render(clip(h.Header(), width)))
		for _, row := range pairHunkRows(h) {
			out = append(out, padRight(sideCell(row.old, pane, true), pane)+divider+sideCell(row.new, pane, false))
		}
	}
	return out
}

// sideCell renders one pane cell: line number plus text, styled by kind.
func sideCell(l *diff.Line, width int, oldSide bool) string {
	if l == nil {
		return ""
	}
	style := contextStyle
	no := l.NewNo
	if oldSide {
		no = l.OldNo
	}
	switch l.Kind {
	case diff.Add:
		style = addStyle
	case diff.Del:
		style = delStyle
	}
	text := fmt.Sprintf("%4d  %s", no, strings.ReplaceAll(l.Text, "\t", "    "))
	return style.Render(clip(text, width))
}
