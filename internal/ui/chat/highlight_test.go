package chat

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func TestRenderMarkdown_FencedBlock(t *testing.T) {
	input := "before\n```bash\necho hello\n```\nafter"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "echo hello") {
		t.Fatalf("code block content should be present, got: %s", result)
	}
	if !strings.Contains(result, "before") {
		t.Fatal("text before block should be present")
	}
	if !strings.Contains(result, "after") {
		t.Fatal("text after block should be present")
	}
}

func TestRenderMarkdown_NoLanguage(t *testing.T) {
	input := "text\n```\nls -la\n```\nmore"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "ls -la") {
		t.Fatal("code content should be present")
	}
	if !strings.Contains(result, "text") {
		t.Fatal("surrounding text should be present")
	}
}

func TestRenderMarkdown_InlineCode(t *testing.T) {
	input := "use `ls -la` to list files"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "ls -la") {
		t.Fatal("inline code content should be present")
	}
}

func TestRenderMarkdown_Bold(t *testing.T) {
	input := "this is **bold** text"
	result := stripANSI(renderMarkdown(input, 80))

	if strings.Contains(result, "**") {
		t.Fatal("asterisks should be stripped from bold text")
	}
	if !strings.Contains(result, "bold") {
		t.Fatal("bold content should be present")
	}
}

func TestRenderMarkdown_Italic(t *testing.T) {
	input := "this is *italic* text"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "italic") {
		t.Fatal("italic content should be present")
	}
}

func TestRenderMarkdown_NoMarkdown(t *testing.T) {
	input := "plain text with no formatting at all"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "plain text with no formatting at all") {
		t.Fatalf("plain text should pass through, got: %s", result)
	}
}

func TestRenderMarkdown_MultipleFences(t *testing.T) {
	input := "first:\n```\ncmd1\n```\nsecond:\n```\ncmd2\n```"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "cmd1") {
		t.Fatal("first block content should be present")
	}
	if !strings.Contains(result, "cmd2") {
		t.Fatal("second block content should be present")
	}
}

func TestDiffSyntax_GoFile(t *testing.T) {
	syntax := diffSyntax("internal/agent/loop.go")
	if syntax == nil {
		t.Fatal("a .go path should get a highlighter")
	}
	line := "func main() {\treturn}"
	segs := syntax(line)
	if segs == nil {
		t.Fatal("expected segments for a Go line")
	}
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	if b.String() != line {
		t.Fatalf("segments must reconstruct the line exactly: %q != %q", b.String(), line)
	}
	colored := false
	for _, s := range segs {
		if s.Color != (components.Token{}) {
			colored = true
		}
	}
	if !colored {
		t.Fatal("a Go keyword line should get at least one colored segment")
	}
}

func TestDiffSyntax_UnknownExtension(t *testing.T) {
	if syntax := diffSyntax("notes.unknownext"); syntax != nil {
		t.Fatal("an unrecognized extension should disable highlighting")
	}
}

func TestMatchLexer_Memoized(t *testing.T) {
	first := matchLexer("loop.go")
	if first == nil {
		t.Fatal("a .go basename should match a lexer")
	}
	if second := matchLexer("loop.go"); second != first {
		t.Fatal("a repeat lookup should return the cached lexer, not a new one")
	}
	if _, ok := lexerCache.Load("loop.go"); !ok {
		t.Fatal("the hit should be cached")
	}
}

func TestMatchLexer_CachesMisses(t *testing.T) {
	if lexer := matchLexer("notes.unknownext"); lexer != nil {
		t.Fatal("an unrecognized extension should not match a lexer")
	}
	// A miss costs the same full-registry walk as a hit, so it is cached too.
	v, ok := lexerCache.Load("notes.unknownext")
	if !ok {
		t.Fatal("the miss should be cached")
	}
	if v != nil {
		t.Fatalf("a cached miss should be nil, got %v", v)
	}
	if lexer := matchLexer("notes.unknownext"); lexer != nil {
		t.Fatal("a repeat lookup of a miss should still be nil")
	}
}

