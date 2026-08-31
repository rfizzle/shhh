package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/browse"
	"github.com/spf13/cobra"
)

func newSnippetsCmd() *cobra.Command {
	var table bool

	cmd := &cobra.Command{
		Use:   "snippets",
		Short: "Manage saved command snippets",
		Long:  "List, run, copy, or delete saved command snippets.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			snippets, err := db.ListSnippets()
			if err != nil {
				return fmt.Errorf("list snippets: %w", err)
			}

			isTTY := term.IsTerminal(os.Stdout.Fd())
			// Nothing to browse is said as text whichever way it was reached;
			// a browser drawn over no rows is a screen the reader has to leave
			// to be told anything.
			if table || !isTTY || len(snippets) == 0 {
				return report.Fprint(cmd.OutOrStdout(), snippetsReport(snippets, time.Now()))
			}
			return runSnippetsBrowser(db, snippets)
		},
	}

	cmd.Flags().BoolVar(&table, "table", false, "show table view instead of interactive browser")

	cmd.AddCommand(newSnippetRunCmd())
	cmd.AddCommand(newSnippetCopyCmd())
	cmd.AddCommand(newSnippetDeleteCmd())
	cmd.AddCommand(newSnippetShowCmd())

	return cmd
}

// snippetsReport is the listing as text: the name and what it is for on the
// row, the command it saves under it. The command is a body line rather than
// a column because it is the thing itself — a command clipped to a column is
// a command nobody can run.
func snippetsReport(snippets []storage.Snippet, now time.Time) report.Report {
	r := report.Report{Title: "shhh snippets", Subject: countOf(len(snippets), "snippet", "snippets")}
	if len(snippets) == 0 {
		return emptyInto(r, "no snippets saved yet",
			"press [s] on an answer, or `shhh snippets --help`")
	}
	rows := make([]report.Row, 0, len(snippets))
	for _, s := range snippets {
		row := report.Row{
			State:   report.Pass,
			Name:    s.Name,
			Subject: s.Description,
			Detail:  historyAgo(s.UpdatedAt, now),
		}
		if command := oneLineText(s.Command); command != "" {
			row.Body = []string{command}
		}
		rows = append(rows, row)
	}
	r.Sections = []report.Section{{Rows: rows}}
	return r
}

func runSnippetsBrowser(db *storage.DB, snippets []storage.Snippet) error {
	items := make([]browse.Item, len(snippets))
	for i, s := range snippets {
		preview := s.Command
		if s.Description != "" {
			preview = s.Description
		}
		items[i] = browse.Item{
			ID:      strconv.FormatInt(s.ID, 10),
			Title:   s.Name,
			Preview: truncate(preview, 60),
			Detail:  fmt.Sprintf("Name:         %s\nDescription:  %s\nCommand:      %s\nSaved:        %s", s.Name, s.Description, s.Command, s.UpdatedAt.Local().Format("2006-01-02 15:04:05")),
		}
	}

	actions := []browse.ActionDef{
		{Label: "Copy", Shortcut: "c"},
		{Label: "Run", Shortcut: "r"},
		{Label: "Delete", Shortcut: "d"},
		{Label: "Rename", Shortcut: "n"},
	}

	model := browse.New(items, actions)
	p := newProgram(model)
	result, err := p.Run()
	if err != nil {
		return err
	}

	m := result.(browse.Model)
	if m.Result == nil {
		return nil
	}

	name := extractField(m.Result.Item.Detail, "Name:")
	command := extractField(m.Result.Item.Detail, "Command:")

	switch m.Result.Action {
	case "Copy":
		res := clipboard.Copy(command)
		if !res.OK {
			_ = report.Fprintln(os.Stderr, report.Row{State: report.Fail,
				Subject: "clipboard", Detail: res.Warning})
		} else {
			_ = report.Fprintln(os.Stdout, report.Done("copied snippet", name))
		}
	case "Run":
		code := runner.Run(command)
		os.Exit(code)
	case "Delete":
		if err := db.DeleteSnippet(name); err != nil {
			return fmt.Errorf("delete snippet: %w", err)
		}
		_ = report.Fprintln(os.Stdout, report.Done("deleted snippet", name))
	case "Rename":
		fmt.Print("New name: ")
		var newName string
		// No answer reads as an empty name, which keeps the old one.
		_, _ = fmt.Scanln(&newName)
		if newName != "" {
			if err := db.RenameSnippet(name, newName); err != nil {
				return fmt.Errorf("rename snippet: %w", err)
			}
			_ = report.Fprintln(os.Stdout, report.Done("renamed snippet", name+" → "+newName))
		}
	}

	return nil
}

func newSnippetRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name>",
		Short: "Run a saved snippet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			s, err := db.GetSnippet(args[0])
			if err != nil {
				return err
			}

			code := runner.Run(s.Command)
			os.Exit(code)
			return nil
		},
	}
}

func newSnippetCopyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "copy <name>",
		Short: "Copy a snippet to clipboard",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			s, err := db.GetSnippet(args[0])
			if err != nil {
				return err
			}

			cr := clipboard.Copy(s.Command)
			if cr.Warning != "" {
				return report.Fprintln(os.Stderr, report.Row{State: report.Fail,
					Subject: "clipboard", Detail: cr.Warning})
			}
			return report.Fprintln(os.Stderr, report.Done("copied snippet", s.Name))
		},
	}
}

func newSnippetDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a saved snippet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			if err := db.DeleteSnippet(args[0]); err != nil {
				return err
			}
			return report.Fprintln(os.Stderr, report.Done("deleted snippet", args[0]))
		},
	}
}

func newSnippetShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a snippet's full command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			s, err := db.GetSnippet(args[0])
			if err != nil {
				return err
			}

			fmt.Println(s.Command)
			return nil
		},
	}
}

// extractField pulls a labelled line back out of a browse detail body. The
// snippet browser is the last surface that needs it: the history browser
// stopped round-tripping its fields through rendered text when it moved onto
// the cockpit's own components.
func extractField(detail, prefix string) string {
	for _, line := range strings.Split(detail, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
