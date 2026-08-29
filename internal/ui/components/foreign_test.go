package components

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// ground is what a detail body is painted in (§6a) — dimmer, the token the
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
// same thing here (§10i). The assertion is against what lipgloss renders the
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
	got, _ := repaint("\x1b[1;3;4mloud\x1b[0m", testGround)
	want := lipgloss.NewStyle().Foreground(testGround.Color()).
		Bold(true).Italic(true).Underline(true).Render("loud")
	if !strings.Contains(got, want) {
		t.Fatalf("repaint = %q, want it to contain %q", got, want)
	}
	// And they come off again when the program takes them off.
	got, _ = repaint("\x1b[1mloud\x1b[22m quiet", testGround)
	if !strings.Contains(got, lipgloss.NewStyle().Foreground(testGround.Color()).Render(" quiet")) {
		t.Fatalf("repaint = %q, want the tail unbolded", got)
	}
}

// §10b: exactly three background tints exist and all three collapse onto the
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

// An explicit colour is one the program could see when it chose it, and one
// the palette has no token to stand in for. It is kept rather than guessed at.
func TestForeignText_ExplicitColoursAreKept(t *testing.T) {
	withColorProfile(t, colorprofile.TrueColor)
	for _, c := range []struct {
		in    string
		token color.Color
	}{
		{"\x1b[38;5;208mwarn\x1b[0m", lipgloss.Color("208")},
		{"\x1b[38;2;255;170;0mwarn\x1b[0m", lipgloss.Color("#ffaa00")},
	} {
		got, _ := repaint(c.in, testGround)
		want := lipgloss.NewStyle().Foreground(c.token).Render("warn")
		if got != want {
			t.Fatalf("%q rendered as %q, want %q", c.in, got, want)
		}
	}
}

// Invariant 1, on the one surface shhh does not write: with mono on, no
// foreign colour survives at all — the diff renderer's answer to chroma
// (§10f), not a recolouring. Two lines that differ only in which colour they
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
