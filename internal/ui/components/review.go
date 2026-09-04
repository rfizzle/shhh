package components

// Review mode (docs/interface/surfaces.md#the-turns-close): one
// surface showing every file a turn touched with its hunks, so reviewing an
// agent's work is a pass over a list rather than a scroll through a
// transcript.
//
// The file list is on the left with a staging box per file, the focused
// file's hunks on the right, and the turn's verdict pinned under the list —
// the failing test beside the hunks that claim to fix it. Nothing here
// renders a diff of its own: the hunk pane calls the same UnifiedLines and
// sideBySideHunks the approval card's body, the transcript row and /diff go
// through, so there is one diff renderer and review is a layout around it.
//
// Staging is a selection, never an action. The component reports what was
// staged when enter is pressed and the host decides what that means — a
// child's proposed patch to apply, or, for edits already on disk, what an
// undo would put back. Esc returns the selection nowhere: the surface is not
// destructive, and it says so on screen while it is up.

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

// ReviewFile is one file in the review: its hunks, who wrote it, and which
// of its hunks are staged.
type ReviewFile struct {
	Path  string
	Hunks []diff.Hunk
	// Agent names the sub-agent that authored the file; empty is the
	// session's own agent, which is not worth a column.
	Agent string
	// Staged is one flag per hunk. A shorter slice reads as unstaged, so a
	// caller that never touches it gets a review with nothing selected.
	Staged []bool
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

// stagedCount is how many of the file's hunks are staged.
func (f ReviewFile) stagedCount() int {
	n := 0
	for i := range f.Hunks {
		if i < len(f.Staged) && f.Staged[i] {
			n++
		}
	}
	return n
}

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

// ReviewSelection is one file's staged hunks, by index into ReviewView.Files
// and into that file's Hunks.
type ReviewSelection struct {
	File  int
	Hunks []int
}

// ReviewResult is the surface's Update result: the staged selection, or a
// cancel. Canceled means the user left with esc and nothing was chosen.
type ReviewResult struct {
	Canceled bool
	Staged   []ReviewSelection
}

// Files is how many files the selection covers.
func (r ReviewResult) Files() int { return len(r.Staged) }

// ReviewView is the takeover review surface. Like every component here it is
// plain state: the host owns it, routes keys to Update while it is up, and
// renders View every frame.
type ReviewView struct {
	// Title names what is being reviewed, e.g. "turn 7".
	Title string
	// Note is the header's right-hand note in a read-only review, where
	// there is no staged count to put there — the file count when it is
	// empty. A staging review always counts what is staged instead.
	Note  string
	Files []ReviewFile
	// Verdict is the test status pinned under the file list; nil when the
	// turn checked nothing.
	Verdict *ReviewVerdict
	// Shield is the standing note that review changes nothing, with an
	// optional second line saying what would put the work back.
	Shield, ShieldDetail string
	// ApplyVerb is what enter offers to do with the staged files ("apply" by
	// default); the host names the action it will actually take.
	ApplyVerb string
	// ReadOnly drops staging entirely: no boxes, no apply, just the files
	// and their hunks. It is what a cumulative diff wants, where there is
	// nothing to select.
	ReadOnly bool
	// Height is the surface's row budget, footer included.
	Height int
	// SideBySide forces the paired layout; it is automatic at
	// sideBySideMinWidth columns either way.
	SideBySide bool

	// File is the focused row of the list, Hunk the focused hunk within it,
	// and Offset the first visible row of the hunk pane.
	File, Hunk, Offset int

	// notice is a one-line answer to a key that could not do anything, e.g.
	// enter with nothing staged. It clears on the next key.
	notice string
	// wide is the last render's automatic side-by-side verdict, taken from
	// the surface's own width rather than the hunk pane's: the layout
	// switches at the same terminal width the full-screen viewer does.
	wide bool
}

// SetSize gives the surface the terminal's rectangle. It lays itself out
// from the width it is rendered at, so only the height is kept.
func (v *ReviewView) SetSize(_, height int) { v.Height = height }

// Update handles keys while review has the screen. done reports that the
// surface is finished, with a ReviewResult saying what was staged or that it
// was cancelled.
func (v *ReviewView) Update(msg tea.KeyPressMsg) (done bool, result ReviewResult) {
	v.notice = ""
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Review.Back):
		// Esc never applies and never destroys.
		return true, ReviewResult{Canceled: true}
	case pressed == "j", pressed == "down":
		v.moveFile(1)
	case pressed == "k", pressed == "up":
		v.moveFile(-1)
	case pressed == "n":
		v.moveHunk(1)
	case pressed == "p":
		v.moveHunk(-1)
	case keys.Is(pressed, keys.Review.PageDown):
		v.Offset += max(v.paneHeight()-1, 1)
	case keys.Is(pressed, keys.Review.PageUp):
		v.Offset -= max(v.paneHeight()-1, 1)
	case keys.Is(pressed, keys.Review.SideBySide):
		v.SideBySide = !v.SideBySide
	case keys.Is(pressed, keys.Review.StageHunk):
		v.stageHunk()
	case keys.Is(pressed, keys.Review.StageFile):
		v.stageFile()
	case keys.Is(pressed, keys.Review.StageAll):
		v.stageAll()
	case keys.Is(pressed, keys.Review.Apply):
		if v.ReadOnly {
			return true, ReviewResult{Canceled: true}
		}
		staged := v.selection()
		if len(staged) == 0 {
			v.notice = "nothing staged — " + keys.Shown(keys.Review.StageHunk) +
				" stages a hunk, " + keys.Shown(keys.Review.StageFile) + " a file, " +
				keys.Shown(keys.Review.StageAll) + " everything"
			return false, ReviewResult{}
		}
		return true, ReviewResult{Staged: staged}
	}
	return false, ReviewResult{}
}

