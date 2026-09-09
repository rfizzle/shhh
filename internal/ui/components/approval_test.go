package components

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func TestApprovalCard_CommandVariant(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand,
		Title:   "Approve command",
		Act:     "go test ./...",
		Answer:  "run it once",
	}
	view := c.View(80)
	if !strings.Contains(view, "Approve command") || !strings.Contains(view, "go test ./...") {
		t.Fatalf("command card should show title and command:\n%s", view)
	}
	if !strings.Contains(view, "[y] run it once") || strings.Contains(view, "[a]") {
		t.Fatalf("without AllowAlways the card offers the two answers alone:\n%s", view)
	}

	c.AllowAlways = true
	c.AlwaysHint = "allow commands without asking this session"
	view = ansi.Strip(c.View(110))
	if !strings.Contains(view, "[a] allow commands without asking this session") {
		t.Fatalf("AllowAlways should offer [a] under what it grants:\n%s", view)
	}
}

func TestApprovalCard_Warnings(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Act:      "rm -rf /",
		Warnings: []string{"deletes files recursively"},
		Answer:   "run it once",
	}
	view := c.View(80)
	if !strings.Contains(view, "⚠ deletes files recursively") {
		t.Fatalf("safety warnings should render as ⚠ rows:\n%s", view)
	}
}

// Severity is said three ways — the border, the chip on the title rail and
// the first body row — and the three say it in the same words, so a reader
// comparing them is not first working out that they are one claim. What the
// row adds is the reading behind the level, which is what the chip has no
// room for.
func TestApprovalCard_SeverityIsAWordNotOnlyAColour(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Act:      "rm -rf ./dist",
		Severity: SeverityHigh,
		Warnings: []string{"deletes files recursively (rm -rf)"},
		Answer:   "run it once",
	}
	view := ansi.Strip(c.View(90))
	if strings.Count(view, "⚠ HIGH") != 2 {
		t.Fatalf("the chip and the body row both state the level:\n%s", view)
	}
	if !strings.Contains(view, "⚠ HIGH · deletes files recursively (rm -rf)") {
		t.Fatalf("the body row should state the level with what makes it that:\n%s", view)
	}
	c.Severity, c.Warnings = SeverityMedium, nil
	c.SeverityReason = "edits one file under internal/agent"
	view = ansi.Strip(c.View(90))
	if !strings.Contains(view, "⚠ medium · edits one file under internal/agent") {
		t.Fatalf("a card with a reason states it beside the level:\n%s", view)
	}
	c.Severity, c.SeverityReason = SeverityLow, ""
	if !strings.Contains(ansi.Strip(c.View(90)), "⚠ low") {
		t.Fatal("a low-severity card still states its level")
	}
}

// The three colours of the ladder: low and medium are the mutation rail's
// accent, HIGH and an uncontained card are del, and a card with no rating at
// all takes the tone of a surface waiting for an answer. The chip, the body
// row and the border are one colour, whichever it is.
func TestApprovalCard_TheSeverityLadderHasThreeColours(t *testing.T) {
	for _, tc := range []struct {
		severity Severity
		want     string
	}{
		{SeverityLow, sty.Accent.Render("⚠ low")},
		{SeverityMedium, sty.Accent.Render("⚠ medium")},
		{SeverityHigh, sty.Del.Render("⚠ HIGH")},
	} {
		c := &ApprovalCard{
			Variant: ApprovalCommand, Title: "Approve command",
			Act:      "go test ./...",
			Severity: tc.severity, Answer: "run it once",
		}
		view := c.View(90)
		if strings.Count(view, tc.want) != 2 {
			t.Fatalf("%v should paint its chip and its body row %q:\n%s", tc.severity, tc.want, view)
		}
		if !strings.Contains(view, tc.severity.tone().Render("╭─ Approve command ")) {
			t.Fatalf("%v should paint its border to match:\n%s", tc.severity, view)
		}
	}
	unrated := &ApprovalCard{
		Variant: ApprovalGeneric, Title: "Approve tool",
		Act: "use web_fetch", Answer: "allow it",
	}
	if !strings.Contains(unrated.View(90), sty.Info.Render("╭─ Approve tool ")) {
		t.Fatalf("a card with no rating takes the decision tone:\n%s", unrated.View(90))
	}
}

