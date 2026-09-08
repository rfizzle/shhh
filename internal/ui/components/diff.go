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
	// ModeChange states a change of permissions the file carries, already
	// worded by whoever knows the two modes. A file whose whole change is
	// its mode has no hunks to count, so this stands where the counts would
	// — a header reading `+0 −0 · 0 hunks` over a real change is a zero
	// nothing measured
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out)
	// — and a file that changed lines as well states it after them, because
	// putting that file back puts the permissions back too.
	ModeChange string
	// Allowed is the account of an edit that applied without the reader being
	// asked — `auto-allowed · auto mode`. It leads the stats in both
	// in-transcript forms, for the reason the activity row carries the same
	// field: an act and the approval of it are one row, not two. Empty on an
	// edit the reader approved at the card.
	Allowed string
	// Duration is how long the edit took, in the grid's own 6-column field.
	// An applied edit is an act like any other and its row says what the act
	// cost; empty renders blank, the way a call under the threshold does.
	Duration string
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

// DiffResult is the viewer's answer, and it carries nothing. The viewer
// walks between its three forms and scrolls; it decides nothing about the
// session, so `done` — the viewer was dismissed — is the whole of what it
// has to report. It is a type of its own rather than `any` so the viewer
// answers a key the way every other surface does (Keyed).
type DiffResult struct{}

// SetSize gives the viewer the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept — and the height it
// keeps is the full-screen budget rather than the bound on the inline body,
// which is the other number this type carries.
func (d *DiffView) SetSize(_, height int) { d.Height = height }

// Update handles keys while the viewer is focused. done reports that the
// viewer was dismissed (esc from the collapsed or expanded form). Esc from
// full screen steps back to the expanded view — esc never destroys.
func (d *DiffView) Update(msg tea.KeyPressMsg) (done bool, result DiffResult) {
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
		return false, DiffResult{}
	case keys.Is(pressed, keys.Diff.Back):
		if d.Mode == DiffFull {
			d.Mode = DiffExpanded
			return false, DiffResult{}
		}
		return true, DiffResult{}
	}
	if d.Mode != DiffFull {
		return false, DiffResult{}
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Diff.Scroll):
		d.scrollTo(d.Offset + keys.Step(pressed, keys.Diff.Scroll))
	case keys.Is(pressed, keys.Diff.Hunk):
		d.jumpHunk(keys.Step(pressed, keys.Diff.Hunk))
	case keys.Is(pressed, keys.Diff.SideBySide):
		d.SideBySide = !d.SideBySide
	}
	return false, DiffResult{}
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
// multi-file view counts files instead. It is plain text because it is
// measured before it is painted — paintCounts is what gives the two counts
// the tokens they carry on every other surface.
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
	if d.ModeChange != "" {
		if len(d.Hunks) == 0 {
			return d.ModeChange
		}
		return fmt.Sprintf("+%d −%d · %s · %s", adds, dels, plural(len(d.Hunks), "hunk"), d.ModeChange)
	}
	return fmt.Sprintf("+%d −%d · %s", adds, dels, plural(len(d.Hunks), "hunk"))
}

// row is the collapsed form as an ordinary activity row. An applied edit is
// an act the session took, so it is the same seven fields as the call that
// made it — pointer, mutation rail, ✎, the verb, the path, what it counted
// and how long it took (docs/interface/principles.md#one-grid).
//
// It used to be a shape of its own: a glyph and a path at column 0, with the
// stats pushed to the right edge and an expand key after them. That made the
// archetypal mutation the one row in the transcript with no rail on it, so
// the gutter a reader scrolls a long session by
// (docs/interface/principles.md#weight-tracks-risk) skipped exactly the acts
// they were scrolling to find. The expand key went with the shape: no other
// row advertises the press, and reading mode's own bar is where the offer
// belongs (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The account gives way to the path here the way it does on every other row,
// because it is the same field on the same grid rather than this view's own
// arithmetic saying the same thing a second time.
func (d *DiffView) row() ActivityRow {
	verb := d.Verb
	if verb == "" {
		verb = "edit"
	}
	return ActivityRow{
		Kind:     ActivityEdit,
		Verb:     verb,
		Target:   d.Path,
		Allowed:  d.Allowed,
		Counts:   d.statsLabel(),
		Duration: d.Duration,
	}
}

