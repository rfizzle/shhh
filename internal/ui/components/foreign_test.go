package components

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// ground is what a detail body is painted in — dimmer, the token the
// activity row hands repaint; every test below asks for the same one so the
// assertions read as claims about the foreign half.
var testGround = Palette.Dimmer

// A line shhh wrote itself has no sequences in it and must come back byte for
// byte, because that is the overwhelming majority of every detail body and
// the caller styles it exactly as it did before this door existed.
func TestForeignText_LinesWithNothingToRepaintAreUntouched(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	for _, s := range []string{
		"",
		"--- FAIL: TestRoundLimit (0.00s)",
		"    loop_test.go:88: want ErrRoundLimit, got nil",
		"a line with a tab\tin it",
	} {
		if got, ok := repaint(s, testGround); got != s || ok {
			t.Fatalf("repaint(%q) = %q, want it unchanged and reported so", s, got)
		}
	}
}

// The sixteen colours a terminal theme owns become the tokens that mean the
// same thing here. The assertion is against what lipgloss renders the
// token as rather than against a literal escape, so the test says "del", not
// "91", and survives P2-1 making del a truecolor value.
func TestForeignText_ThemeColoursBecomeTokens(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	for _, c := range []struct {
		name  string
		param string
		token Token
	}{
		{"red is del", "31", Palette.Del},
		{"green is add", "32", Palette.Add},
		{"yellow is accent", "33", Palette.Accent},
		{"blue is info", "34", Palette.Info},
		{"magenta is spin", "35", Palette.Spin},
		{"cyan is hunk", "36", Palette.Hunk},
		{"white is body", "37", Palette.Body},
		{"black is dim", "30", Palette.Dim},
		{"bright red is del as well", "91", Palette.Del},
		{"bright white is bright", "97", Palette.Bright},
	} {
		got, _ := repaint("\x1b["+c.param+"mWORD\x1b[0m", testGround)
		want := lipgloss.NewStyle().Foreground(c.token.Color()).Render("WORD")
		if !strings.Contains(got, want) {
			t.Fatalf("%s: repaint = %q, want it to contain %q", c.name, got, want)
		}
	}
}

// Nothing a program says may cost a reader a character of what it said. The
// stripped render is the layout, and the layout is the output with its
// decoration taken off — not a shortened version of it.
func TestForeignText_TheTextItselfAlwaysSurvives(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	for _, c := range []struct {
		name, in, want string
	}{
		{"a colour around one word", "--- \x1b[31mFAIL\x1b[0m: TestRoundLimit", "--- FAIL: TestRoundLimit"},
		{"a background nobody asked for", "\x1b[41;37mBLOCK\x1b[0m after", "BLOCK after"},
		{"an erase and a cursor move", "\x1b[2K\x1b[1Gerased", "erased"},
		{"a window title", "\x1b]0;npm run build\x07visible", "visible"},
		{"a clipboard write", "\x1b]52;c;c2hoaA==\x07visible", "visible"},
		{"a truncated introducer", "\x1b[38;5mwarn", "warn"},
		{"a bare reset", "\x1b[mplain", "plain"},
	} {
		if painted, _ := repaint(c.in, testGround); ansi.Strip(painted) != c.want {
			t.Fatalf("%s: %q rendered as %q, want %q", c.name, c.in, ansi.Strip(painted), c.want)
		}
	}
}

// A progress bar rewrites its line rather than adding to it, so the line the
// reader is shown is the last one written. A carriage return that ends the
// line is a terminator, not an overwrite — dropping the line for it would
// blank every line of a program that writes CRLF.
func TestForeignText_CarriageReturnOverwritesTheLine(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	for _, c := range []struct {
		name, in, want string
	}{
		{"the last write wins", "building 10%\rbuilding 90%", "building 90%"},
		{"and so does the last of many", "10%\r50%\r100%", "100%"},
		{"a trailing return is a terminator", "done\r", "done"},
		{"and so is one leading a newline", "done\r\nnext", "done\nnext"},
		{"the attributes survive the overwrite", "\x1b[31m10%\r90%", "90%"},
	} {
		if painted, _ := repaint(c.in, testGround); ansi.Strip(painted) != c.want {
			t.Fatalf("%s: %q rendered as %q, want %q", c.name, c.in, ansi.Strip(painted), c.want)
		}
	}
	// The colour set before the return is still the colour after it: the
	// terminal moved the cursor, it did not reset the pen.
	got, _ := repaint("\x1b[31m10%\r90%", testGround)
	want := lipgloss.NewStyle().Foreground(Palette.Del.Color()).Render("90%")
	if got != want {
		t.Fatalf("attributes after a carriage return: %q, want %q", got, want)
	}
}