// current is the focused file, or nil when there is nothing to review.
func (v *ReviewView) current() *ReviewFile {
	if v.File < 0 || v.File >= len(v.Files) {
		return nil
	}
	return &v.Files[v.File]
}

// moveFile moves the list cursor, resetting the hunk cursor and the pane
// scroll: a new file starts at its first hunk.
func (v *ReviewView) moveFile(delta int) {
	if len(v.Files) == 0 {
		return
	}
	v.File = min(max(v.File+delta, 0), len(v.Files)-1)
	v.Hunk, v.Offset = 0, 0
}

// moveHunk moves the hunk cursor within the focused file, spilling into the
// neighbouring file at either end so n walks the whole review.
func (v *ReviewView) moveHunk(delta int) {
	f := v.current()
	if f == nil {
		return
	}
	next := v.Hunk + delta
	switch {
	case next < 0:
		if v.File == 0 {
			v.Hunk = 0
			return
		}
		v.moveFile(-1)
		if prev := v.current(); prev != nil {
			v.Hunk = max(len(prev.Hunks)-1, 0)
		}
	case next >= len(f.Hunks):
		if v.File >= len(v.Files)-1 {
			v.Hunk = max(len(f.Hunks)-1, 0)
			return
		}
		v.moveFile(1)
	default:
		v.Hunk = next
	}
}

// ensureStaged sizes the focused file's staging slice so a file built
// without one can still be staged.
func (f *ReviewFile) ensureStaged() {
	for len(f.Staged) < len(f.Hunks) {
		f.Staged = append(f.Staged, false)
	}
}

// stageHunk toggles the hunk under the cursor.
func (v *ReviewView) stageHunk() {
	f := v.current()
	if v.ReadOnly || f == nil || v.Hunk >= len(f.Hunks) {
		return
	}
	f.ensureStaged()
	f.Staged[v.Hunk] = !f.Staged[v.Hunk]
}

// stageFile stages the whole focused file, or clears it when it is already
// wholly staged — the file-level counterpart of a all/none.
func (v *ReviewView) stageFile() {
	f := v.current()
	if v.ReadOnly || f == nil {
		return
	}
	f.ensureStaged()
	want := f.stagedCount() < len(f.Hunks)
	for i := range f.Staged {
		f.Staged[i] = want
	}
}

// stageAll flips the whole review between everything and nothing.
func (v *ReviewView) stageAll() {
	if v.ReadOnly {
		return
	}
	total, staged := 0, 0
	for _, f := range v.Files {
		total += len(f.Hunks)
		staged += f.stagedCount()
	}
	want := staged < total
	for i := range v.Files {
		v.Files[i].ensureStaged()
		for j := range v.Files[i].Staged {
			v.Files[i].Staged[j] = want
		}
	}
}

// selection is what enter reports: every file with at least one staged hunk,
// and which of its hunks those are.
func (v *ReviewView) selection() []ReviewSelection {
	var out []ReviewSelection
	for i, f := range v.Files {
		var hunks []int
		for j := range f.Hunks {
			if j < len(f.Staged) && f.Staged[j] {
				hunks = append(hunks, j)
			}
		}
		if len(hunks) > 0 {
			out = append(out, ReviewSelection{File: i, Hunks: hunks})
		}
	}
	return out
}

