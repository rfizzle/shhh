package components

// The editor pane (docs/interface/surfaces.md#the-editor-pane): a file open
// over the feed, typed into.
//
// It is the second surface that takes typing and the first that is not the
// draft, so it keeps the draft's rule while it holds the keyboard: every
// keystroke a sentence produces is the file's, and the keys the pane answers
// are chords and esc
// (docs/interface/principles.md#a-surface-typed-into-keeps-the-drafts-rule).
// The movement and the line editing are the draft's own field's keys, read
// off the same keymap, so a person who can edit a sentence can edit a file.
//
// The buffer is the pane's own rather than a textarea's. The textarea turns a
// tab into four spaces as it takes it and stops at ten thousand lines, which
// is right for a sentence and wrong for a file: a save would rewrite every
// indented line of a Go file and cut a long one short, without a word about
// either. So the pane keeps the file's lines as they are and borrows only
// the keymap.
//
// What the pane does not do is write. It says what was asked — save, save
// and leave, leave, the key list — and the host writes through the write
// tool's own path, so the changeset records the file the way it records any
// edit.

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// EditorResult is what a key on the pane asks of the host.
type EditorResult int

const (
	// EditorTyping is a key the pane answered itself: a letter, a move, the
	// question opened or put away.
	EditorTyping EditorResult = iota
	// EditorSave writes the buffer and keeps the pane up.
	EditorSave
	// EditorSaveLeave writes the buffer and leaves once the write has
	// landed; a write that did not land keeps the pane and the buffer.
	EditorSaveLeave
	// EditorLeave hands the keyboard back with nothing written: the buffer
	// was the disk's, or the person said to discard it.
	EditorLeave
	// EditorKeys opens the pane's key list.
	EditorKeys
)

// Editor geometry, from the artboard: a six-column gutter (the mutation
// rail's mark, the four-digit number, a space), the ` │ ` divider and a
// thirty-six-column outline, which folds below the rail's rung.
const (
	editorNumberWidth  = 4
	editorOutlineWidth = 36
	editorDivider      = " │ "
	editorTabWidth     = 4
	// editorChromeRows is the header, the rule under it, the rule over the
	// foot row and the foot row.
	editorChromeRows = 4
)

// editorPos is a place in the buffer: a line, and a rune within it. col may
// equal the line's length, which is just past its last rune.
type editorPos struct{ row, col int }

func (p editorPos) before(q editorPos) bool {
	return p.row < q.row || (p.row == q.row && p.col < q.col)
}

// EditorPane is the pane. It is held by pointer, the way the screens are,
// because the host hands it the rectangle on every paint and reads back where
// its outline was drawn.
type EditorPane struct {
	// Path is the file as the header names it, relative to the workspace.
	Path string
	// Notice is one line the foot row carries in place of its keys until the
	// next key: what a save wrote, or why it did not.
	Notice string

	lines [][]rune
	disk  string
	cur   editorPos
	// anchor is where a selection began; selecting says one is open.
	anchor    editorPos
	selecting bool
	// asking is the question esc puts on the foot row over a buffer that
	// differs from the disk. The typing stops while it is up.
	asking bool
	// top is the first line drawn.
	top           int
	width, height int

	// The readings of the buffer that cost a pass over it, kept until the
	// next edit: which lines differ from the disk, and the outline.
	version   int
	readAt    int
	marks     map[int]bool
	modified  bool
	outline   []outlineEntry
	outlineOf string

	// outlineHits is where the last paint drew each outline entry, by row,
	// for a click to find.
	outlineHits map[int]int
	outlineX    int

	km textarea.KeyMap
}

// outlineEntry is one top-level declaration or heading, by the line it
// starts on.
type outlineEntry struct {
	line  int
	label string
	// name is what the folded header calls it: `runRound` for `func (l
	// *Loop) runRound`.
	name string
}

// NewEditorPane opens content as the file at path.
func NewEditorPane(path, content string) *EditorPane {
	p := &EditorPane{Path: path, disk: content, km: textarea.DefaultKeyMap(), readAt: -1}
	for _, l := range strings.Split(content, "\n") {
		p.lines = append(p.lines, []rune(l))
	}
	return p
}

// SetSize hands the pane its rectangle.
func (p *EditorPane) SetSize(width, height int) { p.width, p.height = width, height }