// The containment in force is a body row and survives every width; only an
// uncontained action reaches the title rail, where ⚠ UNCONTAINED is promoted
// ahead of the severity.
func TestApprovalCard_ContainmentIsARowAndSurvivesTheNarrowCard(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Act:      "go build ./...",
		Severity: SeverityHigh,
		Fields: []CardField{
			{Label: "⛨", Value: "workspace-write · network allowed", Tone: ToneChrome},
		},
		Answer: "run it once",
	}
	top := strings.SplitN(ansi.Strip(c.View(100)), "\n", 2)[0]
	if strings.Contains(top, "⛨") || !strings.Contains(top, "⚠ HIGH") {
		t.Fatalf("the title rail carries the severity and no containment chip: %q", top)
	}
	// Sixty columns is the width the rail sheds a chip at, and the row a
	// flagged card must not stop stating is the one saying what contains it.
	for _, width := range []int{100, 60} {
		if view := ansi.Strip(c.View(width)); !strings.Contains(view, "⛨") ||
			!strings.Contains(view, "workspace-write") {
			t.Fatalf("the containment row should survive w%d:\n%s", width, view)
		}
	}

	c.Uncontained = true
	top = strings.SplitN(ansi.Strip(c.View(100)), "\n", 2)[0]
	if !strings.Contains(top, "⚠ UNCONTAINED") {
		t.Fatalf("an uncontained action promotes ⚠ UNCONTAINED into the title: %q", top)
	}
}

// The blast-radius block states what the action touches before the keys, and
// the keys sit below a rule so they never blend into it.
func TestApprovalCard_BlastRadiusBlockAndRule(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Act:      "rm -rf ./dist",
		Severity: SeverityHigh,
		Fields: []CardField{
			{Label: "touches", Value: "./dist", Detail: "412 files, 84.0 MB"},
			{Label: "undo", Value: "none", Detail: "rm bypasses the changeset", Tone: ToneRisk},
			{Label: "network", Value: "open", Detail: "the workspace profile allows it", Tone: ToneOpen},
		},
		Answer:   "run it once",
		Return:   "don't — the safe answer; the decision waits",
		Footnote: "[a] always — not offered: a safety-flagged command is never pre-approved",
	}
	lines := strings.Split(ansi.Strip(c.View(90)), "\n")
	view := strings.Join(lines, "\n")
	for _, want := range []string{
		"touches   ./dist — 412 files, 84.0 MB",
		"undo      none — rm bypasses the changeset",
		"network   open — the workspace profile allows it",
		"[esc] don't — the safe answer",
		"[a] always — not offered",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("card should contain %q:\n%s", want, view)
		}
	}
	rule, keys := -1, -1
	for i, line := range lines {
		if strings.HasPrefix(line, "├") {
			rule = i
		}
		if strings.Contains(line, "[y] run it once") {
			keys = i
		}
	}
	if rule < 0 || keys < 0 || rule >= keys {
		t.Fatalf("the keys must sit below a horizontal rule (rule=%d keys=%d):\n%s", rule, keys, view)
	}
}

// A detail that cannot fit is dropped, leaving a whole statement rather than
// half of one.
func TestApprovalCard_FieldDropsDetailBeforeClipping(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand,
		Title:   "Approve command",
		Act:     "rm -rf ./dist",
		Fields:  []CardField{{Label: "touches", Value: "./dist", Detail: strings.Repeat("very long detail ", 8)}},
		Answer:  "run it once",
	}
	view := ansi.Strip(c.View(44))
	if !strings.Contains(view, "touches   ./dist") {
		t.Fatalf("the value should survive a narrow card:\n%s", view)
	}
	if strings.Contains(view, "very long") {
		t.Fatalf("a detail that does not fit is dropped, not clipped:\n%s", view)
	}
}

