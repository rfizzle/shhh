package cli

// The metrics surface (
// docs/interface/surfaces.md#the-supporting-screens). `shhh metrics` printed
// a fifteen-column tabwriter table and had no Bubble Tea at all; it hosts
// `components.MetricsScreen` now and owns everything the screen deliberately
// does not — what the store recorded, what a token costs, what became of a
// command, and which of those readings there is anything to say about.
//
// The screen is a renderer: every number reaching it is already a string, and
// every share is already a percentage. That is why it can draw `$12.80` and
// `94% answered` without knowing what a price table or an exit code is.
//
// `--text` (and a non-terminal stdout) prints the report, because a metrics
// run is the one thing in this product people pipe. It is one block per
// provider and model rather than the fifteen-column table it used to be:
// fifteen columns soft-wrap into unreadability at eighty, and a figure the
// store has nothing for is left out rather than dashed
// (docs/interface/surfaces.md#outside-the-tui).

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

// defaultMetricsWidth is what the surface is drawn at before the terminal has
// said how wide it is — the width the `Tools` artboard draws it at.
const defaultMetricsWidth = 120

// metricsTrendDays is how many days of token totals the per-model sparkline
// carries. It is a week, and it is fixed rather than following --window, so
// two runs of `shhh metrics` are comparing the same span.
const metricsTrendDays = 7

func newMetricsCmd() *cobra.Command {
	var window string
	var text bool
	var table bool
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Show provider usage metrics",
		Long:  "Display summary statistics for each provider and model you've used.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, label, err := parseMetricsWindow(window)
			if err != nil {
				return err
			}
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			summary, err := db.MetricsSummary(since)
			if err != nil {
				return fmt.Errorf("query metrics: %w", err)
			}
			trend, err := db.MetricsTokensByDay(since)
			if err != nil {
				return fmt.Errorf("query token trend: %w", err)
			}
			actions, err := db.MetricsByAction(since)
			if err != nil {
				return fmt.Errorf("query action split: %w", err)
			}
			data := metricsData{
				Summary: summary, Trend: trend, Actions: actions,
				Prices: loadPricing(), Window: label, Now: time.Now(),
			}
			switch {
			case asJSON:
				return writeJSON(cmd, metricsJSON(data))
			case text || table || len(summary) == 0 || !term.IsTerminal(os.Stdout.Fd()):
				return report.Fprint(cmd.OutOrStdout(), metricsReport(data))
			}
			return runMetricsScreen(newMetricsScreen(data))
		},
	}

	cmd.Flags().StringVar(&window, "window", "all",
		`time window: "all", or a number of days (e.g. 7d, 30d)`)
	cmd.Flags().BoolVar(&text, "text", false, "print the report as text instead of the surface")
	cmd.Flags().BoolVar(&table, "table", false, "")
	// --table named a fifteen-column table that no longer exists. The flag
	// stays so a script that passes it keeps working, and is hidden so
	// nothing new learns it.
	_ = cmd.Flags().MarkHidden("table")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the metrics as JSON")

	return cmd
}

// parseMetricsWindow reads the --window flag into its cutoff and the words
// the header says it in. "all" is the default and the zero time, because
// metrics were all-time before this screen existed and narrowing them
// silently would be the interface deciding which of your history counts.
func parseMetricsWindow(s string) (time.Time, string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(s))
	if trimmed == "" || trimmed == "all" {
		return time.Time{}, "all time", nil
	}
	days, err := strconv.Atoi(strings.TrimSuffix(trimmed, "d"))
	if err != nil || days <= 0 || !strings.HasSuffix(trimmed, "d") {
		return time.Time{}, "", fmt.Errorf(
			"invalid window %q (use \"all\" or a number of days, e.g. 7d, 30d)", s)
	}
	if days == 1 {
		return time.Now().AddDate(0, 0, -1), "last day", nil
	}
	return time.Now().AddDate(0, 0, -days), fmt.Sprintf("last %d days", days), nil
}

// metricsData is everything the host read out of the store for one run, so
// building the screen is a pure function of it and testable without a
// database.
type metricsData struct {
	Summary []storage.ProviderMetrics
	Trend   []storage.MetricsDayTokens
	Actions []storage.MetricsActionUsage
	Prices  *pricing.Table
	Window  string
	Now     time.Time
}

