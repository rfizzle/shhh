package components

// The one confirm every yes/no question in the product goes through, and the
// adapter its screens arm it behind.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestConfirm_Keys(t *testing.T) {
	c := &Confirm{Prompt: "Discard 14 unsaved turns?"}
	if done, yes := c.Update(key("y")); !done || !yes {
		t.Fatal("y should confirm")
	}
	for _, k := range []string{"n", "enter", "esc"} {
		if done, yes := c.Update(key(k)); !done || yes {
			t.Fatalf("%s should decline (default No)", k)
		}
	}
	if done, _ := c.Update(key("z")); done {
		t.Fatal("other keys should wait")
	}
	view := ansi.Strip(c.View(80))
	if !strings.Contains(view, "Discard 14 unsaved turns?") || !strings.Contains(view, "[y/N]") {
		t.Fatalf("confirm should render prompt and [y/N]: %q", view)
	}
	// Only the capital is emphasised: it is the default, and the default is
	// the answer that changes nothing.
	if !strings.Contains(c.View(80), sty.Bright.Bold(true).Render("N")) {
		t.Fatalf("the default letter carries the emphasis: %q", c.View(80))
	}
}

// The adapter reports what was said and takes the question down with it, so
// no screen can read an answer and leave the question on the surface.
func TestConfirmed_TakesTheQuestionDownWithTheAnswer(t *testing.T) {
	for _, tc := range []struct {
		key      string
		answered bool
		yes      bool
	}{
		{"y", true, true},
		{"n", true, false},
		{"esc", true, false},
		{"z", false, false},
	} {
		c := &Confirm{Prompt: "Write the 3 changes?"}
		answered, yes := confirmed(&c, key(tc.key))
		if answered != tc.answered || yes != tc.yes {
			t.Fatalf("%q: got answered=%v yes=%v, want %v/%v",
				tc.key, answered, yes, tc.answered, tc.yes)
		}
		if answered && c != nil {
			t.Fatalf("%q: an answered question should be down, got %+v", tc.key, c)
		}
		if !answered && c == nil {
			t.Fatalf("%q: a question nobody answered should still be up", tc.key)
		}
	}
}

// A question that is not up answers nothing, which is what lets a screen
// route to the adapter on the state it keeps rather than on a nil check of
// its own.
func TestConfirmed_NothingArmedAnswersNothing(t *testing.T) {
	var c *Confirm
	if answered, yes := confirmed(&c, key("y")); answered || yes {
		t.Fatalf("no question should answer nothing, got answered=%v yes=%v", answered, yes)
	}
}