func TestApprovalCard_EditVariantShowsDiffAndStats(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalEdit,
		Title:   "Approve edit",
		Act:     "edit main.go",
		Hunks:   diff.Compute("a\nb\n", "a\nc\nd\n"),
		Answer:  "apply the change",
	}
	// The diff body carries line numbers (
	// docs/interface/surfaces.md#the-approval-card), and the reversibility line
	// rides the stats row.
	c.Reversibility = "undo yes — recorded, and git has this file"
	view := c.View(80)
	for _, want := range []string{"@@", "- 2  b", "+ 2  c", "+ 3  d",
		"+2 −1 · 1 hunk · undo yes — recorded, and git has this file"} {
		if !strings.Contains(view, want) {
			t.Fatalf("edit card should contain %q:\n%s", want, view)
		}
	}
}

func TestApprovalCard_FullDiffKey(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalEdit,
		Title:    "Approve edit",
		Hunks:    diff.Compute("a\n", "b\n"),
		Answer:   "apply the change",
		FullDiff: true,
	}
	if !strings.Contains(ansi.Strip(c.View(80)), "[d] full diff") {
		t.Fatal("card should hint the full-diff key when FullDiff is set")
	}
	done, result := c.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if !done || result != ApprovalFullDiff {
		t.Fatalf("d should request the full diff, got done=%v result=%v", done, result)
	}

	// Without FullDiff, d is unrecognized and the card keeps waiting.
	c.FullDiff = false
	if done, _ := c.Update(tea.KeyPressMsg{Code: 'd', Text: "d"}); done {
		t.Fatal("d should be ignored when FullDiff is off")
	}
}

func TestApprovalCard_EditVariantBoundsHeight(t *testing.T) {
	var old, new strings.Builder
	for i := 0; i < 100; i++ {
		new.WriteString("line\n")
	}
	c := &ApprovalCard{
		Variant:  ApprovalEdit,
		Title:    "Approve edit",
		Act:      "write big.txt",
		Hunks:    diff.Compute(old.String(), new.String()),
		Answer:   "apply the change",
		MaxLines: 12,
	}
	view := c.View(80)
	if got := len(strings.Split(view, "\n")); got != 12 {
		t.Fatalf("card should occupy exactly its MaxLines budget, got %d rows", got)
	}
	if !strings.Contains(view, "more lines · "+keys.Bracket(keys.Decision.ScrollDown)) {
		t.Fatalf("bounded card should count the scrolled-off diff and name the key:\n%s", view)
	}
}

// A body taller than the panel scrolls behind counted tails, and the block
// under the rule never moves (docs/interface/surfaces.md#the-approval-card).
func TestApprovalCard_BodyScrollsBehindCountedTails(t *testing.T) {
	var old, new strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&new, "line %d\n", i)
	}
	c := &ApprovalCard{
		Variant:  ApprovalEdit,
		Title:    "Approve edit",
		Act:      "write big.txt",
		Hunks:    diff.Compute(old.String(), new.String()),
		Answer:   "apply the change",
		MaxLines: 12,
	}
	plain := ansi.Strip(c.View(80))
	if !strings.Contains(plain, "more lines · "+keys.Bracket(keys.Decision.ScrollDown)) {
		t.Fatalf("the window should count what it cut:\n%s", plain)
	}
	if !strings.Contains(plain, "[y] apply the change") {
		t.Fatalf("the decision block never scrolls off:\n%s", plain)
	}

	c.BodyOffset = 10
	scrolled := ansi.Strip(c.View(80))
	if scrolled == plain {
		t.Fatal("scrolling should move the body")
	}
	if !strings.Contains(scrolled, "lines above · "+keys.Bracket(keys.Decision.ScrollUp)) {
		t.Fatalf("a scrolled window counts what is above it too:\n%s", scrolled)
	}
	if !strings.Contains(scrolled, "[y] apply the change") {
		t.Fatalf("the decision block never scrolls off:\n%s", scrolled)
	}

	maxBody, _ := c.ScrollBounds(80)
	c.BodyOffset = maxBody
	bottom := ansi.Strip(c.View(80))
	if strings.Contains(bottom, "more lines · "+keys.Bracket(keys.Decision.ScrollDown)) {
		t.Fatalf("at the end there is nothing below to count:\n%s", bottom)
	}
}