// newMetricsScreen reads the store into the surface. Every reading happens
// here: what a model is called, what its tokens cost, what became of the
// commands it answered with, and which blocks there is anything to say in.
func newMetricsScreen(data metricsData) components.MetricsScreen {
	names := metricsNames(data.Summary)
	trend := metricsTrend(data.Trend, data.Now)

	var (
		models []components.MetricsModel
		total  float64
		count  int
	)
	for _, m := range data.Summary {
		cost, priced := metricsCost(data.Prices, m.Model, m.TotalTokensIn, m.TotalTokensOut)
		if priced {
			total += cost
		}
		count += m.Count
		models = append(models, components.MetricsModel{
			Name:      names[metricsKey(m.Provider, m.Model)],
			Requests:  strconv.Itoa(m.Count),
			TokensIn:  metricsTokens(m.TotalTokensIn),
			TokensOut: metricsTokens(m.TotalTokensOut),
			Spend:     metricsSpend(cost, priced),
			TTFT:      metricsLatency(m.AvgTTFT),
			P95:       metricsLatency(m.P95TTFT),
			Trend:     trend[metricsKey(m.Provider, m.Model)],
		})
	}

	screen := components.MetricsScreen{
		Subject: fmt.Sprintf("%s · %s · %s", data.Window,
			countOf(count, "request", "requests"), countOf(len(models), "model", "models")),
		Models: models,
	}
	if total > 0 {
		screen.Spend = metricsSpend(total, true)
	}
	blocks := []components.MetricsBlock{metricsSpendBlock(data, names)}
	for _, ratio := range metricsRatios {
		blocks = append(blocks, metricsRateBlock(ratio, data.Summary, names))
	}
	for _, block := range blocks {
		// A reading the store has nothing for is left out rather than drawn
		// as a row of empty bars.
		if len(block.Bars) > 0 {
			screen.Blocks = append(screen.Blocks, block)
		}
	}
	return screen
}

// metricsKey identifies one provider/model pair in the store's own terms.
func metricsKey(provider, model string) string { return provider + "/" + model }

// metricsNames is what each model is called on the surface. A model is known
// by its own name; it only carries its provider when two providers served the
// same name, which is the one case where the bare name would be a lie about
// two rows being the same thing.
func metricsNames(summary []storage.ProviderMetrics) map[string]string {
	providers := map[string]map[string]bool{}
	for _, m := range summary {
		if providers[m.Model] == nil {
			providers[m.Model] = map[string]bool{}
		}
		providers[m.Model][m.Provider] = true
	}
	names := map[string]string{}
	for _, m := range summary {
		name := m.Model
		if name == "" {
			name = m.Provider
		} else if len(providers[m.Model]) > 1 {
			name = metricsKey(m.Provider, m.Model)
		}
		names[metricsKey(m.Provider, m.Model)] = name
	}
	return names
}

// metricsTrend is each model's token total for each of the last
// metricsTrendDays days, oldest first. Days with nothing on them are zeroes
// rather than gaps: a sparkline drawn only over the days that happened would
// be a shape of a different week for every row.
func metricsTrend(rows []storage.MetricsDayTokens, now time.Time) map[string][]float64 {
	days := make(map[string]int, metricsTrendDays)
	for i := range metricsTrendDays {
		day := now.UTC().AddDate(0, 0, -(metricsTrendDays - 1 - i)).Format("2006-01-02")
		days[day] = i
	}
	trend := map[string][]float64{}
	for _, row := range rows {
		at, ok := days[row.Day]
		if !ok {
			continue
		}
		key := metricsKey(row.Provider, row.Model)
		if trend[key] == nil {
			trend[key] = make([]float64, metricsTrendDays)
		}
		trend[key][at] += float64(row.TokensIn + row.TokensOut)
	}
	return trend
}

