package components

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// anim builds the label the frame's activity slot animates, so every test
// below is reading the same thing the top rail draws.
func anim(frame, arriving int) Anim {
	return Anim{Frame: frame, Arriving: arriving, Lead: "⠋ ", Label: "running go test"}
}

// The line does not reflow while it moves. The entrance swaps a cell for a
// mark of the same width and the sweep swaps only colour, so a host can lay
// the slot out once.
func TestAnim_WidthIsInvariant(t *testing.T) {
	want := lipgloss.Width(anim(0, 0).View())
	for arriving := 0; arriving <= animBirthSteps+4; arriving++ {
		for frame := range animBirthSteps + len("running go test") + animRest {
			if got := lipgloss.Width(anim(frame, arriving).View()); got != want {
				t.Fatalf("frame %d arriving %d is %d columns, want %d", frame, arriving, got, want)
			}
		}
	}
}

// The word arrives in reading order, so whatever is on screen at any frame is
// the beginning of the label rather than a scatter of its letters. This is
// the departure from Crush's seeded birth schedule, and the reason for it is
// that this rail is read for what the turn is doing.
func TestAnim_ArrivesInReadingOrder(t *testing.T) {
	const label = "running go test"
	prev := 0
	for arriving := animBirthSteps; arriving >= 0; arriving-- {
		plain := ansi.Strip(anim(0, arriving).View())
		plain = strings.TrimPrefix(plain, "⠋ ")
		// Everything before the first mark is the label itself, and
		// everything from it on is marks: the word fills from the left.
		n := strings.Index(plain, animBirthMark)
		if n < 0 {
			n = len(plain)
		}
		if got, want := plain[:n], label[:n]; got != want {
			t.Fatalf("arriving %d spelled %q, want the label's first %d cells %q", arriving, got, n, want)
		}
		if strings.TrimRight(plain[n:], animBirthMark) != "" {
			t.Fatalf("arriving %d has a letter after a mark: %q", arriving, plain)
		}
		if n < prev {
			t.Fatalf("arriving %d went backwards: %d cells arrived, %d before", arriving, n, prev)
		}
		prev = n
	}
	if got := ansi.Strip(anim(0, 0).View()); got != "⠋ "+label {
		t.Fatalf("a settled label is %q, want the whole word", got)
	}
}

// A host that stages no entrance gets the settled label: Arriving's zero
// value is a label that has been on screen a while, not one that has just
// appeared.
func TestAnim_ZeroArrivingIsSettled(t *testing.T) {
	if got := ansi.Strip(Anim{Label: "thinking…"}.View()); got != "thinking…" {
		t.Fatalf("the zero value rendered %q, want the settled label", got)
	}
}

// The sweep rests either side of its pass, and the lead merges into the
// label's first run — which together are why a label at rest is the single
// escape sequence it once was, and why no golden of a still frame
// moved when the animation landed.
func TestAnim_AtRestIsOneRun(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	want := sty.SpinText.Render("⠋ thinking…")
	for frame := range animRest - animCrestSpread {
		if got := (Anim{Frame: frame, Lead: "⠋ ", Label: "thinking…"}).View(); got != want {
			t.Fatalf("frame %d rendered %q, want the single run %q", frame, got, want)
		}
	}
}

// Mid-pass the crest is bright over a spin label — a light running along the
// word, and the only thing on the line that is colour and nothing else.
func TestAnim_SweepLightsTheCrest(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	lit := 0
	for frame := range animRest + len("thinking…") {
		out := (Anim{Frame: frame, Label: "thinking…"}).View()
		if plain := ansi.Strip(out); plain != "thinking…" {
			t.Fatalf("frame %d changed the text to %q", frame, plain)
		}
		if out != sty.SpinText.Render("thinking…") {
			lit++
		}
	}
	if lit == 0 {
		t.Fatal("the sweep never lit a cell")
	}
	if lit == animRest+len("thinking…") {
		t.Fatal("the sweep never rested — a light that never stops is a barber's pole")
	}
}

// Colour never carries meaning alone (invariant 1), and the sweep carries
// nothing at all: under mono the crest collapses onto the base and the swept
// label is byte-for-byte the unswept one. The entrance survives, because it
// is a shape.
func TestAnim_MonoDeclinesTheSweepAndKeepsTheEntrance(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	was := Mono()
	SetMono(true)
	t.Cleanup(func() { SetMono(was) })

	still := (Anim{Lead: "⠋ ", Label: "thinking…"}).View()
	for frame := range animRest + len("thinking…") {
		if got := (Anim{Frame: frame, Lead: "⠋ ", Label: "thinking…"}).View(); got != still {
			t.Fatalf("mono frame %d rendered %q, want the still label %q", frame, got, still)
		}
	}
	arriving := ansi.Strip((Anim{Arriving: animBirthSteps, Lead: "⠋ ", Label: "thinking…"}).View())
	if !strings.Contains(arriving, animBirthMark) {
		t.Fatalf("the entrance should still read in two greys, got %q", arriving)
	}
}

// The frames are a pure function of the value, so a golden can capture them
// and the cache can be dropped at any time without the render changing.
func TestAnim_IsDeterministicAcrossTheCache(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	first := make([]string, 0, animBirthSteps)
	for arriving := animBirthSteps; arriving >= 0; arriving-- {
		first = append(first, anim(3, arriving).View())
	}
	clearAnimCache()
	for i, arriving := 0, animBirthSteps; arriving >= 0; i, arriving = i+1, arriving-1 {
		if got := anim(3, arriving).View(); got != first[i] {
			t.Fatalf("arriving %d rebuilt as %q, want %q", arriving, got, first[i])
		}
	}
}

// The palette swap is the memo's only invalidation: a frame kept across it
// would be a colour from the theme the session just left.
func TestAnim_PaletteSwapDropsTheFrames(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	SetMono(false)
	colour := (Anim{Frame: animRest, Label: "thinking…"}).View()
	SetMono(true)
	if got := (Anim{Frame: animRest, Label: "thinking…"}).View(); got == colour {
		t.Fatal("the mono render came back with the coloured frames still cached")
	}
}

// Past the cap the table is emptied rather than grown without bound: the set
// of labels is open, because `running <tool>` names whatever the round runs.
func TestAnim_CacheIsBounded(t *testing.T) {
	clearAnimCache()
	t.Cleanup(clearAnimCache)
	for i := range animCacheCap + 2 {
		animFramesFor("running tool-" + strconv.Itoa(i))
	}
	if got := animCached.Load(); got > animCacheCap {
		t.Fatalf("the memo holds %d labels, want at most %d", got, animCacheCap)
	}
}

// AnimArriving reads the entrance off an age in tick periods, and a label
// older than the window has nothing left to arrive.
func TestAnimArriving_CountsDownWithTheTurn(t *testing.T) {
	for _, c := range []struct {
		age  time.Duration
		want int
	}{
		{0, animBirthSteps},
		{SpinnerInterval, animBirthSteps - 1},
		{animBirthSteps * SpinnerInterval, 0},
		{time.Minute, 0},
	} {
		if got := AnimArriving(c.age); got != c.want {
			t.Fatalf("an age of %s leaves %d entrance frames, want %d", c.age, got, c.want)
		}
	}
}