// The register's rule: a code body is painted in the palette read as a
// syntax register, and never in the four tokens that say something about the
// row it sits in. A string literal drawn in add would have the card telling
// the reader that a span of a removed line was added.
func TestSyntaxRegister_NeverBorrowsTheRowsOwnTokens(t *testing.T) {
	p := components.Palette
	for _, banned := range []struct {
		name  string
		token components.Token
	}{{"add", p.Add}, {"del", p.Del}, {"hunk", p.Hunk}, {"spin", p.Spin}} {
		for kind, tone := range syntaxTones {
			if tone == banned.token {
				t.Errorf("%v is drawn in %s, which is the row's colour and not the register's",
					kind, banned.name)
			}
		}
	}
}

// The register is keyed on the broadest type that should carry a tone, and
// resolved by walking a token's parents — the inheritance a chroma.Style
// would have done, which is why none is built. A type nothing above it claims
// gets no tone and renders in the diff kind's own colour.
func TestSyntaxRegister_ResolvesThroughTheTypeHierarchy(t *testing.T) {
	p := components.Palette
	for _, c := range []struct {
		kind chroma.TokenType
		want components.Token
	}{
		{chroma.LiteralStringDouble, p.Accent},
		{chroma.LiteralNumberInteger, p.Accent},
		{chroma.KeywordDeclaration, p.Info},
		{chroma.CommentSingle, p.Dim},
		{chroma.TextWhitespace, p.Body},
		{chroma.NameFunction, p.Bright},
	} {
		got, ok := syntaxTone(c.kind)
		if !ok || got != c.want {
			t.Errorf("%v resolves to %+v (ok=%v), want %+v", c.kind, got, ok, c.want)
		}
	}
	for _, unclaimed := range []chroma.TokenType{chroma.Error, chroma.None, chroma.Other} {
		if _, ok := syntaxTone(unclaimed); ok {
			t.Errorf("%v should be left to the diff kind's colour", unclaimed)
		}
	}
}

// The width decides the layout and nothing else. A reader who opens the same
// edit in a 130-column terminal and in a 110-column one gets the same verdict
// in the same colour on every line, even though one of them is the paired
// layout and the other the unified one
// (docs/interface/surfaces.md#the-diff-view).
func TestDiffColouring_TheTerminalsWidthDecidesTheLayoutAndNotTheColour(t *testing.T) {
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })

	hunks := []diff.Hunk{{
		OldStart: 12, OldCount: 3, NewStart: 12, NewCount: 3,
		Lines: []diff.Line{
			{Kind: diff.Context, Text: "func retryAfter(h http.Header) time.Duration {", OldNo: 12, NewNo: 12},
			{Kind: diff.Del, Text: "\treturn 30 * time.Second", OldNo: 13},
			{Kind: diff.Add, Text: "\treturn parseSeconds(h.Get(\"Retry-After\"))", NewNo: 13},
		},
	}}
	view := func(width int) string {
		d := &components.DiffView{
			Path: "internal/provider/retry.go", Verb: "edit", Hunks: hunks,
			Mode: components.DiffFull, Height: 12,
			Syntax: diffSyntax("internal/provider/retry.go"),
		}
		return d.View(width)
	}
	paired, unified := view(130), view(110)
	if !strings.Contains(ansi.Strip(paired), " │ ") || strings.Contains(ansi.Strip(unified), " │ ") {
		t.Fatal("130 columns is the paired layout and 110 the unified one")
	}
	// One probe per rung of the register plus the ground of each kind of
	// line, so the claim is about the whole body and not one word of it.
	for _, word := range []string{
		"func", "retryAfter", "http", "return", "30", "parseSeconds", "Retry-After",
	} {
		got, want := toneIn(t, paired, word), toneIn(t, unified, word)
		if got == "" {
			t.Fatalf("%q is painted by nothing at all", word)
		}
		if got != want {
			t.Fatalf("%q reads %q at 130 columns and %q at 110", word, got, want)
		}
	}
}

