package components

// Review mode (docs/interface/surfaces.md#the-turns-close): one
// surface showing every file a turn touched with its hunks, so reviewing an
// agent's work is a pass over a list rather than a scroll through a
// transcript.
//
// The file list is on the left, the focused file's hunks on the right, and
// the turn's verdict pinned under the list — the failing test beside the
// hunks that claim to fix it. Nothing here renders a diff of its own: the
// hunk pane calls the same unifiedLines and sideBySideHunks the approval
// card's body, the transcript row and /diff go through, so there is one diff
// renderer and review is a layout around it.
//
// It is a reading and nothing else. There is no staging: a turn is taken
// back with /undo, and a file of it by asking for it, so the surface offers
// what reading needs — moving, paging, the paired layout — and a way out
// that changes nothing, and it says so on screen while it is up
// (docs/interface/surfaces.md#the-turns-close).

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// reviewStackWidth is the width below which the list and the hunk pane
	// stack instead of truncating each other.
	reviewStackWidth = 60
	// The file list's column budget in the two-pane layout: two fifths of
	// the surface, held between these bounds.
	reviewListMin = 24
	reviewListMax = 44
	// reviewDivider separates the panes; its width is counted out of the
	// hunk pane, never out of the list.
	reviewDivider = " │ "
	// reviewMinStatement is how much of a row's statement has to survive
	// before its right-aligned counts are worth keeping.
	reviewMinStatement = 8
	// reviewMinPane is the smallest hunk pane the stacked layout will leave
	// itself; the file list gives way before the hunks do.
	reviewMinPane = 4
)

// ReviewFile is one file in the review: its hunks and who wrote it.
type ReviewFile struct {
	Path  string
	Hunks []diff.Hunk
	// Agent names the sub-agent that authored the file; empty is the
	// session's own agent, which is not worth a column.
	Agent string
	// Syntax highlights this file's lines in the hunk pane; nil renders the
	// plain diff colors.
	Syntax Syntax
	// Mode states a change of permissions the file carries, already worded
	// by whoever knows the two modes. A file whose whole change is its mode
	// has no hunks and no counts, so this stands where they would be — a
	// row saying `+0 −0` about a real change is the reading being fixed —
	// and a file that changed content as well states it after them, because
	// an undo of that file puts the permissions back too.
	Mode string
}

// stats is the file's +N −M.
func (f ReviewFile) stats() (added, removed int) { return diff.Stats(f.Hunks) }

// ReviewVerdict is the turn's own verdict, pinned beside the files: what it
// ran to check its own work and what came back. Failed says the
// verdict in a field rather than leaving it to the glyph's color.
type ReviewVerdict struct {
	Failed bool
	// Label names what ran, e.g. `go test ./internal/agent/...`.
	Label string
	// Detail are the first lines of what it printed — the failure, where
	// there is one.
	Detail []string
}

// ReviewView is the takeover review surface. Like every component here it is
// plain state: the host owns it, routes keys to Update while it is up, and
// renders View every frame.
type ReviewView struct {
	// Title names what is being reviewed, e.g. "turn 7".
	Title string
	// Note is the header's right-hand note — the file count when it is
	// empty.
	Note  string
	Files []ReviewFile
	// Verdict is the test status pinned under the file list; nil when the
	// turn checked nothing.
	Verdict *ReviewVerdict
	// Shield is the standing note that review changes nothing, with an
	// optional second line saying what would put the work back.
	Shield, ShieldDetail string
	// Height is the surface's row budget, footer included.
	Height int
	// sideBySide forces the paired layout; it is automatic at
	// sideBySideMinWidth columns either way.
	sideBySide bool

	// file is the focused row of the list, hunk the focused hunk within it,
	// and Offset the first visible row of the hunk pane.
	file, hunk, Offset int

	// wide is the last render's automatic side-by-side verdict, taken from
	// the surface's own width rather than the hunk pane's: the layout
	// switches at the same terminal width the full-screen viewer does.
	wide bool
}

// SetSize gives the surface the terminal's rectangle. It lays itself out
// from the width it is rendered at, so only the height is kept.
func (v *ReviewView) SetSize(_, height int) { v.Height = height }

// Update handles keys while review has the screen. done reports that the
// reader has left it, which changes nothing.
func (v *ReviewView) Update(msg tea.KeyPressMsg) (done bool) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Review.Back):
		return true
	case keys.Is(pressed, keys.Review.MoveFile):
		v.moveFile(keys.Step(pressed, keys.Review.MoveFile))
	case keys.Is(pressed, keys.Review.MoveHunk):
		v.moveHunk(keys.Step(pressed, keys.Review.MoveHunk))
	case keys.Is(pressed, keys.Review.PageDown):
		v.Offset += max(v.paneHeight()-1, 1)
	case keys.Is(pressed, keys.Review.PageUp):
		v.Offset -= max(v.paneHeight()-1, 1)
	case keys.Is(pressed, keys.Review.SideBySide):
		v.sideBySide = !v.sideBySide
	}
	return false
}

