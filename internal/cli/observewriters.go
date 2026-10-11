package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
)

// newObserveWritersCmd lists what each child spent against the budget it was
// admitted with, from the record. It is the figure the default budget is set
// from, gathered from every run rather than one measured run
// (docs/capabilities/subagents.md#limits-are-about-attention-not-resources).
func newObserveWritersCmd(window *string) *cobra.Command {
	return &cobra.Command{
		Use:   "writers",
		Short: "Set each sub-agent's new tokens against its budget",
		Long: "For each sub-agent the window ran: the new tokens it took in (the five fields its end row prints, summed), " +
			"the budget it was admitted with, the share it used and how it ended; then the count, the median and worst " +
			"new tokens, and how many ended by spending the budget.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, err := parseObserveWindow(*window)
			if err != nil {
				return err
			}
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			writers, err := db.AgentWriters(since)
			if err != nil {
				return fmt.Errorf("query writers: %w", err)
			}
			return report.Fprint(cmd.OutOrStdout(), observeWritersReport(writers, *window))
		},
	}
}

// observeWritersReport is the list: the window's summary above one row per
// child.
func observeWritersReport(writers []storage.AgentWriter, window string) report.Report {
	r := report.Report{Title: "shhh observe writers", Subject: "last " + window}
	if len(writers) == 0 {
		return emptyInto(r, "no sub-agent finished in the last "+window, "shhh chat")
	}
	r.Sections = append(r.Sections,
		report.Section{Header: "WRITERS", Rows: observeWritersRows(writers)},
		report.Section{Header: "EACH", Rows: observeWriterRows(writers)})
	r.Notes = append(r.Notes, report.Note{State: report.Queue,
		Text: "new tokens are the prompt the provider did not serve from its cache plus what the child wrote, " +
			"the budget's own measure; stopped is any end that was not done or the budget"})
	return r
}

// observeWritersRows is the dashboard block: the count, the median and worst
// new tokens, and how many ended by the budget.
func observeWritersRows(writers []storage.AgentWriter) []report.Row {
	if len(writers) == 0 {
		return nil
	}
	spent := make([]int64, 0, len(writers))
	ended := 0
	for _, w := range writers {
		spent = append(spent, w.NewTokens)
		if w.Reason == observe.ChildBudget {
			ended++
		}
	}
	slices.Sort(spent)
	n := len(spent)
	median := spent[n/2]
	if n%2 == 0 {
		median = (spent[n/2-1] + spent[n/2]) / 2
	}
	return []report.Row{
		{State: report.Queue, Name: "writers", Subject: countOf(n, "writer", "writers"),
			Outcome: fmt.Sprintf("%d ended by the budget", ended)},
		{State: report.Queue, Name: "new tokens", Subject: "median " + tokenCount(median),
			Outcome: "worst " + tokenCount(spent[n-1])},
	}
}

func observeWriterRows(writers []storage.AgentWriter) []report.Row {
	rows := make([]report.Row, 0, len(writers))
	for _, w := range writers {
		name := w.Name
		if name == "" {
			name = fmt.Sprintf("#%d", w.ID)
		}
		end := observeWriterEnd(w.Reason)
		state := report.Queue
		switch end {
		case "done":
			state = report.Pass
		case "budget":
			state = report.Warn
		}
		rows = append(rows, report.Row{State: state, Name: name,
			Subject: tokenCount(w.NewTokens) + " of " + tokenCount(w.Budget),
			Outcome: fmt.Sprintf("%.0f%% · %s", float64(w.NewTokens)/float64(w.Budget)*100, end)})
	}
	return rows
}

// observeWriterEnd is how a child ended in three words: it answered, it spent
// its budget, or something else ended it (a kill, a cancel, the round cap, the
// provider, a failure).
func observeWriterEnd(reason string) string {
	switch reason {
	case observe.ChildDone:
		return "done"
	case observe.ChildBudget:
		return "budget"
	}
	return "stopped"
}
