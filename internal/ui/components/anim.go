package components

// The working label's motion (S-154, docs/interface/README.md). The product's
// whole motion budget used to be one braille glyph: the plumbing under it —
// one tick, one frame counter, one place the chain starts — was the careful
// part
// and the payload was `⠋`. This is the payload. The word beside the glyph
// arrives cell by cell when a turn starts, and a light runs along it while
// the turn lasts.
//
// Four things make it shhh's rather than a port of Crush's
// `internal/ui/anim`:
//
// **It has no clock.** Crush's Anim owns a `tea.Tick` chain per instance and
// stamps every message with a generation so a re-`Start()` can supersede the
// last one. That machinery exists to make many independent chains safe; the
// one-clock rule says there is one chain and never three, so shhh's animation
// is a *value* that reads the frame it is told (`Frame`) and holds no state
// at all. There is nothing to start, nothing to stop, nothing to supersede —
// and `View` is a pure function, which is what lets a golden capture it.
//
// **The ramp is two rungs, not a gradient.** Crush blends two arbitrary
// colours through HCL across the label. The palette is a closed set of
// fifteen tokens and the reason a mono swap is a swap rather than a rewrite
// is that nothing on screen is a colour the table does not name;
// interpolating spin → bright would put twenty unnamed colours on the top
// rail. So the sweep is the ramp the palette can afford: the label in spin, a
// three-cell crest in bright. Under mono both tokens are the same grey, the
// runs merge, and the swept label is byte-for-byte the unswept one — the
// motion is declined the way the mono palette declines every other colour it
// cannot carry, and it is declined by the palette rather than by a branch.
//
// **The entrance is a shape, so it survives mono.** A cell that has not
// arrived draws `·` from the drawing kit, same width as the letter it
// stands in for, so the label never reflows and the entrance reads in two
// greys exactly as it reads in colour. It is the half of the motion that says
// something — a turn just started — and it is the half that is not a hue.
//
// **The frames are prerendered per label**, so a frame is a slice index and a
// couple of `Render` calls on whole runs rather than one per cell. The cache
// is keyed by the label alone and emptied whenever the palette is swapped
// (`applyPalette`), which is the same door every other derived style goes
// through.

import (
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
)

const (
	// animBirthSteps is how many tick frames the entrance takes — twelve at
	// The 80ms tick is a hair under a second, which is long enough to read as
	// arriving and short enough that the label is settled before the first
	// number beside it has moved.
	animBirthSteps = 12

	// animRest is the beat between two passes of the sweep, in cells. The
	// crest starts this far to the left of the label and walks off its right
	// end, so a label at rest — the frames either side of the pass — is the
	// single spin-coloured run it has always been. A light that never rests
	// is a barber's pole, and this line is read for its numbers.
	animRest = 6

	// animCrestSpread is how far either side of the crest's cell stays lit.
	// Three cells is a glint; one is a blink at this cadence.
	animCrestSpread = 1

	// animBirthMark stands in for a cell that has not arrived. `·` is the
	// drawing kit's neutral mark and one cell wide, so nothing on the
	// rail moves while the word fills in.
	animBirthMark = "·"

	// animCacheCap bounds the prerendered frames. Phase words are four, but
	// `running <tool>` names whatever the round is executing, so the set of
	// labels a long session sees is open. Past the cap the cache is emptied
	// rather than evicted one by one: rebuilding a dozen frames costs less
	// than keeping a recency order for a table this small.
	animCacheCap = 512
)

// The rungs a cell can be drawn at. Base is the label's own colour, crest the
// sweep's, and birth the mark standing in for a cell that has not arrived.
const (
	animBase = iota
	animCrest
	animBirth
)

// animRung resolves a rung to the style that draws it.
func animRung(rung int) lipgloss.Style {
	switch rung {
	case animCrest:
		return sty.AnimCrest
	case animBirth:
		return sty.Dim
	}
	return sty.SpinText
}