// Value is the buffer as the file it would write.
func (p *EditorPane) Value() string {
	parts := make([]string, len(p.lines))
	for i, l := range p.lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}

// Modified reports that the buffer differs from the disk as the pane last
// read or wrote it.
func (p *EditorPane) Modified() bool {
	p.read()
	return p.modified
}

// Asking reports that the leave question is up.
func (p *EditorPane) Asking() bool { return p.asking }

// Saved is the host saying the buffer is on the disk now, and what the save
// said. The marks go with it: a line is the person's change until it is
// written.
func (p *EditorPane) Saved(content, notice string) {
	p.disk = content
	p.Notice = notice
	p.asking = false
	p.version++
}

// LineCount is how many lines the file has: a final newline ends the last
// line rather than starting another.
func (p *EditorPane) LineCount() int {
	n := len(p.lines)
	if n > 1 && len(p.lines[n-1]) == 0 {
		n--
	}
	return n
}

// Cursor is where the cursor stands, one-based line and display column, the
// way the foot row states it.
func (p *EditorPane) Cursor() (line, col int) {
	return p.cur.row + 1, displayCol(p.lines[p.cur.row], p.cur.col) + 1
}

// Update answers one key. done reports that the pane is finished with the
// keyboard; a save that is to leave reports EditorSaveLeave without done,
// because whether it leaves is the write's to decide.
func (p *EditorPane) Update(msg tea.KeyPressMsg) (done bool, result EditorResult) {
	p.Notice = ""
	if p.asking {
		switch {
		case keys.Match(msg, keys.Editor.Discard):
			return true, EditorLeave
		case keys.Match(msg, keys.Editor.SaveLeave):
			// The question is answered either way: a write that lands
			// leaves, and one that does not is said on the foot row over
			// the buffer it kept.
			p.asking = false
			return false, EditorSaveLeave
		case keys.Match(msg, keys.Editor.Keep):
			p.asking = false
		}
		// Anything else is not typed while the question is up: the reader
		// is being asked something, and a letter that went into the file
		// behind the question would be a change made without seeing it.
		return false, EditorTyping
	}
	switch {
	case keys.Match(msg, keys.Editor.Save):
		return false, EditorSave
	case keys.Match(msg, keys.Draft.KeyList):
		return false, EditorKeys
	case keys.Match(msg, keys.Editor.Back):
		if p.Modified() {
			p.asking = true
			return false, EditorTyping
		}
		return true, EditorLeave
	}
	p.edit(msg)
	return false, EditorTyping
}

