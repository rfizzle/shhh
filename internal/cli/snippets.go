package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

func newSnippetsCmd() *cobra.Command {
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

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCOMMAND\tSAVED")
			for _, s := range snippets {
				fmt.Fprintf(w, "%s\t%s\t%s\n",
					s.Name,
					truncate(s.Command, 60),
					s.UpdatedAt.Local().Format("Jan 02 15:04"),
				)
			}
			return w.Flush()
		},
	}

	cmd.AddCommand(newSnippetRunCmd())
	cmd.AddCommand(newSnippetCopyCmd())
	cmd.AddCommand(newSnippetDeleteCmd())
	cmd.AddCommand(newSnippetShowCmd())

	return cmd
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