// animCanon collapses the crest onto the base when the live palette gives
// them the same colour, which is what mono does (bright and spin are both
// mono-fg). Collapsing before the runs are merged is what makes the swept
// label byte-for-byte the unswept one there rather than three runs of one
// grey: the sweep is declined by the palette, exactly the way mono declines
// every other colour it cannot carry, and no branch has to remember to.
func animCanon(rung int) int {
	if rung == animCrest && Palette.Bright == Palette.Spin {
		return animBase
	}
	return rung
}

// Anim is a label in motion. It is a value the host rebuilds every frame from
// state it already has, not an object with a life of its own: `Frame` is the
// session's one frame counter and `Arriving` how much of the entrance
// is still to run.
type Anim struct {
	// Frame is the tick the host is on. The sweep advances with it, so this
	// label and every other moving thing on screen show the same instant.
	Frame int
	// Arriving is the number of entrance frames left, counting down to zero.
	// Zero is a label that has been on screen a while — and the zero value,
	// so a host with no entrance to stage renders the settled label without
	// having to say so. AnimArriving converts an age into it.
	Arriving int
	// Lead is drawn at the base rung ahead of the label and is not swept —
	// the spinner's own frame, whose eight-frame cycle is not this label's
	// business. It merges into the label's first run when that run is also
	// base, so a label at rest is one escape sequence rather than two.
	Lead string
	// Label is the word in motion.
	Label string
	// Suffix is written after the label exactly as the host styled it: the
	// fields the caller's own drop ladder left standing. It is a string
	// rather than Crush's `func() string` because this value is rebuilt every
	// frame anyway, and a closure would make View impure for no gain.
	Suffix string
}

// View renders the label at the frame it was given. Width is invariant across
// every frame — the entrance swaps a cell for a mark of the same width and
// the sweep swaps only colour — so a host can lay this out once and never
// again.
func (a Anim) View() string {
	if a.Label == "" {
		if a.Lead == "" {
			return a.Suffix
		}
		return sty.SpinText.Render(a.Lead) + a.Suffix
	}
	f := animFramesFor(a.Label)
	frame := f.sweep[modIndex(a.Frame, len(f.sweep))]
	if a.Arriving > 0 {
		frame = f.entrance[min(a.Arriving, animBirthSteps)-1]
	}
	runs := make([]animRun, 0, len(frame)+1)
	if a.Lead != "" {
		runs = appendRun(runs, animBase, a.Lead)
	}
	for _, r := range frame {
		runs = appendRun(runs, r.rung, r.text)
	}
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(animRung(r.rung).Render(r.text))
	}
	b.WriteString(a.Suffix)
	return b.String()
}

// AnimArriving converts a label's age into the entrance frames it has left.
// The age is measured in tick periods because that is what the entrance is
// counted in, and it is read off the caller's own clock — the turn's elapsed,
// which the status line already prints — rather than from a timer of this
// package's own. The session is allowed one clock; this borrows a number it
// is already keeping instead of asking for a second.
func AnimArriving(age time.Duration) int {
	left := animBirthSteps - int(age/SpinnerInterval)
	return max(left, 0)
}

// animRun is a stretch of cells sharing a rung — what one Render call draws.
type animRun struct {
	rung int
	text string
}

// appendRun adds text at a rung, extending the last run when it is the same
// one. This is what collapses the sweep back into a single escape sequence
// under mono, where all three rungs resolve to the same style.
func appendRun(runs []animRun, rung int, text string) []animRun {
	if n := len(runs); n > 0 && runs[n-1].rung == rung {
		runs[n-1].text += text
		return runs
	}
	return append(runs, animRun{rung: rung, text: text})
}

// animFrames is one label's prerendered frames: the entrance indexed by the
// frames it has left, and the sweep indexed by the tick.
type animFrames struct {
	entrance [][]animRun
	sweep    [][]animRun
}

var (
	animCache  sync.Map // label → *animFrames
	animCached atomic.Int64
)

