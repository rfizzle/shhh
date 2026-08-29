package components

// The doctor surface (S-130,
// docs/interface/surfaces.md#the-supporting-screens). The assertions here are
// about the three rules the screen exists to keep: a check is a grid row and
// nothing else, a check that did not pass states its consequence in the words
// the reader will meet it in, and the fix is offered on the row that failed
// rather than in a footer.

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func doctorChecks() []DoctorCheck {
	return []DoctorCheck{
		{Name: "binary", Subject: "shhh 0.9.4", Detail: "darwin/arm64 · via brew", Outcome: "ok"},
		{Name: "config", Subject: "~/.shhh/config.toml", Detail: "6 settings set", Outcome: "ok"},
		{Name: "model", Subject: "anthropic", Detail: "no key in any of the four places",
			Outcome: "no key", State: DoctorWarned,
			Consequence: "no session will start until a key is found",
			FixLabel:    "show the four places shhh looks",
			Fix:         []string{"env       ANTHROPIC_API_KEY — unset", "config    ~/.shhh/config.toml — no api_key"}},
		{Name: "sandbox", Subject: "no containment mechanism", Detail: "sandbox-exec not found",
			Outcome: "uncontained", Duration: "0.1s", State: DoctorFailed,
			Consequence: "every approval will show ⚠ UNCONTAINED, and an approved command runs as you",
			Fix:         []string{"sudo apt install bubblewrap", "shhh doctor"}},
		{Name: "engine", Subject: "no container engine", Detail: "podman not on PATH",
			Outcome: "not available", State: DoctorSkipped},
		{Name: "git", Subject: "~/src/shhh", Detail: "3 files changed, all tracked",
			Outcome: "ok", Duration: "0.2s"},
	}
}

func doctorScreen() *DoctorScreen {
	return &DoctorScreen{Checks: doctorChecks(), Elapsed: "0.4s"}
}

func doctorPlain(d *DoctorScreen, width int) string { return ansi.Strip(d.View(width)) }

func doctorLines(d *DoctorScreen, width int) []string {
	return strings.Split(doctorPlain(d, width), "\n")
}

// doctorRowFor is the plain line the given text appears on.
func doctorRowFor(d *DoctorScreen, width int, text string) string {
	for _, line := range doctorLines(d, width) {
		if strings.Contains(line, text) {
			return line
		}
	}
	return ""
}

// doctorIndent is how far a line is indented, in display columns.
func doctorIndent(line string) int {
	return lipgloss.Width(line) - lipgloss.Width(strings.TrimLeft(line, " "))
}

// The header names the command, how many checks it is over, how long the run
// has taken and the two keys every supporting screen offers.
func TestDoctorScreen_HeaderStatesTheRunAndTheKeys(t *testing.T) {
	head := doctorLines(doctorScreen(), 110)[0]
	for _, want := range []string{"shhh doctor", "6 checks", "0.4s", "[?] keys", "[q] quit"} {
		if !strings.Contains(head, want) {
			t.Fatalf("the header %q does not state %q", head, want)
		}
	}
}

// A run still going says so in the header, beside the count.
func TestDoctorScreen_HeaderSaysWhileTheRunIsGoing(t *testing.T) {
	d := doctorScreen()
	d.Running = true
	head := doctorLines(d, 110)[0]
	if !strings.Contains(head, "running") {
		t.Fatalf("a running header does not say so: %q", head)
	}
	if strings.Contains(doctorLines(doctorScreen(), 110)[0], "running") {
		t.Fatal("a finished run still says it is running")
	}
}

// It is the left that gives ground as the terminal narrows, not the keys: a
// takeover surface that dropped `[q]` would have no stated way out of it
// (invariant 5).
func TestDoctorScreen_NarrowHeaderKeepsTheWayOut(t *testing.T) {
	head := doctorLines(doctorScreen(), 44)[0]
	if !strings.Contains(head, "[q] quit") {
		t.Fatalf("a narrow header dropped the way out: %q", head)
	}
	if strings.Contains(head, "6 checks") {
		t.Fatalf("a narrow header kept the whole subject: %q", head)
	}
}

