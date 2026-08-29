package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The host half of the metrics surface: what the store recorded, what
// a token costs, what became of a command, and which readings there is
// anything to say about. The screen's own rules are tested where the screen
// is.

func ptrFloat(v float64) *float64 { return &v }

// metricsPrices is a table with one priced model and one the gateway returned
// as a bare id, which is the case that separates "free" from "not priced".
func metricsPrices() *pricing.Table {
	return pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-5.2": {InputCostPerToken: 0.000001, OutputCostPerToken: 0.00001},
	})
}

func metricsSummaryFixture() []storage.ProviderMetrics {
	return []storage.ProviderMetrics{
		{
			Provider: "openai", Model: "gpt-5.2", Count: 10, SuccessRate: 0.9,
			AvgTTFT: ptrFloat(640), P95TTFT: ptrFloat(1400),
			TotalTokensIn: ptrInt64(2_900_000), TotalTokensOut: ptrInt64(318_000),
			ExecCount: 6, ExecSuccessRate: ptrFloat(0.5),
			RatedCount: 4, RatingRate: ptrFloat(0.75),
		},
		{
			Provider: "gateway", Model: "house-model", Count: 4, SuccessRate: 1,
			AvgTTFT: ptrFloat(310), TotalTokensIn: ptrInt64(88_000), TotalTokensOut: ptrInt64(7_000),
		},
	}
}

// --window is "all" by default, because metrics were all-time before this
// screen existed and narrowing them silently would be the interface deciding
// which of your history counts.
func TestParseMetricsWindow_DefaultsToEverything(t *testing.T) {
	since, label, err := parseMetricsWindow("all")
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if !since.IsZero() {
		t.Fatalf("all time has a cutoff of %v", since)
	}
	if label != "all time" {
		t.Fatalf("all time reads %q", label)
	}
}