// RowView is the collapsed one-row transcript form.
func (d *DiffView) RowView(width int) string { return d.row().View(width) }

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
		rows = append(rows, sty.Hunk.Render(Clip(h.Header(), width)))
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

// ExpandedLines is the bounded in-transcript unified view: the row it was
// opened from, unchanged, and the change itself indented under it.
//
// The head is the collapsed row rather than a second rendering of the same
// facts, so opening an edit never moves it on the grid or loses what the
// closed row said. The body indents because that is what every detail body
// under a row does — it does not re-grid
// (docs/interface/principles.md#one-grid).
func (d *DiffView) ExpandedLines(width int) []string {
	body := max(d.MaxLines-1, 1)
	if d.MaxLines == 0 {
		body = 0
	}
	inner := max(width-detailIndent, 1)
	lines := []string{d.RowView(width)}
	for _, l := range UnifiedLines(d.Hunks, inner, UnifiedOpts{
		LineNumbers: true, Emphasis: true, MaxLines: body, Syntax: d.Syntax}) {
		lines = append(lines, strings.Repeat(" ", detailIndent)+l)
	}
	return lines
}

// kindStyles are the two styles a line of the given kind is drawn in: its own
// colour, and that colour over the intraline emphasis ground.
func kindStyles(kind diff.Kind) (style, emph lipgloss.Style) {
	switch kind {
	case diff.Add:
		return sty.Add, sty.AddEmph
	case diff.Del:
		return sty.Del, sty.DelEmph
	}
	return sty.Context, sty.Context
}

// emphSpan is the intraline span to tint on this line, or nil. Tab expansion
// shifts offsets, so only lines without tabs keep exact spans.
func emphSpan(l diff.Line) *diff.Span {
	if len(l.Emph) == 0 || strings.ContainsRune(l.Text, '\t') {
		return nil
	}
	return &l.Emph[0]
}

// renderUnifiedLine renders one diff line: marker, optional line number, text
// with tab expansion, syntax highlighting when available, and optional
// intraline emphasis.
func renderUnifiedLine(l diff.Line, width, numWidth int, opts UnifiedOpts) string {
	marker := " "
	switch l.Kind {
	case diff.Add:
		marker = "+"
	case diff.Del:
		marker = "-"
	}

	number := ""
	if numWidth > 0 {
		no := l.NewNo
		if l.Kind == diff.Del {
			no = l.OldNo
		}
		number = fmt.Sprintf(" %*d  ", numWidth, no)
	}

	var span *diff.Span
	if opts.Emphasis {
		span = emphSpan(l)
	}
	style, _ := kindStyles(l.Kind)
	head, avail := paintGutter(marker, number, style, width)
	return head + renderLineBody(l.Text, avail, l.Kind, span, opts.Syntax)
}

// renderLineBody renders one line's text in the columns the gutter left it:
// the register with the verdict layered over it where a highlighter is
// available (renderSyntaxBody), and the verdict alone otherwise. It is the
// body of a unified row and of one side-by-side cell alike, so the two
// layouts state the same verdict in the same colour and only the columns
// differ (docs/interface/surfaces.md#the-diff-view).
func renderLineBody(raw string, avail int, kind diff.Kind, span *diff.Span, syntax Syntax) string {
	text := strings.ReplaceAll(raw, "\t", "    ")
	if body, ok := renderSyntaxBody(text, avail, kind, span, syntax); ok {
		return body
	}
	style, emphStyle := kindStyles(kind)
	var b strings.Builder
	if span == nil {
		writeRun(&b, style, Clip(text, avail))
		return b.String()
	}

	// Clip on the raw runes first, then apply the emphasis span within the
	// visible part.
	r := []rune(text)
	clipped := false
	if avail > 0 && len(r) > avail {
		r = r[:avail-1]
		clipped = true
	}
	s := min(span.Start, len(r))
	e := max(min(span.End, len(r)), s)
	writeRun(&b, style, string(r[:s]))
	writeRun(&b, emphStyle, string(r[s:e]))
	writeRun(&b, style, string(r[e:]))
	if clipped {
		b.WriteString(style.Render("…"))
	}
	return b.String()
}

