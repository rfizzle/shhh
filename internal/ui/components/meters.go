package components

// Meters (S-094, DESIGN-TUI.md §10c). Every quantitative glyph run in the
// product is one of three things — a block meter, a sparkline, or the spinner
// — and each has exactly one implementation here, so the context bar, step
// progress, agent lanes and the retry countdown cannot drift apart.
//
// Two rules are enforced rather than documented. A meter always states its
// percent or count in text beside its bar: a bar alone is a shape, not a
// number, and the bar and the number turn colour together. The spinner always
// names what is running: motion without a subject says only that something is
// happening. And a ratio is never fabricated — a lane whose agent declared no
// step count gets the spinner, not a bar drawn against a denominator nobody
// supplied.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
)

// Meter cell counts by role (§10c). The only supported widths: a meter that
// wants some other count is a meter that is drifting.
const (
	MeterCellsVitals    = 8  // the context meter in the vitals rail
	MeterCellsRail      = 22 // context and step progress in the inspector rail
	MeterCellsAgent     = 5  // an agent lane's progress
	MeterCellsCountdown = 20 // a retry countdown (§17)
)

// Context-meter warning thresholds, matching S-055's trim warnings. Hosts
// with their own thresholds override them per meter.
const (
	ctxWarnPct  = 70
	ctxAlertPct = 90
)

// MeterTone selects how a meter colours its cells and its number (§10c).
type MeterTone int

const (
	// MeterPressure colours by the value: add below the warn threshold,
	// accent from it, del from the alert threshold — where it also goes bold.
	// This is the context meter, in both rails and in the pressure card.
	MeterPressure MeterTone = iota
	// MeterProgress is step progress: the completed cells add, the cells of
	// the step in flight spin, the rest dim.
	MeterProgress
	// MeterAgent is an agent lane — always info. A lane is never colour-coded
	// by health.
	MeterAgent
	// MeterCountdown is a draining countdown — always accent.
	MeterCountdown
	// MeterCategory is the category meter of §19c: one share of a total
	// nobody set a threshold on — where the money went, how the answers came
	// back. Always accent, never the context ladder, because a threshold
	// colour here would imply a limit that does not exist.
	MeterCategory
	// MeterUnasked is a category meter over a share that is a cost nobody
	// asked for — §19c's retries row, and the requests that never answered.
	// Del, for the same reason the ladder's own top rung is: this is the
	// part of the total the reader would rather not have paid.
	MeterUnasked
)

// Meter is the block meter: `▰` filled, `▱` empty. Never a bar element, never
// a percentage without its bar, never a bar without its number.
type Meter struct {
	// Pct is the fill, 0–100; values outside the range are clamped.
	Pct int
	// Cells is the bar's width; 0 means MeterCellsRail.
	Cells int
	Tone  MeterTone
	// Label is the optional leading field — the "ctx" of the vitals rail, or
	// the percent itself when the host wants the number ahead of the bar.
	Label string
	// Text is what the meter states after its bar. Empty means its own
	// percent, because the bar is never the only carrier of the value.
	Text string
	// Warn and Alert override MeterPressure's thresholds (0 keeps 70/90), so
	// a meter matches the host's own trim warnings.
	Warn, Alert int
	// Running is how many of the filled cells belong to the step in flight
	// (MeterProgress only); they render in spin, the rest in add.
	Running int
}

// StepMeter is the step-progress meter for step of steps: the completed steps
// filled, the step in flight taking its own slice of cells in spin, and the
// count stated beside the bar. It returns ok=false when no total was
// declared — there is no honest ratio to draw (S-094).
func StepMeter(step, steps, cells int, running bool) (Meter, bool) {
	if steps <= 0 {
		return Meter{}, false
	}
	if cells <= 0 {
		cells = MeterCellsRail
	}
	step = min(max(step, 0), steps)
	m := Meter{
		Pct:   step * 100 / steps,
		Cells: cells,
		Tone:  MeterProgress,
		Text:  fmt.Sprintf("step %d of %d", step, steps),
	}
	if running && step > 0 {
		m.Running = max(cells/steps, 1)
	}
	return m, true
}

// AgentMeter is a lane's progress meter — five cells, always info, and only
// when the child declared a step count (S-094). A lane without one gets the
// Spinner, because a bar drawn against a denominator nobody supplied is a
// number the interface invented.
func AgentMeter(step, steps int) (Meter, bool) {
	m, ok := StepMeter(step, steps, MeterCellsAgent, false)
	if !ok {
		return Meter{}, false
	}
	m.Tone = MeterAgent
	return m, true
}

// Style is the meter's colour, so a host stating the value in its own row can
// colour the number to match the bar.
func (m Meter) Style() lipgloss.Style {
	switch m.Tone {
	case MeterProgress:
		return sty.Add
	case MeterAgent:
		return sty.Info
	case MeterCountdown, MeterCategory:
		return sty.Accent
	case MeterUnasked:
		return sty.Del
	default:
		return ctxStyle(min(max(m.Pct, 0), 100), m.Warn, m.Alert)
	}
}

// View renders the meter: leading label, bar, and the value stated beside it.
func (m Meter) View() string {
	if m.Tone == MeterProgress {
		return join(sty.Dim.Render(m.Label), m.Bar(), m.Style().Render(m.text()))
	}
	// One styling pass, so nothing but spaces separates the bar from its
	// number: the two are one field, and they turn colour together.
	return m.Style().Render(join(m.Label, meterCells(m.pct(), m.cells()), m.text()))
}

