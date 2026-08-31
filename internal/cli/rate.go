package cli

// `shhh rate` — the question the accuracy figures are built out of.
//
// Rating asks whether a command the model wrote actually did what was wanted,
// and nothing else in the product knows the answer: an exit code says the
// shell was happy, not that the reader was. So the metrics screen's accuracy
// row is only ever as honest as this command, and this command is only ever
// answered if answering it is quick
// (docs/capabilities/sessions-and-memory.md#rating-is-what-the-accuracy-figures-are-made-of).
//
// Which is why the terminal path is a screen rather than a prompt loop: one
// card, three keys, no enter between them, and esc as the way out. Everywhere
// else — a pipe, a script, `--table` — it is the same report every other
// listing prints, because a command that draws a card at a file descriptor
// is a command nobody can script.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
	"github.com/spf13/cobra"
)

func newRateCmd() *cobra.Command {
	var limit int
	var table bool

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
			out, now := cmd.OutOrStdout(), time.Now()
			// Nothing to rate and `--table` land on the same listing: a walk
			// through no questions is not a screen the reader has to leave to
			// be told there were none.
			if len(entries) == 0 || table {
				return report.Fprint(out, rateReport(entries, now))
			}
			if rateInteractive() {
				return runRateScreen(db, out, entries, now)
			}
			return rateByLine(db, cmd.InOrStdin(), out, entries, now)
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "max commands to review")
	cmd.Flags().BoolVar(&table, "table", false, "list what is unrated without asking about any of it")
	return cmd
}

// rateInteractive is whether the screen can be drawn and answered. Both ends
// have to be a terminal: the card is drawn on stdout and the keys come from
// stdin, and a program holding only one of them is a program that either
// paints a card nobody sees or waits for a key nobody can press.
func rateInteractive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// rateStore is the half of the store the rating host writes through. It is an
// interface so the host can be driven through a key sequence in a test
// without a database behind it — the thing worth testing here is which
// keystroke writes what, not SQL.
type rateStore interface {
	RateRequest(id int64, up bool) error
}

// rateModel hosts the rating screen
// (docs/interface/surfaces.md#the-supporting-screens). It owns everything the
// screen deliberately does not: what an entry means, how long ago it was,
// when an answer reaches the store, and what the tally at the end says.
type rateModel struct {
	db      rateStore
	entries []storage.UnratedRequest
	rated   int
	skipped int
	width   int

	screen components.RateScreen
}

// defaultRateWidth is what the screen is drawn at for the one frame before
// the terminal says how wide it is. It is the working width rather than the
// browser's 130: this screen is one card and lays itself out the same way at
// every width, so it has no wide layout to guess at.
const defaultRateWidth = 110

func newRateModel(db rateStore, entries []storage.UnratedRequest, now time.Time) rateModel {
	m := rateModel{db: db, entries: entries}
	rows := make([]components.RateRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, rateRow(e, now))
	}
	m.screen.Rows = rows
	m.width = defaultRateWidth
	return m
}

func (m rateModel) Init() tea.Cmd { return nil }

