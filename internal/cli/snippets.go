package cli

import (
	"fmt"
	"os"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
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

// defaultSnippetsWidth is what the screen is drawn at before the terminal has
// said how wide it is — the working width the artboard is drawn at.
const defaultSnippetsWidth = 130

// snippetsModel hosts the snippet browser
// (docs/interface/surfaces.md#the-supporting-screens). It owns everything the
// screen deliberately does not: what a snippet is, how long ago it was saved,
// and when any of it reaches the store.
//
// The screen resolves a key to a components.SnippetCommand; the host carries
// it out, says so in the notice line, and hands back fresh rows. `[enter]` is
// the exception and closes the screen, because running a command takes the
// terminal the TUI is holding.
type snippetsModel struct {
	db       *storage.DB
	snippets []storage.Snippet
	now      time.Time
	result   components.SnippetResult

	screen components.SnippetScreen
}

func newSnippetsModel(db *storage.DB, snippets []storage.Snippet, now time.Time) *snippetsModel {
	m := &snippetsModel{db: db, snippets: snippets, now: now}
	m.refresh()
	return m
}

// answer carries out the housekeeping a key asked for and keeps what the
// screen closed with, which is read once the terminal has been given back.
func (m *snippetsModel) answer(done bool, result components.SnippetResult) tea.Cmd {
	m.screen.Notice = ""
	if result.Do != nil {
		m.apply(*result.Do)
	}
	if !done {
		return nil
	}
	m.result = result
	return tea.Quit
}

// apply carries out one command against the store and rebuilds the rows, so
// the screen redraws from the store rather than from what it thinks changed.
func (m *snippetsModel) apply(command components.SnippetCommand) {
	s, ok := m.snippet(command.ID)
	if !ok {
		return
	}
	switch command.Act {
	case components.SnippetCopy:
		if res := clipboard.Copy(s.Command); !res.OK {
			m.screen.Notice = "clipboard: " + res.Warning
		} else {
			m.screen.Notice = "copied the command to the clipboard"
		}
	case components.SnippetRename:
		if err := m.db.RenameSnippet(s.Name, command.Name); err != nil {
			m.screen.Notice = "rename: " + err.Error()
			return
		}
		m.screen.Notice = fmt.Sprintf("renamed %q to %q", s.Name, command.Name)
	case components.SnippetDelete:
		if err := m.db.DeleteSnippet(s.Name); err != nil {
			m.screen.Notice = "delete: " + err.Error()
			return
		}
		m.screen.Notice = fmt.Sprintf("deleted %q", s.Name)
	}
	m.reread()
}

// snippet is the store's record behind a row id.
func (m *snippetsModel) snippet(id string) (storage.Snippet, bool) {
	for _, s := range m.snippets {
		if strconv.FormatInt(s.ID, 10) == id {
			return s, true
		}
	}
	return storage.Snippet{}, false
}

// reread asks the store for the listing again after a command changed it. A
// read that fails leaves the rows it already had and says so, because a
// browser that emptied itself on a failed read would look like a store that
// had lost everything.
func (m *snippetsModel) reread() {
	snippets, err := m.db.ListSnippets()
	if err != nil {
		m.screen.Notice = "list: " + err.Error()
		return
	}
	m.snippets = snippets
	if m.screen.Focus >= len(m.snippets) {
		m.screen.Focus = max(len(m.snippets)-1, 0)
	}
	m.refresh()
}

// refresh rebuilds every row and the header subject from the snippets the
// host is holding.
func (m *snippetsModel) refresh() {
	rows := make([]components.SnippetRow, 0, len(m.snippets))
	for _, s := range m.snippets {
		rows = append(rows, components.SnippetRow{
			ID:          strconv.FormatInt(s.ID, 10),
			Name:        s.Name,
			Description: s.Description,
			Command:     s.Command,
			Saved:       historyAgo(s.UpdatedAt, m.now),
		})
	}
	m.screen.Rows = rows
	m.screen.Subject = countOf(len(m.snippets), "snippet", "snippets")
}

func runSnippetsBrowser(db *storage.DB, snippets []storage.Snippet) error {
	m := newSnippetsModel(db, snippets, time.Now())
	if _, err := newProgram(newScreenModel(&m.screen, defaultSnippetsWidth, m.answer)).Run(); err != nil {
		return err
	}
	// Nothing is run until [enter], which is what the screen's key row
	// promised. The program has given the terminal back by now, so the command
	// runs in the shell's own stdio.
	if m.result.Run && m.result.Command != "" {
		os.Exit(runner.Run(m.result.Command))
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
