package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

func newMetricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "Show provider usage metrics",
		Long:  "Display summary statistics for each provider and model you've used.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			summary, err := db.MetricsSummary()
			if err != nil {
				return fmt.Errorf("query metrics: %w", err)
			}

			if len(summary) == 0 {
				fmt.Println("No usage data yet. Generate some commands first!")
				return nil
			}

			prices, _ := pricing.Load()

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tMODEL\tCOUNT\tSUCCESS\tEXEC\tEXEC OK\tRATED\tRATED OK\tAVG TTFT\tP95 TTFT\tAVG TOTAL\tP95 TOTAL\tTOKENS IN\tTOKENS OUT\tEST. COST")
			for _, m := range summary {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					m.Provider,
					m.Model,
					m.Count,
					fmtPct(m.SuccessRate),
					m.ExecCount,
					fmtPctPtr(m.ExecSuccessRate),
					m.RatedCount,
					fmtPctPtr(m.RatingRate),
					fmtMs(m.AvgTTFT),
					fmtMs(m.P95TTFT),
					fmtMs(m.AvgDuration),
					fmtMs(m.P95Duration),
					fmtTokens(m.TotalTokensIn),
					fmtTokens(m.TotalTokensOut),
					fmtCost(prices, m.Model, m.TotalTokensIn, m.TotalTokensOut),
				)
			}
			return w.Flush()
		},
	}
}

func fmtMs(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.0fms", *v)
}

func fmtPct(v float64) string {
	return fmt.Sprintf("%.0f%%", v*100)
}

func fmtPctPtr(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", *v*100)
}

func fmtTokens(v *int64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *v)
}

func fmtCost(prices *pricing.Table, model string, tokensIn, tokensOut *int64) string {
	if prices == nil || tokensIn == nil || tokensOut == nil {
		return "-"
	}
	inCost, outCost, found := prices.Cost(model, *tokensIn, *tokensOut)
	if !found {
		return "-"
	}
	total := inCost + outCost
	if total < 0.01 {
		return fmt.Sprintf("$%.4f", total)
	}
	return fmt.Sprintf("$%.2f", total)
}
