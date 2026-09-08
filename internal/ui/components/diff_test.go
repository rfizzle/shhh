package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/diff"
)

func sampleHunks(t *testing.T) []diff.Hunk {
	t.Helper()
	return diff.Compute("a\nb\nc\n", "a\nB\nc\n")
}

func TestUnifiedLines_Basics(t *testing.T) {
	lines := UnifiedLines(sampleHunks(t), 80, UnifiedOpts{})
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"@@ -1,3 +1,3 @@", "-b", "+B", " a"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unified rendering should contain %q:\n%s", want, joined)
		}
	}
}

func TestUnifiedLines_LineNumbers(t *testing.T) {
	lines := UnifiedLines(sampleHunks(t), 80, UnifiedOpts{LineNumbers: true})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "- 2  b") || !strings.Contains(joined, "+ 2  B") {
		t.Fatalf("numbered rendering should carry old/new line numbers:\n%s", joined)
	}
}

func TestUnifiedLines_TruncatesWithNotice(t *testing.T) {
	hunks := diff.Compute("", strings.Repeat("x\n", 20))
	lines := UnifiedLines(hunks, 80, UnifiedOpts{MaxLines: 5})
	if len(lines) != 5 {
		t.Fatalf("expected 5 rendered lines, got %d", len(lines))
	}
	// 21 total rows (header + 20 adds), 4 kept → 17 dropped.
	if !strings.Contains(lines[4], "+17 more diff lines") {
		t.Fatalf("last line should be the truncation notice, got %q", lines[4])
	}
}

func TestUnifiedLines_EmptyShowsNoChanges(t *testing.T) {
	lines := UnifiedLines(nil, 80, UnifiedOpts{})
	if len(lines) != 1 || !strings.Contains(lines[0], "no changes") {
		t.Fatalf("empty diff should render a no-changes notice, got %v", lines)
	}
}

// An applied edit is an activity row: the mutation rail in the gutter, the
// verb in the verb column and the stats in the outcome field, so it lines up
// with the call that made it instead of standing at column 0 in a shape of
// its own.
func TestDiffView_RowView(t *testing.T) {
	v := &DiffView{Path: "main.go", Verb: "edit", Hunks: sampleHunks(t), Duration: "1.1s"}
	row := stripANSI(v.RowView(80))
	for _, want := range []string{"▎✎ edit    main.go", "+1 −1 · 1 hunk", "1.1s"} {
		if !strings.Contains(row, want) {
			t.Fatalf("collapsed row should contain %q, got %q", want, row)
		}
	}
	if !strings.HasPrefix(row, strings.Repeat(" ", GridPointerWidth)+"▎") {
		t.Fatalf("the rail sits in the gutter, after the pointer column: %q", row)
	}
	if strings.Contains(row, "\n") {
		t.Fatalf("collapsed row must be a single line, got %q", row)
	}
}

// A file whose whole change is its permissions has nothing to count and no
// hunks to show. The header says what did change, because `+0 −0 · 0 hunks`
// over a real change reads as a viewer opened on the wrong file.
func TestDiffView_ModeChangeStandsWhereTheCountsWould(t *testing.T) {
	v := &DiffView{Path: "build.sh", Verb: "edit", ModeChange: "mode 0644 → 0755", Height: 6}
	row := v.RowView(80)
	if !strings.Contains(row, "mode 0644 → 0755") {
		t.Fatalf("the row should state the mode, got %q", row)
	}
	if strings.Contains(row, "+0 −0") {
		t.Fatalf("nothing counted this change, got %q", row)
	}
	v.Mode = DiffFull
	full := v.View(80)
	if !strings.Contains(full, "mode 0644 → 0755") || !strings.Contains(full, "no textual changes") {
		t.Fatalf("the full view should state the mode over an empty body, got:\n%s", full)
	}

	// With lines as well, the counts are the header and the mode follows
	// them: putting the file back puts both back.
	both := &DiffView{Path: "build.sh", Hunks: sampleHunks(t), ModeChange: "mode 0644 → 0755"}
	if got := both.RowView(80); !strings.Contains(got, "+1 −1 · 1 hunk · mode 0644 → 0755") {
		t.Fatalf("the row should state both, got %q", got)
	}
}

