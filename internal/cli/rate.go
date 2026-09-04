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
// A session is the same question over a different table. The record infers
// how a session came out from how it ended, and an inference is worth what
// the checks on it are worth — so the walk offers sessions beside commands,
// on the one card and the one set of keys
// (docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference).
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
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	var table, sessionsOnly, commandsOnly bool

	cmd := &cobra.Command{
		Use:   "rate",
		Short: "Rate past commands and sessions",
		Long: "Walk through recent unrated commands and sessions and mark whether they " +
			"actually worked, so accuracy metrics and the session record reflect real outcomes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			scope := rateScopeOf(commandsOnly, sessionsOnly)
			items, err := unratedItems(db, scope, limit)
			if err != nil {
				return err
			}
			out, now := cmd.OutOrStdout(), time.Now()
			// Nothing to rate and `--table` land on the same listing: a walk
			// through no questions is not a screen the reader has to leave to
			// be told there were none.
			if len(items) == 0 || table {
				return report.Fprint(out, rateReport(items, scope, now))
			}
			if rateInteractive() {
				return runRateScreen(db, out, items, now)
			}
			return rateByLine(db, cmd.InOrStdin(), out, items, now)
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "max entries to review")
	cmd.Flags().BoolVar(&table, "table", false, "list what is unrated without asking about any of it")
	cmd.Flags().BoolVar(&commandsOnly, "commands", false, "ask only about commands")
	cmd.Flags().BoolVar(&sessionsOnly, "sessions", false, "ask only about sessions")
	return cmd
}

// rateScope is which halves of the walk the flags left in it.
type rateScope struct{ commands, sessions bool }

// rateScopeOf reads the two narrowing flags. Neither given is both — the walk
// is one question and its default is to ask it about everything — and both
// given is both as well, because a reader who names the two kinds has named
// every kind there is and refusing that would be pedantry.
func rateScopeOf(commandsOnly, sessionsOnly bool) rateScope {
	return rateScope{
		commands: !sessionsOnly || commandsOnly,
		sessions: !commandsOnly || sessionsOnly,
	}
}

// unratedItems reads whichever halves are in scope. A narrowed walk does not
// run the query it excluded, so `--commands` costs nothing on a store full of
// sessions and never counts them into anything it prints.
func unratedItems(db *storage.DB, scope rateScope, limit int) ([]rateItem, error) {
	var (
		commands []storage.UnratedRequest
		sessions []storage.UnratedSession
		err      error
	)
	if scope.commands {
		if commands, err = db.ListUnrated(limit); err != nil {
			return nil, fmt.Errorf("query unrated commands: %w", err)
		}
	}
	if scope.sessions {
		if sessions, err = db.ListUnratedSessions(limit); err != nil {
			return nil, fmt.Errorf("query unrated sessions: %w", err)
		}
	}
	return rateItems(commands, sessions, limit), nil
}

// rateItem is one thing the walk asks about: a command the model wrote, or a
// session it ran. Exactly one of the two is set, and every reading the walk
// makes of it happens on this type — what the card says, what the listing
// says, and where the answer goes.
type rateItem struct {
	command *storage.UnratedRequest
	session *storage.UnratedSession
}