// current is the focused file, or nil when there is nothing to review.
func (v *ReviewView) current() *ReviewFile {
	if v.file < 0 || v.file >= len(v.Files) {
		return nil
	}
	return &v.Files[v.file]
}

// moveFile moves the list cursor, resetting the hunk cursor and the pane
// scroll: a new file starts at its first hunk.
func (v *ReviewView) moveFile(delta int) {
	if len(v.Files) == 0 {
		return
	}
	v.file = min(max(v.file+delta, 0), len(v.Files)-1)
	v.hunk, v.Offset = 0, 0
}

// moveHunk moves the hunk cursor within the focused file, spilling into the
// neighbouring file at either end so n walks the whole review.
func (v *ReviewView) moveHunk(delta int) {
	f := v.current()
	if f == nil {
		return
	}
	next := v.hunk + delta
	switch {
	case next < 0:
		if v.file == 0 {
			v.hunk = 0
			return
		}
		v.moveFile(-1)
		if prev := v.current(); prev != nil {
			v.hunk = max(len(prev.Hunks)-1, 0)
		}
	case next >= len(f.Hunks):
		if v.file >= len(v.Files)-1 {
			v.hunk = max(len(f.Hunks)-1, 0)
			return
		}
		v.moveFile(1)
	default:
		v.hunk = next
	}
}

// View renders the surface at the given width.
func (v *ReviewView) View(width int) string {
	footer := v.footerRows(width)
	rows := max(v.Height-1-len(footer), 1)
	v.wide = width >= sideBySideMinWidth

	var body []string
	if width < reviewStackWidth {
		body = v.stackedBody(width, rows)
	} else {
		listWidth := min(max(width*2/5, reviewListMin), reviewListMax)
		paneWidth := max(width-listWidth-lipgloss.Width(reviewDivider), 8)
		body = joinReviewPanes(
			v.listRows(listWidth), v.paneRows(paneWidth, rows), listWidth, rows)
	}
	for len(body) < rows {
		body = append(body, "")
	}

	out := append(body[:rows:rows], sty.dim.Render(strings.Repeat("─", max(width, 0))))
	return strings.Join(append(out, footer...), "\n")
}

// paneHeight is roughly the hunk pane's row budget — the surface less its
// rule and footer. Page scrolling is the only caller, and the clamp in
// paneRows corrects an overshoot on the next frame.
func (v *ReviewView) paneHeight() int {
	return max(v.Height-2, 1)
}

// stackedBody is the narrow layout: the list above, the hunks below,
// nothing truncated sideways. The hunk pane keeps a floor — the list gives
// way to it, since the pane is what review is for — and the pinned rows go
// on last so the shield note is on screen at any height.
func (v *ReviewView) stackedBody(width, rows int) []string {
	list := append(v.headRows(width), v.fileRows(width)...)
	pinned := v.pinnedRows(width)

	// The rule between the list and the pane costs a row.
	avail := rows - len(pinned) - 1
	if avail < len(list)+1+reviewMinPane {
		// A short terminal drops the detail under the verdict and the shield
		// before it drops files or hunks.
		pinned = v.pinnedCompact(width)
		avail = rows - len(pinned) - 1
	}
	if avail < reviewMinPane+2 {
		// No room for both: what changed wins, since a surface that cannot
		// show a hunk can still say which files to look at.
		return truncRows(append(list, pinned...), rows, width)
	}
	pane := avail - len(list) - 1
	if pane < reviewMinPane {
		pane = reviewMinPane
		list = truncRows(list, avail-pane-1, width)
	}
	body := append(list, screenRule(width))
	body = append(body, v.paneRows(width, pane)...)
	return append(body, pinned...)
}

// truncRows bounds rows to limit, saying how many it swallowed rather than
// dropping them quietly.
func truncRows(rows []string, limit, width int) []string {
	if limit < 1 || len(rows) <= limit {
		return rows
	}
	keep := max(limit-1, 1)
	return append(rows[:keep:keep],
		sty.hint.Render(Clip(fmt.Sprintf("… (+%d more rows)", len(rows)-keep), width)))
}

// joinReviewPanes lays the list and the pane side by side, padding the list
// to its column so the divider is straight down the surface.
func joinReviewPanes(list, pane []string, listWidth, rows int) []string {
	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		var l, r string
		if i < len(list) {
			l = list[i]
		}
		if i < len(pane) {
			r = pane[i]
		}
		// The divider runs the full height of the surface, so the panes stay
		// framed rather than trailing off into blank rows.
		out = append(out, strings.TrimRight(padRight(l, listWidth)+sty.dim.Render(reviewDivider)+r, " "))
	}
	return out
}

