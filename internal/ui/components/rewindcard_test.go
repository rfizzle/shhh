package components

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

func rewindCardFixture() RewindCard {
	return RewindCard{
		Title: "Rewind to turn 5",
		Code: CardField{Label: "code", Value: "3 files restored " + DiffStat(4, 30),
			Detail: "the reverse of turns 6–7; what a command changed was never recorded"},
		Talk: CardField{Label: "talk", Value: "turns 6–7 leave the window",
			Detail: "ctx 62% → 41% · kept as a branch, /branches to switch back"},
		Undo: CardField{Label: "undo", Value: "yes", Tone: ToneSafe,
			Detail: "a rewind is a turn, and [u] takes it back"},
	}
}

// The card states the two halves of a rewind apart, and each answer is a key
// with words beside it (docs/interface/surfaces.md#the-rewind).
func TestRewindCard_StatesBothHalvesAndOffersThree(t *testing.T) {
	view := rewindCardFixture().View(110)
	for _, want := range []string{"code", "talk", "undo",
		keys.Bracket(keys.Rewind.Both), keys.Bracket(keys.Rewind.Code),
		keys.Bracket(keys.Rewind.Talk), keys.Bracket(keys.Rewind.Cancel)} {
		if !strings.Contains(view, want) {
			t.Errorf("the card does not carry %q:\n%s", want, view)
		}
	}
}

// Esc is the safe answer and says what is still standing, which is the whole
// of what the card would have changed
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestRewindCard_EscSaysWhatIsStillStanding(t *testing.T) {
	view := rewindCardFixture().View(110)
	if !strings.Contains(view, "nothing restored") {
		t.Errorf("the safe answer should name what it leaves:\n%s", view)
	}
}

// TestGolden_RewindScope captures the card: the two halves of a rewind stated
// apart, and the three answers under the rule.
func TestGolden_RewindScope(t *testing.T) {
	captureBoundedGolden(t, "rewind-scope", "the rewind scope card", goldenWidths,
		func(width int) []golden.Panel {
			one := rewindCardFixture()
			one.Title = "Rewind to turn 6"
			one.Code = CardField{Label: "code", Value: "1 file restored " + DiffStat(0, 12),
				Detail: "the reverse of turn 7; what a command changed was never recorded"}
			one.Talk = CardField{Label: "talk", Value: "turn 7 leaves the window",
				Detail: "ctx 62% → 58% · kept as a branch, /branches to switch back"}
			return []golden.Panel{
				{Label: "a run of turns · code, talk and both", View: rewindCardFixture().View(width)},
				{Label: "one turn · the same three answers", View: one.View(width)},
			}
		})
}