// Bar is the styled bar alone, for a host that states the meter's value
// elsewhere on the same row — the inspector rail right-aligns its token count
// at the rail's edge. A host using it still has to state that number: the
// rule is that the value is beside the bar, not that View wrote it.
func (m Meter) Bar() string {
	cells, pct := m.cells(), m.pct()
	filled := meterFill(pct, cells)
	if m.Tone != MeterProgress {
		return m.Style().Render(meterCells(pct, cells))
	}
	// The only two-colour meter: the run in flight is motion, and motion is
	// never the same colour as what is already done.
	spin := min(max(m.Running, 0), filled)
	return sty.Add.Render(strings.Repeat("▰", filled-spin)) +
		sty.SpinText.Render(strings.Repeat("▰", spin)) +
		sty.Dim.Render(strings.Repeat("▱", cells-filled))
}

func (m Meter) cells() int {
	if m.Cells <= 0 {
		return MeterCellsRail
	}
	return m.Cells
}

func (m Meter) pct() int { return min(max(m.Pct, 0), 100) }

// text is what the meter states beside its bar — the host's own count, or the
// percent when it gave none.
func (m Meter) text() string {
	if m.Text != "" {
		return m.Text
	}
	return fmt.Sprintf("%d%%", m.pct())
}

// join spaces the present fields of a meter row.
func join(fields ...string) string {
	var out []string
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, " ")
}

// meterCells renders the ▰/▱ run for a percentage, unstyled. The bar is never
// the only carrier of the value — every caller states the number beside it
// (§10c).
func meterCells(pct, cells int) string {
	pct = min(max(pct, 0), 100)
	filled := meterFill(pct, cells)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", cells-filled)
}

// meterFill is the cell count for a percentage. Cells are truncated rather
// than rounded, so a bar only fills completely at 100%, with one exception: a
// non-zero percentage always shows at least one cell, because an empty bar
// beside "1%" reads as nothing running.
func meterFill(pct, cells int) int {
	filled := pct * cells / 100
	if filled == 0 && pct > 0 {
		filled = 1
	}
	return min(filled, cells)
}

// ctxStyle colours the context meter and its number together (§10c): healthy
// in add, pressured in accent, and bold del once compaction is the next
// thing that should happen.
func ctxStyle(pct, warn, alert int) lipgloss.Style {
	if warn <= 0 {
		warn = ctxWarnPct
	}
	if alert <= 0 {
		alert = ctxAlertPct
	}
	switch {
	case pct >= alert:
		return sty.Err.Bold(true)
	case pct >= warn:
		return sty.Accent
	default:
		return sty.Add
	}
}

// SparkCells is the sparkline's fixed cell count: eight rounds, the last
// eight samples, no more.
const SparkCells = 8

// Sparkline is the `▁▂▃▄▅▆▇█` run — tokens per round in the CONTEXT block, and
// nothing else so far. It is a shape, not a measurement; the numbers beside it
// are the measurement, which is why it is always dimmer and never coloured.
type Sparkline struct {
	// Values is the series, oldest first; only the last Cells samples are
	// drawn.
	Values []float64
	// Cells is the run's width; 0 means SparkCells.
	Cells int
}

// View renders the run. An empty series renders nothing at all rather than a
// flat line, because a series with no samples has no shape.
func (s Sparkline) View() string {
	cells := s.Cells
	if cells <= 0 {
		cells = SparkCells
	}
	run := sparkCells(s.Values, cells)
	if run == "" {
		return ""
	}
	return sty.Dimmer.Render(run)
}

// sparkCells renders the last cells values of a series as a ▁▂▃▄▅▆▇█ run,
// scaled to the series' own maximum, unstyled. A flat series is a flat run at
// full height — the shape is honest about having no variation — and an
// all-zero series is a flat run at the floor.
func sparkCells(series []float64, cells int) string {
	if len(series) == 0 || cells <= 0 {
		return ""
	}
	if len(series) > cells {
		series = series[len(series)-cells:]
	}
	ramp := []rune("▁▂▃▄▅▆▇█")
	maxV := series[0]
	for _, v := range series {
		if v > maxV {
			maxV = v
		}
	}
	var b strings.Builder
	for _, v := range series {
		idx := 0
		if maxV > 0 {
			idx = int(v / maxV * float64(len(ramp)-1))
		}
		b.WriteRune(ramp[min(max(idx, 0), len(ramp)-1)])
	}
	return b.String()
}

// SpinnerFrames is the product's only animation (§10c). Anything else that
// wants to move gets a meter.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧"}

// SpinnerInterval is the frame cadence.
const SpinnerInterval = 80 * time.Millisecond

// Spinner is one frame of that animation beside the word naming what is
// running. Hosts that animate with Bubble Tea use NewSpinnerModel; hosts that
// render a passive surface (the inspector rail) pass the frame index they are
// ticking.
type Spinner struct {
	Frame int
	// Label names what is running ("running go test"). Without it the spinner
	// renders nothing: motion with no subject reports only that the program
	// is alive, which the user can already see.
	Label string
	// Elapsed is the optional wall-clock tail ("3s").
	Elapsed string
}

// Glyph is the current frame.
func (s Spinner) Glyph() string {
	n := len(SpinnerFrames)
	return SpinnerFrames[((s.Frame%n)+n)%n]
}

// View renders the frame, its label, and any elapsed time.
func (s Spinner) View() string {
	if s.Label == "" {
		return ""
	}
	out := sty.SpinText.Render(s.Glyph() + " " + s.Label)
	if s.Elapsed != "" {
		out += sty.Dim.Render(" · " + s.Elapsed)
	}
	return out
}

// NewSpinnerModel is the Bubble Tea spinner every animating host uses, so the
// frame set and the 80ms cadence live in one place.
func NewSpinnerModel() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Spinner{Frames: SpinnerFrames, FPS: SpinnerInterval}
	s.Style = sty.SpinText
	return s
}
