package components

// What these assert is the climb's honesty rather than its shape: it arrives
// exactly, it arrives in a bounded number of frames, it advances on frames
// and never on calls, and it never moves a figure the host has not measured.

import "testing"

// climb runs an odometer aimed at target from a settled start and returns the
// figure it printed on each of the frames after the aim.
func climb(start, target int64) (Odometer, []int64) {
	var o Odometer
	o.Toward(start, 0)
	var seen []int64
	for frame := 1; frame <= odometerSteps; frame++ {
		o.Toward(target, frame)
		seen = append(seen, o.Value())
	}
	return o, seen
}

func TestOdometer_ArrivesExactlyAndInBoundedFrames(t *testing.T) {
	o, seen := climb(0, 1000)
	if got := seen[len(seen)-1]; got != 1000 {
		t.Fatalf("the climb should land on the target, got %d after %d frames", got, odometerSteps)
	}
	if o.Easing() {
		t.Fatal("the frame a climb lands on is the frame it stops asking for another")
	}
	prev := int64(0)
	for i, v := range seen {
		if v <= prev || v > 1000 {
			t.Fatalf("frame %d printed %d, which is not between %d and the target", i+1, v, prev)
		}
		prev = v
	}
	// Every intermediate figure is one the count could have been: nothing
	// overshoots and nothing is printed the turn has not reached.
	if seen[0] >= seen[len(seen)-1] {
		t.Fatalf("the first frame should be short of the target, got %v", seen)
	}
}

// The zero value has nothing to climb from, so the first figure it is given
// is the figure it prints: a session opening on a total does not wind up to
// it.
func TestOdometer_FirstTargetIsExact(t *testing.T) {
	var o Odometer
	o.Toward(41200, 0)
	if got := o.Value(); got != 41200 {
		t.Fatalf("the first target should be shown exactly, got %d", got)
	}
	if o.Easing() {
		t.Fatal("an odometer that has never moved is not easing")
	}
}

// The rule that keeps a tool round still: a target that has not changed is
// not motion, however many frames go past.
func TestOdometer_HoldsWhenTheTargetHolds(t *testing.T) {
	var o Odometer
	o.Toward(9834, 0)
	for frame := 1; frame <= 20; frame++ {
		o.Toward(9834, frame)
		if got := o.Value(); got != 9834 {
			t.Fatalf("frame %d moved a count nothing measured: %d", frame, got)
		}
		if o.Easing() {
			t.Fatalf("frame %d claimed a climb with no target to climb to", frame)
		}
	}
}

// A count only grows within a turn, so a fall is a reset — the next turn
// opening, a cleared session — and it lands at once rather than being walked
// back down.
func TestOdometer_AFallingTargetSnaps(t *testing.T) {
	var o Odometer
	o.Toward(41200, 0)
	o.Toward(0, 1)
	if got := o.Value(); got != 0 {
		t.Fatalf("a reset should land at once, got %d", got)
	}
	if o.Easing() {
		t.Fatal("a reset is not a climb and must not keep the tick alive")
	}
}

// The tick and the update carrying it both re-aim the odometer; only the
// frame advances it, or the climb would run at whatever rate the session
// happened to be handling messages at.
func TestOdometer_AdvancesOnFramesNotOnCalls(t *testing.T) {
	var o Odometer
	o.Toward(0, 0)
	o.Toward(1000, 1)
	once := o.Value()
	for range 5 {
		o.Toward(1000, 1)
	}
	if got := o.Value(); got != once {
		t.Fatalf("a second call on one frame advanced the climb: %d -> %d", once, got)
	}
}

// A round reporting while the last one is still arriving does not send the
// figure back to where the previous climb started.
func TestOdometer_RetargetsFromWhereItIs(t *testing.T) {
	var o Odometer
	o.Toward(0, 0)
	o.Toward(1000, 1)
	mid := o.Value()
	o.Toward(5000, 2)
	if got := o.Value(); got < mid {
		t.Fatalf("a new target should climb on from %d, not back to %d", mid, got)
	}
	for frame := 3; frame <= 2+odometerSteps; frame++ {
		o.Toward(5000, frame)
	}
	if got := o.Value(); got != 5000 {
		t.Fatalf("the second climb should land too, got %d", got)
	}
}

// The ease may lag the truth and may not contradict it: a figure the host has
// not aimed the odometer at is printed as it stands.
func TestOdometer_ReadingPrefersTheMeasuredFigure(t *testing.T) {
	var o Odometer
	if got := o.Reading(4096); got != 4096 {
		t.Fatalf("an unaimed odometer should print the figure, got %d", got)
	}
	o.Toward(0, 0)
	o.Toward(1000, 1)
	if got := o.Reading(1000); got == 1000 || got == 0 {
		t.Fatalf("a climb in progress should print an intermediate figure, got %d", got)
	}
	if got := o.Reading(7); got != 7 {
		t.Fatalf("a figure the odometer is not aimed at should be printed, got %d", got)
	}
}

func TestFormatCount_RestedAndMovingShapes(t *testing.T) {
	cases := []struct {
		n            int64
		rest, moving string
	}{
		{0, "0", "0"},
		{412, "412", "412"},
		{9834, "9.8k", "9,834"},
		{41200, "41.2k", "41,200"},
		{99999, "100.0k", "99,999"},
		// Past the ceiling the moving figure gives up its last digits: the
		// ones column would change several times a frame and read as noise.
		{128400, "128.4k", "128.4k"},
	}
	for _, c := range cases {
		if got := FormatCount(c.n); got != c.rest {
			t.Errorf("FormatCount(%d) = %q, want %q", c.n, got, c.rest)
		}
		if got := FormatLiveCount(c.n); got != c.moving {
			t.Errorf("FormatLiveCount(%d) = %q, want %q", c.n, got, c.moving)
		}
	}
}
