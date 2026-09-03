package components

// The counters' climb (docs/interface/surfaces.md#the-input-frame). The top
// rail states one turn's account while it is still moving and the vitals rail
// states the session's, and both used to move the way a ledger moves: a round
// reported, and the figure cut from one number to another with nothing in
// between. A cut says that a number changed. It does not say by how much, and
// at the token scale the difference between a round that cost four hundred
// and one that cost forty thousand is most of why anyone is watching the rail.
//
// So a figure that changes climbs to its new value over half a second. Three
// rules keep the climb honest:
//
// **It has no clock.** Like the working label beside it (anim.go), an
// Odometer reads the frame it is told and owns no timer: the session has one
// tick source and this is a consumer of it. Toward advances at most one step
// per frame it has not already seen, so being called twice on one frame — by
// the tick and again by the update carrying it — costs nothing.
//
// **It never invents movement.** A target that has not changed does not move,
// which is what a tool round with nothing streaming looks like from here. And
// a target that *falls* snaps instead of easing down: within a turn the counts
// only grow, so a fall is a reset — the next turn opening, a cleared
// session — and walking a number back down to where it started would be
// motion nothing measured.
//
// **It lands exactly.** The last step is the target itself rather than an
// asymptote, and Easing goes false on the frame that arrives, so the frame
// that lands is the frame the tick chain can end on. An ease that only
// approached its target would keep an idle session animating forever.

import "strconv"

// odometerSteps is how many frames a climb takes. Six at the spinner's
// cadence is a hair under half a second: long enough that the eye reads a
// climb rather than a cut, and short enough that a figure has settled well
// before the next round reports one.
const odometerSteps = 6

// liveCountCeiling is where a moving count gives up its last digits. Below it
// every digit fits in the space the rail already gives the figure, and each
// one is a token the turn actually spent; above it the ones column changes
// several times a frame and reads as noise, so the thousands rung takes over
// and the count moves in hundreds like the rested figure beside it.
const liveCountCeiling = 100_000

// Odometer is a count on its way to a new figure. It is a value the host
// keeps beside the number it prints, not an object with a life of its own,
// and its zero value is an odometer that has not been aimed: the first target
// it is given is shown exactly, so a session opening on a figure does not
// climb to it from nothing.
type Odometer struct {
	// from is where the climb started and to where it is going. from == to
	// whenever the odometer is at rest.
	from, to int64
	// step is how far into the climb it is, counted in frames; odometerSteps
	// is arrived.
	step int
	// frame is the host frame the last step was taken on. A call on the same
	// frame re-aims without advancing, which is what makes Toward safe to
	// call from every path that might have changed the target.
	frame int
	aimed bool
}

// Toward aims the odometer at target as of the host's frame, advancing the
// climb by one step if this frame has not been seen. A target above the
// figure on screen starts a fresh climb from wherever that figure is, so a
// count that is still moving when the next round reports keeps climbing
// rather than jumping back; a target at or below it lands at once.
func (o *Odometer) Toward(target int64, frame int) {
	if !o.aimed {
		o.aimed = true
		o.from, o.to, o.step, o.frame = target, target, odometerSteps, frame
		return
	}
	if target != o.to {
		if shown := o.Value(); target > shown {
			o.from, o.to, o.step = shown, target, 0
		} else {
			o.from, o.to, o.step = target, target, odometerSteps
		}
	}
	if frame != o.frame {
		o.frame = frame
		if o.step < odometerSteps {
			o.step++
		}
	}
}

// Value is the figure to print this frame.
func (o Odometer) Value() int64 {
	if o.step >= odometerSteps {
		return o.to
	}
	// Ease-out: the distance still to cover shrinks as the cube of the frames
	// left, so the first frame carries most of the gap and the last few
	// settle onto the number. A linear climb at this cadence reads as a
	// wipe — six evenly spaced stills — where the ease reads as one movement
	// arriving. The arithmetic is integer because the count is: a float
	// intermediate would print a figure that is nobody's token count.
	rem := int64(odometerSteps - o.step)
	span := int64(odometerSteps)
	return o.to - (o.to-o.from)*rem*rem*rem/(span*span*span)
}

// Settle ends the climb where the figure stands rather than where it was
// going, for a host that has nothing reading this counter any more. Two
// things make it the right move at that moment and simply leaving the
// odometer alone the wrong one: a counter left part-way is still easing, and
// something still easing keeps a tick chain alive over a screen with nothing
// on it to move; and a figure settled where it stands costs nothing to hand
// back the number it is already showing, where one re-aimed at a closed
// account's zero would have to climb the whole way to it again.
func (o *Odometer) Settle() {
	if !o.aimed {
		return
	}
	v := o.Value()
	o.from, o.to, o.step = v, v, odometerSteps
}

// Reading is the figure to print for target: the climb's intermediate value
// while the odometer is on its way to that very number, and target itself
// otherwise. The ease is allowed to lag the truth by a few frames; it is
// never allowed to contradict it, so a host that renders a figure the
// odometer has not been aimed at gets the figure rather than a stale one.
func (o Odometer) Reading(target int64) int64 {
	if !o.aimed || o.to != target {
		return target
	}
	return o.Value()
}

// Easing reports whether the figure is still short of its target. A host
// animating on one tick source asks this to decide whether the chain still
// has work: it is false on the frame the climb lands, never after it.
func (o Odometer) Easing() bool { return o.aimed && o.step < odometerSteps }

// FormatCount is a settled count's shape: `412` under a thousand, `41.2k`
// above it. It is the shape every rail has always printed a total in, and it
// is what a figure goes back to once nothing is moving it.
func FormatCount(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
}

// FormatLiveCount is the same count while a turn is still producing it: every
// digit, grouped in threes, up to the ceiling. `41.2k` is the right figure to
// carry a finished session in and the wrong one to watch, because a hundred
// tokens of movement happen entirely inside the rounding — the number sits
// still while the turn does not.
func FormatLiveCount(n int64) string {
	if n < 0 || n >= liveCountCeiling {
		return FormatCount(n)
	}
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
