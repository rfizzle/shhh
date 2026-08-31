package reports

import (
	"regexp"
	"strings"
	"testing"
)

func TestRenderChart_BarShape(t *testing.T) {
	svg := renderChart(Block{
		Type:    BlockBarChart,
		XLabels: []string{"a", "b", "c"},
		Series:  []Series{{Name: "x", Values: []float64{1, 4, 2}}, {Name: "y", Values: []float64{2, 1, 3}}},
	})
	if got := strings.Count(svg, "<rect"); got != 6 {
		t.Fatalf("bar chart drew %d rects, want 6", got)
	}
	for _, want := range []string{`fill="var(--series-1)"`, `fill="var(--series-2)"`, `stroke="var(--rule)"`, ">a<", ">5<"} {
		if !strings.Contains(svg, want) {
			t.Fatalf("bar svg missing %s:\n%s", want, svg)
		}
	}
}

func TestRenderChart_LineShape(t *testing.T) {
	svg := renderChart(Block{
		Type:   BlockLineChart,
		Series: []Series{{Values: []float64{1, 2, 3}}, {Values: []float64{3, 2, 1}}},
	})
	if got := strings.Count(svg, "<polyline"); got != 2 {
		t.Fatalf("line chart drew %d polylines, want 2", got)
	}
	if got := strings.Count(svg, "<circle"); got != 6 {
		t.Fatalf("line chart drew %d markers, want 6", got)
	}
}

func TestRenderChart_ManyPointsDropMarkers(t *testing.T) {
	vals := make([]float64, 60)
	svg := renderChart(Block{Type: BlockLineChart, Series: []Series{{Values: vals}}})
	if strings.Contains(svg, "<circle") {
		t.Fatal("a 60-point line still drew per-point markers")
	}
}

func TestRenderChart_DegenerateDataStillDraws(t *testing.T) {
	for name, b := range map[string]Block{
		"empty series": {Type: BlockBarChart, Series: []Series{{}}},
		"all equal":    {Type: BlockLineChart, Series: []Series{{Values: []float64{5, 5, 5}}}},
		"single point": {Type: BlockLineChart, Series: []Series{{Values: []float64{7}}}},
		"negatives":    {Type: BlockBarChart, Series: []Series{{Values: []float64{-3, 4}}}},
	} {
		svg := renderChart(b)
		if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
			t.Fatalf("%s: chart did not draw a frame:\n%s", name, svg)
		}
	}
}

// The token invariant holds for our own output too: a chart never paints a
// literal color.
func TestRenderChart_NoLiteralColors(t *testing.T) {
	svg := renderChart(Block{
		Type:    BlockBarChart,
		XLabels: []string{"one"},
		Series:  []Series{{Values: []float64{1200000}}},
	})
	for _, bad := range []string{"#", "rgb(", "hsl("} {
		if strings.Contains(svg, bad) {
			t.Fatalf("chart output contains literal color %q:\n%s", bad, svg)
		}
	}
}

func TestNiceCeilAndTicks(t *testing.T) {
	for v, want := range map[float64]float64{3: 5, 7: 10, 12: 20, 43: 50, 99: 100, 100: 100, 0.4: 0.5} {
		if got := niceCeil(v); got != want {
			t.Fatalf("niceCeil(%v) = %v, want %v", v, got, want)
		}
	}
	for v, want := range map[float64]string{1200: "1.2k", 1000: "1k", 3400000: "3.4M", 2000000000: "2B", 7: "7", 0.5: "0.5"} {
		if got := fmtTick(v); got != want {
			t.Fatalf("fmtTick(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestRenderChart_EscapesLabels(t *testing.T) {
	svg := renderChart(Block{
		Type:    BlockBarChart,
		XLabels: []string{`<script>`},
		Series:  []Series{{Values: []float64{1}}},
	})
	if strings.Contains(svg, "<script>") {
		t.Fatal("label landed unescaped in the svg")
	}
	if !regexp.MustCompile(`&lt;script&gt;`).MatchString(svg) {
		t.Fatalf("escaped label missing:\n%s", svg)
	}
}
