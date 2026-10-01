package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// largeStep is the catalogue's large step: twenty-two calls, two writes and
// a failed command, by the receipt's precedence a failure.
func largeStep() StepCard {
	strip := make([]StripCell, 0, 22)
	for i := 0; i < 22; i++ {
		cell := StripCell{Kind: ActivityTool}
		switch i {
		case 10, 19:
			cell.Kind = ActivityCommand
		case 18, 20:
			cell.Kind = ActivityEdit
		case 21:
			cell = StripCell{Kind: ActivityCommand, State: ActivityFailed}
		}
		strip = append(strip, cell)
	}
	return StepCard{Kind: ActivityCommand, State: ActivityFailed, Rail: true, Verb: "read",
		Rollup:   "14 files in internal/ui/, 3 in docs/ · ran 3 commands · wrote 2 files",
		Bare:     "17 files · ran 3 commands · wrote 2 files",
		Outcome:  "+88 −5",
		Duration: "2m 41s",
		Body: "The copy key lives in the reply pane's key map, next to the existing fold key. I've added it " +
			"there and a flash to render.go so the block blinks once on copy. Tests pass except one golden I'll refresh next.",
		Evidence: "--- FAIL: TestReplyGolden: reply_copy.golden differs (+3 −0) · go test ./internal/ui/",
		Strip:    strip}
}

// readStep is a step that only read, judged by a reading.
func readStep() StepCard {
	return StepCard{Kind: ActivityTool, Verb: "read", Rollup: "2 files · searched once · 1 lookup",
		Bare: "2 files · searched once · 1 lookup", Verdict: "≡ on target", Duration: "3.1s",
		Body:    "Listed .plan and read the backlog's format; both earlier copy stories are closed. On course to write the epic.",
		Reading: true}
}

// commandStep is one command a person approved.
func commandStep() StepCard {
	return StepCard{Kind: ActivityCommand, Rail: true, Verb: "ran", Subject: "git status --short .plan/",
		Outcome: "ok", Verdict: "approved by you", Duration: "0.1s",
		Body:     "Confirming .plan/ is untracked, so nothing I write gets committed.",
		Evidence: "?? .plan/", EvidenceRight: "1 line"}
}

// header is the card's header line with the colour taken off.
func header(c StepCard, width int) string {
	lines := strings.Split(stripANSI(c.View(width)), "\n")
	if c.Folded || c.Density == CardLow {
		return lines[0]
	}
	return lines[1]
}

// The header gives fields up in one order as the pane narrows — the
// duration, the verdict, the directory clause, the rollup — and never the
// glyph, the rail, the verb or the outcome; a lone call's subject is cut,
// never dropped.
func TestCard_HeaderDropOrder(t *testing.T) {
	cases := []struct {
		name  string
		card  StepCard
		width int
		has   []string
		lacks []string
	}{
		{"the large step whole", largeStep(), 96,
			[]string{"▎✗ read 14 files in internal/ui/, 3 in docs/", "+88 −5", "2m 41s"}, nil},
		{"the duration goes first", largeStep(), 95,
			[]string{"14 files in internal/ui/", "+88 −5"}, []string{"2m 41s"}},
		{"then the directory clause", largeStep(), 86,
			[]string{"read 17 files · ran 3 commands · wrote 2 files", "+88 −5"}, []string{"internal/ui/"}},
		{"then the rollup", largeStep(), 58,
			[]string{"✗ read", "+88 −5"}, []string{"17 files"}},
		{"the verb and the outcome to the end", largeStep(), 17,
			[]string{"✗ read", "+88 −5"}, nil},
		{"a read whole", readStep(), 64,
			[]string{"⚙ read 2 files · searched once · 1 lookup", "≡ on target", "3.1s"}, nil},
		{"the verdict before the rollup", readStep(), 46,
			[]string{"read 2 files · searched once · 1 lookup"}, []string{"on target", "3.1s"}},
		{"one command, the account goes second", commandStep(), 55,
			[]string{"$ ran git status --short .plan/", "ok"}, []string{"approved", "0.1s"}},
		{"its subject is cut, never dropped", commandStep(), 22,
			[]string{"$ ran git", "…", "ok"}, []string{".plan/"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := header(tc.card, tc.width)
			if w := len([]rune(h)); w > tc.width {
				t.Fatalf("the header runs past the pane at %d: %d cells %q", tc.width, w, h)
			}
			for _, want := range tc.has {
				if !strings.Contains(h, want) {
					t.Errorf("at %d the header lost %q: %q", tc.width, want, h)
				}
			}
			for _, gone := range tc.lacks {
				if strings.Contains(h, gone) {
					t.Errorf("at %d the header kept %q: %q", tc.width, gone, h)
				}
			}
		})
	}
}