// stagedFiles is how many files have anything staged — the count enter
// offers to act on.
func (v *ReviewView) stagedFiles() int { return len(v.selection()) }

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

	out := append(body[:rows:rows], sty.Dim.Render(strings.Repeat("─", max(width, 0))))
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
		sty.Hint.Render(Clip(fmt.Sprintf("… (+%d more rows)", len(rows)-keep), width)))
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
		out = append(out, strings.TrimRight(padRight(l, listWidth)+sty.Dim.Render(reviewDivider)+r, " "))
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

// stageBox is the file's staging box: staged, partly staged, or not. The
// three differ as text, so color never carries which one it is (invariant 1).
func stageBox(staged, total int) string {
	switch {
	case total > 0 && staged == total:
		return sty.Add.Render("[x]")
	case staged > 0:
		return sty.Accent.Render("[~]")
	default:
		return sty.Dim.Render("[ ]")
	}
}

// listRows is the whole left pane: the file list, the verdict and the shield
// note, in that order.
func (v *ReviewView) listRows(width int) []string {
	rows := append(v.headRows(width), v.fileRows(width)...)
	return append(rows, v.pinnedRows(width)...)
}

// headRows are the list's header and the rule under it.
func (v *ReviewView) headRows(width int) []string {
	head := sty.Info.Bold(true).Render("REVIEW")
	if v.Title != "" {
		head += sty.Dim.Render(" " + v.Title)
	}
	return []string{reviewLine(head, v.stagedLabel(), width), screenRule(width)}
}

// fileRows are the files themselves: the staging box, the mutation glyph,
// the path with whoever wrote it, and the file's own +N −M.
func (v *ReviewView) fileRows(width int) []string {
	if len(v.Files) == 0 {
		return []string{sty.Hint.Render("(nothing changed)")}
	}
	rows := make([]string, 0, len(v.Files))
	for i, f := range v.Files {
		added, removed := f.stats()
		lead := " "
		if i == v.File {
			lead = sty.Info.Render("❯")
		}
		if !v.ReadOnly {
			lead += stageBox(f.stagedCount(), len(f.Hunks)) + " "
		}
		lead += sty.Accent.Render("✎ ")
		note := DiffStat(added, removed)
		switch {
		case f.Mode != "" && len(f.Hunks) == 0:
			note = sty.Dim.Render(f.Mode)
		case f.Mode != "":
			note += sty.Dim.Render(" · " + f.Mode)
		}

		// A file list is read by its filenames, so a path that does not fit
		// loses its leading directories rather than its name, and the agent
		// that wrote it is what the row gives up first.
		tail := ""
		if f.Agent != "" {
			tail = " · " + f.Agent
		}
		budget := width - lipgloss.Width(note) - 1 - lipgloss.Width(lead)
		if budget-lipgloss.Width(tail) < reviewMinStatement {
			tail = ""
		}
		path := clipLeft(f.Path, budget-lipgloss.Width(tail))
		name := sty.Body.Render(path)
		if i == v.File {
			name = brightStyle().Render(path)
		}
		rows = append(rows, reviewLine(lead+name+sty.Dim.Render(tail), note, width))
	}
	return rows
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
	glyph, verdict := sty.Add.Render("✓"), " passing"
	if vd.Failed {
		glyph, verdict = sty.Del.Render("✗"), " failing"
	}
	rows := []string{screenRule(width), Clip(glyph+" "+sty.Body.Render(vd.Label+verdict), width)}
	for _, d := range vd.Detail {
		rows = append(rows, "  "+sty.Dimmer.Render(Clip(d, max(width-2, 0))))
	}
	return rows
}

// shieldRows are the standing note that nothing here is destructive. It is
// on screen the whole time review is, which is the point of it.
func (v *ReviewView) shieldRows(width int) []string {
	if v.Shield == "" {
		return nil
	}
	rows := []string{screenRule(width), Clip(sty.Shield.Render("⛨ "+v.Shield), width)}
	if v.ShieldDetail != "" {
		rows = append(rows, "  "+sty.Dim.Render(Clip(v.ShieldDetail, max(width-2, 0))))
	}
	return rows
}

// brightStyle is the focused row's text: the one place the list says which
// row it is on with weight as well as a pointer.
func brightStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(Palette.Bright.Color())
}

// stagedLabel is the list header's right-hand note: how much of the review
// is selected. A read-only review has nothing to count.
func (v *ReviewView) stagedLabel() string {
	if v.ReadOnly {
		if v.Note != "" {
			return sty.Dim.Render(v.Note)
		}
		return sty.Dim.Render(plural(len(v.Files), "file"))
	}
	return sty.Add.Render(fmt.Sprintf("%d of %d staged", v.stagedFiles(), len(v.Files)))
}