// writeRun appends one styled run, and nothing at all for an empty one — a
// style renders its escapes around no text otherwise, which costs the reader
// of a capture more than it costs the terminal.
func writeRun(b *strings.Builder, style lipgloss.Style, text string) {
	if text == "" {
		return
	}
	b.WriteString(style.Render(text))
}

// paintGutter draws a diff row's left edge and reports the columns left for
// the code. The marker carries the line's verdict, because that is the glyph
// the reading rests on
// (docs/interface/principles.md#colour-never-carries-meaning-alone); the line
// number beside it is chrome and carries Dim, so a number is never mistaken
// for a second statement about the line
// (docs/interface/surfaces.md#the-diff-view). A blank marker has no verdict
// to carry, and a pane cell has no marker at all — both draw the gutter as
// one run of chrome.
func paintGutter(marker, number string, style lipgloss.Style, width int) (string, int) {
	mark := Clip(marker, width)
	num := Clip(number, width-lipgloss.Width(mark))
	avail := width - lipgloss.Width(mark) - lipgloss.Width(num)
	var b strings.Builder
	if strings.TrimSpace(mark) == "" {
		writeRun(&b, sty.Dim, mark+num)
		return b.String(), avail
	}
	writeRun(&b, style, mark)
	writeRun(&b, sty.Dim, num)
	return b.String(), avail
}

// registerTone is how the syntax register and the diff's verdict layer
// (docs/interface/surfaces.md#the-diff-view). It answers, for one segment's
// tone on one kind of line, whether the register paints it or the line's own
// colour does.
//
// A changed line has to read as added or removed at a glance, and the
// register's receding rungs are exactly the tones that would take that
// reading away from it: ordinary text, the glue between the words and a
// comment are most of the characters on the line, and painting them grey
// leaves a green marker in front of a grey line. So on a changed line those
// three yield to the verdict, and the tones that name something — a keyword,
// a value, the identifiers a reader scans a diff for — stand inside it. A
// context line states no verdict, so it takes the register whole and an
// unclaimed span recedes to the context grey with the rest of the line.
func registerTone(t Token, kind diff.Kind) (Token, bool) {
	if t == (Token{}) {
		return Token{}, false
	}
	if kind == diff.Context {
		return t, true
	}
	switch t {
	case Palette.Body, Palette.Dimmer, Palette.Dim:
		return Token{}, false
	}
	return t, true
}