// A card rests on the band: a padding row inside it above and below, every
// row carried to the pane's edge on the band's ground. Where the palette has
// no band the card is its padding rows alone, empty rows with nothing on
// them, and a ground painted the band's own colour steps the band up a rung.
func TestCard_BandIsATokenOrThePaddingRowsAlone(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	card := commandStep()
	lines := strings.Split(card.View(80), "\n")
	if stripANSI(lines[0]) != strings.Repeat(" ", 80) || stripANSI(lines[len(lines)-1]) != strings.Repeat(" ", 80) {
		t.Fatalf("the card does not open and close on a padding row of the band:\n%q", lines)
	}
	for i, l := range lines {
		if !strings.Contains(l, "48;5;234") {
			t.Errorf("row %d is not on the band: %q", i, l)
		}
	}

	t.Run("painted ground", func(t *testing.T) {
		was := GroundPainted()
		PaintGround(true)
		t.Cleanup(func() { PaintGround(was) })
		if !strings.Contains(card.View(80), "48;5;235") {
			t.Error("a ground painted the band's colour hides the band; the card's band steps up")
		}
	})

	t.Run("mono", func(t *testing.T) {
		was := Mono()
		SetMono(true)
		t.Cleanup(func() { SetMono(was) })
		lines := strings.Split(card.View(80), "\n")
		if lines[0] != "" || lines[len(lines)-1] != "" {
			t.Errorf("with no band the padding rows are empty rows:\n%q", lines)
		}
		if strings.Contains(card.View(80), "48;") {
			t.Error("mono draws no ground under a card")
		}
	})
}

// A card prints no key: what enter does on it is the hint bar's to say.
func TestCard_NoKeyOnTheCard(t *testing.T) {
	for _, c := range []StepCard{largeStep(), readStep(), commandStep()} {
		for _, width := range goldenWidths {
			if view := stripANSI(c.View(width)); strings.Contains(view, "[enter]") {
				t.Errorf("at %d the card prints a key:\n%s", width, view)
			}
		}
	}
}

// TestGolden_StepCards captures the card in every state the catalogue draws
// that the transcript renders: finished with header, body and footer; with
// no body; with no footer; running; failed with its strip; folded by the
// reader; the header alone at low; open at high; and on a painted ground.
func TestGolden_StepCards(t *testing.T) {
	captureBoundedGolden(t, "step-cards", "a step as a card", goldenWidths, func(width int) []golden.Panel {
		running := commandStep()
		running.State, running.Spin, running.Frame = ActivityRunning, true, 3
		running.Subject, running.Outcome, running.Verdict = "go test ./internal/ui/...", "", ""
		running.Evidence, running.EvidenceRight = "", ""
		running.Duration, running.Tail = "42s", "--- FAIL: TestReplyGolden (0.42s)"
		noBody := commandStep()
		noBody.Body = ""
		noFooter := readStep()
		folded := commandStep()
		folded.Folded = true
		low := commandStep()
		low.Density = CardLow
		open := commandStep()
		open.Density = CardHigh
		open.Calls = []string{ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "git status --short .plan/",
			Outcome: OutcomeOK, Allowed: ApprovedBy("you"), Expanded: true, Detail: []string{"?? .plan/"}}.View(width)}
		write := StepCard{Kind: ActivityEdit, Rail: true, Verb: "wrote", Subject: ".plan/BACKLOG.md",
			Outcome: "+41", Duration: "1m04s", Body: "Writing the epic and two stories now, appended after the last one.",
			Evidence: "+ ## A Code Block Is Copied by Itself", EvidenceRight: "≡ on target · 2 stories"}
		painted := func() string {
			was := GroundPainted()
			PaintGround(true)
			defer PaintGround(was)
			return write.View(width)
		}
		return []golden.Panel{
			{Label: "finished · header, body and footer", View: commandStep().View(width)},
			{Label: "finished · a write, its hunk head and the verdict beside it", View: write.View(width)},
			{Label: "no body · the footer follows the header", View: noBody.View(width)},
			{Label: "no footer · a reading's sentence for a body, its verdict on the header", View: noFooter.View(width)},
			{Label: "running · the spinner, the command's tail, the clock", View: running.View(width)},
			{Label: "failed · the failure over the write, and the strip", View: largeStep().View(width)},
			{Label: "folded by the reader · ▸ in the pointer column", View: folded.View(width)},
			{Label: "low · the header alone", View: low.View(width)},
			{Label: "high · the card open on its calls", View: open.View(width)},
			{Label: "on a painted ground · the band a rung up", View: painted()},
		}
	})
}