// Bold and its neighbours are how a program emphasises something without
// spending a colour, which is the same thing the mono palette relies on. They
// pass through whatever happens to the hue.
func TestForeignText_AttributesPassThrough(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	got, _ := repaint("\x1b[1;4;9mloud\x1b[0m", testGround)
	want := lipgloss.NewStyle().Foreground(testGround.Color()).
		Bold(true).Underline(true).Strikethrough(true).Render("loud")
	if !strings.Contains(got, want) {
		t.Fatalf("repaint = %q, want it to contain %q", got, want)
	}
	// And they come off again when the program takes them off.
	got, _ = repaint("\x1b[1mloud\x1b[22m quiet", testGround)
	if !strings.Contains(got, lipgloss.NewStyle().Foreground(testGround.Color()).Render(" quiet")) {
		t.Fatalf("repaint = %q, want the tail unbolded", got)
	}
}

// Exactly three background tints exist and all three collapse onto the
// selection grey in mono. A program painting a block of a detail body would
// be drawing the reading cursor, so it does not get to.
func TestForeignText_BackgroundsAreDropped(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	for _, in := range []string{
		"\x1b[41mBLOCK\x1b[0m",
		"\x1b[101mBLOCK\x1b[0m",
		"\x1b[48;5;52mBLOCK\x1b[0m",
		"\x1b[48;2;255;0;0mBLOCK\x1b[0m",
	} {
		got, _ := repaint(in, testGround)
		want := lipgloss.NewStyle().Foreground(testGround.Color()).Render("BLOCK")
		if got != want {
			t.Fatalf("%q rendered as %q, want the ground alone (%q)", in, got, want)
		}
	}
}

// The extended half is the only route by which a colour outside the fifteen
// could reach a detail body, so it is folded the way the sixteen are: a
// program's red is del whether it wrote the short form, the index or the
// triple, and a colour with no hue in it joins the grey ramp.
func TestForeignText_ExplicitColoursArriveAsTokens(t *testing.T) {
	withColorProfile(t, colorprofile.TrueColor)
	for _, c := range []struct {
		name, in string
		token    Token
	}{
		{"an indexed orange is accent", "\x1b[38;5;208mwarn\x1b[0m", Palette.Accent},
		{"an indexed red is del, like 31", "\x1b[38;5;196mwarn\x1b[0m", Palette.Del},
		{"an indexed green is add", "\x1b[38;5;46mwarn\x1b[0m", Palette.Add},
		{"the first sixteen go through the theme table", "\x1b[38;5;5mwarn\x1b[0m", Palette.Spin},
		{"an indexed grey joins the ramp", "\x1b[38;5;250mwarn\x1b[0m", Palette.Body},
		{"a truecolor amber is accent", "\x1b[38;2;255;170;0mwarn\x1b[0m", Palette.Accent},
		{"a truecolor violet is info", "\x1b[38;2;122;19;219mwarn\x1b[0m", Palette.Info},
		{"a truecolor near-black is dim", "\x1b[38;2;18;18;18mwarn\x1b[0m", Palette.Dim},
	} {
		got, _ := repaint(c.in, testGround)
		want := lipgloss.NewStyle().Foreground(c.token.Color()).Render("warn")
		if got != want {
			t.Fatalf("%s: %q rendered as %q, want %q", c.name, c.in, got, want)
		}
	}
}

// Nothing outside the fifteen reaches the screen: whatever a program names,
// what comes back is a colour the palette issued
// (docs/interface/principles.md#one-grid). Every index a terminal can be sent
// is checked, because the cube and the ramp are where a stray colour would
// hide.
func TestForeignText_NoIndexEscapesThePalette(t *testing.T) {
	withColorProfile(t, colorprofile.TrueColor)
	issued := map[color.Color]bool{}
	for _, tok := range []Token{
		FullPalette.Add, FullPalette.Del, FullPalette.Accent, FullPalette.Info,
		FullPalette.Hunk, FullPalette.Spin, FullPalette.Dim, FullPalette.Dimmer,
		FullPalette.Status, FullPalette.Body, FullPalette.Bright,
	} {
		issued[tok.TrueColor] = true
	}
	for n := range 256 {
		if got := indexToken(n).TrueColor; !issued[got] {
			t.Fatalf("38;5;%d arrives as %v, which no token issued", n, got)
		}
	}
}

