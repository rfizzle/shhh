package cli

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
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
			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			snippets, err := db.ListSnippets()
			if err != nil {
				return fmt.Errorf("list snippets: %w", err)
			}

			if len(snippets) == 0 {
				fmt.Println("No saved snippets. Use \"Save\" from the action bar after generating a command.")
				return nil
			}

			isTTY := term.IsTerminal(os.Stdout.Fd())
			if table || !isTTY {
				return printSnippetsTable(snippets)
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

func printSnippetsTable(snippets []storage.Snippet) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tCOMMAND\tSAVED")
	for _, s := range snippets {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			s.Name,
			truncate(s.Description, 30),
			truncate(s.Command, 50),
			s.UpdatedAt.Local().Format("Jan 02 15:04"),
		)
	}
	return w.Flush()
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
	p := tea.NewProgram(model, tea.WithAltScreen())
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
			fmt.Fprintf(os.Stderr, "clipboard: %s\n", res.Warning)
		} else {
			fmt.Println("Copied to clipboard.")
		}
	case "Run":
		code := runner.Run(command)
		os.Exit(code)
	case "Delete":
		if err := db.DeleteSnippet(name); err != nil {
			return fmt.Errorf("delete snippet: %w", err)
		}
		fmt.Printf("Deleted snippet %q.\n", name)
	case "Rename":
		fmt.Print("New name: ")
		var newName string
		fmt.Scanln(&newName)
		if newName != "" {
			if err := db.RenameSnippet(name, newName); err != nil {
				return fmt.Errorf("rename snippet: %w", err)
			}
			fmt.Printf("Renamed %q → %q.\n", name, newName)
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
			db, err := storage.Open()
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
			db, err := storage.Open()
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
				fmt.Fprintln(os.Stderr, cr.Warning)
			} else {
				fmt.Fprintln(os.Stderr, "Copied to clipboard.")
			}
			return nil
		},
	}
}

func newSnippetDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a saved snippet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			if err := db.DeleteSnippet(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Deleted snippet %q.\n", args[0])
			return nil
		},
	}
}

func newSnippetShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a snippet's full command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open()
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