func TestDiffView_ModeCycleAndEsc(t *testing.T) {
	v := &DiffView{Path: "main.go", Hunks: sampleHunks(t), Height: 10}
	if done, _ := v.Update(key("enter")); done || v.Mode != DiffExpanded {
		t.Fatalf("enter should expand, got mode %d", v.Mode)
	}
	if done, _ := v.Update(key("enter")); done || v.Mode != DiffFull {
		t.Fatalf("second enter should open the full view, got mode %d", v.Mode)
	}
	if done, _ := v.Update(key("esc")); done || v.Mode != DiffExpanded {
		t.Fatalf("esc from full view should step back to expanded, got mode %d", v.Mode)
	}
	if done, _ := v.Update(key("esc")); !done {
		t.Fatal("esc from expanded should dismiss the viewer")
	}
}

func TestDiffView_FullViewScrollAndToggle(t *testing.T) {
	hunks := diff.Compute("", strings.Repeat("x\n", 30))
	v := &DiffView{Path: "big.txt", Hunks: hunks, Mode: DiffFull, Height: 10}

	view := v.View(80)
	if !strings.Contains(view, "[j/k] scroll") {
		t.Fatalf("full view should show its key hints:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != 10 {
		t.Fatalf("full view should occupy its Height budget, got %d rows", got)
	}

	v.Update(key("j"))
	if v.Offset != 1 {
		t.Fatalf("j should scroll down, offset %d", v.Offset)
	}
	v.Update(key("k"))
	v.Update(key("k"))
	if v.Offset != 0 {
		t.Fatalf("k should clamp at the top, offset %d", v.Offset)
	}

	v.Update(key("s"))
	if !v.SideBySide {
		t.Fatal("s should toggle side-by-side")
	}
}

func TestDiffView_SideBySideAtWideWidth(t *testing.T) {
	v := &DiffView{Path: "main.go", Hunks: sampleHunks(t), Mode: DiffFull, Height: 12}
	view := v.View(140)
	if !strings.Contains(view, "│") {
		t.Fatalf("wide full view should render side-by-side panes:\n%s", view)
	}
	// The changed pair shares one row: old-side b and new-side B.
	found := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "b") && strings.Contains(line, "B") && strings.Contains(line, "│") {
			found = true
		}
	}
	if !found {
		t.Fatalf("del/add pair should align on one side-by-side row:\n%s", view)
	}
}

func TestDiffView_HunkJump(t *testing.T) {
	var oldSb, newSb strings.Builder
	for i := 0; i < 30; i++ {
		oldSb.WriteString("l\n")
		if i == 2 || i == 25 {
			newSb.WriteString("changed\n")
		} else {
			newSb.WriteString("l\n")
		}
	}
	hunks := diff.Compute(oldSb.String(), newSb.String())
	if len(hunks) != 2 {
		t.Fatalf("test setup expects 2 hunks, got %d", len(hunks))
	}
	v := &DiffView{Hunks: hunks, Mode: DiffFull, Height: 8}
	v.Update(key("n"))
	if v.Offset == 0 {
		t.Fatal("n should jump to the next hunk")
	}
	v.Update(key("p"))
	if v.Offset != 0 {
		t.Fatalf("p should jump back to the first hunk, offset %d", v.Offset)
	}
}

func TestUnifiedLines_IntralineEmphasisKeepsText(t *testing.T) {
	hunks := diff.Compute("return nil\n", "return err\n")
	lines := UnifiedLines(hunks, 80, UnifiedOpts{Emphasis: true})
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "-return nil") || !strings.Contains(joined, "+return err") {
		t.Fatalf("emphasized rendering must preserve the full text:\n%s", joined)
	}
}