// A bound so tight the hint block eats it still shows one row of body —
// never an unbounded card, never a body hidden uncounted.
func TestApprovalCard_TinyBoundKeepsTheDecisionAndOneBodyRow(t *testing.T) {
	var old, new strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&new, "line %d\n", i)
	}
	edit := &ApprovalCard{
		Variant:  ApprovalEdit,
		Title:    "Approve edit",
		Act:      "write big.txt",
		Hunks:    diff.Compute(old.String(), new.String()),
		Answer:   "apply the change",
		MaxLines: 6,
	}
	view := ansi.Strip(edit.View(80))
	if rows := len(strings.Split(view, "\n")); rows > 7 {
		t.Fatalf("a MaxLines 6 card must stay near its budget, got %d rows:\n%s", rows, view)
	}
	if !strings.Contains(view, "[y] apply the change") {
		t.Fatalf("the decision run never gives way to the body:\n%s", view)
	}
	if !strings.Contains(view, "more lines") {
		t.Fatalf("the one body row counts everything the bound swallowed:\n%s", view)
	}

	cmd := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Act:      "rm -rf ./dist",
		Answer:   "run it once",
		Return:   "[esc] back to your draft — the decision stays waiting, nothing is denied",
		MaxLines: 5,
	}
	view = ansi.Strip(cmd.View(80))
	if !strings.Contains(view, "rm -rf ./dist") {
		t.Fatalf("the command being approved is never hidden uncounted:\n%s", view)
	}
}

// A card whose keys are not yet live counts its fold without naming a chord
// the draft still owns.
func TestApprovalCard_NotYetLiveTailNamesNoKey(t *testing.T) {
	var old, new strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&new, "line %d\n", i)
	}
	c := &ApprovalCard{
		Variant:    ApprovalEdit,
		Title:      "Approve edit",
		Act:        "write big.txt",
		Hunks:      diff.Compute(old.String(), new.String()),
		Answer:     "apply the change",
		MaxLines:   12,
		NotYetLive: true,
		Handover:   "ctrl+space",
	}
	view := ansi.Strip(c.View(80))
	if !strings.Contains(view, "more lines") {
		t.Fatalf("the fold is still counted:\n%s", view)
	}
	if strings.Contains(view, "more lines · "+keys.Bracket(keys.Decision.ScrollDown)) {
		t.Fatalf("an inert card must not advertise a chord the draft owns:\n%s", view)
	}
}

// A body wider than the panel pans by columns, and a row still running past
// the right edge ends in ›.
func TestApprovalCard_WideBodyPans(t *testing.T) {
	wide := "run: " + strings.Repeat("abcdefghij", 30) // 300+ columns
	c := &ApprovalCard{
		Variant: ApprovalCommand,
		Title:   "Approve command",
		Act:     wide,
		Answer:  "run it once",
	}
	plain := ansi.Strip(c.View(80))
	if !strings.Contains(plain, "›") {
		t.Fatalf("a clipped row should end in the pan marker:\n%s", plain)
	}
	c.PanOffset = 5
	panned := ansi.Strip(c.View(80))
	if panned == plain {
		t.Fatal("panning should move the body")
	}
	if !strings.Contains(panned, "fghij") || strings.Contains(panned, "run: ") {
		t.Fatalf("the pan should cut the first five columns:\n%s", panned)
	}
	_, maxPan := c.ScrollBounds(80)
	c.PanOffset = maxPan
	if end := ansi.Strip(c.View(80)); strings.Contains(end, "›") {
		t.Fatalf("panned to the end, nothing runs past the edge:\n%s", end)
	}
}

