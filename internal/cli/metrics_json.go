package cli

// `shhh metrics --json` as data. The report groups the figures by the question
// they answer and leaves out what was never measured; a script wants the
// fields the store has, named as the store names them, so this is its own
// shape rather than the report's.

import "github.com/rfizzle/shhh/internal/storage"

type metricsDoc struct {
	Window   string             `json:"window"`
	Requests int                `json:"requests"`
	Models   []metricsModelDoc  `json:"models"`
	ByDay    []metricsDayDoc    `json:"by_day,omitempty"`
	ByAction []metricsActionDoc `json:"by_action,omitempty"`
}

// metricsModelDoc carries a measurement only where one was taken: a null and
// an absent field both say "not measured", and a zero would say "measured as
// none", which is a different claim.
type metricsModelDoc struct {
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	Requests    int      `json:"requests"`
	SuccessRate float64  `json:"success_rate"`
	Runs        int      `json:"runs,omitempty"`
	RunRate     *float64 `json:"run_success_rate,omitempty"`
	Ratings     int      `json:"ratings,omitempty"`
	RatingRate  *float64 `json:"rating_rate,omitempty"`
	AvgTTFTMs   *float64 `json:"avg_ttft_ms,omitempty"`
	P95TTFTMs   *float64 `json:"p95_ttft_ms,omitempty"`
	AvgTotalMs  *float64 `json:"avg_total_ms,omitempty"`
	P95TotalMs  *float64 `json:"p95_total_ms,omitempty"`
	TokensIn    *int64   `json:"tokens_in,omitempty"`
	TokensOut   *int64   `json:"tokens_out,omitempty"`
	Cost        *float64 `json:"est_cost,omitempty"`
}

type metricsDayDoc struct {
	Day       string `json:"day"`
	TokensIn  int64  `json:"tokens_in"`
	TokensOut int64  `json:"tokens_out"`
}

type metricsActionDoc struct {
	Action   string `json:"action"`
	Requests int    `json:"requests"`
}

func metricsJSON(data metricsData) metricsDoc {
	doc := metricsDoc{Window: data.Window, Models: []metricsModelDoc{}}
	for _, m := range data.Summary {
		doc.Requests += m.Count
		entry := metricsModelDoc{
			Provider: m.Provider, Model: m.Model, Requests: m.Count, SuccessRate: m.SuccessRate,
			Runs: m.ExecCount, RunRate: m.ExecSuccessRate, Ratings: m.RatedCount,
			RatingRate: m.RatingRate, AvgTTFTMs: m.AvgTTFT, P95TTFTMs: m.P95TTFT,
			AvgTotalMs: m.AvgDuration, P95TotalMs: m.P95Duration,
			TokensIn: m.TotalTokensIn, TokensOut: m.TotalTokensOut,
		}
		if cost, priced := metricsCost(data.Prices, m.Model, m.TotalTokensIn, m.TotalTokensOut); priced {
			entry.Cost = &cost
		}
		doc.Models = append(doc.Models, entry)
	}
	doc.ByDay = append(doc.ByDay, metricsDaysOf(data.Trend)...)
	counts := map[string]int{}
	var order []string
	for _, a := range data.Actions {
		category := metricsCategory(a.Action, a.Success)
		if _, seen := counts[category]; !seen {
			order = append(order, category)
		}
		counts[category] += a.Count
	}
	for _, category := range order {
		doc.ByAction = append(doc.ByAction, metricsActionDoc{Action: category, Requests: counts[category]})
	}
	return doc
}

// metricsDaysOf folds the per-model day totals into one row per day, which is
// what both the report's BY DAY section and this document count.
func metricsDaysOf(trend []storage.MetricsDayTokens) []metricsDayDoc {
	at := map[string]int{}
	var days []metricsDayDoc
	for _, row := range trend {
		i, seen := at[row.Day]
		if !seen {
			i = len(days)
			at[row.Day] = i
			days = append(days, metricsDayDoc{Day: row.Day})
		}
		days[i].TokensIn += row.TokensIn
		days[i].TokensOut += row.TokensOut
	}
	return days
}