// metricsSpendBlock is `where the money went`: what became of the commands,
// as a share of what they cost. Where nothing can be priced — a gateway whose
// catalog returns bare ids, an offline pricing table — it splits the tokens
// instead and says so in its own title, because the split is the reading and
// the currency is only the unit it happens to be in.
func metricsSpendBlock(data metricsData, names map[string]string) components.MetricsBlock {
	type share struct {
		cost   float64
		tokens int64
		count  int
	}
	shares, priced := map[string]*share{}, false
	for _, a := range data.Actions {
		category := metricsCategory(a.Action, a.Success)
		if shares[category] == nil {
			shares[category] = &share{}
		}
		cost, ok := metricsCost(data.Prices, a.Model, &a.TokensIn, &a.TokensOut)
		priced = priced || ok
		shares[category].cost += cost
		shares[category].tokens += a.TokensIn + a.TokensOut
		shares[category].count += a.Count
	}

	var total float64
	amount := func(s *share) float64 { return s.cost }
	title, field := "where the money went", data.Window
	if !priced {
		amount = func(s *share) float64 { return float64(s.tokens) }
		title = "where the tokens went"
	}
	for _, s := range shares {
		total += amount(s)
	}
	if total <= 0 {
		return components.MetricsBlock{}
	}

	block := components.MetricsBlock{Title: title, Field: field}
	for _, category := range metricsCategories {
		s := shares[category.name]
		// A category with nothing in it is left out rather than drawn as an
		// empty bar.
		if s == nil || amount(s) <= 0 {
			continue
		}
		pct := int(amount(s) / total * 100)
		text := fmt.Sprintf("%s · %d%%", metricsSpend(s.cost, priced), pct)
		if !priced {
			text = fmt.Sprintf("%s · %d%%", metricsTokens(&s.tokens), pct)
		}
		bar := components.MetricsBar{
			Label: category.label, Pct: pct, Text: text,
			Note: countOf(s.count, "request", "requests"), Tone: components.MeterCategory,
		}
		if category.unasked {
			// The retries row: a cost you did not ask for keeps its del
			// fill, and the glyph on its label is what carries that once the
			// colour is gone (invariant 1).
			bar.Tone, bar.NoteTone = components.MeterUnasked, components.ToneRisk
		}
		block.Bars = append(block.Bars, bar)
	}
	sort.SliceStable(block.Bars, func(i, j int) bool { return block.Bars[i].Pct > block.Bars[j].Pct })
	return block
}

// metricsCategory buckets one request into the share of the spend it belongs
// to. A request that never answered is its own category however it was
// reached: it is not a thing that was done with a command, it is the cost of
// there having been no command.
func metricsCategory(action string, success bool) string {
	if !success {
		return "unanswered"
	}
	switch action {
	case "run", "run-all", "run-step":
		return "run"
	case "copy":
		return "copied"
	case "save":
		return "saved"
	case "edit", "revise":
		return "edited"
	case "cancel":
		return "dismissed"
	}
	return "never used"
}

// metricsCategories is the closed set of spend categories and how each reads
// on the surface, in the order they are laid out before their shares sort
// them.
var metricsCategories = []struct {
	name    string
	label   string
	unasked bool
}{
	{name: "run", label: "$ run"},
	{name: "copied", label: "copied"},
	{name: "saved", label: "saved"},
	{name: "edited", label: "edited"},
	{name: "dismissed", label: "⊘ dismissed"},
	{name: "never used", label: "· never used"},
	{name: "unanswered", label: "✗ no answer", unasked: true},
}

// metricsRatio is one reading a model can be measured by: what the block is
// called, the noun its denominator counts, and how to read one model. ok is
// false where the store has nothing to read — a model nobody rated has no
// rating, and a bar drawn against a denominator nobody supplied is a number
// the interface invented.
type metricsRatio struct {
	title     string
	one, many string
	read      func(m storage.ProviderMetrics) (pct int, text, note string, denom int, ok bool)
}

// metricsRatios is every ratio block under the table, in the order they are
// drawn. Latency is not among them, because latency is not a ratio — it has
// no denominator to draw a bar against, so it stays a column of the table.
var metricsRatios = []metricsRatio{
	{title: "how the answers came back", one: "request", many: "requests", read: metricsAnswered},
	{title: "how the commands ran", one: "run", many: "runs", read: metricsRan},
	{title: "how the answers were rated", one: "rating", many: "ratings", read: metricsRated},
}

// metricsRateBlock is one ratio block: a block meter per model with its
// number beside it, over the count its own reading is against — the answers
// block is over every request, the runs block only over the ones an exit code
// was recorded for.
func metricsRateBlock(ratio metricsRatio, summary []storage.ProviderMetrics,
	names map[string]string) components.MetricsBlock {
	block := components.MetricsBlock{Title: ratio.title}
	over := 0
	for _, m := range summary {
		pct, text, note, denom, ok := ratio.read(m)
		if !ok {
			continue
		}
		over += denom
		block.Bars = append(block.Bars, components.MetricsBar{
			Label: names[metricsKey(m.Provider, m.Model)],
			Pct:   pct, Text: text, Note: note, Tone: components.MeterCategory,
		})
	}
	block.Field = countOf(over, ratio.one, ratio.many)
	return block
}

// metricsAnswered is how much of what was asked came back at all.
func metricsAnswered(m storage.ProviderMetrics) (int, string, string, int, bool) {
	if m.Count == 0 {
		return 0, "", "", 0, false
	}
	pct := int(m.SuccessRate*100 + 0.5)
	answered := int(m.SuccessRate*float64(m.Count) + 0.5)
	return pct, fmt.Sprintf("%d%% answered", pct),
		fmt.Sprintf("%d of %d", answered, m.Count), m.Count, true
}