// A token folded onto anything but itself would mean the mapping moves a
// colour the palette did issue, which is the one thing the fold must not do
// to its own materials.
func TestForeignText_EveryTokenFoldsOntoItself(t *testing.T) {
	for _, tok := range []Token{
		FullPalette.Add, FullPalette.Del, FullPalette.Accent, FullPalette.Info,
		FullPalette.Hunk, FullPalette.Spin, FullPalette.Dim, FullPalette.Dimmer,
		FullPalette.Status, FullPalette.Body, FullPalette.Bright,
	} {
		r, g, b, _ := tok.TrueColor.RGBA()
		if got := nearestToken(int(r>>8), int(g>>8), int(b>>8)); got != tok {
			t.Fatalf("%v folds onto %v, want itself", tok.TrueColor, got.TrueColor)
		}
	}
}

// Three attributes do not survive: italic is the mark on quoted model output,
// blink is in no part of the type system, and reverse video paints the ground
// with the foreground — which is what the reading cursor does to the row it
// is on.
func TestForeignText_ItalicBlinkAndReverseAreStripped(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	plain := lipgloss.NewStyle().Foreground(testGround.Color()).Render("WORD")
	for _, c := range []struct {
		name, in string
	}{
		{"italic", "\x1b[3mWORD\x1b[0m"},
		{"blink", "\x1b[5mWORD\x1b[0m"},
		{"fast blink", "\x1b[6mWORD\x1b[0m"},
		{"reverse", "\x1b[7mWORD\x1b[0m"},
		{"all three at once", "\x1b[3;5;7mWORD\x1b[0m"},
	} {
		if got, _ := repaint(c.in, testGround); got != plain {
			t.Fatalf("%s: %q rendered as %q, want the ground alone (%q)", c.name, c.in, got, plain)
		}
	}
	// And their off-switches leave the run where it already was rather than
	// being read as some other attribute.
	for _, in := range []string{"\x1b[23mWORD", "\x1b[25mWORD", "\x1b[27mWORD"} {
		if got, _ := repaint(in, testGround); got != plain {
			t.Fatalf("%q rendered as %q, want the ground alone (%q)", in, got, plain)
		}
	}
}

// Invariant 1, on the one surface shhh does not write: with mono on, no
// foreign colour survives at all — the diff renderer's answer to chroma
// , not a recolouring. Two lines that differ only in which colour they
// name render identically, and the words are what tell them apart.
func TestForeignText_MonoDeclinesForeignColour(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	was := Mono()
	SetMono(true)
	t.Cleanup(func() { SetMono(was) })

	plain := lipgloss.NewStyle().Foreground(Palette.Dimmer.Color()).Render("WORD")
	for _, in := range []string{
		"\x1b[31mWORD\x1b[0m",
		"\x1b[32mWORD\x1b[0m",
		"\x1b[95mWORD\x1b[0m",
		"\x1b[38;5;208mWORD\x1b[0m",
		"\x1b[38;2;255;170;0mWORD\x1b[0m",
	} {
		if got, _ := repaint(in, Palette.Dimmer); got != plain {
			t.Fatalf("mono: %q rendered as %q, want the ground alone (%q)", in, got, plain)
		}
	}
	// Bold is not colour, so mono keeps it — it is half of how the invariant
	// is met in the first place.
	bold := lipgloss.NewStyle().Foreground(Palette.Dimmer.Color()).Bold(true).Render("WORD")
	if got, _ := repaint("\x1b[1;31mWORD\x1b[0m", Palette.Dimmer); got != bold {
		t.Fatalf("mono: bold red rendered as %q, want %q", got, bold)
	}
}

// The row is where this lands: a failed command's detail body and a running
// one's live tail both come through indented, and neither may carry a colour
// the palette does not own.
func TestActivityRow_ForeignOutputIsRepainted(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	row := ActivityRow{
		Kind: ActivityCommand, Verb: "run", Target: "go test ./...",
		State: ActivityFailed, Outcome: OutcomeExit(1), Expanded: true,
		Detail: []string{"--- \x1b[31mFAIL\x1b[0m: TestRoundLimit"},
	}
	view := row.View(80)
	if !strings.Contains(ansi.Strip(view), "--- FAIL: TestRoundLimit") {
		t.Fatalf("detail body lost its text:\n%s", ansi.Strip(view))
	}
	if !strings.Contains(view, lipgloss.NewStyle().Foreground(Palette.Del.Color()).Render("FAIL")) {
		t.Fatalf("detail body did not repaint FAIL as del:\n%q", view)
	}

	tail := ActivityRow{
		Kind: ActivityCommand, Verb: "run", Target: "go build ./cmd/shhh",
		State: ActivityRunning, Outcome: OutcomeRunning,
		Tail: "\x1b[2K\x1b[1Gbuilding 40%\rbuilding 100%",
	}
	if got := ansi.Strip(tail.View(80)); !strings.HasSuffix(got, "  building 100%") {
		t.Fatalf("live tail did not settle on the last write:\n%s", got)
	}
}