// animFramesFor is the memo. A label is prerendered once per palette; the
// palette swap empties the table rather than keying it, because a stale entry
// here is a colour from the theme the session just left.
func animFramesFor(label string) *animFrames {
	if v, ok := animCache.Load(label); ok {
		return v.(*animFrames)
	}
	f := buildAnimFrames(label)
	if animCached.Load() >= animCacheCap {
		clearAnimCache()
	}
	if _, loaded := animCache.LoadOrStore(label, f); !loaded {
		animCached.Add(1)
	}
	return f
}

// clearAnimCache empties the memo. applyPalette calls it, so the frames are
// rebuilt from the tokens the session is now using.
func clearAnimCache() {
	animCache.Clear()
	animCached.Store(0)
}

// buildAnimFrames prerenders every frame a label has. There are
// animBirthSteps of the entrance and one per cell of the sweep plus its
// beat — a few dozen slices of at most three runs each, built once.
func buildAnimFrames(label string) *animFrames {
	var cells []string
	for _, r := range label {
		cells = append(cells, string(r))
	}
	// A cell arrives when Arriving has fallen to its own step.
	//
	// The schedule sweeps left to right with a cell of jitter either side,
	// which is where this parts company with Crush: its birth steps are a
	// seeded draw, so a label materialises as a scatter of its letters and is
	// unreadable until the last one lands. This rail is read for what the
	// turn is doing, and the first second of a turn is exactly when someone
	// looks. Arriving in reading order means whatever is on screen at any
	// frame is the *beginning of the word* — `thin·····` is already
	// `thinking…` to a reader — so the entrance costs no legibility and the
	// jitter still keeps it from marching.
	//
	// The jitter is a hash of the label and the position rather than a draw
	// from a seeded RNG: same answer in every process, no state to carry, and
	// two phases stagger differently because their words differ.
	//
	// A cell wider than one column is born at once. `·` is one column and the
	// entrance's whole claim is that the label does not reflow while it
	// arrives; a tool argument carrying a wide rune keeps that promise by
	// skipping the stagger for it.
	born := make([]int, len(cells))
	for i, c := range cells {
		if lipgloss.Width(c) != 1 {
			born[i] = animBirthSteps
			continue
		}
		// The sweep: cell 0 at the top of the window, so the word starts
		// arriving on the first frame rather than out of a full row of marks,
		// and the last cell at the bottom of it. Plus the jitter, clamped
		// back into the window.
		step := animBirthSteps - i*animBirthSteps/len(cells)
		step += animBirthJitter(label, i)
		born[i] = min(max(step, 0), animBirthSteps)
	}

	f := &animFrames{
		entrance: make([][]animRun, animBirthSteps),
		sweep:    make([][]animRun, len(cells)+animRest),
	}
	for left := 1; left <= animBirthSteps; left++ {
		var runs []animRun
		for i, c := range cells {
			if left <= born[i] {
				runs = appendRun(runs, animBase, c)
				continue
			}
			runs = appendRun(runs, animBirth, animBirthMark)
		}
		f.entrance[left-1] = runs
	}
	for step := range f.sweep {
		// The crest enters from animRest cells left of the label and walks
		// off its right end, which is what gives the pass a beat either side.
		crest := step - animRest
		var runs []animRun
		for i, c := range cells {
			rung := animBase
			if abs(i-crest) <= animCrestSpread {
				rung = animCanon(animCrest)
			}
			runs = appendRun(runs, rung, c)
		}
		f.sweep[step] = runs
	}
	return f
}

// animBirthJitter is the cell's offset from the reading-order sweep: -1, 0 or
// +1, so neighbours arrive out of step without the word arriving out of
// order.
func animBirthJitter(label string, i int) int {
	h := fnv.New32a()
	h.Write([]byte(label))
	h.Write([]byte{byte(i), byte(i >> 8), byte(i >> 16)})
	return int(h.Sum32()%3) - 1
}

// modIndex is a wrapped index that stays in range for a negative frame.
func modIndex(i, n int) int { return ((i % n) + n) % n }

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