// rateItems merges the two lists into the one walk, newest first, and cuts it
// to the limit the reader asked for.
//
// Interleaved rather than one kind after the other, because the limit is a
// number of questions and a walk that did every command first would spend all
// of it on commands whenever there were enough of them — the sessions would
// then only ever be offered to someone who had already caught up on
// everything else, which is nobody.
func rateItems(commands []storage.UnratedRequest, sessions []storage.UnratedSession, limit int) []rateItem {
	items := make([]rateItem, 0, len(commands)+len(sessions))
	for i := range commands {
		items = append(items, rateItem{command: &commands[i]})
	}
	for i := range sessions {
		items = append(items, rateItem{session: &sessions[i]})
	}
	slices.SortStableFunc(items, func(a, b rateItem) int { return b.at().Compare(a.at()) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

// at is when the thing happened, which is the only field the two kinds are
// ordered on.
func (i rateItem) at() time.Time {
	if i.session != nil {
		return i.session.StartedAt
	}
	return i.command.CreatedAt
}

// handle is where the answer goes.
func (i rateItem) handle() rateHandle {
	if i.session != nil {
		return rateHandle{session: true, id: i.session.ID}
	}
	return rateHandle{id: i.command.ID}
}

// card is the entry as the screen draws it. A session takes the sub-agent's
// glyph and verb because that is what it is — an agent run, judged as a
// whole. What it does not take is the rail a command always carries: a
// session that came out well touched the workspace through rows of its own
// and this one is a report of it, not an act. A session that failed or was
// cut short does keep a rail, because the grid gives every broken row one
// whatever its kind, and that is the rule rather than an exception to it
// (docs/interface/principles.md#weight-tracks-risk).
func (i rateItem) card(now time.Time) components.RateRow {
	row := components.RateRow{ID: i.handle().String()}
	if s := i.session; s != nil {
		row.State, row.Outcome = sessionOutcome(s.Outcome)
		row.Prompt, row.Target = sessionReminder(*s), sessionTarget(*s)
		row.Kind, row.Verb = components.ActivitySubagent, "agent"
		row.When = historyAgo(s.StartedAt, now)
		return row
	}
	c := i.command
	row.State, row.Outcome = commandOutcome(c.ExitCode, c.Action)
	row.Prompt, row.Target = oneLineText(c.Prompt), c.Command
	row.Kind, row.Verb = components.ActivityCommand, "run"
	row.When = historyAgo(c.CreatedAt, now)
	return row
}

// row is the entry on the report grid, for `--table`. A stored command reads
// the way the history browser lists it; a session reads the way `shhh observe`
// lists one — what kind it was, when, what it was about, and how it came out.
func (i rateItem) row(now time.Time) report.Row {
	if s := i.session; s != nil {
		state, outcome := sessionOutcome(s.Outcome)
		return report.Row{
			State:   historyState(state),
			Name:    s.Kind,
			Subject: historyAgo(s.StartedAt, now),
			Detail:  sessionReminder(*s),
			Outcome: outcome,
			Body:    []string{sessionTarget(*s)},
		}
	}
	c := i.command
	state, outcome := commandOutcome(c.ExitCode, c.Action)
	return commandReportRow(storedCommand{
		Action: c.Action, At: c.CreatedAt, Prompt: c.Prompt, Command: c.Command,
	}, state, outcome, now)
}

// pairs is the entry where there is no card to draw it in: the line walk's
// reading, in the field names each kind answers to.
func (i rateItem) pairs() []report.Pair {
	if s := i.session; s != nil {
		_, outcome := sessionOutcome(s.Outcome)
		return []report.Pair{
			{Key: "about", Value: sessionReminder(*s)},
			{Key: "session", Value: sessionTarget(*s)},
			{Key: "outcome", Value: outcome},
		}
	}
	c := i.command
	_, outcome := commandOutcome(c.ExitCode, c.Action)
	return []report.Pair{
		{Key: "prompt", Value: oneLineText(c.Prompt)},
		{Key: "command", Value: oneLineText(c.Command)},
		{Key: "outcome", Value: outcome},
	}
}

// rateHandle is the walk's handle on an entry: which table the answer belongs
// to and the row's id in it. It rides on the card as an opaque string and
// comes back on the answer — the screen draws it nowhere and knows nothing
// about there being two tables to write to.
type rateHandle struct {
	session bool
	id      int64
}

func (h rateHandle) String() string {
	if h.session {
		return "s" + strconv.FormatInt(h.id, 10)
	}
	return "c" + strconv.FormatInt(h.id, 10)
}

func parseRateHandle(s string) (rateHandle, bool) {
	if len(s) < 2 || (s[0] != 'c' && s[0] != 's') {
		return rateHandle{}, false
	}
	id, err := strconv.ParseInt(s[1:], 10, 64)
	if err != nil || id <= 0 {
		return rateHandle{}, false
	}
	return rateHandle{session: s[0] == 's', id: id}, true
}

// write puts one answer where the entry came from.
func (h rateHandle) write(db rateStore, up bool) error {
	if h.session {
		return db.RateAgentSession(h.id, up)
	}
	return db.RateRequest(h.id, up)
}

// sessionOutcome is the record's reading of how a session ended, in the word
// it stored and with the state that goes with it.
//
// The words are literals rather than the constants they were written from,
// and for the reason the dashboard's own reading uses literals: these rows
// were written by whatever build was running at the time, so a word this
// build has never seen is a live possibility — and the one thing it must not
// do is arrive wearing a tick.
func sessionOutcome(outcome string) (components.ActivityState, string) {
	switch outcome {
	case "completed":
		return components.ActivityDone, outcome
	case "error":
		return components.ActivityFailed, outcome
	case "interrupted", "abandoned":
		return components.ActivityDenied, outcome
	case "":
		// No outcome at all is what the dashboard calls unknown, and naming it
		// here rather than leaving the field blank is the whole point: a card
		// with nothing in the outcome column reads as a session that came out
		// fine, which is the one thing the record is not saying.
		return components.ActivityQueued, "unknown"
	}
	return components.ActivityQueued, outcome
}

// sessionReminder is what the card says the session was about. The title a
// model wrote for the conversation comes first and the opening line is the
// fallback, because the title is a reading of the whole session and the
// opening is a reading of its first minute — and a session that went
// somewhere else entirely is exactly the one worth remembering correctly.
//
// It is bounded, which a command's prompt is not. A prompt is a sentence
// somebody typed at a shell; an opening message is whatever was pasted into
// a session, and a stack trace as the card's body would push the row the
// question is actually about off the bottom of it.
func sessionReminder(s storage.UnratedSession) string {
	if title := oneLineText(s.Title); title != "" {
		return boundReminder(title)
	}
	return boundReminder(oneLineText(s.Opening))
}

// maxReminder is how much of the conversation the card will show. It is the
// bound a written title already obeys, so a title passes through untouched
// and only a long opening line is cut.
const maxReminder = 60

func boundReminder(s string) string {
	r := []rune(s)
	if len(r) <= maxReminder {
		return s
	}
	return strings.TrimRightFunc(string(r[:maxReminder-1]), unicode.IsSpace) + "…"
}

// sessionTarget is the row under the reminder: which conversation it was,
// what wrote it, on what model, and how much of it there is. The name leads
// because it is the handle — `shhh chats show <name>` is where a reader who
// wants more than the reminder goes.
func sessionTarget(s storage.UnratedSession) string {
	return joinDetail(s.Chat, joinDetail(joinDetail(s.Kind, s.Model),
		countOf(int(s.Turns), "turn", "turns")))
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
// keystroke writes what and where, not SQL.
type rateStore interface {
	RateRequest(id int64, up bool) error
	RateAgentSession(id int64, up bool) error
}

// rateModel hosts the rating screen
// (docs/interface/surfaces.md#the-supporting-screens). It owns everything the
// screen deliberately does not: what an entry means, how long ago it was,
// which table an answer reaches, and what the tally at the end says.
type rateModel struct {
	db      rateStore
	items   []rateItem
	rated   int
	skipped int

	screen components.RateScreen
}

// defaultRateWidth is what the screen is drawn at for the one frame before
// the terminal says how wide it is. It is the working width rather than the
// browser's 130: this screen is one card and lays itself out the same way at
// every width, so it has no wide layout to guess at.
const defaultRateWidth = 110

func newRateModel(db rateStore, items []rateItem, now time.Time) *rateModel {
	m := &rateModel{db: db, items: items}
	rows := make([]components.RateRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, item.card(now))
	}
	m.screen.Rows = rows
	return m
}

// answer writes down what the reader said about the card that was showing.
func (m *rateModel) answer(done bool, result components.RateResult) tea.Cmd {
	m.screen.Notice = ""
	if result.Answer != nil {
		m.apply(*result.Answer)
	}
	if !done {
		return nil
	}
	return tea.Quit
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
	// The handle is the one this host put on the row, so it parses; a screen
	// that somehow handed back another is not something to guess at.
	h, ok := parseRateHandle(answer.ID)
	if !ok {
		return
	}
	if err := h.write(m.db, answer.Act == components.RateWorked); err != nil {
		m.screen.Notice = "rate: " + err.Error()
		return
	}
	m.rated++
}

// tally is the closing line: what was answered, what was passed over, and how
// much of the list the reader never got to. A run that was stopped says how
// much is left, because that is the number that decides whether it is worth
// running again.
func (m *rateModel) tally() string {
	return rateTally(m.rated, m.skipped, len(m.items)-m.rated-m.skipped)
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

func runRateScreen(db rateStore, out io.Writer, items []rateItem, now time.Time) error {
	m := newRateModel(db, items, now)
	if _, err := newProgram(newScreenModel(&m.screen, defaultRateWidth, m.answer)).Run(); err != nil {
		return err
	}
	// The program has given the terminal back by now, so the tally is left in
	// the scrollback the way every other listing leaves its own. It carries the
	// title because the alt screen took everything above it with it; the line
	// walk's tally is bare, because there the title is still on the screen.
	return report.Fprint(out, report.Report{Title: "shhh rate", Tally: m.tally()})
}

// rateByLine is the same walk for a stdin that is not a terminal: each entry
// through the report primitive, one line read per answer. It is not a
// degraded screen — it is the shape a listing has when nobody is holding the
// keyboard, and it says the same things in the same words.
func rateByLine(db rateStore, in io.Reader, out io.Writer, items []rateItem, now time.Time) error {
	// The keys go on the row's body rather than beside its subject, because
	// the body wraps and the target field clips: a key row that lost `[q]` to
	// an eighty-column terminal would be a surface with no stated way out
	// (invariant 4).
	_ = report.Fprint(out, report.Report{
		Title:   "shhh rate",
		Subject: rateSubject(items),
		Sections: []report.Section{{Rows: []report.Row{{State: report.Run,
			Subject: "did each one do what you wanted?",
			Body:    []string{rateLineKeys()}}}}},
	})
	fmt.Fprintln(out)

	reader := bufio.NewReader(in)
	rated, skipped := 0, 0
	tally := func() error {
		return report.Fprint(out, report.Report{
			Tally: rateTally(rated, skipped, len(items)-rated-skipped)})
	}
	for i, item := range items {
		_ = report.Fprint(out, report.Report{
			Title:    fmt.Sprintf("%d of %d", i+1, len(items)),
			Subject:  historyAgo(item.at(), now),
			Sections: []report.Section{{Pairs: item.pairs()}},
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
		case "y", "yes", "n", "no":
			if err := item.handle().write(db, answer[0] == 'y'); err != nil {
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

// rateSubject counts what is waiting, by kind. A reader who asked for both
// kinds is told how much of each there is rather than one total: they are
// separate questions over separate tables, and "is this worth doing now" has
// its own answer for each.
func rateSubject(items []rateItem) string {
	commands, sessions := 0, 0
	for _, item := range items {
		if item.session != nil {
			sessions++
			continue
		}
		commands++
	}
	parts := make([]string, 0, 2)
	if commands > 0 {
		parts = append(parts, countOf(commands, "unrated command", "unrated commands"))
	}
	if sessions > 0 {
		parts = append(parts, countOf(sessions, "unrated session", "unrated sessions"))
	}
	return strings.Join(parts, " · ")
}

// rateReport is `--table` and the empty state: what is waiting to be rated,
// on the grid every other listing uses.
func rateReport(items []rateItem, scope rateScope, now time.Time) report.Report {
	r := report.Report{Title: "shhh rate"}
	if len(items) == 0 {
		return emptyInto(r, "nothing to rate", rateNothingLeft(scope))
	}
	rows := make([]report.Row, 0, len(items))
	for _, item := range items {
		rows = append(rows, item.row(now))
	}
	r.Subject = rateSubject(items)
	r.Sections = []report.Section{{Rows: rows}}
	return r
}

// rateNothingLeft is the way out on the empty state. It says what was looked
// at rather than what exists, twice over: a walk narrowed to commands does
// not report on sessions, and none of the three claims every session is
// rated — a session with no saved conversation was never offered, and a
// line that counted it as answered would have the walk vouching for work it
// declined to ask about. `saved` is the word that carries that, because a
// session with nothing saved is the one there is nothing to ask about.
func rateNothingLeft(scope rateScope) string {
	switch {
	case !scope.sessions:
		return "every recent command is rated"
	case !scope.commands:
		return "every recent saved session is rated"
	}
	return "every recent command and saved session is rated"
}