// toneIn is the escape the first run carrying want was opened with.
func toneIn(t *testing.T, rendered, want string) string {
	t.Helper()
	open := ""
	var text strings.Builder
	for i := 0; i < len(rendered); {
		if rendered[i] != '\x1b' {
			text.WriteByte(rendered[i])
			i++
			continue
		}
		if strings.Contains(text.String(), want) {
			return open
		}
		text.Reset()
		j := i
		for j < len(rendered) && rendered[j] != 'm' {
			j++
		}
		if seq := rendered[i:min(j+1, len(rendered))]; seq == "\x1b[m" || seq == "\x1b[0m" {
			open = ""
		} else {
			open = seq
		}
		i = j + 1
	}
	if strings.Contains(text.String(), want) {
		return open
	}
	t.Fatalf("no run carries %q", want)
	return ""
}

// A construct that spans lines is lexed as what it is: the block is
// tokenised whole and split back into its lines, so the digits inside a
// block comment are comment and not numbers, a raw string's are string, and
// the code after a comment closes is code again.
func TestFenceSyntax_AConstructSpanningLinesIsLexedWhole(t *testing.T) {
	cases := []struct {
		name  string
		lang  string
		lines []string
		line  int
		text  string
		tone  components.Token
	}{
		{"digits inside a block comment", "go", []string{"/* the cap is", "   120 rounds */", "const n = 1"}, 1, "120", components.Palette.Dim},
		{"code after the comment closes", "go", []string{"/* the cap is", "   120 rounds */", "const n = 1"}, 2, "const", components.Palette.Info},
		{"digits inside a raw string", "go", []string{"s := `first", "42 second`"}, 1, "42", components.Palette.Accent},
		{"words inside a docstring", "python", []string{"def f():", "    \"\"\"Return 7", "    things.\"\"\"", "    return 7"}, 2, "things", components.Palette.Accent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			segs := fenceSyntax(tc.lang, tc.lines)
			if len(segs) != len(tc.lines) {
				t.Fatalf("%d lines came back for %d", len(segs), len(tc.lines))
			}
			var joined strings.Builder
			found := false
			for _, seg := range segs[tc.line] {
				joined.WriteString(seg.Text)
				if !strings.Contains(seg.Text, tc.text) {
					continue
				}
				found = true
				if !seg.Styled || seg.Style.GetForeground() != tc.tone.Color() {
					t.Errorf("%q took %v, want %v", seg.Text, seg.Style.GetForeground(), tc.tone.Color())
				}
			}
			if !found {
				t.Errorf("no segment holds %q", tc.text)
			}
			if joined.String() != tc.lines[tc.line] {
				t.Errorf("the segments say %q, the line is %q", joined.String(), tc.lines[tc.line])
			}
		})
	}
}

// proseTurn is a turn the way the Rows catalogue's model screen draws it: a
// thought, a step, a paragraph the model wrote between its calls, the write
// it led to, and the reply with a list and a code block in it.
func proseTurn() []entry {
	return []entry{
		{kind: entryUser, text: "add the block copy to the backlog"},
		{kind: entryThink, text: "Both checks should be cheap: one grep for the fence marker in docs, " +
			"one look at the scene driver's event list."},
		{kind: entryAssistant, text: "Checking the docs and the scene driver"},
		searchEntry("```", 300*time.Millisecond),
		readEntry("scripts/tui/drive.sh", 200*time.Millisecond),
		{kind: entryAssistant, text: "Both checks pass. The reply renderer already knows where a fenced " +
			"block starts and ends, so copying one needs no new parse.\n\n" +
			"I'll write the epic with two stories."},
		{kind: entryTool, toolName: "write_file", toolArgs: `{"path":".plan/BACKLOG.md"}`,
			toolResult: "wrote .plan/BACKLOG.md", duration: 1100 * time.Millisecond},
		{kind: entryAssistant, text: "I've added the request to .plan/BACKLOG.md as a new epic, " +
			"A Code Block Is Copied by Itself, with two stories.\n\n" +
			"- [c] on a focused code block copies it, keyboard first.\n" +
			"- Clicking a code block copies it; the block flashes once.\n\n" +
			"The copy itself is three lines in keys.go:\n\n" +
			"```go\ncase \"c\":\n\treturn m, m.copyBlock(m.focusedBlock())\n```"},
	}
}

