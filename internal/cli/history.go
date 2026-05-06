package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/browse"
	"github.com/spf13/cobra"
)

func newHistoryCmd() *cobra.Command {
	var search string
	var limit int
	var table bool

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

			isTTY := term.IsTerminal(os.Stdout.Fd())
			if table || !isTTY {
				return printHistoryTable(entries)
			}

			return runHistoryBrowser(db, entries)
		},
	}

	cmd.Flags().StringVarP(&search, "search", "s", "", "filter by prompt or command text")
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "max entries to show")
	cmd.Flags().BoolVar(&table, "table", false, "show table view instead of interactive browser")

	return cmd
}

func printHistoryTable(entries []storage.HistoryEntry) error {
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
}

func runHistoryBrowser(db *storage.DB, entries []storage.HistoryEntry) error {
	items := make([]browse.Item, len(entries))
	for i, e := range entries {
		items[i] = browse.Item{
			ID:      strconv.FormatInt(e.ID, 10),
			Title:   e.CreatedAt.Local().Format("Jan 02 15:04") + "  " + truncate(e.Prompt, 50),
			Preview: truncate(e.Command, 60),
			Detail: fmt.Sprintf("Prompt:    %s\nCommand:   %s\nProvider:  %s/%s\nAction:    %s\nTime:      %s",
				e.Prompt, e.Command, e.Provider, e.Model, e.Action,
				e.CreatedAt.Local().Format("2006-01-02 15:04:05")),
		}
	}

	actions := []browse.ActionDef{
		{Label: "Copy", Shortcut: "c"},
		{Label: "Delete", Shortcut: "d"},
		{Label: "Save as snippet", Shortcut: "s"},
	}

	model := browse.New(items, actions)
	p := tea.NewProgram(model, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return err
	}

	m := result.(browse.Model)
	if m.Result == nil {
		return nil
	}

	id, _ := strconv.ParseInt(m.Result.Item.ID, 10, 64)

	switch m.Result.Action {
	case "Copy":
		cmd := extractCommand(m.Result.Item.Detail)
		res := clipboard.Copy(cmd)
		if !res.OK {
			fmt.Fprintf(os.Stderr, "clipboard: %s\n", res.Warning)
		} else {
			fmt.Println("Copied to clipboard.")
		}
	case "Delete":
		if err := db.DeleteHistoryEntry(id); err != nil {
			return fmt.Errorf("delete entry: %w", err)
		}
		fmt.Println("Entry deleted.")
	case "Save as snippet":
		cmd := extractCommand(m.Result.Item.Detail)
		prompt := extractField(m.Result.Item.Detail, "Prompt:")
		name := truncate(prompt, 30)
		if err := db.SaveSnippet(name, cmd); err != nil {
			return fmt.Errorf("save snippet: %w", err)
		}
		fmt.Printf("Saved as snippet %q.\n", name)
	}

	return nil
}

func extractCommand(detail string) string {
	return extractField(detail, "Command:")
}

func extractField(detail, prefix string) string {
	for _, line := range strings.Split(detail, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