// metricsRan is how much of what was run exited clean. Only requests whose
// exit code was recorded are in it, which is what the block is counted over.
func metricsRan(m storage.ProviderMetrics) (int, string, string, int, bool) {
	if m.ExecCount == 0 || m.ExecSuccessRate == nil {
		return 0, "", "", 0, false
	}
	pct := int(*m.ExecSuccessRate*100 + 0.5)
	clean := int(*m.ExecSuccessRate*float64(m.ExecCount) + 0.5)
	return pct, fmt.Sprintf("%d%% exited 0", pct),
		fmt.Sprintf("%d of %d", clean, m.ExecCount), m.ExecCount, true
}

// metricsRated is how much of what was rated was rated up.
func metricsRated(m storage.ProviderMetrics) (int, string, string, int, bool) {
	if m.RatedCount == 0 || m.RatingRate == nil {
		return 0, "", "", 0, false
	}
	pct := int(*m.RatingRate*100 + 0.5)
	liked := int(*m.RatingRate*float64(m.RatedCount) + 0.5)
	return pct, fmt.Sprintf("%d%% liked", pct),
		fmt.Sprintf("%d of %d", liked, m.RatedCount), m.RatedCount, true
}

// metricsCost prices one model's tokens, and says whether it could. A gateway
// whose catalog returns bare ids has no price for its models at all, and a
// zero there means "not priced" rather than "free".
func metricsCost(prices *pricing.Table, model string, tokensIn, tokensOut *int64) (float64, bool) {
	if prices == nil || tokensIn == nil || tokensOut == nil {
		return 0, false
	}
	in, out, found := prices.Cost(model, *tokensIn, *tokensOut)
	if !found {
		return 0, false
	}
	return in + out, true
}

// metricsSpend is the money column: two decimals where there is a cent to
// show, and `<$0.01` rather than `$0.00` for a spend too small to round to
// one — a zero there would read as free.
func metricsSpend(cost float64, priced bool) string {
	switch {
	case !priced:
		return components.NoDuration
	case cost == 0:
		return "$0.00"
	case cost < 0.01:
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", cost)
}

// metricsTokens is the surface's token column. A count the store never
// recorded is an em dash rather than a zero, which is the one thing the
// surface needs that a report does not; the number itself is the shared one.
func metricsTokens(v *int64) string {
	if v == nil {
		return components.NoDuration
	}
	return tokenCount(*v)
}

// metricsLatency is the surface's TTFT column: the shared reading, with an em
// dash where nothing was timed.
func metricsLatency(ms *float64) string {
	if ms == nil {
		return components.NoDuration
	}
	return latencyText(ms)
}

// metricsModel hosts the surface. It carries no state of its own beyond the
// terminal's size: the screen has one key and nothing to change.
type metricsModel struct {
	width  int
	screen components.MetricsScreen
}

func newMetricsModel(screen components.MetricsScreen) metricsModel {
	return metricsModel{width: defaultMetricsWidth, screen: screen}
}

func (m metricsModel) Init() tea.Cmd { return nil }

func (m metricsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.screen.MaxLines = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		if done, _ := m.screen.Update(msg); done {
			return m, tea.Quit
		}
	}
	return m, nil
}

// View is the frame: the metrics screen, on the alt screen it takes over.
func (m metricsModel) View() tea.View {
	v := tea.NewView(m.screen.View(m.width))
	v.AltScreen = true
	return v
}

func runMetricsScreen(screen components.MetricsScreen) error {
	_, err := newProgram(newMetricsModel(screen)).Run()
	return err
}

// metricsReport is the run as text: one block per provider and model, with
// every figure the store has grouped by the question it answers, and the day
// and action breakdowns under their own headers.
func metricsReport(data metricsData) report.Report {
	names := metricsNames(data.Summary)
	count := 0
	for _, m := range data.Summary {
		count += m.Count
	}
	r := report.Report{
		Title: "shhh metrics",
		Subject: fmt.Sprintf("%s · %s · %s", data.Window,
			countOf(count, "request", "requests"), countOf(len(data.Summary), "model", "models")),
	}
	if len(data.Summary) == 0 {
		return emptyInto(r, "no usage recorded for "+data.Window, "run `shhh cmd <prompt>` to record one")
	}
	for _, m := range data.Summary {
		r.Sections = append(r.Sections, report.Section{
			Header: metricsBlockHeader(m, names),
			Pairs:  metricsFigures(m, data.Prices),
		})
	}
	if rows := metricsDayRows(data.Trend); len(rows) > 0 {
		r.Sections = append(r.Sections, report.Section{Header: "BY DAY", Rows: rows})
	}
	if rows := metricsActionRows(data.Actions); len(rows) > 0 {
		r.Sections = append(r.Sections, report.Section{Header: "BY ACTION", Rows: rows})
	}
	return r
}