// proseLines renders proseTurn at a width and returns its rows, unstyled.
func proseLines(t *testing.T, width int) []string {
	t.Helper()
	m := frameModel(t, width, 40)
	m.transcript = proseTurn()
	m.invalidateRenderCache()
	return strings.Split(ansi.Strip(m.renderHistory()), "\n")
}

// paragraphFrom is the rows from the first one carrying probe up to the next
// blank row: one paragraph of the render.
func paragraphFrom(t *testing.T, lines []string, probe string) []string {
	t.Helper()
	for i, l := range lines {
		if !strings.Contains(l, probe) {
			continue
		}
		var rows []string
		for _, r := range lines[i:] {
			if strings.TrimSpace(r) == "" {
				break
			}
			rows = append(rows, r)
		}
		return rows
	}
	t.Fatalf("the render holds no %q:\n%s", probe, strings.Join(lines, "\n"))
	return nil
}

// onBodyColumn reports whether a row's words start exactly at the body
// column.
func onBodyColumn(row string) bool {
	body := strings.Repeat(" ", components.GridDetailIndent)
	return strings.HasPrefix(row, body) && !strings.HasPrefix(row, body+" ")
}

// TestReply_SitsAtTheBodyColumn: the reply starts four columns in, on the
// column a card's sentence and a thought start on, wraps back to it, keeps
// the markdown's right margin, and carries its list and its code block with
// it (docs/interface/surfaces.md#the-leading-columns).
func TestReply_SitsAtTheBodyColumn(t *testing.T) {
	const first = "    I've added the request to .plan/BACKLOG.md as a new epic"
	body := strings.Repeat(" ", components.GridDetailIndent)
	for _, width := range goldenWidths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			lines := proseLines(t, width)
			rows := paragraphFrom(t, lines, "I've added the request")
			// The narrowest pane wraps inside the literal, after "as a"; its
			// first row is still the literal's opening.
			want := first
			if width < 80 {
				want = strings.TrimSuffix(first, " new epic")
			}
			if !strings.HasPrefix(rows[0], want) {
				t.Errorf("the reply's first row: %q, want it to open %q", rows[0], want)
			}
			for _, r := range rows {
				if !onBodyColumn(r) {
					t.Errorf("a reply row is off the body column: %q", r)
				}
				if w := ansi.StringWidth(strings.TrimRight(r, " ")); w > width-2 {
					t.Errorf("a reply row reaches into the right margin (%d of %d): %q", w, width, r)
				}
			}
			for _, tc := range []struct{ what, row string }{
				{"a list item", body + "· [c] on a focused"},
				{"the paragraph after the list", body + "The copy itself"},
				{"the code block's heading, two further in", body + "  go"},
				{"the code, under its heading", body + "  case \"c\":"},
			} {
				found := false
				for _, l := range lines {
					if strings.HasPrefix(l, tc.row) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s does not open %q:\n%s", tc.what, tc.row, strings.Join(lines, "\n"))
				}
			}
		})
	}
}

// TestParagraph_SitsAtTheBodyColumn: a paragraph the model wrote between its
// calls is the reply's prose, at the reply's column, wrapped there.
func TestParagraph_SitsAtTheBodyColumn(t *testing.T) {
	for _, width := range goldenWidths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			lines := proseLines(t, width)
			for _, probe := range []string{"Both checks pass.", "I'll write the epic"} {
				for _, r := range paragraphFrom(t, lines, probe) {
					if !onBodyColumn(r) {
						t.Errorf("a paragraph row is off the body column: %q", r)
					}
				}
			}
		})
	}
}

// TestGolden_ProseColumn: the model's prose at the body column — a thought,
// a paragraph between calls and the reply under the last card — beside the
// cards whose sentences start on the same column.
func TestGolden_ProseColumn(t *testing.T) {
	captureBoundedGolden(t, "prose-column", "the model's prose at the body column", goldenWidths, func(width int) []golden.Panel {
		m := frameModel(t, width, 40)
		m.transcript = proseTurn()
		m.invalidateRenderCache()
		return []golden.Panel{{Label: "a thought, a step, a paragraph mid-turn, and the reply", View: m.renderHistory()}}
	})
}