// reviewLine lays out one list row: a statement on the left and a
// right-aligned note in what is left. The note is the row's counts, so a
// narrow column clips the statement rather than dropping them; only a column
// with no room for both at all loses the note.
func reviewLine(text, note string, width int) string {
	noteW := lipgloss.Width(note)
	if noteW == 0 || width-noteW-1 < reviewMinStatement {
		return Clip(text, width)
	}
	text = Clip(text, width-noteW-1)
	return padRight(text, width-noteW) + note
}

// listRows is the whole left pane: the file list, the verdict and the shield
// note, in that order.
func (v *ReviewView) listRows(width int) []string {
	rows := append(v.headRows(width), v.fileRows(width)...)
	return append(rows, v.pinnedRows(width)...)
}

// headRows are the list's header and the rule under it.
func (v *ReviewView) headRows(width int) []string {
	head := sty.info.Bold(true).Render("REVIEW")
	if v.Title != "" {
		head += sty.dim.Render(" " + v.Title)
	}
	return []string{reviewLine(head, v.countLabel(), width), screenRule(width)}
}

// fileRows are the files themselves: the mutation glyph,
// the path with whoever wrote it, and the file's own +N −M. The row the
// cursor is on is lit the way every list lights one — the ❯ in its own
// column outside the highlight, the row bright on the focus background —
// rather than by colouring the filename and leaving the ground under it
// unchanged (litrow.go).
func (v *ReviewView) fileRows(width int) []string {
	if len(v.Files) == 0 {
		return []string{sty.hint.Render("(nothing changed)")}
	}
	inner := max(width-GridPointerWidth, 1)
	rows := make([]string, 0, len(v.Files))
	for i, f := range v.Files {
		added, removed := f.stats()
		lead := sty.accent.Render("✎ ")
		note := DiffStat(added, removed)
		switch {
		case f.Mode != "" && len(f.Hunks) == 0:
			note = sty.dim.Render(f.Mode)
		case f.Mode != "":
			note += sty.dim.Render(" · " + f.Mode)
		}

		// A file list is read by its filenames, so a path that does not fit
		// loses its leading directories rather than its name, and the agent
		// that wrote it is what the row gives up first.
		tail := ""
		if f.Agent != "" {
			tail = " · " + f.Agent
		}
		budget := inner - lipgloss.Width(note) - 1 - lipgloss.Width(lead)
		if budget-lipgloss.Width(tail) < reviewMinStatement {
			tail = ""
		}
		path := clipLeft(f.Path, budget-lipgloss.Width(tail))
		row := reviewLine(lead+sty.body.Render(path)+sty.dim.Render(tail), note, inner)
		rows = append(rows, reviewRow(row, lipgloss.Width(lead), i == v.file, inner))
	}
	return rows
}

// reviewRow is how both of this surface's lists draw one row: the pointer's
// own column first, then the row — lit where the cursor is on it. marks is
// how much of the row keeps its own colours inside the highlight, counted
// by the caller rather than measured here.
//
// The marks keep their tones because they say what the row is; the
// highlight says only where the keyboard is. They are different facts, so
// the highlight is not allowed to answer either of them.
func reviewRow(row string, marks int, lit bool, width int) string {
	if !lit {
		return PointerColumn() + row
	}
	return sty.focusPointer.Render("❯") + " " + litRowKeeping(row, 0, marks, width)
}

// clipLeft trims s to width from the front, keeping its tail.
func clipLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return "…" + string(r[len(r)-(width-1):])
}

// pinnedRows are what sits under the file list whatever else is on screen:
// the turn's verdict, and the standing note that review commits nothing.
func (v *ReviewView) pinnedRows(width int) []string {
	return append(v.verdictRows(width), v.shieldRows(width)...)
}

// pinnedCompact is the same two blocks with their detail lines dropped —
// what a short terminal keeps. The verdict and the shield note themselves
// never go: they are the two claims the surface makes about itself.
func (v *ReviewView) pinnedCompact(width int) []string {
	verdict, shield := v.verdictRows(width), v.shieldRows(width)
	if len(verdict) > 2 {
		verdict = verdict[:2]
	}
	if len(shield) > 2 {
		shield = shield[:2]
	}
	return append(verdict, shield...)
}

// verdictRows are the turn's own verdict — what it ran to check itself and,
// where it failed, the first lines of what it said.
func (v *ReviewView) verdictRows(width int) []string {
	vd := v.Verdict
	if vd == nil {
		return nil
	}
	glyph, verdict := sty.add.Render("✓"), " passing"
	if vd.Failed {
		glyph, verdict = sty.del.Render("✗"), " failing"
	}
	rows := []string{screenRule(width), Clip(glyph+" "+sty.body.Render(vd.Label+verdict), width)}
	for _, d := range vd.Detail {
		rows = append(rows, "  "+sty.dimmer.Render(Clip(d, max(width-2, 0))))
	}
	return rows
}