// Every state says what it is in a glyph and in a word, so the render carries
// its meaning with no colour at all (invariant 1).
func TestDoctorScreen_EveryStateStatesItselfTwice(t *testing.T) {
	for _, tc := range []struct {
		state DoctorState
		glyph string
	}{
		{DoctorPassed, "✓"}, {DoctorWarned, "⚠"}, {DoctorFailed, "✗"},
		{DoctorSkipped, "⊘"}, {DoctorRunning, "▸"}, {DoctorQueued, "·"},
	} {
		d := &DoctorScreen{Checks: []DoctorCheck{
			{Name: "sandbox", Subject: "bwrap", Outcome: "an outcome", State: tc.state},
		}}
		row := doctorRowFor(d, 110, "sandbox")
		if !strings.Contains(row, tc.glyph) {
			t.Fatalf("state %d does not carry %q: %q", tc.state, tc.glyph, row)
		}
		if !strings.Contains(row, "an outcome") {
			t.Fatalf("state %d dropped its outcome: %q", tc.state, row)
		}
	}
}

// A check is a grid row: the duration ends it, in the same 6-column field
// every activity row uses.
func TestDoctorScreen_TheDurationEndsTheRow(t *testing.T) {
	d := doctorScreen()
	row := doctorRowFor(d, 110, "no containment mechanism")
	if !strings.HasSuffix(row, "0.1s") {
		t.Fatalf("the duration does not end the row: %q", row)
	}
}

// The verb field is the closed vocabulary's own eight columns, so every
// target starts in the same place down the screen.
func TestDoctorScreen_TargetsStartInOneColumn(t *testing.T) {
	d := doctorScreen()
	want := -1
	for _, tc := range []struct{ name, subject string }{
		{"binary", "shhh 0.9.4"}, {"config", "~/.shhh/config.toml"},
		{"sandbox", "no containment mechanism"}, {"git", "~/src/shhh"},
	} {
		row := doctorRowFor(d, 110, tc.subject)
		at := lipgloss.Width(row[:strings.Index(row, tc.subject)])
		if want < 0 {
			want = at
			continue
		}
		if at != want {
			t.Fatalf("%s starts its target at %d, not %d: %q", tc.name, at, want, row)
		}
	}
}

// The mutation-rail column stays blank on every row, failures included: a
// check reports on the machine, it does not change it, and the mutation rail
// means the row did (invariant 2).
func TestDoctorScreen_NoRowCarriesTheMutationRail(t *testing.T) {
	for _, line := range doctorLines(doctorScreen(), 110) {
		if strings.Contains(line, "▎") {
			t.Fatalf("a check drew the mutation rail: %q", line)
		}
	}
}

// A check that did not pass states what it will cost, on its own line under
// the row, in the grid's detail column.
func TestDoctorScreen_AFailureStatesItsConsequence(t *testing.T) {
	d := doctorScreen()
	line := doctorRowFor(d, 110, "⚠ UNCONTAINED, and an approved command runs as you")
	if line == "" {
		t.Fatal("the failure does not say what it costs")
	}
	if got := doctorIndent(line); got != detailIndent {
		t.Fatalf("the consequence sits at indent %d, not %d", got, detailIndent)
	}
}

// A check that passed has nothing to say under it.
func TestDoctorScreen_APassStatesNothingUnderIt(t *testing.T) {
	d := &DoctorScreen{Checks: []DoctorCheck{
		{Name: "git", Subject: "~/src/shhh", Detail: "clean", Outcome: "ok"},
	}}
	body := doctorLines(d, 110)
	rows := 0
	for _, line := range body {
		if strings.TrimSpace(line) != "" {
			rows++
		}
	}
	// The header, its rule, the row, and the summary. Nothing else.
	if rows != 4 {
		t.Fatalf("a passing check drew %d lines, not 4:\n%s", rows, strings.Join(body, "\n"))
	}
}

// The fix is offered on the row that failed, not in a footer, and the
// offer says how much is behind it.
func TestDoctorScreen_TheFixIsOfferedOnTheRow(t *testing.T) {
	d := doctorScreen()
	line := doctorRowFor(d, 110, "show the four places shhh looks")
	if line == "" {
		t.Fatal("the row with a fix does not offer it")
	}
	if !strings.Contains(line, "[f]") {
		t.Fatalf("the offer is not a bracketed key: %q", line)
	}
	if got := doctorIndent(line); got != detailIndent {
		t.Fatalf("the offer sits at indent %d, not %d", got, detailIndent)
	}
}