// edit is a key that changes the buffer or moves in it.
func (p *EditorPane) edit(msg tea.KeyPressMsg) {
	km := p.km
	switch {
	case keys.Match(msg, km.SelectCharacterForward):
		p.extend(func() { p.right() })
	case keys.Match(msg, km.SelectCharacterBackward):
		p.extend(func() { p.left() })
	case keys.Match(msg, km.SelectWordForward):
		p.extend(func() { p.wordRight() })
	case keys.Match(msg, km.SelectWordBackward):
		p.extend(func() { p.wordLeft() })
	case keys.Match(msg, km.SelectLineUp):
		p.extend(func() { p.vertical(-1) })
	case keys.Match(msg, km.SelectLineDown):
		p.extend(func() { p.vertical(1) })
	case keys.Match(msg, km.SelectAll):
		p.anchor, p.selecting = editorPos{}, true
		last := len(p.lines) - 1
		p.cur = editorPos{last, len(p.lines[last])}
	case keys.Match(msg, km.CharacterForward):
		p.move(func() { p.right() })
	case keys.Match(msg, km.CharacterBackward):
		p.move(func() { p.left() })
	case keys.Match(msg, km.WordForward):
		p.move(func() { p.wordRight() })
	case keys.Match(msg, km.WordBackward):
		p.move(func() { p.wordLeft() })
	case keys.Match(msg, km.LineNext):
		p.move(func() { p.vertical(1) })
	case keys.Match(msg, km.LinePrevious):
		p.move(func() { p.vertical(-1) })
	case keys.Match(msg, km.LineStart):
		p.move(func() { p.cur.col = 0 })
	case keys.Match(msg, km.LineEnd):
		p.move(func() { p.cur.col = len(p.lines[p.cur.row]) })
	case keys.Match(msg, km.PageUp):
		p.move(func() { p.vertical(-p.bodyRows()) })
	case keys.Match(msg, km.PageDown):
		p.move(func() { p.vertical(p.bodyRows()) })
	case keys.Match(msg, km.InputBegin):
		p.move(func() { p.cur = editorPos{} })
	case keys.Match(msg, km.InputEnd):
		p.move(func() {
			last := len(p.lines) - 1
			p.cur = editorPos{last, len(p.lines[last])}
		})
	case keys.Match(msg, km.InsertNewline):
		p.insert([]rune{'\n'})
	case keys.Match(msg, km.DeleteCharacterBackward):
		if !p.deleteSelection() {
			p.deleteRange(p.stepBack(p.cur), p.cur)
		}
	case keys.Match(msg, km.DeleteCharacterForward):
		if !p.deleteSelection() {
			p.deleteRange(p.cur, p.stepOn(p.cur))
		}
	case keys.Match(msg, km.DeleteWordBackward):
		if !p.deleteSelection() {
			end := p.cur
			p.wordLeft()
			p.deleteRange(p.cur, end)
		}
	case keys.Match(msg, km.DeleteWordForward):
		if !p.deleteSelection() {
			start := p.cur
			p.wordRight()
			end := p.cur
			p.cur = start
			p.deleteRange(start, end)
		}
	case keys.Match(msg, km.DeleteAfterCursor):
		if !p.deleteSelection() {
			p.deleteRange(p.cur, editorPos{p.cur.row, len(p.lines[p.cur.row])})
		}
	case keys.Match(msg, km.DeleteBeforeCursor):
		if !p.deleteSelection() {
			p.deleteRange(editorPos{p.cur.row, 0}, p.cur)
		}
	case msg.Code == tea.KeyTab && msg.Mod == 0:
		p.insert([]rune{'\t'})
	default:
		// A keystroke a sentence produces, or a paste carrying a run of
		// them. A chord carries no text, and one this pane does not answer
		// changes nothing.
		if msg.Text == "" || msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModSuper) != 0 {
			return
		}
		p.insert(bufferRunes(msg.Text))
	}
}

// bufferRunes is text as the buffer takes it: line breaks as one newline
// whatever the terminal sent, tabs kept, and no other control character.
func bufferRunes(text string) []rune {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	out := make([]rune, 0, len(text))
	for _, r := range text {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			out = append(out, r)
		}
	}
	return out
}

// move runs a movement with any selection put away, and extend runs one that
// carries the selection with it.
func (p *EditorPane) move(f func()) {
	p.selecting = false
	f()
}

func (p *EditorPane) extend(f func()) {
	if !p.selecting {
		p.anchor, p.selecting = p.cur, true
	}
	f()
}

func (p *EditorPane) right() { p.cur = p.stepOn(p.cur) }
func (p *EditorPane) left()  { p.cur = p.stepBack(p.cur) }

// stepOn and stepBack are one rune on and back, across a line's end.
func (p *EditorPane) stepOn(at editorPos) editorPos {
	if at.col < len(p.lines[at.row]) {
		return editorPos{at.row, at.col + 1}
	}
	if at.row < len(p.lines)-1 {
		return editorPos{at.row + 1, 0}
	}
	return at
}

func (p *EditorPane) stepBack(at editorPos) editorPos {
	if at.col > 0 {
		return editorPos{at.row, at.col - 1}
	}
	if at.row > 0 {
		return editorPos{at.row - 1, len(p.lines[at.row-1])}
	}
	return at
}

// wordRight and wordLeft move over the spaces and then the word.
func (p *EditorPane) wordRight() {
	line := p.lines[p.cur.row]
	if p.cur.col >= len(line) {
		p.right()
		return
	}
	for p.cur.col < len(line) && unicode.IsSpace(line[p.cur.col]) {
		p.cur.col++
	}
	for p.cur.col < len(line) && !unicode.IsSpace(line[p.cur.col]) {
		p.cur.col++
	}
}