func TestApprovalCard_GenericVariant(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalGeneric,
		Title:   "Approve tool",
		Act:     "use my_tool",
		Summary: "do the thing",
		Answer:  "allow it",
	}
	view := c.View(80)
	if !strings.Contains(view, "use my_tool") || !strings.Contains(view, "do the thing") {
		t.Fatalf("generic card should show tool and summary:\n%s", view)
	}
}

func TestApprovalCard_Keys(t *testing.T) {
	c := &ApprovalCard{Answer: "run it once"}
	cases := []struct {
		key    string
		done   bool
		result ApprovalDecision
	}{
		{"y", true, ApprovalApprove},
		{"enter", true, ApprovalApprove},
		{"n", true, ApprovalDeny},
		{"esc", true, ApprovalDeny},
		{"ctrl+c", true, ApprovalDeny},
		{"a", false, ApprovalWaiting}, // AllowAlways off: [a] ignored
		{"z", false, ApprovalWaiting},
	}
	for _, tc := range cases {
		done, result := c.Update(key(tc.key))
		if done != tc.done || result != tc.result {
			t.Fatalf("key %q: got done=%v result=%v, want done=%v result=%v",
				tc.key, done, result, tc.done, tc.result)
		}
	}

	c.AllowAlways = true
	if done, result := c.Update(key("a")); !done || result != ApprovalAlways {
		t.Fatalf("with AllowAlways, [a] should resolve to ApprovalAlways, got done=%v result=%v", done, result)
	}
}

// --- the decision run ----------------------------------------

// runRow is the rendered row carrying a given offer.
func runRow(t *testing.T, c *ApprovalCard, width int, mark string) string {
	t.Helper()
	for _, line := range strings.Split(c.View(width), "\n") {
		if strings.Contains(ansi.Strip(line), mark) {
			return line
		}
	}
	t.Fatalf("no rendered row carries %q", mark)
	return ""
}

// The run is the bracket grammar and nothing else: a key, its imperative,
// and no compact `[y/n/a]` prompt anywhere on the card.
func TestApprovalCard_TheRunIsBracketedOffers(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Act:    "go test ./...",
		Answer: "run it once", AllowAlways: true,
		AlwaysHint: `allow "go test" without asking`,
		FullDiff:   true, FullLabel: "full view",
	}
	view := ansi.Strip(c.View(110))
	for _, want := range []string{
		"[y] run it once", "[n] deny", `[a] allow "go test" without asking`,
		"[d] full view", "[esc] " + waitingWords,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the run should offer %q:\n%s", want, view)
		}
	}
	for _, gone := range []string{"[y/n", "[y/N", "(a:", "(d:", "Run this command?"} {
		if strings.Contains(view, gone) {
			t.Fatalf("the compact prompt is gone; found %q:\n%s", gone, view)
		}
	}
}

// Every variant carries the esc line, not only the ones a flagged command put
// one on: the way out of a decision is what a reader must be able to find
// without having pressed anything.
func TestApprovalCard_EveryVariantSaysWhatEscDoes(t *testing.T) {
	for _, variant := range []ApprovalVariant{ApprovalCommand, ApprovalEdit, ApprovalGeneric} {
		c := &ApprovalCard{
			Variant: variant, Title: "Approve", Act: "act",
			Answer: "do it",
		}
		if view := ansi.Strip(c.View(90)); !strings.Contains(view, "[esc] "+waitingWords) {
			t.Fatalf("variant %d should state what esc does:\n%s", variant, view)
		}
	}
	// A card with its own words about esc says those instead.
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Act: "rm -rf ./dist", Answer: "run it once",
		Return: "don't — the safe answer",
	}
	if view := ansi.Strip(c.View(90)); !strings.Contains(view, "[esc] don't — the safe answer") {
		t.Fatalf("a card that named its safe answer should say so:\n%s", view)
	}
}

