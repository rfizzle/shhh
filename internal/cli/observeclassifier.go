package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/storage"
)

// newObserveClassifierCmd sets the classifier's backends side by side from
// the record. It is `classifier` and not `classify`, which already means
// writing a purpose word onto old command rows.
func newObserveClassifierCmd(window *string) *cobra.Command {
	return &cobra.Command{
		Use:   "classifier",
		Short: "Set the auto-mode classifier's backends side by side",
		Long: "For each classifier backend the window's sessions ran on: its verdicts, how many allowed and denied, the median and worst " +
			"time to a verdict, what a verdict cost, and how often you overturned one — a denial you then allowed at the card, " +
			"an allow you then stopped while the call ran.",
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
			backends, err := db.AgentClassifierBackends(since)
			if err != nil {
				return fmt.Errorf("query classifier verdicts: %w", err)
			}
			return report.Fprint(cmd.OutOrStdout(), observeClassifierReport(backends, *window))
		},
	}
}

// observeClassifierReport draws one block per backend. The block is rows
// from observeClassifierRows under the backend's name, the shape the
// dashboard's blocks take: a function from a reading to rows, and a header.
func observeClassifierReport(backends []storage.AgentClassifierBackend, window string) report.Report {
	r := report.Report{Title: "shhh observe classifier", Subject: "last " + window}
	if len(backends) == 0 {
		return emptyInto(r, "no classifier verdict recorded in the last "+window, "shhh chat")
	}
	for _, b := range backends {
		r.Sections = append(r.Sections, report.Section{Header: observeBackendHeader(b.Backend), Rows: observeClassifierRows(b)})
	}
	r.Notes = append(r.Notes, report.Note{State: report.Queue,
		Text: "cost is the classifier origin's spend over the backend's sessions, divided by its verdicts; " +
			"a classifier that failed closed reached no verdict and is not counted"})
	return r
}

// observeBackendHeader names a backend, and a session recorded before the
// backend was stamped as what it is.
func observeBackendHeader(backend string) string {
	if backend == "" {
		return "UNRECORDED BACKEND"
	}
	return strings.ToUpper(backend)
}

func observeClassifierRows(b storage.AgentClassifierBackend) []report.Row {
	rows := []report.Row{{
		State: report.Queue, Name: "verdicts", Subject: countOf(b.Verdicts, "verdict", "verdicts"),
		Outcome: fmt.Sprintf("%d allowed · %d denied", b.Allows, b.Denies)}}
	if b.Timed > 0 {
		rows = append(rows, report.Row{State: report.Queue, Name: "time",
			Subject: "median " + observeSpan(b.MedianMs), Outcome: "worst " + observeSpan(b.WorstMs),
			Detail: countOf(b.Timed, "verdict timed", "verdicts timed")})
	}
	if cost := observeCost(b.Cost / float64(b.Verdicts)); cost != "" {
		rows = append(rows, report.Row{State: report.Queue, Name: "cost", Subject: cost + " per verdict",
			Outcome: observeCost(b.Cost)})
	}
	if n := b.Overturns.Judged; n > 0 {
		rows = append(rows, report.Row{State: report.Queue, Name: "overturned",
			Subject: fmt.Sprintf("%d of %s allowed at the card", b.Overturns.Overturned,
				countOf(n, "judged denial", "judged denials")),
			Outcome: fmt.Sprintf("%.0f%%", float64(b.Overturns.Overturned)/float64(n)*100)})
	}
	if b.Allows > 0 {
		rows = append(rows, report.Row{State: report.Queue, Name: "stopped",
			Subject: fmt.Sprintf("%d of %s stopped while the call ran", b.Stopped, countOf(b.Allows, "allow", "allows")),
			Outcome: fmt.Sprintf("%.0f%%", float64(b.Stopped)/float64(b.Allows)*100)})
	}
	return rows
}
