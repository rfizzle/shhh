package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			isTTY := term.IsTerminal(os.Stdout.Fd())
			interactive := isTTY && !table

			// The browser filters in the screen rather than in SQL, so its
			// query row can say `6 of 41 match` honestly. --search
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

			// An empty store has nothing to browse, so it says so as text
			// whichever way it was reached: a browser drawn over no rows is a
			// screen the reader has to leave to be told anything.
			if !interactive || len(entries) == 0 {
				return report.Fprint(cmd.OutOrStdout(), historyReport(entries, search, time.Now()))
			}
			return runHistoryBrowser(db, entries, search)
		},
	}

	cmd.Flags().StringVarP(&search, "search", "s", "", "filter by prompt or command text")
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "max entries to show")
	cmd.Flags().BoolVar(&table, "table", false, "show table view instead of interactive browser")

	cmd.AddCommand(newHistoryClearCmd())
	cmd.AddCommand(newHistoryShowCmd())

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
				fmt.Fprint(cmd.OutOrStdout(), "Delete all history entries? [y/N] ")
				var confirm string
				// No answer — a closed stdin — reads as an empty line, and an
				// empty line is No.
				_, _ = fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					return report.Fprintln(cmd.OutOrStdout(),
						report.Row{State: report.Skip, Subject: "cancelled", Detail: "nothing was deleted"})
				}
			}

			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			n, err := db.ClearAllHistory()
			if err != nil {
				return err
			}
			return report.Fprintln(cmd.OutOrStdout(),
				report.Done("deleted", countOf(int(n), "history entry", "history entries")))
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompt")

	return cmd
}

// newHistoryShowCmd is one entry in full — the half of a row the listing drops
// so its prompts line up. Which model answered is here rather than in the
// listing because it is the field a reader goes looking for after something
// went wrong, not one they scan.
func newHistoryShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one history entry in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			entries, err := db.ListHistory(storage.HistoryFilter{})
			if err != nil {
				return fmt.Errorf("query history: %w", err)
			}
			for _, e := range entries {
				if strconv.FormatInt(e.ID, 10) != args[0] {
					continue
				}
				return report.Fprint(cmd.OutOrStdout(), historyEntryReport(e, time.Now()))
			}
			return fmt.Errorf("no history entry %s; `shhh history --table` lists them", args[0])
		},
	}
}

// historyReport is the listing as text: one row per entry, the prompt as the
// target and the command it produced under it.
func historyReport(entries []storage.HistoryEntry, search string, now time.Time) report.Report {
	subject := countOf(len(entries), "command", "commands")
	if search != "" {
		subject = fmt.Sprintf("%d matching %q", len(entries), search)
	}
	r := report.Report{Title: "shhh history", Subject: subject}
	if len(entries) == 0 {
		if search != "" {
			r.Subject = fmt.Sprintf("nothing matching %q", search)
			return emptyInto(r, "no history matching "+strconv.Quote(search), "shhh history")
		}
		return emptyInto(r, "no history yet", "run `shhh <prompt>` to record one")
	}
	rows := make([]report.Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, historyReportRow(e, now))
	}
	r.Sections = []report.Section{{Rows: rows}}
	return r
}

// historyReportRow is one entry on the grid. The glyph and the outcome are
// the same reading the browser makes — an exit code outranks what was done
// with the command — and the action is the row's name, because `run` and
// `copy` are a closed vocabulary and a name column is what one of those is
// for.
func historyReportRow(e storage.HistoryEntry, now time.Time) report.Row {
	state, outcome := historyOutcome(e)
	return commandReportRow(storedCommand{
		Action: e.Action, At: e.CreatedAt, Prompt: e.Prompt, Command: e.Command,
	}, state, outcome, now)
}

// storedCommand is the part of a recorded request that a listing draws,
// separated from the two row types that hold it so one function can list
// both. It is a struct rather than an interface because the two shapes have
// these fields already and neither should grow methods to be listable.
type storedCommand struct {
	Action  string
	At      time.Time
	Prompt  string
	Command string
}