// A check with no fix offers no key: a key that cannot act is not an offer
// (invariant 5).
func TestDoctorScreen_ACheckWithNoFixOffersNoKey(t *testing.T) {
	d := &DoctorScreen{Checks: []DoctorCheck{
		{Name: "engine", Subject: "no container engine", Outcome: "not available", State: DoctorSkipped},
	}}
	if strings.Contains(doctorPlain(d, 110), "[f]") {
		t.Fatalf("a check with no fix offered one:\n%s", doctorPlain(d, 110))
	}
}

// `[f]` opens the fix under the row it belongs to, one indent past the
// consequence, and says how to close it again.
func TestDoctorScreen_FShowsTheFixAndThenHidesIt(t *testing.T) {
	d := doctorScreen()
	d.Update(key("f"))
	line := doctorRowFor(d, 110, "env       ANTHROPIC_API_KEY — unset")
	if line == "" {
		t.Fatalf("[f] did not open the fix:\n%s", doctorPlain(d, 110))
	}
	if got := doctorIndent(line); got != doctorFixIndent {
		t.Fatalf("the fix sits at indent %d, not %d", got, doctorFixIndent)
	}
	if doctorRowFor(d, 110, "[f] hide it") == "" {
		t.Fatal("an open fix does not offer to close itself")
	}
	d.Update(key("f"))
	if strings.Contains(doctorPlain(d, 110), "ANTHROPIC_API_KEY") {
		t.Fatal("[f] did not close the fix again")
	}
}

// The pointer stands on the checks that have something to do on them, and
// nowhere else — a row with no key on it is not somewhere to stand
// (invariant 5).
func TestDoctorScreen_ThePointerOnlyStandsWhereThereIsAFix(t *testing.T) {
	d := doctorScreen()
	if row := doctorRowFor(d, 110, "anthropic"); !strings.Contains(row, "❯") {
		t.Fatalf("the pointer did not start on the first check with a fix: %q", row)
	}
	d.Update(key("down"))
	if row := doctorRowFor(d, 110, "no containment mechanism"); !strings.Contains(row, "❯") {
		t.Fatalf("[↓] did not reach the next check with a fix: %q", row)
	}
	// Past the last one it stays put rather than wrapping.
	d.Update(key("down"))
	if row := doctorRowFor(d, 110, "no containment mechanism"); !strings.Contains(row, "❯") {
		t.Fatalf("the pointer moved past the last check with a fix: %q", row)
	}
	if row := doctorRowFor(d, 110, "shhh 0.9.4"); strings.Contains(row, "❯") {
		t.Fatalf("the pointer stood on a check with nothing to do: %q", row)
	}
}

// A run where nothing needs doing has no pointer at all, and does not offer
// the key that moves one.
func TestDoctorScreen_ACleanRunHasNoPointer(t *testing.T) {
	d := &DoctorScreen{Checks: []DoctorCheck{
		{Name: "git", Subject: "~/src/shhh", Detail: "clean", Outcome: "ok"},
		{Name: "store", Subject: "~/.local/share/shhh", Outcome: "ok"},
	}}
	out := doctorPlain(d, 110)
	if strings.Contains(out, "❯") || strings.Contains(out, "[↑↓]") {
		t.Fatalf("a clean run drew a pointer or offered to move it:\n%s", out)
	}
}

// A row that has a fix but does not hold the keyboard carries its key grey
// rather than in the colour that means "you can press this".
func TestDoctorScreen_AnUnpointedFixKeyIsNotAnOffer(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	d := doctorScreen()
	lines := strings.Split(d.View(110), "\n")
	var live, inert string
	for _, line := range lines {
		switch {
		case strings.Contains(ansi.Strip(line), "show the four places"):
			live = line
		case strings.Contains(ansi.Strip(line), "show the 2-line fix"):
			inert = line
		}
	}
	if live == "" || inert == "" {
		t.Fatalf("expected one live and one waiting fix key:\n%s", ansi.Strip(d.View(110)))
	}
	if !strings.Contains(live, sty.Info.Render("[f]")) {
		t.Fatalf("the pointed row's key is not offered in info: %q", live)
	}
	if strings.Contains(inert, sty.Info.Render("[f]")) {
		t.Fatalf("a waiting row's key reads as an offer: %q", inert)
	}
}

