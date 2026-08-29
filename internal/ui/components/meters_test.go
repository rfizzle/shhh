package components

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMeterCells(t *testing.T) {
	cases := []struct {
		pct, cells, filled int
	}{
		{0, 22, 0}, {1, 22, 1}, {50, 22, 11}, {99, 22, 21}, {100, 22, 22},
		// The same boundaries at the vitals rail's eight cells and an agent
		// lane's five: a bar fills completely only at 100%, and any non-zero
		// percentage shows at least one cell.
		{0, 8, 0}, {1, 8, 1}, {99, 8, 7}, {100, 8, 8},
		{1, 5, 1}, {99, 5, 4}, {100, 5, 5},
		{-5, 8, 0}, {150, 8, 8},
	}
	for _, c := range cases {
		bar := meterCells(c.pct, c.cells)
		if got := strings.Count(bar, "▰"); got != c.filled {
			t.Fatalf("meterCells(%d, %d) filled %d cells, want %d (%q)", c.pct, c.cells, got, c.filled, bar)
		}
		if got := len([]rune(bar)); got != c.cells {
			t.Fatalf("meterCells(%d, %d) is %d cells wide", c.pct, c.cells, got)
		}
	}
}

func TestMeterStatesItsValueBesideTheBar(t *testing.T) {
	for _, pct := range []int{0, 1, 50, 99, 100} {
		view := stripANSI(Meter{Pct: pct, Cells: MeterCellsVitals, Label: "ctx"}.View())
		if !strings.HasPrefix(view, "ctx ▰") && !strings.HasPrefix(view, "ctx ▱") {
			t.Fatalf("the label leads the bar at %d%%: %q", pct, view)
		}
		if !strings.HasSuffix(view, strconv.Itoa(pct)+"%") {
			t.Fatalf("a bar never carries its value alone, %d%% rendered %q", pct, view)
		}
	}
	// A host-supplied count replaces the percent — the rule is that something
	// states the value, not that it has to be a percentage.
	stepped := stripANSI(Meter{Pct: 75, Cells: MeterCellsRail, Tone: MeterProgress, Text: "step 3 of 4"}.View())
	if !strings.HasSuffix(stepped, "step 3 of 4") || !strings.Contains(stepped, "▰") {
		t.Fatalf("a counted meter states its count beside its bar: %q", stepped)
	}
	// Bar is the bar alone, for a row that states the value at its own edge.
	if bar := stripANSI(Meter{Pct: 50, Cells: 8}.Bar()); bar != "▰▰▰▰▱▱▱▱" {
		t.Fatalf("Bar renders the run alone, got %q", bar)
	}
}

func TestMeterThresholdColours(t *testing.T) {
	// The bar and the number turn together (§10c), so one style decides both.
	for _, c := range []struct {
		pct  int
		want Token
		bold bool
	}{{40, Palette.Add, false}, {62, Palette.Accent, false}, {95, Palette.Del, true}} {
		style := Meter{Pct: c.pct, Warn: 60, Alert: 80}.Style()
		if got := style.GetForeground(); got != c.want.Color() {
			t.Fatalf("context at %d%% uses %v, want %v", c.pct, got, c.want)
		}
		if got := style.GetBold(); got != c.bold {
			t.Fatalf("context at %d%% bold=%v, want %v", c.pct, got, c.bold)
		}
	}
	// Zero thresholds keep the cockpit's defaults (70/90).
	if got := ctxStyle(75, 0, 0).GetForeground(); got != Palette.Accent.Color() {
		t.Fatalf("default thresholds: 75%% should warn, got %v", got)
	}
	if got := ctxStyle(69, 0, 0).GetForeground(); got != Palette.Add.Color() {
		t.Fatalf("default thresholds: 69%% is still healthy, got %v", got)
	}
	// The other tones do not colour by value at all.
	for _, c := range []struct {
		tone MeterTone
		want Token
	}{{MeterProgress, Palette.Add}, {MeterAgent, Palette.Info}, {MeterCountdown, Palette.Accent}} {
		if got := (Meter{Pct: 95, Tone: c.tone}).Style().GetForeground(); got != c.want.Color() {
			t.Fatalf("tone %d at 95%% uses %v, want %v", c.tone, got, c.want)
		}
	}
}