func (p *EditorPane) wordLeft() {
	if p.cur.col == 0 {
		p.left()
		return
	}
	line := p.lines[p.cur.row]
	for p.cur.col > 0 && unicode.IsSpace(line[p.cur.col-1]) {
		p.cur.col--
	}
	for p.cur.col > 0 && !unicode.IsSpace(line[p.cur.col-1]) {
		p.cur.col--
	}
}

// vertical moves n lines, keeping the display column where the line is long
// enough to have it.
func (p *EditorPane) vertical(n int) {
	col := displayCol(p.lines[p.cur.row], p.cur.col)
	row := min(max(p.cur.row+n, 0), len(p.lines)-1)
	p.cur = editorPos{row, runeAtCol(p.lines[row], col)}
}

// selection is the open selection in buffer order, and false where there is
// none or it covers nothing.
func (p *EditorPane) selection() (start, end editorPos, ok bool) {
	if !p.selecting || p.anchor == p.cur {
		return editorPos{}, editorPos{}, false
	}
	start, end = p.anchor, p.cur
	if end.before(start) {
		start, end = end, start
	}
	return start, end, true
}

func (p *EditorPane) deleteSelection() bool {
	start, end, ok := p.selection()
	p.selecting = false
	if !ok {
		return false
	}
	p.deleteRange(start, end)
	return true
}

// deleteRange takes out the text from start to end and leaves the cursor at
// start.
func (p *EditorPane) deleteRange(start, end editorPos) {
	if !start.before(end) {
		return
	}
	head := p.lines[start.row][:start.col]
	tail := p.lines[end.row][end.col:]
	joined := append(append([]rune{}, head...), tail...)
	p.lines = append(append(p.lines[:start.row:start.row], joined), p.lines[end.row+1:]...)
	p.cur = start
	p.version++
}

// insert puts runes at the cursor, over the selection where there is one.
func (p *EditorPane) insert(rs []rune) {
	if len(rs) == 0 {
		return
	}
	p.deleteSelection()
	line := p.lines[p.cur.row]
	head := append([]rune{}, line[:p.cur.col]...)
	tail := append([]rune{}, line[p.cur.col:]...)
	var added [][]rune
	cur := head
	for _, r := range rs {
		if r == '\n' {
			added = append(added, cur)
			cur = nil
			continue
		}
		cur = append(cur, r)
	}
	col := len(cur)
	added = append(added, append(cur, tail...))
	rest := append([][]rune{}, p.lines[p.cur.row+1:]...)
	p.lines = append(append(p.lines[:p.cur.row], added...), rest...)
	p.cur = editorPos{p.cur.row + len(added) - 1, col}
	p.version++
}

// read refreshes what costs a pass over the buffer, once per edit.
func (p *EditorPane) read() {
	if p.readAt == p.version {
		return
	}
	p.readAt = p.version
	value := p.Value()
	p.modified = value != p.disk
	p.marks = map[int]bool{}
	if p.modified {
		for _, h := range diff.Compute(p.disk, value) {
			for _, l := range h.Lines {
				if l.Kind == diff.Add && l.NewNo > 0 {
					p.marks[l.NewNo-1] = true
				}
			}
		}
	}
	if p.outlineOf != value {
		p.outline, p.outlineOf = outlineOf(p.Path, p.lines), value
	}
}

// bodyRows is how many rows the code takes: the rectangle less the chrome,
// and the folded header's second line where the outline has folded.
func (p *EditorPane) bodyRows() int {
	if p.height <= 0 {
		return max(len(p.lines), 1)
	}
	rows := p.height - editorChromeRows
	if p.folded(p.width) {
		rows--
	}
	return max(rows, 1)
}

// folded reports that the outline has no column of its own at this width:
// below the rail's rung, or for a file with nothing to outline.
func (p *EditorPane) folded(width int) bool {
	p.read()
	return width < InspectorMinContentWidth || len(p.outline) == 0
}

// gutterWidth is the rail mark, the number and the space after it. The
// number takes four columns until the file is longer than that.
func (p *EditorPane) gutterWidth() int {
	return 1 + max(editorNumberWidth, len(fmt.Sprint(len(p.lines)))) + 1
}

// codeWidth is the columns the text wraps to.
func (p *EditorPane) codeWidth(width int) int {
	w := width - p.gutterWidth()
	if !p.folded(width) {
		w -= len([]rune(editorDivider)) + editorOutlineWidth
	}
	return max(w, 1)
}