// renderSyntaxBody renders one line's text with the diff colouring layered
// over syntax highlighting (docs/interface/surfaces.md#the-diff-view): the
// line's verdict carries the ground, the register's naming tones stand inside
// it (registerTone), and the emphasis span gets a background tint so both
// survive. The gutter is not its business — paintGutter draws that — so this
// is the same body whether the row is a unified line or one side of a pair,
// which is what keeps the two depths and the two layouts agreeing.
//
// ok=false falls back to plain rendering when the segments don't reconstruct
// the line.
func renderSyntaxBody(text string, avail int, kind diff.Kind, span *diff.Span, syntax Syntax) (string, bool) {
	if syntax == nil {
		return "", false
	}
	// Syntax colours are declined in mono rather than stripped: the +/- diff
	// styling is already carrying the distinction that matters there.
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
		if tone, ok := registerTone(seg.Color, kind); ok {
			st = lipgloss.NewStyle().Foreground(tone.Color())
		}
		s, e := 0, 0
		if span != nil {
			s = min(max(span.Start-pos, 0), len(sr))
			e = min(max(span.End-pos, 0), len(sr))
		}
		if e > s {
			// The three runs are written only when they have something in
			// them: an empty one still renders its escapes, and a golden
			// fixture is read for its colour assignments.
			writeRun(&b, st, string(sr[:s]))
			writeRun(&b, st.Background(emphBg.Color()), string(sr[s:e]))
			writeRun(&b, st, string(sr[e:]))
		} else {
			writeRun(&b, st, string(sr))
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
	header := padRight(" "+d.Path, max(0, width-lipgloss.Width(d.statsLabel()))) + paintCounts(d.statsLabel(), sty.Dim)
	// Clipped to the screen it is drawn on: a key row wider than the
	// terminal wraps onto the body's last line and takes a line of the diff
	// with it, which costs the reader more than the last offer costs.
	footer := sty.Hint.Render(Clip("diff · "+strings.Join([]string{
		offer(keys.Diff.Scroll), offer(keys.Diff.Hunk),
		offer(keys.Diff.SideBySide), offer(keys.Diff.Back),
	}, " · "), width))

	p := Pager{Offset: d.Offset, Height: d.bodyHeight()}
	visible := p.Window(d.fullBody(width))
	d.Offset = p.Offset
	return p.Screen(header, visible, footer)
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
			rows = append(rows, Clip(sty.Accent.Render("─ "+sec.path)+"  "+sty.Dim.Render(fmt.Sprintf("+%d −%d", adds, dels)), width))
		}
		switch {
		case sec.binary:
			rows = append(rows, sty.Hint.Render("(binary file differs)"))
		case len(sec.hunks) == 0:
			rows = append(rows, sty.Hint.Render("(no textual changes)"))
		case sbs:
			rows = append(rows, sideBySideHunks(sec.hunks, width, sec.syntax)...)
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

// scrollTo holds a new full-screen offset inside the body and applies it.
func (d *DiffView) scrollTo(offset int) {
	d.Offset = Pager{Offset: offset, Height: d.bodyHeight(), Total: d.fullBodyLen()}.Held()
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
//
// It takes the same highlighter the unified body does, because which of the
// two layouts a reader is looking at is the terminal's width talking and the
// register is the file's (docs/interface/surfaces.md#the-diff-view). A diff
// that lost its syntax on the way past the side-by-side threshold would be
// two different objects wearing one name.
func sideBySideHunks(hunks []diff.Hunk, width int, syntax Syntax) []string {
	pane := max((width-3)/2, 8)
	divider := sty.Dim.Render(" │ ")
	var out []string
	for _, h := range hunks {
		out = append(out, sty.Hunk.Render(Clip(h.Header(), width)))
		for _, row := range pairHunkRows(h) {
			out = append(out, padRight(sideCell(row.old, pane, true, syntax), pane)+divider+sideCell(row.new, pane, false, syntax))
		}
	}
	return out
}

// sideCell renders one pane cell: the line number in the gutter and the text
// beside it, drawn by the same body the unified row uses — the cell's side
// says added or removed, the number is chrome, and the register names what it
// can inside the verdict.
func sideCell(l *diff.Line, width int, oldSide bool, syntax Syntax) string {
	if l == nil {
		return ""
	}
	no := l.NewNo
	if oldSide {
		no = l.OldNo
	}
	style, _ := kindStyles(l.Kind)
	// A pane has no marker column — which side a cell is on is what says
	// added or removed — so the gutter is the number alone.
	head, avail := paintGutter("", fmt.Sprintf("%4d  ", no), style, width)
	return head + renderLineBody(l.Text, avail, l.Kind, emphSpan(*l), syntax)
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

// LineChange renders one line's change as a single row: the line as it would
// read, and the line it replaces struck through beside it.
//
// It is the diff a proposal makes when the unit being decided on is a line
// rather than a hunk — a backlog item corrected against the tree, where each
// correction is accepted or declined on its own. Two rows would be the
// unified form, and a checkbox against two rows is a checkbox against
// neither; the strike is what lets the old text stay legible on the row the
// new text is offered on.
func LineChange(before, after string) string {
	before, after = strings.TrimSpace(before), strings.TrimSpace(after)
	switch {
	case after == "":
		return strikeStyle().Render(before)
	case before == "":
		return sty.Add.Render(after)
	}
	return sty.Add.Render(after) + sty.Dim.Render("  ← ") + strikeStyle().Render(before)
}

// strikeStyle is how a line being replaced is drawn: struck through and
// dim, so it reads as the text that was there rather than as text to read.
func strikeStyle() lipgloss.Style { return sty.Dim.Strikethrough(true) }

// DimText is body text at chrome weight, for a caller outside this package
// that is composing a row out of parts and has one of them to recede.
func DimText(s string) string { return sty.Dim.Render(s) }

// Toned renders one part of such a row in the tone a card field is read at.
// It is the same closed vocabulary a selector's own fields use, so a caller
// composing a row by hand cannot invent a fifth weight.
func Toned(t FieldTone, s string) string { return t.style().Render(s) }