// fakeSyntax colors every rune-run of letters, leaving the rest default, and
// reconstructs the input exactly.
func fakeSyntax(line string) []Segment {
	var segs []Segment
	cur := Segment{}
	flush := func() {
		if cur.Text != "" {
			segs = append(segs, cur)
			cur = Segment{}
		}
	}
	for _, r := range line {
		var color Token
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			color = Palette.Info
		}
		if color != cur.Color {
			flush()
			cur.Color = color
		}
		cur.Text += string(r)
	}
	flush()
	return segs
}

func TestUnifiedLines_SyntaxKeepsTextAndNumbers(t *testing.T) {
	hunks := diff.Compute("return nil\n", "return err\n")
	lines := UnifiedLines(hunks, 80, UnifiedOpts{LineNumbers: true, Emphasis: true, Syntax: fakeSyntax})
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "- 1  return nil") || !strings.Contains(joined, "+ 1  return err") {
		t.Fatalf("syntax rendering must preserve text, markers, and line numbers:\n%s", joined)
	}
}

func TestUnifiedLines_SyntaxClipsWithEllipsis(t *testing.T) {
	hunks := diff.Compute("", "abcdefghijklmnopqrstuvwxyz\n")
	lines := UnifiedLines(hunks, 12, UnifiedOpts{Syntax: fakeSyntax})
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "…") {
		t.Fatalf("overlong syntax lines should clip with an ellipsis:\n%s", joined)
	}
	if strings.Contains(joined, "z") {
		t.Fatalf("clipped tail should not render:\n%s", joined)
	}
}

func TestUnifiedLines_SyntaxMismatchFallsBack(t *testing.T) {
	bad := func(string) []Segment { return []Segment{{Text: "wrong"}} }
	hunks := diff.Compute("a\n", "b\n")
	lines := UnifiedLines(hunks, 80, UnifiedOpts{Syntax: bad})
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "-a") || !strings.Contains(joined, "+b") {
		t.Fatalf("segments that don't reconstruct the line must fall back to plain rendering:\n%s", joined)
	}
}

func TestDiffView_MultiFileFullView(t *testing.T) {
	files := []diff.File{
		{Path: "a.go", Hunks: diff.Compute("x\n", "y\n")},
		{Path: "img.png", Binary: true},
		{Path: "b.go", Hunks: diff.Compute("1\n2\n", "1\n3\n")},
	}
	v := &DiffView{Path: "session diff", Files: files, Mode: DiffFull, Height: 30}
	view := stripANSI(v.View(100))
	v.Height = 6 // shrink so the body scrolls and hunk jumps move the offset
	for _, want := range []string{"session diff", "─ a.go", "─ b.go", "(binary file differs)", "· 3 files"} {
		if !strings.Contains(view, want) {
			t.Fatalf("multi-file full view should contain %q:\n%s", want, view)
		}
	}
	// n jumps across files to the second textual hunk.
	v.Update(key("n"))
	if v.Offset == 0 {
		t.Fatal("n should jump to the next hunk across files")
	}
}