// View draws the pane into the rectangle SetSize gave it.
func (p *EditorPane) View(width int) string {
	p.read()
	folded := p.folded(width)
	rows := []string{p.headerRow(width, folded)}
	if folded {
		rows = append(rows, sty.dim.Render(Clip(p.whereLine(), width)))
	}
	rows = append(rows, titleRule(width))
	body := p.bodyRows()
	code := p.codeRows(width, body)
	if !folded {
		code = p.besideOutline(code, width, body, len(rows))
	} else {
		p.outlineHits = nil
		// A short file still ends the pane at the foot of its rectangle,
		// so the keys sit where they sit for a long one.
		for p.height > 0 && len(code) < body {
			code = append(code, "")
		}
	}
	rows = append(rows, code...)
	rows = append(rows, screenRule(width), p.footRow(width, folded))
	return strings.Join(rows, "\n")
}

// headerRow is the command and the file, the line count where there is room
// and `modified` in the accent while the buffer differs from the disk; the
// keys at its right. Folded, the file is its name alone, because the line
// under the header names its directory.
func (p *EditorPane) headerRow(width int, folded bool) string {
	name := p.Path
	if folded {
		name = filepath.Base(p.Path)
	}
	left := []RailSegment{{Text: brightStyle().Render("/edit") + sty.body.Render(" "+name), Drop: RailKeep}}
	if !folded {
		left = append(left, screenField(plural(p.LineCount(), "line")))
	}
	if p.modified {
		left = append(left, RailSegment{Text: sty.dim.Render(" · ") + sty.accent.Render("modified"), Drop: RailVital})
	}
	back := words(keys.Editor.Back, "back")
	right := words(keys.Draft.KeyList, "keys") + " · " + back
	if folded {
		right = back
	}
	return screenHeader{left: left, keys: right}.row(width)
}

// whereLine is the folded header's second line: the declaration the cursor
// is in, and the file's directory.
func (p *EditorPane) whereLine() string {
	var parts []string
	if e, ok := p.current(); ok {
		parts = append(parts, "in "+e.name)
	}
	if dir := filepath.Dir(p.Path); dir != "." && dir != "" {
		parts = append(parts, filepath.ToSlash(dir)+"/")
	}
	return strings.Join(parts, " · ")
}

// current is the outline entry the cursor is in: the last one that starts at
// or above it.
func (p *EditorPane) current() (outlineEntry, bool) {
	at := -1
	for i, e := range p.outline {
		if e.line <= p.cur.row {
			at = i
		}
	}
	if at < 0 {
		return outlineEntry{}, false
	}
	return p.outline[at], true
}

// segment is one row of a wrapped line: the runes [start, end) of it.
type segment struct{ start, end int }

// widths is each rune's columns on screen, a tab to the next stop.
func widths(line []rune) []int {
	ws := make([]int, len(line))
	col := 0
	for i, r := range line {
		switch {
		case r == '\t':
			ws[i] = editorTabWidth - col%editorTabWidth
		case unicode.IsControl(r):
			ws[i] = 0
		default:
			ws[i] = ansi.StringWidth(string(r))
		}
		col += ws[i]
	}
	return ws
}

// displayCol is the screen column a rune index starts at.
func displayCol(line []rune, col int) int {
	ws := widths(line)
	at := 0
	for i := 0; i < col && i < len(ws); i++ {
		at += ws[i]
	}
	return at
}

// runeAtCol is the rune index a screen column lands on.
func runeAtCol(line []rune, col int) int {
	at := 0
	for i, w := range widths(line) {
		if at+w > col {
			return i
		}
		at += w
	}
	return len(line)
}

// wrap cuts a line into rows of width columns, never inside a rune. The
// cursor's line gets a row past its end where the cursor stands after a full
// last row, so it is never drawn off the edge.
func wrap(line []rune, width int, cursorAtEnd bool) ([]segment, []int) {
	ws := widths(line)
	var segs []segment
	start, used := 0, 0
	for i, w := range ws {
		if used+w > width && i > start {
			segs = append(segs, segment{start, i})
			start, used = i, 0
		}
		used += w
	}
	segs = append(segs, segment{start, len(line)})
	if cursorAtEnd && used >= width && len(line) > 0 {
		segs = append(segs, segment{len(line), len(line)})
	}
	return segs, ws
}