// A key owns its bracket and the imperative after it: the words are what a
// reader aims at, and one cell is not a target.
func TestApprovalCard_AKeyOwnsItsWords(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Act:    "go test ./...",
		Answer: "run it once", AllowAlways: true,
		AlwaysHint: `allow "go test" without asking`,
	}
	for _, tc := range []struct{ offer, key string }{
		{"[y] run it once", "y"},
		{"[n] deny", "n"},
		{`[a] allow "go test" without asking`, "a"},
	} {
		row := runRow(t, c, 110, tc.offer)
		plain := ansi.Strip(row)
		at := strings.Index(plain, tc.offer)
		start := ansi.StringWidth(plain[:at])
		for i := range ansi.StringWidth(tc.offer) {
			got, ok := c.KeyAt(row, start+i)
			if !ok || got != tc.key {
				t.Fatalf("cell %d of %q should be %q, got %q (found=%v)", i, tc.offer, tc.key, got, ok)
			}
		}
		if _, ok := c.KeyAt(row, start-1); ok && start > 0 {
			t.Fatalf("the cell before %q belongs to no key", tc.offer)
		}
	}
}

// notedCard is the card as the session's own decisions draw it: the two
// answers, and the two that carry a sentence.
func notedCard() *ApprovalCard {
	return &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Act:    "go test ./...",
		Answer: "run it once", Noted: true,
	}
}

// The shifted pair sits beside the answer it carries, and each of the four
// resolves to the key the run drew.
func TestApprovalCard_TheNotedRunPairsEachAnswerWithItsSentence(t *testing.T) {
	c := notedCard()
	view := ansi.Strip(c.View(110))
	for _, want := range []string{"[y] run it once", "[Y] ", "[n] deny", "[N] "} {
		if !strings.Contains(view, want) {
			t.Fatalf("a noted card draws both spellings of both answers, missing %q:\n%s", want, view)
		}
	}
	for _, tc := range []struct {
		key  string
		want ApprovalDecision
	}{
		{"y", ApprovalApprove}, {"Y", ApprovalApproveNoted},
		{"n", ApprovalDeny}, {"N", ApprovalDenyNoted},
	} {
		if done, got := notedCard().Update(key(tc.key)); !done || got != tc.want {
			t.Errorf("%q should answer %v, got %v/%v", tc.key, tc.want, done, got)
		}
	}
}

// A card with nothing waiting to read a sentence offers neither shifted key,
// and answers neither.
func TestApprovalCard_WithoutTheOfferTheShiftedKeysAreNotDrawn(t *testing.T) {
	c := notedCard()
	c.Noted = false
	if view := ansi.Strip(c.View(110)); strings.Contains(view, "[Y]") || strings.Contains(view, "[N]") {
		t.Fatalf("a card with no note offer draws neither shifted key:\n%s", view)
	}
	for _, k := range []string{"Y", "N"} {
		if done, got := c.Update(key(k)); done && (got == ApprovalApproveNoted || got == ApprovalDenyNoted) {
			t.Errorf("%q must not open a field on a card that offers none, got %v", k, got)
		}
	}
}

// A noted card that took the keyboard by arrival claims all four answers —
// none of them settles anything a reader could not take back with esc — and
// still nothing whose consequence outlives the call.
func TestApprovalCard_ANotedArrivalClaimsAllFourAnswers(t *testing.T) {
	c := notedCard()
	c.HeldOnArrival, c.Handover = true, "ctrl+space"
	c.AllowAlways, c.AlwaysHint = true, `allow "go test" without asking`
	c.FullDiff = true
	var got []string
	for _, k := range c.KeyRun() {
		got = append(got, k.Key)
	}
	if strings.Join(got, "") != "yYnN" {
		t.Fatalf("an arrival card should draw the four answers alone, got %v", got)
	}
	row := runRow(t, c, 80, "[y] run it once")
	for _, absent := range []string{"a", "d"} {
		for col := range ansi.StringWidth(ansi.Strip(row)) {
			if k, ok := c.KeyAt(row, col); ok && k == absent {
				t.Fatalf("an arrival card must offer no cell for %q", absent)
			}
		}
	}
}

