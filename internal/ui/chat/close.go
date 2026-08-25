package chat

// Turn summary and changeset row (S-098, DESIGN-TUI.md §16): a turn closes
// with the rows that answer what it did, what it changed, and whether the
// checks still pass. They are ordinary transcript entries — raw data plus a
// passive renderer — so they re-render at any width like everything else, and
// focus mode is what handles the keys they offer.
//
// Nothing here recomputes what the session already knows: the steps and tools
// come from the turn's own entries, the wall time and spend from the vitals
// history (S-093), and the files from the changeset store (S-097).

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The offers the changeset row makes. Focus mode on the row consumes them, so
// the input keeps every other key (§7).
const (
	reviewKey = "v"
	undoKey   = "u"
)

// appendTurnClose closes the turn with its summary rows. It runs where the
// turn's accounting is closed — one place, so a turn cannot end without
// saying what it did — and only for a turn the user actually started: a /run
// finishing is not a turn ending.
func (m *Model) appendTurnClose() {
	if !m.turnOpen {
		return
	}
	m.turnOpen = false
	m.appendEntry(entry{kind: entryTurnClose, turn: m.turnCount, close: m.turnCloseData()})
}

// turnCloseData assembles the close block from what the session already
// tracks.
func (m Model) turnCloseData() *components.TurnClose {
	es := m.turnEntries()
	c := components.TurnClose{
		State:   m.turnOutcome,
		Elapsed: components.FormatElapsed(m.turnElapsed()),
		Note:    m.roundNote(),
		Changes: m.turnChangesRow(),
		Checks:  turnChecksRow(es),
	}
	for _, blk := range stepBlocks(es) {
		if blk.step != nil {
			c.Steps = blk.step.ordinal
		}
	}
	for _, e := range es {
		if isActivityEntry(e) {
			c.Tools++
		}
	}
	// The turn's own cost, priced per request as it went; an unpriced model
	// reports tokens rather than a made-up zero (S-093).
	if t, ok := m.vitals.lastTurn(); ok {
		if t.Priced {
			c.Spend = formatCost(t.Cost)
		} else {
			c.Spend = m.spendLabel(t.In, t.Out)
		}
	}
	return &c
}

// roundNote is the first row's right-aligned note: how much of the turn's
// tool-round budget it used. A turn that called nothing has no budget to
// report.
func (m Model) roundNote() string {
	rounds := m.agent.Rounds()
	if rounds <= 0 {
		return ""
	}
	return fmt.Sprintf("round %d/%d", rounds, m.effectiveMaxToolRounds())
}

// turnChangesRow is the changed-files row, read from the turn's changeset.
// A turn that changed nothing has no row — the summary row stands alone.
func (m Model) turnChangesRow() *components.TurnChanges {
	t, ok := m.changes.Turn(m.turnCount)
	if !ok || t.Files() == 0 {
		return nil
	}
	return &components.TurnChanges{
		Files:   t.Files(),
		Added:   t.Added,
		Removed: t.Removed,
		Keys: []components.TurnKey{
			{Key: "[" + reviewKey + "]", Label: "review"},
			{Key: "[" + undoKey + "]", Label: "undo turn"},
		},
		Note: trackingNote(t),
	}
}

// trackingNote says what git knew about the files when they were edited — the
// input to how reversible the turn is (S-097). Outside a repository every
// answer is unknown, which is not the same as untracked, so the note says so
// differently.
func trackingNote(t changeset.Turn) string {
	var tracked, untracked int
	for _, r := range t.Records {
		switch r.Track {
		case changeset.TrackTracked:
			tracked++
		case changeset.TrackUntracked:
			untracked++
		}
	}
	switch {
	case tracked == 0 && untracked == 0:
		return "no git here"
	case untracked == 0:
		return "all tracked in git"
	case tracked == 0:
		return "all new to git"
	}
	return fmt.Sprintf("%d tracked · %d new", tracked, untracked)
}

// testCommandHints are the command shapes whose exit code is a verdict about
// the code rather than about the shell. The quality gate is the authoritative
// source — it reports a tally the session can quote — and this list is the
// approximation for the turns that just ran the suite themselves.
var testCommandHints = []string{
	"go test", "gotestsum", "npm test", "npm run test", "yarn test",
	"pnpm test", "pytest", "cargo test", "make test", "mix test",
	"dotnet test", "rspec", "bundle exec rspec",
}