// A check with no FixLabel says how many lines are behind the key anyway,
// because an offer that did not say what it opens is a keystroke taken on
// trust.
func TestDoctorScreen_TheFixKeyCountsWhatItOpens(t *testing.T) {
	d := doctorScreen()
	if doctorRowFor(d, 110, "show the 2-line fix") == "" {
		t.Fatalf("an unlabelled fix does not count its lines:\n%s", doctorPlain(d, 110))
	}
}

// The summary counts every outcome, including the ones still running, and
// leads with the glyph of the worst of them.
func TestDoctorScreen_TheSummaryCountsEveryOutcome(t *testing.T) {
	d := doctorScreen()
	d.Checks = append(d.Checks, DoctorCheck{Name: "update", Outcome: "queued", State: DoctorQueued})
	foot := doctorRowFor(d, 110, "passed")
	for _, want := range []string{"✗", "1 failed", "1 warning", "3 passed", "1 not checked", "1 queued"} {
		if !strings.Contains(foot, want) {
			t.Fatalf("the summary %q does not state %q", foot, want)
		}
	}
}

// With nothing failed or warned the summary leads with the glyph that says
// so.
func TestDoctorScreen_ACleanSummaryLeadsWithThePassGlyph(t *testing.T) {
	d := &DoctorScreen{Checks: []DoctorCheck{{Name: "git", Subject: "~/src/shhh", Outcome: "ok"}}}
	foot := doctorRowFor(d, 110, "1 passed")
	if !strings.Contains(foot, "✓") {
		t.Fatalf("a clean summary does not lead with ✓: %q", foot)
	}
}

// `[c]` copies without closing the screen; `[r]` is not offered while the run
// is still going, because re-running one that has not finished is not an
// offer (invariant 5).
func TestDoctorScreen_CopyAndRerunResolveToTheHost(t *testing.T) {
	d := doctorScreen()
	done, result := d.Update(key("c"))
	if done {
		t.Fatal("[c] closed the screen")
	}
	if command, ok := result.(DoctorCommand); !ok || command.Act != DoctorCopy {
		t.Fatalf("[c] resolved to %#v, not a copy", result)
	}
	done, result = d.Update(key("r"))
	if done {
		t.Fatal("[r] closed the screen")
	}
	if command, ok := result.(DoctorCommand); !ok || command.Act != DoctorRerun {
		t.Fatalf("[r] resolved to %#v, not a re-run", result)
	}

	running := doctorScreen()
	running.Running = true
	if _, result := running.Update(key("r")); result != nil {
		t.Fatalf("[r] resolved to %#v while the run was still going", result)
	}
	if strings.Contains(doctorPlain(running, 110), "[r]") {
		t.Fatal("[r] was offered while the run was still going")
	}
}

// `[q]`, `[esc]` and ctrl+c leave; nothing else does.
func TestDoctorScreen_OnlyTheQuitKeysCloseIt(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		if done, _ := doctorScreen().Update(key(k)); !done {
			t.Fatalf("%s did not close the screen", k)
		}
	}
	for _, k := range []string{"f", "c", "r", "?", "down", "enter", "/"} {
		if done, _ := doctorScreen().Update(key(k)); done {
			t.Fatalf("%s closed the screen", k)
		}
	}
}

// `[?]` lists every key the screen has, one to a line, and closes again.
func TestDoctorScreen_QuestionMarkListsEveryKey(t *testing.T) {
	d := doctorScreen()
	d.Update(key("?"))
	out := doctorPlain(d, 110)
	for _, want := range []string{"[↑↓/jk]", "[f]", "[c]", "[r]", "[esc]", "[q]", "hide the keys"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the key list does not offer %q:\n%s", want, out)
		}
	}
	d.Update(key("?"))
	if strings.Contains(doctorPlain(d, 110), "hide the keys") {
		t.Fatal("[?] did not close the key list")
	}
}

// The notice a key left behind sits at the foot, under the summary.
func TestDoctorScreen_TheNoticeSitsAtTheFoot(t *testing.T) {
	d := doctorScreen()
	d.Notice = "copied the report to the clipboard"
	lines := doctorLines(d, 110)
	if last := lines[len(lines)-1]; !strings.Contains(last, "copied the report") {
		t.Fatalf("the notice is not the last line: %q", last)
	}
}