// metricsBlockHeader names one pair. Two providers serving the same model id
// is the one case the bare name would be a lie about two blocks being the
// same thing, and metricsNames has already decided that.
func metricsBlockHeader(m storage.ProviderMetrics, names map[string]string) string {
	name := names[metricsKey(m.Provider, m.Model)]
	if name == m.Provider || m.Provider == "" {
		return name
	}
	return m.Provider + " · " + name
}

// metricsFigures is one pair's numbers, grouped by the question each answers:
// how much was asked, how fast it came back, how much of it there was, and
// what it cost. A figure the store has nothing for is left out — a dash in a
// column is a reading the interface invented
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func metricsFigures(m storage.ProviderMetrics, prices *pricing.Table) []report.Pair {
	var pairs []report.Pair
	add := func(key, value string) {
		if value != "" {
			pairs = append(pairs, report.Pair{Key: key, Value: value})
		}
	}
	add("requests", joinDetail(countOf(m.Count, "request", "requests"),
		fmt.Sprintf("%d%% answered", int(m.SuccessRate*100+0.5))))
	if m.ExecCount > 0 {
		add("ran", joinDetail(countOf(m.ExecCount, "run", "runs"), metricsShare(m.ExecSuccessRate, "exited 0")))
	}
	if m.RatedCount > 0 {
		add("rated", joinDetail(countOf(m.RatedCount, "rating", "ratings"), metricsShare(m.RatingRate, "liked")))
	}
	add("first token", metricsPair(m.AvgTTFT, m.P95TTFT))
	add("total", metricsPair(m.AvgDuration, m.P95Duration))
	add("tokens", metricsTokenPair(m.TotalTokensIn, m.TotalTokensOut))
	if cost, priced := metricsCost(prices, m.Model, m.TotalTokensIn, m.TotalTokensOut); priced {
		add("cost", metricsSpend(cost, true))
	}
	return pairs
}

// metricsShare is a rate as a percentage and the word it is a percentage of.
func metricsShare(rate *float64, word string) string {
	if rate == nil {
		return ""
	}
	return fmt.Sprintf("%d%% %s", int(*rate*100+0.5), word)
}

// metricsPair is an average and its p95 together, or just the one that was
// measured.
func metricsPair(avg, p95 *float64) string {
	value := latencyText(avg)
	if tail := latencyText(p95); tail != "" {
		value = joinDetail(value, "p95 "+tail)
	}
	return value
}

// metricsTokenPair is the vitals rail's own usage segment: ↑ in and ↓ out,
// each half present only if it was recorded.
func metricsTokenPair(in, out *int64) string {
	var parts []string
	if in != nil {
		parts = append(parts, "↑"+tokenCount(*in))
	}
	if out != nil {
		parts = append(parts, "↓"+tokenCount(*out))
	}
	return strings.Join(parts, " ")
}

// metricsDayRows is the token total for each day something was asked, oldest
// first — the same query the surface's sparklines are drawn from.
func metricsDayRows(trend []storage.MetricsDayTokens) []report.Row {
	days := metricsDaysOf(trend)
	rows := make([]report.Row, 0, len(days))
	for _, day := range days {
		rows = append(rows, report.Row{State: report.Pass, Name: day.Day,
			Subject: metricsTokenPair(&day.TokensIn, &day.TokensOut)})
	}
	return rows
}

// metricsActionRows is what became of the commands, counted. The categories
// are the surface's own, so the two readings of a run cannot drift apart.
func metricsActionRows(actions []storage.MetricsActionUsage) []report.Row {
	counts := map[string]int{}
	var order []string
	for _, a := range actions {
		category := metricsCategory(a.Action, a.Success)
		if _, seen := counts[category]; !seen {
			order = append(order, category)
		}
		counts[category] += a.Count
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	rows := make([]report.Row, 0, len(order))
	for _, category := range order {
		rows = append(rows, report.Row{State: report.Pass, Name: category,
			Subject: countOf(counts[category], "request", "requests")})
	}
	return rows
}