func isTestCommand(command string) bool {
	c := strings.ToLower(firstLine(command))
	for _, hint := range testCommandHints {
		if strings.Contains(c, hint) {
			return true
		}
	}
	return false
}

// turnChecksRow is the verdict row: what the turn ran to check its own work.
// Several runs collapse into one tally rather than one row each — the row
// answers "does it still build", not "what did you run".
func turnChecksRow(es []entry) *components.TurnChecks {
	var checks []components.TurnChecks
	for _, e := range es {
		switch {
		case e.kind == entryTool && e.toolName == quality.ToolName:
			s, ok := quality.Summarize(e.toolResult)
			if !ok {
				continue
			}
			counts := fmt.Sprintf("%d/%d checks", s.Passed, s.Total)
			if s.Duration != "" {
				counts += " · " + s.Duration
			}
			if s.Stale {
				counts += " · stale"
			}
			checks = append(checks, components.TurnChecks{
				Failed: !s.OK(),
				Label:  "quality gate " + s.Suite,
				Counts: counts,
			})
		case e.kind == entryCommand && isTestCommand(e.text):
			// A command has no tally of its own, so the exit code is the
			// count: it either came back clean or it did not.
			var counts []string
			if e.exitCode != 0 {
				counts = append(counts, components.OutcomeExit(e.exitCode))
			}
			if d := activityDuration(e.duration); d != "" {
				counts = append(counts, d)
			}
			checks = append(checks, components.TurnChecks{
				Failed: e.exitCode != 0,
				Label:  firstLine(e.text),
				Counts: strings.Join(counts, " · "),
			})
		}
	}
	switch len(checks) {
	case 0:
		return nil
	case 1:
		return &checks[0]
	}
	passed := 0
	for _, c := range checks {
		if !c.Failed {
			passed++
		}
	}
	return &components.TurnChecks{
		Failed: passed < len(checks),
		Label:  "checks",
		Counts: fmt.Sprintf("%d of %d passing", passed, len(checks)),
	}
}

// reviewTurn opens what a turn changed, full screen. It is the diff renderer
// the transcript rows and /diff already use (S-074); review mode's staging
// surface replaces it in S-099.
func (m Model) reviewTurn(n int64) (tea.Model, tea.Cmd) {
	t, ok := m.changes.Turn(n)
	if !ok {
		if m.changes.WasEvicted(n) {
			return m.systemNotice(fmt.Sprintf(
				"Turn %d's records were dropped to stay inside the changeset store's size limit; there is nothing left to review.", n))
		}
		return m.systemNotice(fmt.Sprintf("Turn %d changed no files.", n))
	}
	files := make([]diff.File, 0, len(t.Records))
	for _, r := range t.Records {
		files = append(files, diff.File{Path: r.Path, Hunks: r.Hunks})
	}
	return m.openDiffFull(&components.DiffView{
		Path:      fmt.Sprintf("turn %d · %s · +%d −%d", n, plural(t.Files(), "file"), t.Added, t.Removed),
		Files:     files,
		SyntaxFor: diffSyntax,
	}, stateFocus)
}

// undoTurn is the offer S-100 fills in. Saying so is better than a key that
// silently does nothing: the records are held, so the turn stays undoable.
func (m Model) undoTurn(n int64) (tea.Model, tea.Cmd) {
	if m.changes.WasEvicted(n) {
		return m.systemNotice(fmt.Sprintf(
			"Turn %d's records were dropped to stay inside the changeset store's size limit; it can no longer be undone.", n))
	}
	if _, ok := m.changes.Turn(n); !ok {
		return m.systemNotice(fmt.Sprintf("Turn %d changed no files; there is nothing to undo.", n))
	}
	return m.systemNotice(fmt.Sprintf(
		"Undo is not wired up yet. Turn %d's changes are still recorded on both sides, so nothing is lost — [v] shows them.", n))
}

// plural is the chat-side counterpart of the components helper, for the
// labels this package builds itself.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