func TestStepMeterNeedsADeclaredTotal(t *testing.T) {
	if _, ok := StepMeter(3, 0, MeterCellsRail, true); ok {
		t.Fatal("no declared total, no ratio (S-094)")
	}
	if _, ok := AgentMeter(2, 0); ok {
		t.Fatal("an agent lane without a step count gets no bar")
	}
	m, ok := StepMeter(3, 4, MeterCellsRail, true)
	if !ok {
		t.Fatal("a declared total earns a meter")
	}
	if m.Text != "step 3 of 4" || m.Pct != 75 {
		t.Fatalf("step 3 of 4 is 75%%: %+v", m)
	}
	// The step in flight owns its own slice of cells, so the bar shows what is
	// done and what is moving as two different things.
	if m.Running != MeterCellsRail/4 {
		t.Fatalf("the running step takes its slice of cells, got %d", m.Running)
	}
	if done, _ := StepMeter(4, 4, MeterCellsRail, false); done.Running != 0 {
		t.Fatalf("a finished turn has nothing in flight, got %d", done.Running)
	}
	// An overrun ordinal is clamped rather than drawn past the end.
	over, _ := StepMeter(9, 4, MeterCellsRail, false)
	if over.Pct != 100 || over.Text != "step 4 of 4" {
		t.Fatalf("an overrun step is clamped: %+v", over)
	}
	lane, _ := AgentMeter(2, 4)
	if lane.Cells != MeterCellsAgent || lane.Tone != MeterAgent {
		t.Fatalf("an agent lane is five info cells: %+v", lane)
	}
	if got := strings.Count(stripANSI(lane.Bar()), "▰"); got != 2 {
		t.Fatalf("step 2 of 4 fills 2 of 5 cells, got %d", got)
	}
}

func TestSparkline(t *testing.T) {
	if got := (Sparkline{}).View(); got != "" {
		t.Fatalf("an empty series renders nothing, got %q", got)
	}
	if got := sparkCells(nil, 8); got != "" {
		t.Fatalf("an empty series renders nothing, got %q", got)
	}
	if got := sparkCells([]float64{3, 3, 3}, 8); got != "███" {
		t.Fatalf("a flat series is flat at the top, got %q", got)
	}
	if got := sparkCells([]float64{0, 0, 0}, 8); got != "▁▁▁" {
		t.Fatalf("an all-zero series is flat at the floor, got %q", got)
	}
	if got := sparkCells([]float64{0, 4}, 8); got != "▁█" {
		t.Fatalf("a series scales to its own maximum, got %q", got)
	}
	if got := sparkCells([]float64{0, 100, 200}, 8); got != "▁▄█" {
		t.Fatalf("the ramp is linear in the series' own range, got %q", got)
	}
	if got := sparkCells([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 8); len([]rune(got)) != 8 {
		t.Fatalf("a long series keeps its last 8 cells, got %q", got)
	}
	// The default cell count is the eight rounds the CONTEXT block draws.
	long := make([]float64, 20)
	for i := range long {
		long[i] = float64(i)
	}
	if got := stripANSI((Sparkline{Values: long}).View()); len([]rune(got)) != SparkCells {
		t.Fatalf("the sparkline is %d cells wide, got %q", SparkCells, got)
	}
}

func TestSpinnerNamesWhatIsRunning(t *testing.T) {
	if strings.Join(SpinnerFrames, "") != "⠋⠙⠹⠸⠼⠴⠦⠧" {
		t.Fatalf("the frame set is fixed, got %q", SpinnerFrames)
	}
	if got := (Spinner{Frame: 2, Label: "running go test", Elapsed: "3s"}); !strings.Contains(stripANSI(got.View()), "⠹ running go test · 3s") {
		t.Fatalf("the spinner renders frame, label and elapsed: %q", got.View())
	}
	// Motion with no subject says only that the program is alive.
	if got := (Spinner{Frame: 1}).View(); got != "" {
		t.Fatalf("a spinner without a label renders nothing, got %q", got)
	}
	// Frames wrap in both directions, so a host can hand it any tick count.
	if got := (Spinner{Frame: len(SpinnerFrames)}).Glyph(); got != SpinnerFrames[0] {
		t.Fatalf("frame %d wraps to the first, got %q", len(SpinnerFrames), got)
	}
	if got := (Spinner{Frame: -1}).Glyph(); got != SpinnerFrames[len(SpinnerFrames)-1] {
		t.Fatalf("a negative frame wraps to the last, got %q", got)
	}
	// The Bubble Tea hosts animate the same frames at the same cadence.
	m := NewSpinnerModel()
	if strings.Join(m.Spinner.Frames, "") != strings.Join(SpinnerFrames, "") {
		t.Fatalf("the model uses the shared frames, got %q", m.Spinner.Frames)
	}
	if m.Spinner.FPS != SpinnerInterval || SpinnerInterval != 80*time.Millisecond {
		t.Fatalf("the cadence is 80ms a frame, got %s", m.Spinner.FPS)
	}
}
