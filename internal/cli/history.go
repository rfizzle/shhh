package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
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

			isTTY := term.IsTerminal(os.Stdout.Fd())
			interactive := isTTY && !table

			// The browser filters in the screen rather than in SQL, so its
			// query row can say `6 of 41 match` honestly (§19b). --search
			// seeds that filter; the table has no filter row, so it keeps
			// asking the store.
			filter := storage.HistoryFilter{Limit: limit}
			if !interactive {
				filter.Search = search
			}
			entries, err := db.ListHistory(filter)
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

			if !interactive {
				return printHistoryTable(entries)
			}

			return runHistoryBrowser(db, entries, search)
		},
	}

	cmd.Flags().StringVarP(&search, "search", "s", "", "filter by prompt or command text")
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "max entries to show")
	cmd.Flags().BoolVar(&table, "table", false, "show table view instead of interactive browser")

	cmd.AddCommand(newHistoryClearCmd())

	return cmd
}

func newHistoryClearCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete all history entries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				fmt.Print("Delete all history entries? [y/N] ")
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			n, err := db.ClearAllHistory()
			if err != nil {
				return err
			}
			fmt.Printf("Deleted %d entries.\n", n)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompt")

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

// historyModel hosts the history browser (S-128, DESIGN-TUI.md §19b). It owns
// everything the screen deliberately does not: what an entry means, how long
// ago it was, what its action and exit code add up to, and when any of it
// reaches the store.
//
// The screen resolves a key to a components.HistoryCommand; the host carries
// it out, says so in the notice line, and hands back fresh rows. `[enter]` is
// the exception and closes the screen, because running a command takes the
// terminal the TUI is holding.
type historyModel struct {
	db      *storage.DB
	entries []storage.HistoryEntry
	now     time.Time
	width   int
	err     error
	result  components.HistoryResult

	screen components.HistoryScreen
}

// defaultHistoryWidth is what the screen is drawn at before the terminal has
// said how wide it is — the working width the artboard is drawn at (§19b).
const defaultHistoryWidth = 130

func newHistoryModel(db *storage.DB, entries []storage.HistoryEntry, query string, now time.Time) historyModel {
	m := historyModel{db: db, entries: entries, now: now, width: defaultHistoryWidth}
	if query != "" {
		m.screen.SetQuery(query)
	}
	m.refresh()
	return m
}

func (m historyModel) Init() tea.Cmd { return nil }