func (m rateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.screen.MaxLines = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		m.screen.Notice = ""
		done, result := m.screen.Update(msg)
		if answer, ok := result.(components.RateAnswer); ok {
			m.apply(answer)
		}
		if !done {
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

// View is the frame: the rating screen, on the alt screen it takes over.
func (m rateModel) View() tea.View {
	v := tea.NewView(m.screen.View(m.width))
	v.AltScreen = true
	return v
}

// apply writes one answer down. A write that fails leaves the entry unrated
// and says so on the notice line rather than ending the run: the reader is
// most of the way through a list of questions, and losing that to one failed
// UPDATE would be the worse trade.
func (m *rateModel) apply(answer components.RateAnswer) {
	if answer.Act == components.RateSkipped {
		m.skipped++
		return
	}
	// The id is the one this host put on the row with FormatInt, so it parses;
	// a screen that somehow handed back another is not something to guess at.
	id, err := strconv.ParseInt(answer.ID, 10, 64)
	if err != nil {
		return
	}
	if err := m.db.RateRequest(id, answer.Act == components.RateWorked); err != nil {
		m.screen.Notice = "rate: " + err.Error()
		return
	}
	m.rated++
}

// tally is the closing line: what was answered, what was passed over, and how
// much of the list the reader never got to. A run that was stopped says how
// much is left, because that is the number that decides whether it is worth
// running again.
func (m rateModel) tally() string {
	return rateTally(m.rated, m.skipped, len(m.entries)-m.rated-m.skipped)
}

func rateTally(rated, skipped, left int) string {
	parts := []string{"rated " + strconv.Itoa(rated)}
	if skipped > 0 {
		parts = append(parts, "skipped "+strconv.Itoa(skipped))
	}
	if left > 0 {
		parts = append(parts, strconv.Itoa(left)+" left")
	}
	return strings.Join(parts, " · ")
}

func runRateScreen(db rateStore, out io.Writer, entries []storage.UnratedRequest, now time.Time) error {
	final, err := newProgram(newRateModel(db, entries, now)).Run()
	if err != nil {
		return err
	}
	// The program has given the terminal back by now, so the tally is left in
	// the scrollback the way every other listing leaves its own. It carries the
	// title because the alt screen took everything above it with it; the line
	// walk's tally is bare, because there the title is still on the screen.
	return report.Fprint(out, report.Report{Title: "shhh rate", Tally: final.(rateModel).tally()})
}

// rateByLine is the same walk for a stdin that is not a terminal: each entry
// through the report primitive, one line read per answer. It is not a
// degraded screen — it is the shape a listing has when nobody is holding the
// keyboard, and it says the same things in the same words.
func rateByLine(db rateStore, in io.Reader, out io.Writer, entries []storage.UnratedRequest, now time.Time) error {
	// The keys go on the row's body rather than beside its subject, because
	// the body wraps and the target field clips: a key row that lost `[q]` to
	// an eighty-column terminal would be a surface with no stated way out
	// (invariant 4).
	_ = report.Fprint(out, report.Report{
		Title:   "shhh rate",
		Subject: countOf(len(entries), "unrated command", "unrated commands"),
		Sections: []report.Section{{Rows: []report.Row{{State: report.Run,
			Subject: "did each one do what you wanted?",
			Body:    []string{rateLineKeys()}}}}},
	})
	fmt.Fprintln(out)

	reader := bufio.NewReader(in)
	rated, skipped := 0, 0
	tally := func() error {
		return report.Fprint(out, report.Report{
			Tally: rateTally(rated, skipped, len(entries)-rated-skipped)})
	}
	for i, e := range entries {
		row := rateRow(e, now)
		_ = report.Fprint(out, report.Report{
			Title:   fmt.Sprintf("%d of %d", i+1, len(entries)),
			Subject: row.When,
			Sections: []report.Section{{Pairs: []report.Pair{
				{Key: "prompt", Value: oneLineText(e.Prompt)},
				{Key: "command", Value: oneLineText(e.Command)},
				{Key: "outcome", Value: row.Outcome},
			}}},
		})
		fmt.Fprint(out, "  worked? [y/n/s/q] ")

		input, err := reader.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(input))
		// A closed stdin is a stop, not a run of skips: nothing more is
		// coming, and counting the rest as skipped would be inventing answers.
		if err != nil && answer == "" {
			break
		}
		// A write that fails ends this walk, where on the screen it is a
		// notice and the run goes on. The difference is who is reading: a
		// script wants the non-zero exit, and a person halfway through a list
		// of questions wants to finish it.
		switch answer {
		case "y", "yes":
			if err := db.RateRequest(e.ID, true); err != nil {
				_ = tally()
				return err
			}
			rated++
		case "n", "no":
			if err := db.RateRequest(e.ID, false); err != nil {
				_ = tally()
				return err
			}
			rated++
		case "q", "quit":
			fmt.Fprintln(out)
			return tally()
		default:
			skipped++
		}
		fmt.Fprintln(out)
		if err != nil {
			break
		}
	}
	return tally()
}

// rateLineKeys is the walk's key row, spelled from the register rather than
// written down: these are the screen's own four keys, and a line that said
// them in its own words would go stale the first time one was reworded. `[q]`
// rather than `[esc]` is the one difference the surface owns — this walk is
// reading lines, and esc is not a line.
func rateLineKeys() string {
	parts := make([]string, 0, 4)
	for _, b := range []keys.Binding{
		keys.Screen.Worked, keys.Screen.Failed, keys.Screen.Skip,
	} {
		parts = append(parts, keys.Bracket(b)+" "+keys.Words(b))
	}
	return strings.Join(append(parts,
		keys.Bracket(keys.Screen.Quit)+" stop"), " · ")
}

// rateReport is `--table` and the empty state: what is waiting to be rated,
// on the grid every other listing uses.
func rateReport(entries []storage.UnratedRequest, now time.Time) report.Report {
	r := report.Report{Title: "shhh rate"}
	if len(entries) == 0 {
		return emptyInto(r, "nothing to rate", "every recent command is rated")
	}
	rows := make([]report.Row, 0, len(entries))
	for _, e := range entries {
		state, outcome := commandOutcome(e.ExitCode, e.Action)
		rows = append(rows, commandReportRow(storedCommand{
			Action: e.Action, At: e.CreatedAt, Prompt: e.Prompt, Command: e.Command,
		}, state, outcome, now))
	}
	r.Subject = countOf(len(entries), "unrated command", "unrated commands")
	r.Sections = []report.Section{{Rows: rows}}
	return r
}

// rateRow reads one unrated request into the card the screen draws. Every
// reading of the store happens here: what the glyph says, what the outcome
// field says, and how long ago "ago" is.
func rateRow(e storage.UnratedRequest, now time.Time) components.RateRow {
	state, outcome := commandOutcome(e.ExitCode, e.Action)
	return components.RateRow{
		ID:      strconv.FormatInt(e.ID, 10),
		Prompt:  oneLineText(e.Prompt),
		Command: e.Command,
		When:    historyAgo(e.CreatedAt, now),
		Outcome: outcome,
		State:   state,
	}
}