// visible reports a line the pane draws. A file's final newline ends its
// last line, so the empty line after it is drawn only once the cursor is on
// it or something has been typed there.
func (p *EditorPane) visible(i int) bool {
	last := len(p.lines) - 1
	return i != last || last == 0 || len(p.lines[last]) > 0 || p.cur.row == last
}

// follow moves the first line drawn so the cursor's row is on screen.
func (p *EditorPane) follow(width, rows int) {
	if p.cur.row < p.top {
		p.top = p.cur.row
	}
	cw := p.codeWidth(width)
	for p.top < p.cur.row {
		used := 0
		for i := p.top; i <= p.cur.row; i++ {
			segs, _ := wrap(p.lines[i], cw, i == p.cur.row && p.cur.col == len(p.lines[i]))
			used += len(segs)
		}
		if used <= rows {
			break
		}
		p.top++
	}
}

// codeRows draws rows of gutter and text, from the first line drawn.
func (p *EditorPane) codeRows(width, rows int) []string {
	p.follow(width, rows)
	cw := p.codeWidth(width)
	numW := p.gutterWidth() - 2
	selStart, selEnd, selOK := p.selection()
	comment := commentMark(p.Path)
	var out []string
	for i := p.top; i < len(p.lines) && len(out) < rows; i++ {
		if !p.visible(i) {
			break
		}
		line := p.lines[i]
		atEnd := i == p.cur.row && p.cur.col == len(line)
		segs, ws := wrap(line, cw, atEnd)
		mark := " "
		if p.marks[i] {
			mark = sty.add.Render("▎")
		}
		tone := sty.body
		if comment != "" && strings.HasPrefix(strings.TrimSpace(string(line)), comment) {
			tone = sty.dim
		}
		for n, s := range segs {
			if len(out) >= rows {
				break
			}
			num := strings.Repeat(" ", numW)
			if n == 0 {
				num = fmt.Sprintf("%*d", numW, i+1)
				if i == p.cur.row {
					num = sty.bright.Render(num)
				} else {
					num = sty.dimmer.Render(num)
				}
			}
			var b strings.Builder
			used := 0
			run, runTone := "", toneText
			flush := func() {
				if run != "" {
					b.WriteString(runTone.style(tone).Render(run))
					run = ""
				}
			}
			for j := s.start; j < s.end; j++ {
				pos := editorPos{i, j}
				t := toneText
				if selOK && !pos.before(selStart) && pos.before(selEnd) {
					t = toneSelected
				}
				if pos == p.cur {
					t = toneCursor
				}
				text := string(line[j])
				switch {
				case line[j] == '\t':
					text = strings.Repeat(" ", ws[j])
				case ws[j] == 0:
					text = ""
				}
				if t != runTone {
					flush()
				}
				run += text
				runTone = t
				used += ws[j]
			}
			flush()
			if atEnd && n == len(segs)-1 {
				b.WriteString(editorCursor().Render(" "))
				used++
			}
			row := mark + num + " " + b.String()
			if !p.folded(width) && used < cw {
				row += strings.Repeat(" ", cw-used)
			}
			out = append(out, row)
		}
	}
	return out
}

// cellTone is how one cell of code is drawn: in the line's own tone, inside
// the selection, or under the cursor.
type cellTone int

const (
	toneText cellTone = iota
	toneSelected
	toneCursor
)

func (t cellTone) style(line lipgloss.Style) lipgloss.Style {
	switch t {
	case toneSelected:
		return sty.litText
	case toneCursor:
		return editorCursor()
	}
	return line
}

// editorCursor is the cell the cursor stands on, drawn rather than left to the
// terminal: the pane is one surface among several on the screen, and the
// terminal's own cursor belongs to whichever field is being typed into at the
// frame's foot.
func editorCursor() lipgloss.Style { return sty.bright.Reverse(true) }