// The open field: the run is drawn dead and says why, the label names what
// the key that opened it asked for, and the field the host handed over is
// under it.
func TestApprovalCard_TheOpenFieldDrawsTheRunDead(t *testing.T) {
	for _, tc := range []struct {
		allow bool
		label string
	}{{false, noteWhyNot}, {true, noteWhatNext}} {
		c := notedCard()
		c.NoteOpen, c.NoteAllow, c.NoteField = true, tc.allow, "not that file"
		view := ansi.Strip(c.View(80))
		for _, want := range []string{typingWords, "┄ " + tc.label, "not that file", "[esc]", "[enter]"} {
			if !strings.Contains(view, want) {
				t.Fatalf("the open field should carry %q:\n%s", want, view)
			}
		}
		// The card's own way out is the field's while the field has the
		// keyboard, so the card does not state a second one.
		if strings.Contains(view, waitingWords) {
			t.Fatalf("a card whose field holds the keyboard states no esc of its own:\n%s", view)
		}
		x, y, ok := c.FieldOrigin(80)
		if !ok {
			t.Fatal("an open field has a place on the card")
		}
		rows := strings.Split(view, "\n")
		if y < 0 || y >= len(rows) || !strings.Contains(rows[y], "not that file") {
			t.Fatalf("FieldOrigin points at row %d, which is %q", y, rows[min(y, len(rows)-1)])
		}
		if at := ansi.StringWidth(rows[y][:strings.Index(rows[y], "not that file")]); at != x {
			t.Fatalf("FieldOrigin says column %d, the field starts at %d in %q", x, at, rows[y])
		}
	}
}

// Below the frame the card is drawn bare — no border, no rules — so the
// field's place is counted in the rows that are left, or the caret would sit
// two rows and two columns off the row it belongs to.
func TestApprovalCard_TheFieldsPlaceSurvivesTheBareCard(t *testing.T) {
	c := notedCard()
	c.NoteOpen, c.NoteField = true, "not that file"
	const narrow = minCardWidth - 1
	x, y, ok := c.FieldOrigin(narrow)
	if !ok {
		t.Fatal("an open field has a place on a bare card too")
	}
	// The card is too narrow to show the sentence, so the row is named by
	// what is left of it: the indent, and the label above it.
	rows := strings.Split(ansi.Strip(c.View(narrow)), "\n")
	if y < 1 || y >= len(rows) || !strings.HasPrefix(rows[y], strings.Repeat(" ", noteIndent)+"not") {
		t.Fatalf("FieldOrigin points at row %d of %q", y, rows)
	}
	if !strings.HasPrefix(rows[y-1], "┄ ") {
		t.Fatalf("the field should sit under its label, but row %d is %q", y-1, rows[y-1])
	}
	if x != noteIndent {
		t.Fatalf("a bare card has no border to step past, so the field starts at %d, not %d", noteIndent, x)
	}
}

// A row that does not carry the run carries no target: the geometry is read
// out of the render, so a key a narrow terminal clipped away is not
// clickable.
func TestApprovalCard_ARowWithoutTheRunHasNoKeys(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Act:    "go test ./...",
		Answer: "run it once",
	}
	if _, ok := c.KeyAt("go test ./...", 4); ok {
		t.Fatal("a body row should carry no decision key")
	}
}

// A card holding the keyboard by arrival claims two keys, so those are the
// only two cells a pointer can land on — [a] and [d] still want the handover.
func TestApprovalCard_HeldOnArrivalOffersOnlyItsTwoKeys(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Act:         "go test ./...",
		Answer:      "run it once",
		AllowAlways: true, AlwaysHint: `allow "go test" without asking`,
		FullDiff: true, HeldOnArrival: true, Handover: "ctrl+space",
	}
	run := c.KeyRun()
	if len(run) != 2 || run[0].Key != "y" || run[1].Key != "n" {
		t.Fatalf("an arrival card should draw y and n alone, got %+v", run)
	}
	row := runRow(t, c, 80, "[y] run it once")
	for _, absent := range []string{"a", "d"} {
		for col := range ansi.StringWidth(ansi.Strip(row)) {
			if k, ok := c.KeyAt(row, col); ok && k == absent {
				t.Fatalf("an arrival card must offer no cell for %q", absent)
			}
		}
	}
}
