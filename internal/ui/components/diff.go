package components

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// DiffMode selects which of the diff viewer's three renderings View produces
// (docs/interface/surfaces.md#the-diff-view).
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

// Segment is one syntax-colored span of a source line. Color is a palette
// token; the zero token means the line's own diff colour, which is
// what an unclaimed chroma type resolves to.
type Segment struct {
	Text  string
	Color Token
}

// Syntax styles one raw source line as colored segments; nil renders
// plain diff colors. Segments must concatenate back to the input line — a
// mismatch falls back to plain rendering.
type Syntax func(line string) []Segment

// DiffView renders one file's hunks in collapsed, expanded, or full-screen
// form. It is a plain-state component: the host owns it and routes keys to
// Update while it is focused.
type DiffView struct {
	Path  string
	Verb  string // row glyph verb, e.g. "edit", "write"
	Hunks []diff.Hunk
	Mode  DiffMode
	// Syntax highlights this file's lines; nil renders plain diff colors.
	Syntax Syntax
	// Files renders a multi-file patch in the full-screen view (the /diff
	// session diff); when set, Path is just the header label and
	// Hunks is ignored.
	Files []diff.File
	// SyntaxFor resolves a per-file highlighter for multi-file views.
	SyntaxFor func(path string) Syntax
	// MaxLines bounds the expanded view's body (0 = unbounded).
	MaxLines int
	// Height is the full-screen view's row budget, including header and
	// footer.
	Height     int
	SideBySide bool
	// Offset is the first visible body row of the full-screen view.
	Offset int
	// Full-screen body cache: rendering (with syntax highlighting) is only
	// redone when the width or layout changes, so scrolling stays cheap.
	cachedBody      []string
	cachedBodyWidth int
	cachedBodySBS   bool
}