// besideOutline puts the outline at the right of the code rows: the heading
// and its count, an entry a row with `▸` on the cursor's, and the sentence
// that says a click moves the cursor. top is the row the body starts on in
// the pane, which is where a click is measured from.
func (p *EditorPane) besideOutline(code []string, width, rows, top int) []string {
	cw := p.codeWidth(width)
	blank := strings.Repeat(" ", p.gutterWidth()+cw)
	for len(code) < rows {
		code = append(code, blank)
	}
	cur, inOne := p.current()
	count := plural(len(p.outline), "declaration")
	if strings.EqualFold(filepath.Ext(p.Path), ".md") || strings.EqualFold(filepath.Ext(p.Path), ".markdown") {
		count = plural(len(p.outline), "heading")
	}
	col := []string{Clip(sty.info.Bold(true).Render("OUTLINE")+" "+sty.dim.Render(count), editorOutlineWidth)}
	// The entries take what the heading and the sentence under them leave;
	// past that the window slides to keep the cursor's entry in it.
	room := max(rows-3, 1)
	first := 0
	for i, e := range p.outline {
		if inOne && e.line == cur.line && i >= room {
			first = i - room + 1
		}
	}
	p.outlineHits = map[int]int{}
	p.outlineX = p.gutterWidth() + cw + len([]rune(editorDivider))
	for i := first; i < len(p.outline) && len(col) < rows && i-first < room; i++ {
		e := p.outline[i]
		marker, label := " ", sty.dim.Render(Clip(e.label, editorOutlineWidth-1))
		if inOne && e.line == cur.line {
			marker = sty.info.Render("▸")
			label = sty.bright.Render(Clip(e.label, editorOutlineWidth-1))
		}
		p.outlineHits[top+len(col)] = e.line
		col = append(col, marker+label)
	}
	if len(col)+2 <= rows {
		col = append(col, "", sty.dimmer.Render(Clip("a click moves the cursor there", editorOutlineWidth)))
	}
	divider := sty.dim.Render(editorDivider)
	for i := range code {
		code[i] += divider
		if i < len(col) {
			code[i] += col[i]
		}
	}
	return code
}

// OutlineAt is the line an outline entry drawn at row of the pane starts on,
// for a click: x and y are cells inside the pane's rectangle.
func (p *EditorPane) OutlineAt(x, y int) (int, bool) {
	if p.outlineHits == nil || x < p.outlineX {
		return 0, false
	}
	line, ok := p.outlineHits[y]
	return line, ok
}

// GoTo puts the cursor at the start of a line, which is what a click on the
// outline does. It never takes the keyboard: the pane already has it.
func (p *EditorPane) GoTo(line int) {
	p.selecting = false
	p.cur = editorPos{min(max(line, 0), len(p.lines)-1), 0}
}

// footRow is the keys and where the cursor stands, or the question esc asked,
// or what the last save said.
func (p *EditorPane) footRow(width int, folded bool) string {
	line, col := p.Cursor()
	long, short := fmt.Sprintf("ln %d · col %d", line, col), fmt.Sprintf("%d:%d", line, col)
	type form struct{ left, at string }
	var forms []form
	switch {
	case p.asking:
		forms = []form{
			{sty.accent.Render("leave without saving?") + " " + keyOffers([]KeyOffer{
				OfferAs(keys.Editor.Discard, "discard the change"),
				OfferAs(keys.Editor.SaveLeave, "save and leave"),
				OfferAs(keys.Editor.Keep, "keep editing"),
			}), long},
			{sty.accent.Render("leave unsaved?") + " " + keyOffers([]KeyOffer{
				OfferAs(keys.Editor.Discard, "discard"),
				OfferAs(keys.Editor.Keep, "keep editing"),
			}), short},
		}
	case p.Notice != "":
		forms = []form{{sty.dim.Render(p.Notice), long}, {sty.dim.Render(p.Notice), short}}
	default:
		forms = []form{
			{keyOffers([]KeyOffer{OfferAs(keys.Editor.Save, "save"), OfferAs(keys.Editor.Back, "back to the prompt")}), long},
			{keyOffers([]KeyOffer{OfferAs(keys.Editor.Save, "save"), OfferAs(keys.Editor.Back, "back")}), short},
		}
	}
	if folded {
		// The narrow form first where the outline has folded: the artboard's
		// 60 columns state the position short whether or not the long one
		// would fit.
		forms = forms[1:]
	}
	for _, f := range forms {
		at := sty.dim.Render(f.at)
		if pad := width - lipgloss.Width(f.left) - lipgloss.Width(at); pad >= 2 {
			return f.left + strings.Repeat(" ", pad) + at
		}
	}
	last := forms[len(forms)-1]
	return Clip(last.left, width)
}