// commandReportRow is one stored command on the report grid: what was done
// with it as the name, how long ago as the subject, what was asked as the
// detail, what became of it as the outcome, and the command itself on the
// line beneath. `shhh history` and `shhh rate` both list stored commands, and
// a row that read one way in the browser's listing and another in the rating
// table would be two grids over one table.
func commandReportRow(c storedCommand, state components.ActivityState, outcome string, now time.Time) report.Row {
	row := report.Row{
		State:   historyState(state),
		Name:    actionName(c.Action),
		Subject: historyAgo(c.At, now),
		Detail:  oneLineText(c.Prompt),
		Outcome: outcome,
	}
	if command := oneLineText(c.Command); command != "" {
		row.Body = []string{command}
	}
	return row
}

// historyEntryReport is `shhh history show`: everything the listing has plus
// the fields it drops.
func historyEntryReport(e storage.HistoryEntry, now time.Time) report.Report {
	_, outcome := historyOutcome(e)
	pairs := []report.Pair{
		{Key: "when", Value: historyAgo(e.CreatedAt, now)},
		{Key: "prompt", Value: oneLineText(e.Prompt)},
		{Key: "command", Value: oneLineText(e.Command)},
		{Key: "outcome", Value: outcome},
	}
	if model := historyModelName(e); model != "" {
		pairs = append(pairs, report.Pair{Key: "model", Value: model})
	}
	if d := historyDuration(e.Duration); d != "" {
		pairs = append(pairs, report.Pair{Key: "took", Value: d})
	}
	if counts := historyTokens(e); counts != "" {
		pairs = append(pairs, report.Pair{Key: "tokens", Value: counts})
	}
	return report.Report{
		Title:    "shhh history " + strconv.FormatInt(e.ID, 10),
		Subject:  actionName(e.Action),
		Sections: []report.Section{{Pairs: pairs}},
	}
}

// actionName is what was done with the command, in one word. An entry
// recorded before the column existed says nothing rather than guessing.
func actionName(action string) string {
	switch action {
	case "run-all", "run-step":
		return "run"
	case "":
		return "—"
	}
	return action
}

// historyState reads the browser's verdict on an entry as the report's. They
// are the same readings under two names: the grid and the report each own the
// vocabulary they draw with, and this is the one seam between them.
func historyState(s components.ActivityState) report.State {
	switch s {
	case components.ActivityFailed:
		return report.Fail
	case components.ActivityDenied:
		return report.Skip
	case components.ActivityQueued:
		return report.Queue
	}
	return report.Pass
}

// historyModel hosts the history browser (
// docs/interface/surfaces.md#the-supporting-screens). It owns everything the
// screen deliberately does not: what an entry means, how long ago it was,
// what its action and exit code add up to, and when any of it reaches the
// store.
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
// said how wide it is — the working width the artboard is drawn at.
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
	case tea.KeyPressMsg:
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

// View is the frame: the history screen, on the alt screen it takes over.
func (m historyModel) View() tea.View {
	v := tea.NewView(m.screen.View(m.width))
	v.AltScreen = true
	return v
}

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

// historyOutcome is the outcome field and the glyph that goes with it. An
// exit code is the strongest thing an entry can say, so it outranks the
// action: a command that was run and failed says so however it was reached. A
// request that never produced one says what was done with it instead, and a
// request that broke before it answered says that — never a blank.
func historyOutcome(e storage.HistoryEntry) (components.ActivityState, string) {
	if e.ExitCode == nil && !e.Success {
		return components.ActivityFailed, "no answer"
	}
	return commandOutcome(e.ExitCode, e.Action)
}

// commandOutcome is that reading for a stored command that is known to have
// answered: the exit code where there is one, and what was done with the
// command where there is not. The rating screen shares it, because a command
// that ended `exit 3` in the browser and something else under the rating
// question would be two readings of one row.
func commandOutcome(exit *int64, action string) (components.ActivityState, string) {
	if exit != nil {
		if *exit == 0 {
			return components.ActivityDone, components.OutcomeExit(0)
		}
		return components.ActivityFailed, components.OutcomeExit(int(*exit))
	}
	switch action {
	case "copy":
		return components.ActivityDone, "copied"
	case "save":
		return components.ActivityDone, "saved"
	case "edit", "revise":
		return components.ActivityDone, action + "ed"
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
// a second it is blank, the same rule every activity row follows.
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
	p := newProgram(model)
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
