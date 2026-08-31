package reports

import (
	"fmt"
	"html"
	"math"
	"strings"
)

// Chart geometry: one fixed viewBox scaled by CSS. The numbers are the
// design, not configuration — every chart on every page is the same shape.
const (
	chartW      = 640
	chartH      = 300
	chartLeft   = 48
	chartRight  = 12
	chartTop    = 12
	chartBottom = 28
	chartTicks  = 4
)

// seriesToken is the fill for series i (0-based): slots assigned in fixed
// order, never cycled past what exists — the ramp holds eight and the block
// validator caps series at eight.
func seriesToken(i int) string { return fmt.Sprintf("var(--series-%d)", i%8+1) }

// renderChart draws a bar or line chart as inline SVG. It never errors:
// degenerate data (one point, all-equal values) still draws a scaled frame,
// because a chart the model got slightly wrong should still open.
func renderChart(b Block) string {
	lo, hi := chartDomain(b.Series)
	plotW := float64(chartW - chartLeft - chartRight)
	plotH := float64(chartH - chartTop - chartBottom)
	y := func(v float64) float64 {
		return float64(chartTop) + plotH - (v-lo)/(hi-lo)*plotH
	}

	var s strings.Builder
	fmt.Fprintf(&s, `<svg viewBox="0 0 %d %d" width="100%%" role="img" preserveAspectRatio="xMidYMid meet">`, chartW, chartH)

	// Gridlines and tick labels.
	for i := 0; i <= chartTicks; i++ {
		v := lo + (hi-lo)*float64(i)/chartTicks
		yy := y(v)
		fmt.Fprintf(&s, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="var(--rule)" stroke-width="1"/>`, chartLeft, yy, chartW-chartRight, yy)
		fmt.Fprintf(&s, `<text x="%d" y="%.1f" fill="var(--caption)" font-size="11" text-anchor="end">%s</text>`, chartLeft-6, yy+4, fmtTick(v))
	}
	// Zero baseline, when it is inside the domain and not already a tick edge.
	if lo < 0 && hi > 0 {
		fmt.Fprintf(&s, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="var(--track)" stroke-width="1"/>`, chartLeft, y(0), chartW-chartRight, y(0))
	}

	points := maxLen(b.Series)
	switch b.Type {
	case BlockBarChart:
		drawBars(&s, b.Series, points, plotW, y)
	case BlockLineChart:
		drawLines(&s, b.Series, points, plotW, y)
	}
	drawXLabels(&s, b.XLabels, points, plotW)
	s.WriteString(`</svg>`)
	return s.String()
}

func drawBars(s *strings.Builder, series []Series, points int, plotW float64, y func(float64) float64) {
	if points == 0 {
		return
	}
	group := plotW / float64(points)
	barW := (group - 6) / float64(len(series))
	if barW < 1 {
		barW = 1
	}
	base := y(0)
	for si, sr := range series {
		for pi, v := range sr.Values {
			x := float64(chartLeft) + group*float64(pi) + 3 + barW*float64(si)
			top, h := y(v), base-y(v)
			if h < 0 { // negative value: bar hangs below the baseline
				top, h = base, -h
			}
			fmt.Fprintf(s, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" fill="%s"/>`, x, top, math.Max(barW-2, 1), h, seriesToken(si))
		}
	}
}

func drawLines(s *strings.Builder, series []Series, points int, plotW float64, y func(float64) float64) {
	if points == 0 {
		return
	}
	step := plotW
	if points > 1 {
		step = plotW / float64(points-1)
	}
	for si, sr := range series {
		var pts []string
		for pi, v := range sr.Values {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", float64(chartLeft)+step*float64(pi), y(v)))
		}
		fmt.Fprintf(s, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round"/>`, strings.Join(pts, " "), seriesToken(si))
		if len(sr.Values) <= 20 {
			for pi, v := range sr.Values {
				fmt.Fprintf(s, `<circle cx="%.1f" cy="%.1f" r="3" fill="%s"/>`, float64(chartLeft)+step*float64(pi), y(v), seriesToken(si))
			}
		}
	}
}

func drawXLabels(s *strings.Builder, labels []string, points int, plotW float64) {
	if len(labels) == 0 || points == 0 {
		return
	}
	// A crowded axis shows every nth label rather than a smear.
	every := (len(labels) + 11) / 12
	group := plotW / float64(points)
	for i, l := range labels {
		if i%every != 0 {
			continue
		}
		x := float64(chartLeft) + group*(float64(i)+0.5)
		fmt.Fprintf(s, `<text x="%.1f" y="%d" fill="var(--caption)" font-size="11" text-anchor="middle">%s</text>`, x, chartH-8, html.EscapeString(clipLabel(l)))
	}
}

func clipLabel(l string) string {
	r := []rune(l)
	if len(r) <= 14 {
		return l
	}
	return string(r[:13]) + "…"
}

// chartDomain is [min(0, lowest), niceCeil(highest)] — bars grow from zero,
// and the top gridline lands on a number a person would pick.
func chartDomain(series []Series) (lo, hi float64) {
	lo, hi = 0, math.Inf(-1)
	for _, s := range series {
		for _, v := range s.Values {
			lo = math.Min(lo, v)
			hi = math.Max(hi, v)
		}
	}
	if math.IsInf(hi, -1) {
		return 0, 1
	}
	if hi <= lo {
		hi = lo + 1
	}
	if hi > 0 {
		hi = niceCeil(hi)
	}
	if lo < 0 {
		lo = -niceCeil(-lo)
	}
	return lo, hi
}

// niceCeil rounds v up to 1, 2 or 5 times a power of ten.
func niceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if v <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

// fmtTick writes an axis number the way the rail writes counts: 1.2k, 3.4M.
func fmtTick(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e9:
		return trimZero(fmt.Sprintf("%.1fB", v/1e9))
	case a >= 1e6:
		return trimZero(fmt.Sprintf("%.1fM", v/1e6))
	case a >= 1e3:
		return trimZero(fmt.Sprintf("%.1fk", v/1e3))
	case a == math.Trunc(a):
		return fmt.Sprintf("%.0f", v)
	default:
		return trimZero(fmt.Sprintf("%.1f", v))
	}
}

func trimZero(s string) string {
	return strings.Replace(s, ".0", "", 1)
}

func maxLen(series []Series) int {
	n := 0
	for _, s := range series {
		if len(s.Values) > n {
			n = len(s.Values)
		}
	}
	return n
}