// commentMark is how a whole-line comment opens in this file's language, ""
// where the pane does not know. Comments are drawn dim and everything else
// plain, as every code block in the product is.
func commentMark(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".c", ".h", ".cc", ".cpp", ".hpp", ".java", ".js", ".jsx", ".ts", ".tsx",
		".rs", ".swift", ".kt", ".scala", ".cs", ".proto", ".dart", ".zig":
		return "//"
	case ".py", ".sh", ".bash", ".zsh", ".rb", ".yaml", ".yml", ".toml", ".pl", ".r", ".mk", ".conf":
		return "#"
	case ".sql", ".lua", ".hs":
		return "--"
	}
	switch filepath.Base(path) {
	case "Makefile", "Dockerfile":
		return "#"
	}
	return ""
}

// outlineOf is a file's top-level declarations, or a document's headings.
// It reads the lines as they stand rather than parsing them, because the
// buffer is being typed into and is not a program that parses between any
// two keystrokes.
func outlineOf(path string, lines [][]rune) []outlineEntry {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return goOutline(lines)
	case ".md", ".markdown":
		return markdownOutline(lines)
	}
	return nil
}

// goOutline is every func, type, var and const at the top level, and each
// name in a grouped var, const or type block.
func goOutline(lines [][]rune) []outlineEntry {
	var out []outlineEntry
	group := ""
	for i, r := range lines {
		l := string(r)
		if group != "" {
			if strings.HasPrefix(l, ")") {
				group = ""
				continue
			}
			if strings.HasPrefix(l, "\t") && !strings.HasPrefix(l, "\t\t") {
				if name := leadingIdent(strings.TrimPrefix(l, "\t")); name != "" {
					out = append(out, outlineEntry{line: i, label: group + " " + name, name: name})
				}
			}
			continue
		}
		for _, kw := range []string{"const", "var", "type"} {
			if !strings.HasPrefix(l, kw+" ") {
				continue
			}
			rest := strings.TrimPrefix(l, kw+" ")
			if strings.HasPrefix(strings.TrimSpace(rest), "(") {
				group = kw
				break
			}
			if name := leadingIdent(rest); name != "" {
				out = append(out, outlineEntry{line: i, label: kw + " " + name, name: name})
			}
		}
		if strings.HasPrefix(l, "func ") {
			if label, name := funcLabel(strings.TrimPrefix(l, "func ")); name != "" {
				out = append(out, outlineEntry{line: i, label: "func " + label, name: name})
			}
		}
	}
	return out
}

// funcLabel is a func line read as far as its name: the receiver, where it
// has one, and the name.
func funcLabel(rest string) (label, name string) {
	recv := ""
	if strings.HasPrefix(rest, "(") {
		depth := 0
		for i, r := range rest {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				recv, rest = rest[:i+1], strings.TrimSpace(rest[i+1:])
				break
			}
		}
	}
	name = leadingIdent(rest)
	if name == "" {
		return "", ""
	}
	if recv != "" {
		return recv + " " + name, name
	}
	return name, name
}

// leadingIdent is the identifier a string opens with.
func leadingIdent(s string) string {
	end := 0
	for i, r := range s {
		if !(unicode.IsLetter(r) || r == '_' || (i > 0 && unicode.IsDigit(r))) {
			break
		}
		end = i + len(string(r))
	}
	return s[:end]
}

// markdownOutline is a document's headings, indented by level, outside its
// fenced blocks.
func markdownOutline(lines [][]rune) []outlineEntry {
	var out []outlineEntry
	fenced := false
	for i, r := range lines {
		l := string(r)
		if strings.HasPrefix(l, "```") || strings.HasPrefix(l, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		level := 0
		for level < len(l) && level < 6 && l[level] == '#' {
			level++
		}
		if level == 0 || level >= len(l) || l[level] != ' ' {
			continue
		}
		text := strings.TrimSpace(l[level:])
		out = append(out, outlineEntry{line: i, label: strings.Repeat("  ", level-1) + text, name: text})
	}
	return out
}