// stripANSI removes escape sequences so tests can assert on plain text.
func stripANSI(s string) string {
	var b strings.Builder
	inSeq := false
	for _, r := range s {
		switch {
		case inSeq:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inSeq = false
			}
		case r == '\x1b':
			inSeq = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// The diff row states what let an edit apply without anybody being asked,
// and gives that field up at the same point every other act's row does —
// rather than each row having its own idea of narrow.
func TestDiffView_TheAccountGivesWayToThePath(t *testing.T) {
	d := &DiffView{Path: "internal/ui/chat/approval.go", Verb: "edit",
		Hunks: sampleHunks(t), Allowed: OutcomeBy(OutcomeAutoAllowed, "auto mode")}

	wide := stripANSI(d.RowView(120))
	if !strings.Contains(wide, "auto-allowed · auto mode") || !strings.Contains(wide, "internal/ui/chat/approval.go") {
		t.Fatalf("a row with room states both:\n%s", wide)
	}
	narrow := stripANSI(d.RowView(60))
	if strings.Contains(narrow, "auto-allowed") {
		t.Fatalf("a row without room drops the account:\n%s", narrow)
	}
	// And spends what the account was taking on the path, which then clips
	// where the grid clips every target rather than where this view would.
	if !strings.Contains(narrow, "internal/ui/chat/approva") {
		t.Fatalf("and spends the room on the path:\n%s", narrow)
	}
	// The expanded form answers the same way, so opening a row never loses
	// what the closed one said.
	if !strings.Contains(stripANSI(d.ExpandedLines(120)[0]), "auto-allowed · auto mode") {
		t.Fatalf("the expanded head keeps the account:\n%s", d.ExpandedLines(120)[0])
	}
}

// paintedRuns splits a rendered row into the styled runs it is made of: the
// escape each run was opened with, and the text under it. It is how these
// tests read a colour assignment — the columns are stripANSI's business.
func paintedRuns(s string) [][2]string {
	var runs [][2]string
	open := ""
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			runs = append(runs, [2]string{open, text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		if s[i] != '\x1b' {
			text.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] != 'm' {
			j++
		}
		flush()
		if seq := s[i:min(j+1, len(s))]; seq == "\x1b[m" || seq == "\x1b[0m" {
			open = ""
		} else {
			open = seq
		}
		i = j + 1
	}
	flush()
	return runs
}

// toneOf is the escape the first run carrying want was opened with.
func toneOf(t *testing.T, rendered, want string) string {
	t.Helper()
	for _, run := range paintedRuns(rendered) {
		if strings.Contains(run[1], want) {
			return run[0]
		}
	}
	t.Fatalf("no run carries %q:\n%s", want, strings.ReplaceAll(rendered, "\x1b", "^["))
	return ""
}

// styleTone is the escape a style opens with, to compare a rendering against.
func toneFor(style lipgloss.Style) string { return paintedRuns(style.Render("x"))[0][0] }

// tokenTone is the same for a bare palette token, which is how a syntax
// segment names its colour.
func tokenTone(tok Token) string {
	return toneFor(lipgloss.NewStyle().Foreground(tok.Color()))
}

// The gutter is chrome and the marker is the verdict: the line number carries
// Dim whatever happened to the line, so it is never read as a second
// statement about it, and the marker beside it keeps the line's own colour.
func TestUnifiedLines_TheNumberIsChromeAndTheMarkerIsTheVerdict(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	lines := UnifiedLines(diff.Compute("return nil\n", "return err\n"), 80,
		UnifiedOpts{LineNumbers: true})
	del, add := lines[1], lines[2]

	if got, want := toneOf(t, del, "-"), toneFor(sty.Del); got != want {
		t.Fatalf("the deletion's marker carries Del, got %q", got)
	}
	if got, want := toneOf(t, add, "+"), toneFor(sty.Add); got != want {
		t.Fatalf("the addition's marker carries Add, got %q", got)
	}
	for _, line := range []string{del, add} {
		if got, want := toneOf(t, line, "1"), toneFor(sty.Dim); got != want {
			t.Fatalf("the line number carries Dim, got %q in:\n%s", got, stripANSI(line))
		}
	}
	if toneOf(t, del, "1") == toneOf(t, del, "return nil") {
		t.Fatal("the number and the line it numbers must not read as one colour")
	}
}

// registerSyntax is the syntax register in miniature — one word per rung — so
// a test can say which of them a verdict takes over and which stand inside it.
func registerSyntax(line string) []Segment {
	tones := map[string]Token{
		"//":   Palette.Dim,    // a comment recedes
		"(":    Palette.Dimmer, // and so does the glue
		"func": Palette.Info,   // structure
		"9":    Palette.Accent, // a value
		"8":    Palette.Accent,
		"Run":  Palette.Bright, // a name the reader scans for
	}
	var segs []Segment
	for _, field := range strings.SplitAfter(line, " ") {
		seg := Segment{Text: field, Color: Palette.Body}
		if tone, ok := tones[strings.TrimSuffix(field, " ")]; ok {
			seg.Color = tone
		}
		segs = append(segs, seg)
	}
	return segs
}

// The verdict layers over the register rather than under it
// (docs/interface/surfaces.md#the-diff-view). On a changed line the register's
// receding rungs give way, so the line still reads as added or removed at a
// glance; the tones that name something stand inside it.
func TestUnifiedLines_TheVerdictCarriesAChangedLineUnderTheRegister(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	hunks := diff.Compute("// func Run ( 9 ) name\n", "// func Run ( 8 ) name\n")
	lines := UnifiedLines(hunks, 80, UnifiedOpts{Syntax: registerSyntax})
	removed := lines[1]

	for _, word := range []string{"//", "(", "name"} {
		if got, want := toneOf(t, removed, word), toneFor(sty.Del); got != want {
			t.Fatalf("the ground under %q is the verdict, got %q", word, got)
		}
	}
	for _, stands := range []struct {
		word string
		tone Token
	}{
		{"func", Palette.Info},
		{"Run", Palette.Bright},
		{"9", Palette.Accent},
	} {
		if got, want := toneOf(t, removed, stands.word), tokenTone(stands.tone); got != want {
			t.Fatalf("%q keeps the register's own tone, got %q", stands.word, got)
		}
	}
}

// A context line states no verdict, so it takes the register whole.
func TestUnifiedLines_AContextLineTakesTheRegisterWhole(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	hunks := diff.Compute("// func Run ( 9 ) name\nx\n", "// func Run ( 9 ) name\ny\n")
	lines := UnifiedLines(hunks, 80, UnifiedOpts{Syntax: registerSyntax})
	context := lines[1]
	for _, kept := range []struct {
		word string
		tone Token
	}{
		{"//", Palette.Dim},
		{"(", Palette.Dimmer},
		{"name", Palette.Body},
	} {
		if got, want := toneOf(t, context, kept.word), tokenTone(kept.tone); got != want {
			t.Fatalf("a context line keeps %q at its own rung, got %q", kept.word, got)
		}
	}
}

// Which layout a reader gets is the terminal's width talking; which register
// the code is read in is the file's. Side by side carries the same syntax the
// unified body does, and lays the verdict over it the same way.
func TestDiffView_SideBySideKeepsTheSyntaxTheUnifiedBodyHas(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	hunks := diff.Compute("// func Run ( 9 ) name\n", "// func Run ( 8 ) name\n")
	d := &DiffView{Path: "loop.go", Hunks: hunks, Syntax: registerSyntax,
		Mode: DiffFull, Height: 12}

	unified := d.View(sideBySideMinWidth - 20)
	paired := d.View(sideBySideMinWidth + 20)
	if strings.Contains(stripANSI(unified), " │ ") || !strings.Contains(stripANSI(paired), " │ ") {
		t.Fatal("the narrow view is the unified one and the wide view the paired one")
	}
	for _, word := range []string{"//", "func", "Run", "9", "name"} {
		if got, want := toneOf(t, paired, word), toneOf(t, unified, word); got != want {
			t.Fatalf("%q reads %q side by side and %q unified", word, got, want)
		}
	}
	if got, want := toneOf(t, paired, "func"), tokenTone(Palette.Info); got != want {
		t.Fatalf("the paired cell carries the register, got %q", got)
	}

	// A pane cell has no marker column, so a line number that fills the
	// gutter is chrome all the way across it rather than reading as a digit
	// of the verdict's own colour.
	wide := &DiffView{Path: "loop.go", Mode: DiffFull, Height: 12, Hunks: []diff.Hunk{{
		OldStart: 1200, OldCount: 1, NewStart: 1200, NewCount: 1,
		Lines: []diff.Line{{Kind: diff.Del, Text: "name", OldNo: 1204}},
	}}}
	if got, want := toneOf(t, wide.View(sideBySideMinWidth+20), "1204"), toneFor(sty.Dim); got != want {
		t.Fatalf("a four-digit number is chrome, got %q", got)
	}
}
