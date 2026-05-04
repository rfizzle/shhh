package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

func newHistoryCmd() *cobra.Command {
	var search string
	var limit int

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Browse past prompts and generated commands",
		Long:  "Show recent prompt/command history with provider, model, and action taken.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			entries, err := db.ListHistory(storage.HistoryFilter{
				Search: search,
				Limit:  limit,
			})
			if err != nil {
				return fmt.Errorf("query history: %w", err)
			}

			if len(entries) == 0 {
				if search != "" {
					fmt.Printf("No history matching %q.\n", search)
				} else {
					fmt.Println("No history yet. Generate some commands first!")
				}
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "TIME\tPROMPT\tCOMMAND\tPROVIDER\tACTION")
			for _, e := range entries {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					e.CreatedAt.Local().Format("Jan 02 15:04"),
					truncate(e.Prompt, 40),
					truncate(e.Command, 40),
					e.Provider+"/"+e.Model,
					e.Action,
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVarP(&search, "search", "s", "", "filter by prompt or command text")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "max entries to show")

	return cmd
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