// Update handles keys while the viewer is focused. done reports that the
// viewer was dismissed (esc from the collapsed or expanded form); result is
// always nil. Esc from full screen steps back to the expanded view — esc
// never destroys.
func (d *DiffView) Update(msg tea.KeyPressMsg) (done bool, result any) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Reading.Expand):
		// [enter] expand · [enter] full view · [enter again] collapse.
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
	case keys.Is(pressed, keys.Diff.Back):
		if d.Mode == DiffFull {
			d.Mode = DiffExpanded
			return false, nil
		}
		return true, nil
	}
	if d.Mode != DiffFull {
		return false, nil
	}
	switch pressed := msg.String(); {
	case pressed == "j", pressed == "down":
		d.scrollTo(d.Offset + 1)
	case pressed == "k", pressed == "up":
		d.scrollTo(d.Offset - 1)
	case pressed == "n":
		d.jumpHunk(1)
	case pressed == "p":
		d.jumpHunk(-1)
	case keys.Is(pressed, keys.Diff.SideBySide):
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

// statsLabel is the "+N −M · H hunks" summary present in every form; a
// multi-file view counts files instead.
func (d *DiffView) statsLabel() string {
	if len(d.Files) > 0 {
		var adds, dels int
		for _, f := range d.Files {
			a, x := diff.Stats(f.Hunks)
			adds, dels = adds+a, dels+x
		}
		return fmt.Sprintf("+%d −%d · %s", adds, dels, plural(len(d.Files), "file"))
	}
	adds, dels := diff.Stats(d.Hunks)
	return fmt.Sprintf("+%d −%d · %s", adds, dels, plural(len(d.Hunks), "hunk"))
}

// RowView is the collapsed one-row transcript form.
func (d *DiffView) RowView(width int) string {
	verb := d.Verb
	if verb == "" {
		verb = "edit"
	}
	left := sty.Accent.Render("✎ "+verb) + " " + d.Path
	right := sty.Dim.Render(d.statsLabel()) + "   " + sty.Hint.Render(GroupExpandKey)
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
	// Syntax highlights line text, with diff coloring layered over it
	// (docs/interface/surfaces.md#the-diff-view); nil keeps plain diff colors.
	Syntax Syntax
}

// UnifiedLines renders hunks as colored unified-diff rows. An empty diff
// renders a single "(no changes)" notice.
func UnifiedLines(hunks []diff.Hunk, width int, opts UnifiedOpts) []string {
	if len(hunks) == 0 {
		return []string{sty.Hint.Render("(no changes)")}
	}
	numWidth := 0
	if opts.LineNumbers {
		last := hunks[len(hunks)-1]
		numWidth = len(fmt.Sprintf("%d", max(last.OldStart+last.OldCount, last.NewStart+last.NewCount)))
	}
	var rows []string
	for _, h := range hunks {
		rows = append(rows, sty.Hunk.Render(clip(h.Header(), width)))
		for _, l := range h.Lines {
			rows = append(rows, renderUnifiedLine(l, width, numWidth, opts))
		}
	}
	if opts.MaxLines > 0 && len(rows) > opts.MaxLines {
		keep := max(opts.MaxLines-1, 1)
		extra := len(rows) - keep
		rows = append(rows[:keep:keep], sty.Hint.Render(fmt.Sprintf("… (+%d more diff lines)", extra)))
	}
	return rows
}

// ExpandedLines is the bounded in-transcript unified view.
func (d *DiffView) ExpandedLines(width int) []string {
	head := sty.Accent.Render("✎ ") + d.Path
	gap := width - lipgloss.Width(head) - lipgloss.Width(d.statsLabel())
	if gap > 1 {
		head += strings.Repeat(" ", gap) + sty.Dim.Render(d.statsLabel())
	}
	body := max(d.MaxLines-1, 1)
	if d.MaxLines == 0 {
		body = 0
	}
	return append([]string{head},
		UnifiedLines(d.Hunks, width, UnifiedOpts{LineNumbers: true, Emphasis: true, MaxLines: body, Syntax: d.Syntax})...)
}

// renderUnifiedLine renders one diff line: marker, optional line number, text
// with tab expansion, syntax highlighting when available, and optional
// intraline emphasis.
func renderUnifiedLine(l diff.Line, width, numWidth int, opts UnifiedOpts) string {
	marker := " "
	style, emphStyle := sty.Context, sty.Context
	switch l.Kind {
	case diff.Add:
		marker, style, emphStyle = "+", sty.Add, sty.AddEmph
	case diff.Del:
		marker, style, emphStyle = "-", sty.Del, sty.DelEmph
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

	// Tab expansion shifts offsets; only lines without tabs keep exact spans.
	var span *diff.Span
	if opts.Emphasis && len(l.Emph) > 0 && !strings.ContainsRune(l.Text, '\t') {
		span = &l.Emph[0]
	}

	if opts.Syntax != nil {
		if out, ok := renderSyntaxLine(prefix, text, avail, l.Kind, span, opts.Syntax); ok {
			return out
		}
	}

	if span == nil {
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
	s, e := min(span.Start, len(r)), min(span.End, len(r))
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

// renderSyntaxLine renders the diff coloring layered over syntax highlighting
// (docs/interface/surfaces.md#the-diff-view): the marker keeps the kind's
// color, the line number is gray, the text keeps its syntax foregrounds, and
// the emphasis span gets a background tint so syntax colors survive. ok=false
// falls back to plain rendering when the segments don't reconstruct the line.
func renderSyntaxLine(prefix, text string, avail int, kind diff.Kind, span *diff.Span, syntax Syntax) (string, bool) {
	// Syntax colours come from a chroma theme, not from Palette, so mono mode
	// cannot strip them — it declines them instead and the line renders with
	// the plain +/- diff styling.
	if Mono() {
		return "", false
	}
	segs := syntax(text)
	if segs == nil {
		return "", false
	}
	total := 0
	for _, s := range segs {
		total += len(s.Text)
	}
	if total != len(text) {
		return "", false
	}

	kindStyle := sty.Context
	var emphBg Token
	switch kind {
	case diff.Add:
		kindStyle, emphBg = sty.Add, Palette.AddBg
	case diff.Del:
		kindStyle, emphBg = sty.Del, Palette.DelBg
	}

	var b strings.Builder
	// The marker is ASCII, so byte slicing is safe.
	b.WriteString(kindStyle.Render(prefix[:1]))
	if len(prefix) > 1 {
		b.WriteString(sty.Dim.Render(prefix[1:]))
	}

	limit := len([]rune(text))
	clipped := false
	if avail > 0 && limit > avail {
		limit = avail - 1
		clipped = true
	}

	pos := 0 // rune position within text
	for _, seg := range segs {
		if pos >= limit {
			break
		}
		sr := []rune(seg.Text)
		if pos+len(sr) > limit {
			sr = sr[:limit-pos]
		}
		st := kindStyle
		if kind == diff.Context {
			st = lipgloss.NewStyle()
		}
		if seg.Color != (Token{}) {
			st = lipgloss.NewStyle().Foreground(seg.Color.Color())
		}
		s, e := 0, 0
		if span != nil {
			s = min(max(span.Start-pos, 0), len(sr))
			e = min(max(span.End-pos, 0), len(sr))
		}
		if e > s {
			b.WriteString(st.Render(string(sr[:s])))
			b.WriteString(st.Background(emphBg.Color()).Render(string(sr[s:e])))
			b.WriteString(st.Render(string(sr[e:])))
		} else if len(sr) > 0 {
			b.WriteString(st.Render(string(sr)))
		}
		pos += len([]rune(seg.Text))
	}
	if clipped {
		b.WriteString(kindStyle.Render("…"))
	}
	return b.String(), true
}

// diffSection is one file of the full-screen body; single-file views have
// one unlabeled section.
type diffSection struct {
	path   string
	binary bool
	hunks  []diff.Hunk
	syntax Syntax
}

// sections normalizes the single-file and multi-file forms.
func (d *DiffView) sections() []diffSection {
	if len(d.Files) == 0 {
		return []diffSection{{hunks: d.Hunks, syntax: d.fileSyntax(d.Path, d.Syntax)}}
	}
	out := make([]diffSection, 0, len(d.Files))
	for _, f := range d.Files {
		out = append(out, diffSection{path: f.Path, binary: f.Binary, hunks: f.Hunks, syntax: d.fileSyntax(f.Path, nil)})
	}
	return out
}

func (d *DiffView) fileSyntax(path string, explicit Syntax) Syntax {
	if explicit != nil {
		return explicit
	}
	if d.SyntaxFor != nil {
		return d.SyntaxFor(path)
	}
	return nil
}

// fullView is the full-screen rendering: header, scrollable body,
// footer hint. Side-by-side when toggled or the terminal is wide enough.
func (d *DiffView) fullView(width int) string {
	header := padRight(" "+d.Path, max(0, width-lipgloss.Width(d.statsLabel()))) + sty.Dim.Render(d.statsLabel())
	footer := sty.Hint.Render("diff · " + strings.Join([]string{
		offer(keys.Diff.Scroll), offer(keys.Diff.Hunk),
		offer(keys.Diff.SideBySide), offer(keys.Diff.Back),
	}, " · "))

	body := d.fullBody(width)
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

// fullBody renders (and caches) the full-screen body rows, so scrolling a
// syntax-highlighted diff doesn't re-highlight every line per keypress.
func (d *DiffView) fullBody(width int) []string {
	sbs := d.sideBySideActive(width)
	if d.cachedBody != nil && d.cachedBodyWidth == width && d.cachedBodySBS == sbs {
		return d.cachedBody
	}
	var rows []string
	for _, sec := range d.sections() {
		if sec.path != "" {
			adds, dels := diff.Stats(sec.hunks)
			rows = append(rows, clip(sty.Accent.Render("─ "+sec.path)+"  "+sty.Dim.Render(fmt.Sprintf("+%d −%d", adds, dels)), width))
		}
		switch {
		case sec.binary:
			rows = append(rows, sty.Hint.Render("(binary file differs)"))
		case len(sec.hunks) == 0:
			rows = append(rows, sty.Hint.Render("(no textual changes)"))
		case sbs:
			rows = append(rows, sideBySideHunks(sec.hunks, width)...)
		default:
			rows = append(rows, UnifiedLines(sec.hunks, width, UnifiedOpts{LineNumbers: true, Emphasis: true, Syntax: sec.syntax})...)
		}
	}
	d.cachedBody, d.cachedBodyWidth, d.cachedBodySBS = rows, width, sbs
	return rows
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

// fullBodyLen is the total body row count of the current full-screen layout;
// row counts don't depend on width.
func (d *DiffView) fullBodyLen() int {
	n := 0
	for _, sec := range d.sections() {
		if sec.path != "" {
			n++
		}
		if sec.binary || len(sec.hunks) == 0 {
			n++
			continue
		}
		for _, h := range sec.hunks {
			n += 1 + d.hunkRows(h)
		}
	}
	return n
}

// hunkRows is one hunk's body row count in the current layout (header line
// excluded).
func (d *DiffView) hunkRows(h diff.Hunk) int {
	if d.SideBySide {
		return len(pairHunkRows(h))
	}
	return len(h.Lines)
}

// hunkStarts lists each hunk header's body row across all sections.
func (d *DiffView) hunkStarts() []int {
	var starts []int
	row := 0
	for _, sec := range d.sections() {
		if sec.path != "" {
			row++
		}
		if sec.binary || len(sec.hunks) == 0 {
			row++
			continue
		}
		for _, h := range sec.hunks {
			starts = append(starts, row)
			row += 1 + d.hunkRows(h)
		}
	}
	return starts
}

// jumpHunk scrolls to the start of the next (+1) or previous (-1) hunk.
func (d *DiffView) jumpHunk(dir int) {
	starts := d.hunkStarts()
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

// sideBySideHunks renders hunks as two panes separated by a divider;
// truncated cells end with ….
func sideBySideHunks(hunks []diff.Hunk, width int) []string {
	pane := max((width-3)/2, 8)
	divider := sty.Dim.Render(" │ ")
	var out []string
	for _, h := range hunks {
		out = append(out, sty.Hunk.Render(clip(h.Header(), width)))
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
	style := sty.Context
	no := l.NewNo
	if oldSide {
		no = l.OldNo
	}
	switch l.Kind {
	case diff.Add:
		style = sty.Add
	case diff.Del:
		style = sty.Del
	}
	text := fmt.Sprintf("%4d  %s", no, strings.ReplaceAll(l.Text, "\t", "    "))
	return style.Render(clip(text, width))
}

// Scroll moves the full-screen body by delta rows, clamped to its bounds. It
// is what the host routes a wheel gesture to: the wheel reads, so it
// never changes the mode the way [enter] and [esc] do. Collapsed and expanded
// views have nothing to scroll and ignore it.
func (d *DiffView) Scroll(delta int) {
	if d.Mode != DiffFull || delta == 0 {
		return
	}
	d.scrollTo(d.Offset + delta)
}