// shieldRows are the standing note that nothing here is destructive. It is
// on screen the whole time review is, which is the point of it.
func (v *ReviewView) shieldRows(width int) []string {
	if v.Shield == "" {
		return nil
	}
	rows := []string{screenRule(width), Clip(sty.shield.Render("⛨ "+v.Shield), width)}
	if v.ShieldDetail != "" {
		rows = append(rows, "  "+sty.dim.Render(Clip(v.ShieldDetail, max(width-2, 0))))
	}
	return rows
}

// brightStyle is the weight a pane's own heading carries — the file the
// hunks under it belong to, the field a row is found by. The row a cursor is
// on is not one of them: that is the lit row, and a bright word inside a lit
// row would say the same thing twice.
func brightStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(Palette.Bright.Color())
}

// countLabel is the list header's right-hand note: the host's own, or how
// many files the review holds.
func (v *ReviewView) countLabel() string {
	if v.Note != "" {
		return sty.dim.Render(v.Note)
	}
	return sty.dim.Render(plural(len(v.Files), "file"))
}

// paneRows is the focused file's hunks, scrolled to keep the focused hunk on
// screen. The hunk bodies come from the shared renderer; only the hunk
// header, which carries the staging box and the cursor, is drawn here.
func (v *ReviewView) paneRows(width, rows int) []string {
	f := v.current()
	if f == nil {
		return []string{sty.hint.Render("(no file selected)")}
	}
	added, removed := f.stats()
	detail := sty.dim.Render("  "+plural(len(f.Hunks), "hunk")+" · ") + DiffStat(added, removed)
	switch {
	case f.Mode != "" && len(f.Hunks) == 0:
		detail = sty.dim.Render("  " + f.Mode)
	case f.Mode != "":
		detail += sty.dim.Render(" · " + f.Mode)
	}
	head := brightStyle().Render(f.Path) + detail

	body, focus := v.hunkRows(*f, width)
	// The pane follows the focused hunk: moving between hunks is how this
	// surface is read, so the row the pointer is on is brought in before the
	// window is taken.
	p := Pager{Offset: v.Offset, Height: max(rows-1, 1), total: len(body)}
	p.reveal(focus)
	visible := p.Window(body)
	v.Offset = p.Offset
	return append([]string{Clip(head, width)}, visible...)
}

// hunkRows renders the file's hunks and reports which row the focused hunk's
// header landed on, so the pane can scroll to it.
func (v *ReviewView) hunkRows(f ReviewFile, width int) (rows []string, focus int) {
	sbs := v.sideBySide || v.wide
	for i, h := range f.Hunks {
		var lines []string
		if sbs {
			lines = sideBySideHunks([]diff.Hunk{h}, width, f.Syntax)
		} else {
			lines = unifiedLines([]diff.Hunk{h}, width,
				unifiedOpts{lineNumbers: true, emphasis: true, syntax: f.Syntax})
		}
		if i == v.hunk {
			focus = len(rows)
		}
		rows = append(rows, v.hunkHeader(h, i, width))
		if len(lines) > 1 {
			// The shared renderer's own header is replaced by the row above;
			// its body is used verbatim, so review shows the same diff every
			// other surface does.
			rows = append(rows, lines[1:]...)
		}
	}
	if len(rows) == 0 {
		rows = append(rows, sty.hint.Render("(no changes)"))
	}
	return rows, focus
}

// hunkHeader is the hunk's own header row with the cursor in front of it.
// It is the file list's row drawn again in the other pane, so it is lit the
// same way — one surface with two lists is still one pointer.
func (v *ReviewView) hunkHeader(h diff.Hunk, i, width int) string {
	inner := max(width-GridPointerWidth, 1)
	row := sty.hunk.Render(Clip(h.Header(), inner))
	return reviewRow(row, 0, i == v.hunk, inner)
}

// footerRows are the keys the surface offers. Below reviewStackWidth the
// offers stack one per line rather than truncating.
func (v *ReviewView) footerRows(width int) []string {
	return packOffers([]TurnKey{
		keyOffer(keys.Review.MoveHunk),
		keyOfferAs(keys.Review.Back, "leave, change nothing"),
	}, width)
}

// Scroll moves the hunk pane by delta rows. The offset is clamped where it is
// read, so an overshoot here settles at the end of the pane rather than
// scrolling into nothing. It is the wheel's entry point, which is why
// it moves the pane and never the file or hunk selection.
func (v *ReviewView) Scroll(delta int) {
	v.Offset += delta
	if v.Offset < 0 {
		v.Offset = 0
	}
}