func (m historyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.screen.MaxLines = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		m.screen.Notice = ""
		done, result := m.screen.Update(msg)
		if command, ok := result.(components.HistoryCommand); ok {
			m.apply(command)
		}
		if !done {
			return m, nil
		}
		if r, ok := result.(components.HistoryResult); ok {
			m.result = r
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m historyModel) View() string { return m.screen.View(m.width) }

// apply carries out one command against the store and rebuilds the rows, so
// the screen redraws from the store rather than from what it thinks changed.
func (m *historyModel) apply(command components.HistoryCommand) {
	entry, ok := m.entry(command.ID)
	if !ok {
		return
	}
	switch command.Act {
	case components.HistoryCopy:
		if res := clipboard.Copy(entry.Command); !res.OK {
			m.screen.Notice = "clipboard: " + res.Warning
		} else {
			m.screen.Notice = "copied the command to the clipboard"
		}
	case components.HistorySave:
		name := truncate(oneLineText(entry.Prompt), 30)
		if err := m.db.SaveSnippet(name, entry.Command); err != nil {
			m.screen.Notice = "save: " + err.Error()
		} else {
			m.screen.Notice = fmt.Sprintf("saved as snippet %q", name)
		}
	case components.HistoryDelete:
		if err := m.db.DeleteHistoryEntry(entry.ID); err != nil {
			m.screen.Notice = "delete: " + err.Error()
			return
		}
		m.drop(entry.ID)
		m.screen.Notice = "deleted the entry"
	}
	m.refresh()
}

// entry is the store's record behind a row id.
func (m *historyModel) entry(id string) (storage.HistoryEntry, bool) {
	for _, e := range m.entries {
		if strconv.FormatInt(e.ID, 10) == id {
			return e, true
		}
	}
	return storage.HistoryEntry{}, false
}

// drop removes a deleted entry and pulls the pointer back onto a row that
// still exists — deleting the last entry should not leave the pointer past
// the end of the list.
func (m *historyModel) drop(id int64) {
	kept := m.entries[:0]
	for _, e := range m.entries {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	m.entries = kept
	if m.screen.Focus >= len(m.entries) {
		m.screen.Focus = max(len(m.entries)-1, 0)
	}
}

// refresh rebuilds every row and the header subject from the entries the host
// is holding.
func (m *historyModel) refresh() {
	rows := make([]components.HistoryRow, 0, len(m.entries))
	ran := 0
	for _, e := range m.entries {
		if e.ExitCode != nil {
			ran++
		}
		rows = append(rows, historyRow(e, m.now))
	}
	m.screen.Rows = rows
	m.screen.Subject = fmt.Sprintf("%s · %d run", countOf(len(m.entries), "entry", "entries"), ran)
}

// historyRow reads one stored request into the row the screen draws. Every
// reading of the store happens here: what the glyph says, what the outcome
// field says, and how long ago "ago" is.
func historyRow(e storage.HistoryEntry, now time.Time) components.HistoryRow {
	state, outcome := historyOutcome(e)
	return components.HistoryRow{
		ID:       strconv.FormatInt(e.ID, 10),
		Prompt:   e.Prompt,
		Command:  e.Command,
		When:     historyAgo(e.CreatedAt, now),
		Model:    historyModelName(e),
		Action:   e.Action,
		Outcome:  outcome,
		State:    state,
		Duration: historyDuration(e.Duration),
		Counts:   historyTokens(e),
	}
}

// historyOutcome is the §6d field and the glyph that goes with it. An exit
// code is the strongest thing an entry can say, so it outranks the action:
// a command that was run and failed says so however it was reached. A request
// that never produced one says what was done with it instead, and a request
// that broke before it answered says that — never a blank.
func historyOutcome(e storage.HistoryEntry) (components.ActivityState, string) {
	if e.ExitCode != nil {
		if *e.ExitCode == 0 {
			return components.ActivityDone, components.OutcomeExit(0)
		}
		return components.ActivityFailed, components.OutcomeExit(int(*e.ExitCode))
	}
	if !e.Success {
		return components.ActivityFailed, "no answer"
	}
	switch e.Action {
	case "copy":
		return components.ActivityDone, "copied"
	case "save":
		return components.ActivityDone, "saved"
	case "edit", "revise":
		return components.ActivityDone, e.Action + "ed"
	case "cancel":
		return components.ActivityDenied, "dismissed"
	case "run", "run-all", "run-step":
		// It was run, and the exit code was never recorded — an older entry,
		// or a run that took the terminal and never came back. Saying "exit
		// 0" here would be inventing the one fact the reader came for.
		return components.ActivityDone, "run · exit not recorded"
	}
	return components.ActivityQueued, "not run"
}

// historyModelName is `openai/gpt-5.2`, or whichever half of it was recorded.
func historyModelName(e storage.HistoryEntry) string {
	switch {
	case e.Provider != "" && e.Model != "":
		return e.Provider + "/" + e.Model
	case e.Model != "":
		return e.Model
	}
	return e.Provider
}

// historyDuration is the 6-column field: how long the model took. Under half
// a second it is blank, the same rule every activity row follows (§6a).
func historyDuration(d *time.Duration) string {
	if d == nil || *d < 500*time.Millisecond {
		return ""
	}
	if *d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// historyTokens is the preview's token line, or "" for an entry recorded
// before the columns existed.
func historyTokens(e storage.HistoryEntry) string {
	if e.TokensIn == nil && e.TokensOut == nil {
		return ""
	}
	var parts []string
	if e.TokensIn != nil {
		parts = append(parts, fmt.Sprintf("↑ %d", *e.TokensIn))
	}
	if e.TokensOut != nil {
		parts = append(parts, fmt.Sprintf("↓ %d", *e.TokensOut))
	}
	return strings.Join(parts, " · ") + " tokens"
}

// historyAgo is how long ago in the row's own words. Past a week it stops
// counting and states the date, because "23 days ago" is not how anyone looks
// for a command they ran last month.
func historyAgo(at, now time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	d := now.Sub(at)
	switch {
	case d < 0:
		return at.Local().Format("Jan 02 15:04")
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return strings.ToLower(at.Local().Format("Mon"))
	}
	return at.Local().Format("Jan 02")
}

// countOf pluralises a count whose plural is not the singular plus an s.
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// oneLineText flattens a prompt typed over several lines onto one.
func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

func runHistoryBrowser(db *storage.DB, entries []storage.HistoryEntry, query string) error {
	model := newHistoryModel(db, entries, query, time.Now())
	p := tea.NewProgram(model, tea.WithAltScreen())
	out, err := p.Run()
	if err != nil {
		return err
	}

	m := out.(historyModel)
	if m.err != nil {
		return m.err
	}
	// Nothing is re-run until [enter], which is what the screen's hint line
	// promised. The program has given the terminal back by now, so the
	// command runs in the shell's own stdio.
	if m.result.Run && m.result.Command != "" {
		os.Exit(runner.Run(m.result.Command))
	}
	return nil
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
