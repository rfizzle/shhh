package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/spf13/cobra"
)

func newRateCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "rate",
		Short: "Rate past commands",
		Long:  "Walk through recent unrated commands and mark whether they actually worked, so accuracy metrics reflect real outcomes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			entries, err := db.ListUnrated(limit)
			if err != nil {
				return fmt.Errorf("query unrated commands: %w", err)
			}
			if len(entries) == 0 {
				return report.Fprint(cmd.OutOrStdout(), emptyInto(
					report.Report{Title: "shhh rate"},
					"nothing to rate", "every recent command is rated already"))
			}

			out := cmd.OutOrStdout()
			_ = report.Fprint(out, report.Report{
				Title:   "shhh rate",
				Subject: countOf(len(entries), "unrated command", "unrated commands"),
				Sections: []report.Section{{Rows: []report.Row{{State: report.Run,
					Subject: "did each one do what you wanted?",
					Detail:  "[y] worked · [n] didn't · [s] skip · [q] quit"}}}},
			})
			fmt.Fprintln(out)

			reader := bufio.NewReader(os.Stdin)
			rated := 0
			for i, e := range entries {
				pairs := []report.Pair{
					{Key: "prompt", Value: oneLineText(e.Prompt)},
					{Key: "command", Value: oneLineText(e.Command)},
				}
				if e.ExitCode != nil {
					pairs = append(pairs, report.Pair{Key: "exit", Value: strconv.FormatInt(*e.ExitCode, 10)})
				}
				_ = report.Fprint(out, report.Report{
					Title:    fmt.Sprintf("%d/%d · %s", i+1, len(entries), historyAgo(e.CreatedAt, time.Now())),
					Sections: []report.Section{{Pairs: pairs}},
				})
				fmt.Fprint(out, "  worked? [y/n/s/q] ")

				input, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				switch strings.ToLower(strings.TrimSpace(input)) {
				case "y", "yes":
					if err := db.RateRequest(e.ID, true); err != nil {
						return err
					}
					rated++
				case "n", "no":
					if err := db.RateRequest(e.ID, false); err != nil {
						return err
					}
					rated++
				case "q", "quit":
					return report.Fprintln(out, report.Done("rated", countOf(rated, "command", "commands")))
				default:
					// skip
				}
				fmt.Fprintln(out)
			}
			return report.Fprintln(out, report.Done("rated", countOf(rated, "command", "commands")))
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "max commands to review")
	return cmd
}