// paneRows is the focused file's hunks, scrolled to keep the focused hunk on
// screen. The hunk bodies come from the shared renderer; only the hunk
// header, which carries the staging box and the cursor, is drawn here.
func (v *ReviewView) paneRows(width, rows int) []string {
	f := v.current()
	if f == nil {
		return []string{sty.Hint.Render("(no file selected)")}
	}
	added, removed := f.stats()
	detail := sty.Dim.Render("  "+plural(len(f.Hunks), "hunk")+" · ") + DiffStat(added, removed)
	switch {
	case f.Mode != "" && len(f.Hunks) == 0:
		detail = sty.Dim.Render("  " + f.Mode)
	case f.Mode != "":
		detail += sty.Dim.Render(" · " + f.Mode)
	}
	head := brightStyle().Render(f.Path) + detail
	if !v.ReadOnly {
		head += sty.Dim.Render(" · ") + v.fileStageLabel(*f)
	}

	body, focus := v.hunkRows(*f, width)
	// The pane follows the focused hunk: moving between hunks is how this
	// surface is read, so the row the pointer is on is brought in before the
	// window is taken.
	p := Pager{Offset: v.Offset, Height: max(rows-1, 1), Total: len(body)}
	p.Reveal(focus)
	visible := p.Window(body)
	v.Offset = p.Offset
	return append([]string{Clip(head, width)}, visible...)
}

// fileStageLabel says how much of the focused file is staged, in words as
// well as color.
func (v *ReviewView) fileStageLabel(f ReviewFile) string {
	switch staged := f.stagedCount(); {
	case len(f.Hunks) > 0 && staged == len(f.Hunks):
		return sty.Add.Render("✓ staged")
	case staged > 0:
		return sty.Accent.Render(fmt.Sprintf("~ %d of %d hunks staged", staged, len(f.Hunks)))
	default:
		return sty.Dim.Render("not staged")
	}
}

// hunkRows renders the file's hunks and reports which row the focused hunk's
// header landed on, so the pane can scroll to it.
func (v *ReviewView) hunkRows(f ReviewFile, width int) (rows []string, focus int) {
	sbs := v.SideBySide || v.wide
	for i, h := range f.Hunks {
		var lines []string
		if sbs {
			lines = sideBySideHunks([]diff.Hunk{h}, width)
		} else {
			lines = UnifiedLines([]diff.Hunk{h}, width,
				UnifiedOpts{LineNumbers: true, Emphasis: true, Syntax: f.Syntax})
		}
		if i == v.Hunk {
			focus = len(rows)
		}
		rows = append(rows, v.hunkHeader(f, i, h, width))
		if len(lines) > 1 {
			// The shared renderer's own header is replaced by the row above;
			// its body is used verbatim, so review shows the same diff every
			// other surface does.
			rows = append(rows, lines[1:]...)
		}
	}
	if len(rows) == 0 {
		rows = append(rows, sty.Hint.Render("(no changes)"))
	}
	return rows, focus
}

// hunkHeader is the hunk's own header row with the staging box and the
// cursor in front of it.
func (v *ReviewView) hunkHeader(f ReviewFile, i int, h diff.Hunk, width int) string {
	lead := " "
	if i == v.Hunk {
		lead = sty.Info.Render("❯")
	}
	if !v.ReadOnly {
		staged := 0
		if i < len(f.Staged) && f.Staged[i] {
			staged = 1
		}
		lead += stageBox(staged, 1) + " "
	} else {
		lead += " "
	}
	return lead + sty.Hunk.Render(Clip(h.Header(), max(width-lipgloss.Width(lead), 0)))
}

// footerRows are the keys the surface offers, plus any notice a key left
// behind. Below reviewStackWidth the offers stack one per line rather than
// truncating.
func (v *ReviewView) footerRows(width int) []string {
	verb := v.ApplyVerb
	if verb == "" {
		verb = "apply"
	}
	offers := []TurnKey{keyOffer(keys.Review.MoveHunk)}
	if !v.ReadOnly {
		offers = []TurnKey{
			keyOffer(keys.Review.StageHunk),
			keyOffer(keys.Review.StageFile),
			keyOffer(keys.Review.StageAll),
			keyOffer(keys.Review.MoveHunk),
			keyOfferAs(keys.Review.Apply, fmt.Sprintf("%s %s", verb, plural(v.stagedFiles(), "file"))),
		}
	}
	offers = append(offers, keyOfferAs(keys.Review.Back, "leave, change nothing"))

	rows := packOffers(offers, width)
	if v.notice != "" {
		rows = append(rows, sty.Warn.Render(Clip(v.notice, width)))
	}
	return rows
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