// A window states itself in the words the header says it in, and a window
// that is not a number of days is refused rather than silently read as one.
func TestParseMetricsWindow_ReadsDaysAndRefusesTheRest(t *testing.T) {
	for _, tc := range []struct{ in, label string }{
		{"7d", "last 7 days"},
		{"30d", "last 30 days"},
		{"1d", "last day"},
	} {
		since, label, err := parseMetricsWindow(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if label != tc.label {
			t.Fatalf("%s reads %q, want %q", tc.in, label, tc.label)
		}
		if since.IsZero() || since.After(time.Now()) {
			t.Fatalf("%s has a cutoff of %v", tc.in, since)
		}
	}
	for _, bad := range []string{"7", "week", "-3d", "0d"} {
		if _, _, err := parseMetricsWindow(bad); err == nil {
			t.Fatalf("%q was accepted as a window", bad)
		}
	}
}

// A model is known by its own name. It only carries its provider where two
// providers served the same name, which is the one case where the bare name
// would claim two different rows are the same thing.
func TestMetricsNames_ProviderOnlyWhereTheNameIsAmbiguous(t *testing.T) {
	names := metricsNames([]storage.ProviderMetrics{
		{Provider: "openai", Model: "gpt-5.2"},
		{Provider: "gateway", Model: "gpt-5.2"},
		{Provider: "gemini", Model: "gemini-3-flash"},
	})
	if got := names["openai/gpt-5.2"]; got != "openai/gpt-5.2" {
		t.Fatalf("a name two providers serve reads %q", got)
	}
	if got := names["gemini/gemini-3-flash"]; got != "gemini-3-flash" {
		t.Fatalf("a name only one provider serves reads %q", got)
	}
}

// The trend carries one cell per day whether or not anything ran on it: a
// sparkline drawn only over the days that happened would be the shape of a
// different week on every row.
func TestMetricsTrend_FillsTheDaysNothingRanOn(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	trend := metricsTrend([]storage.MetricsDayTokens{
		{Provider: "openai", Model: "gpt-5.2", Day: "2026-08-27", TokensIn: 400, TokensOut: 100},
		{Provider: "openai", Model: "gpt-5.2", Day: "2026-08-25", TokensIn: 200, TokensOut: 0},
		{Provider: "openai", Model: "gpt-5.2", Day: "2026-01-01", TokensIn: 9999, TokensOut: 0},
	}, now)

	run := trend["openai/gpt-5.2"]
	if len(run) != metricsTrendDays {
		t.Fatalf("the trend is %d cells, want %d", len(run), metricsTrendDays)
	}
	if run[len(run)-1] != 500 {
		t.Fatalf("today reads %v, want 500", run[len(run)-1])
	}
	if run[len(run)-3] != 200 {
		t.Fatalf("two days ago reads %v, want 200", run[len(run)-3])
	}
	if run[0] != 0 {
		t.Fatalf("a day nothing ran on reads %v, want 0", run[0])
	}
	for _, v := range run {
		if v == 9999 {
			t.Fatal("a day outside the week reached the trend")
		}
	}
}

// A token count the store never recorded is an em dash rather than a zero: a
// blank there would read as "none" where the truth is "never measured".
func TestMetricsTokens_UnrecordedIsNotZero(t *testing.T) {
	if got := metricsTokens(nil); got != components.NoDuration {
		t.Fatalf("an unrecorded count reads %q", got)
	}
	for _, tc := range []struct {
		in   int64
		want string
	}{{412, "412"}, {318_000, "318k"}, {2_900_000, "2.9M"}, {0, "0"}} {
		if got := metricsTokens(&tc.in); got != tc.want {
			t.Fatalf("%d reads %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A spend too small to round to a cent says so rather than reading as free,
// and a model with no price at all says nothing rather than $0.00.
func TestMetricsSpend_ZeroAndUnpricedAreDifferentAnswers(t *testing.T) {
	if got := metricsSpend(0, false); got != components.NoDuration {
		t.Fatalf("an unpriced model reads %q", got)
	}
	if got := metricsSpend(0.0004, true); got != "<$0.01" {
		t.Fatalf("a fraction of a cent reads %q", got)
	}
	if got := metricsSpend(12.8, true); got != "$12.80" {
		t.Fatalf("$12.80 reads %q", got)
	}
}

// Latency is milliseconds under a second and seconds above it, and an em dash
// where nothing was timed — the same reading every duration field makes.
func TestMetricsLatency_ReadsLikeEveryOtherDuration(t *testing.T) {
	if got := metricsLatency(nil); got != components.NoDuration {
		t.Fatalf("an untimed request reads %q", got)
	}
	if got := metricsLatency(ptrFloat(640)); got != "640ms" {
		t.Fatalf("640ms reads %q", got)
	}
	if got := metricsLatency(ptrFloat(1400)); got != "1.4s" {
		t.Fatalf("1400ms reads %q", got)
	}
}

// A request that never answered is its own category however it was reached:
// it is not a thing that was done with a command, it is the cost of there
// having been no command.
func TestMetricsCategory_TheUnansweredOutrankTheAction(t *testing.T) {
	if got := metricsCategory("run", false); got != "unanswered" {
		t.Fatalf("a request that never answered reads %q", got)
	}
	for _, tc := range []struct{ action, want string }{
		{"run", "run"}, {"run-all", "run"}, {"copy", "copied"}, {"save", "saved"},
		{"edit", "edited"}, {"cancel", "dismissed"}, {"", "never used"},
	} {
		if got := metricsCategory(tc.action, true); got != tc.want {
			t.Fatalf("%q reads %q, want %q", tc.action, got, tc.want)
		}
	}
}

// The header states the window, what it counted and what it cost, and the
// table states every model in it.
func TestNewMetricsScreen_HeaderStatesTheWindowAndTheSpend(t *testing.T) {
	screen := newMetricsScreen(metricsData{
		Summary: metricsSummaryFixture(), Prices: metricsPrices(),
		Window: "last 30 days", Now: time.Now(),
	})
	for _, want := range []string{"last 30 days", "14 requests", "2 models"} {
		if !strings.Contains(screen.Subject, want) {
			t.Fatalf("the subject %q does not state %q", screen.Subject, want)
		}
	}
	if screen.Spend != "$6.08" {
		t.Fatalf("the total spend reads %q", screen.Spend)
	}
	if len(screen.Models) != 2 {
		t.Fatalf("the table has %d rows", len(screen.Models))
	}
	if screen.Models[1].Spend != components.NoDuration {
		t.Fatalf("a model with no price reads %q", screen.Models[1].Spend)
	}
}

// Every ratio the store has an answer for is a meter with its number beside
// it, and every ratio it has nothing for is left out rather than drawn as an
// empty bar.
func TestNewMetricsScreen_ARatioWithNothingInItIsLeftOut(t *testing.T) {
	screen := newMetricsScreen(metricsData{
		Summary: metricsSummaryFixture(),
		Actions: []storage.MetricsActionUsage{
			{Provider: "openai", Model: "gpt-5.2", Action: "run", Success: true,
				Count: 6, TokensIn: 2_000_000, TokensOut: 200_000},
			{Provider: "openai", Model: "gpt-5.2", Action: "copy", Success: true,
				Count: 3, TokensIn: 800_000, TokensOut: 100_000},
			{Provider: "openai", Model: "gpt-5.2", Action: "run", Success: false,
				Count: 1, TokensIn: 100_000, TokensOut: 18_000},
		},
		Prices: metricsPrices(), Window: "all time", Now: time.Now(),
	})

	titles := make([]string, 0, len(screen.Blocks))
	for _, block := range screen.Blocks {
		titles = append(titles, block.Title)
		for _, bar := range block.Bars {
			if bar.Text == "" {
				t.Fatalf("%s: a bar with no number beside it", block.Title)
			}
		}
	}
	joined := strings.Join(titles, " | ")
	for _, want := range []string{
		"where the money went", "how the answers came back",
		"how the commands ran", "how the answers were rated",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the blocks %q are missing %q", joined, want)
		}
	}

	// Only gpt-5.2 has an exit code or a rating recorded, so those two blocks
	// carry one bar rather than a second empty one.
	for _, block := range screen.Blocks {
		if block.Title == "how the commands ran" || block.Title == "how the answers were rated" {
			if len(block.Bars) != 1 {
				t.Fatalf("%s carries %d bars, want 1", block.Title, len(block.Bars))
			}
		}
	}
}

// A cost nobody asked for keeps its del fill and a glyph on its label, so the
// row still says what it is once colour is gone (invariant 1).
func TestMetricsSpendBlock_TheUnaskedCostIsToldTwice(t *testing.T) {
	block := metricsSpendBlock(metricsData{
		Actions: []storage.MetricsActionUsage{
			{Provider: "openai", Model: "gpt-5.2", Action: "run", Success: true,
				Count: 6, TokensIn: 2_000_000, TokensOut: 200_000},
			{Provider: "openai", Model: "gpt-5.2", Action: "run", Success: false,
				Count: 2, TokensIn: 400_000, TokensOut: 20_000},
		},
		Prices: metricsPrices(), Window: "all time",
	}, map[string]string{"openai/gpt-5.2": "gpt-5.2"})

	var found bool
	for _, bar := range block.Bars {
		if bar.Tone != components.MeterUnasked {
			continue
		}
		found = true
		if !strings.HasPrefix(bar.Label, "✗") {
			t.Fatalf("the unasked cost reads %q, with no glyph on it", bar.Label)
		}
		if bar.NoteTone != components.ToneRisk {
			t.Fatalf("the unasked cost's note is toned %v", bar.NoteTone)
		}
	}
	if !found {
		t.Fatal("the requests that never answered are not a category")
	}
	if block.Title != "where the money went" {
		t.Fatalf("a priced split reads %q", block.Title)
	}
}

// Where nothing can be priced, the split is over tokens and the block's title
// says so — the split is the reading, and the currency is only the unit it
// happened to be in.
func TestMetricsSpendBlock_UnpricedSplitsTheTokensInstead(t *testing.T) {
	block := metricsSpendBlock(metricsData{
		Actions: []storage.MetricsActionUsage{
			{Provider: "gateway", Model: "house-model", Action: "run", Success: true,
				Count: 3, TokensIn: 60_000, TokensOut: 6_000},
			{Provider: "gateway", Model: "house-model", Action: "copy", Success: true,
				Count: 1, TokensIn: 30_000, TokensOut: 4_000},
		},
		Prices: metricsPrices(), Window: "all time",
	}, map[string]string{"gateway/house-model": "house-model"})

	if block.Title != "where the tokens went" {
		t.Fatalf("an unpriced split reads %q", block.Title)
	}
	if len(block.Bars) != 2 {
		t.Fatalf("the split has %d bars", len(block.Bars))
	}
	for _, bar := range block.Bars {
		if strings.Contains(bar.Text, "$") {
			t.Fatalf("an unpriced bar states money: %q", bar.Text)
		}
	}
}

// A store with nothing in the window has no split at all rather than a block
// of zero-length bars.
func TestMetricsSpendBlock_NothingRecordedDrawsNothing(t *testing.T) {
	block := metricsSpendBlock(metricsData{Prices: metricsPrices(), Window: "all time"}, nil)
	if len(block.Bars) != 0 {
		t.Fatalf("an empty store drew %d bars", len(block.Bars))
	}
}