// A short terminal drops the checks with nothing to say first — the passing
// ones — and names what went (invariant 4).
func TestDoctorScreen_AShortTerminalDropsThePassesFirst(t *testing.T) {
	d := doctorScreen()
	d.MaxLines = 14
	out := doctorPlain(d, 110)
	if !strings.Contains(out, "no containment mechanism") {
		t.Fatalf("a short terminal dropped the failure:\n%s", out)
	}
	if !strings.Contains(out, "anthropic") {
		t.Fatalf("a short terminal dropped the warning:\n%s", out)
	}
	marker := doctorRowFor(d, 110, "↓ ")
	if marker == "" {
		t.Fatalf("a short terminal dropped rows without saying so:\n%s", out)
	}
	if !strings.Contains(marker, "binary") {
		t.Fatalf("the marker does not name what went: %q", marker)
	}
	if lines := len(doctorLines(d, 110)); lines > 14 {
		t.Fatalf("the screen drew %d lines against a budget of 14", lines)
	}
}

// A terminal too short for even the failures still stays inside its budget,
// and still counts every check at the foot.
func TestDoctorScreen_AShorterTerminalStaysInsideItsBudget(t *testing.T) {
	d := doctorScreen()
	d.MaxLines = 8
	if lines := len(doctorLines(d, 110)); lines > 8 {
		t.Fatalf("the screen drew %d lines against a budget of 8:\n%s", lines, doctorPlain(d, 110))
	}
	if doctorRowFor(d, 110, "1 failed") == "" {
		t.Fatalf("the summary stopped counting:\n%s", doctorPlain(d, 110))
	}
}

// The spinner is one tick source: a host that is not ticking gets `▸` rather
// than a frozen braille frame, because a stopped spinner reads as a hang.
func TestDoctorScreen_TheSpinnerIsOneTickSource(t *testing.T) {
	d := &DoctorScreen{Running: true, Checks: []DoctorCheck{
		{Name: "update", Subject: "check for a newer shhh", Outcome: "running…", State: DoctorRunning},
	}}
	// Three places show it: the header, the row that is running, and the
	// summary glyph that leads the count.
	out := doctorPlain(d, 110)
	if strings.Count(out, "▸") != 3 {
		t.Fatalf("a still screen does not show ▸ everywhere it should:\n%s", out)
	}
	d.Spin, d.Frame = true, 2
	frame := SpinnerFrames[2]
	if strings.Count(doctorPlain(d, 110), frame) != 3 {
		t.Fatalf("a ticking screen shows more than one frame:\n%s", doctorPlain(d, 110))
	}
	if strings.Contains(doctorPlain(d, 110), "▸") {
		t.Fatalf("a ticking screen froze a still glyph:\n%s", doctorPlain(d, 110))
	}
}

// Nothing here draws a frame: the supporting screens are takeover surfaces,
// full width, and a box around one would be a card the size of the terminal.
func TestDoctorScreen_DrawsNoFrame(t *testing.T) {
	for _, line := range doctorLines(doctorScreen(), 110) {
		if strings.ContainsAny(line, "╭╮╰╯│") {
			t.Fatalf("the screen drew a frame: %q", line)
		}
	}
}

// Nothing overruns the width it was given.
func TestDoctorScreen_NothingOverrunsTheWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 110, 130} {
		d := doctorScreen()
		d.Notice = "copied the report to the clipboard"
		for _, line := range doctorLines(d, width) {
			if lipgloss.Width(line) > width {
				t.Fatalf("at %d columns a line is %d wide: %q", width, lipgloss.Width(line), line)
			}
		}
	}
}

// A run with no checks in it says so rather than drawing a summary of
// nothing.
func TestDoctorScreen_AnEmptyRunSaysSo(t *testing.T) {
	d := &DoctorScreen{}
	if !strings.Contains(doctorPlain(d, 110), "no checks to run") {
		t.Fatalf("an empty run says nothing:\n%s", doctorPlain(d, 110))
	}
}

// The host replaces every check as the run answers, and the pointer follows
// what is still worth standing on rather than the index it was left at.
func TestDoctorScreen_ThePointerSurvivesTheHostReplacingTheChecks(t *testing.T) {
	d := doctorScreen()
	d.Update(key("down"))
	// The check the pointer was on has since been fixed and re-run.
	d.Checks[3] = DoctorCheck{Name: "sandbox", Subject: "bwrap", Outcome: "contained"}
	if row := doctorRowFor(d, 110, "anthropic"); !strings.Contains(row, "❯") {
		t.Fatalf("the pointer did not fall back to a check that still needs something: %q", row)
	}
}
