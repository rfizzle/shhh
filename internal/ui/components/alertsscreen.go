package components

// The alerts screen (docs/interface/surfaces.md#the-supporting-screens):
// every alert the session has had, the list the rail's ALERTS block holds only
// the two most recent standing rows of.
//
// A row is one episode — a command's run of failures from the first one to
// whatever answered it — carrying the block's own alert, so the name, the last
// outcome, the runs and the turn it broke in are the rail row's words for the
// same fact. The preview is that account laid out, with what answered it, and
// `[enter]` puts each run under it: its turn, how it ended, how long it took,
// and the evidence id its output was kept under where the result was reduced.
// With the runs out the pointer walks them, and `[enter]` on one whose output
// was kept hands its id back to the host to open.
// This is a renderer; the episodes and the store are the host's.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// alertsStackWidth is the width below which the panes stack. The preview
	// is a short account and a list of runs, narrower than a close block, so
	// the panes stand side by side sooner than the turns screen's do.
	alertsStackWidth = 88
	// alertsListMin / alertsListMax bound the list column. A row is a mark
	// and a command's name, the last outcome and the runs, the standing word
	// and the turn; past the ceiling the columns are worth more to the runs.
	alertsListMin = 30
	alertsListMax = 62
	// alertsMinPreview is the smallest preview the stacked layout leaves
	// standing: the title, and the first line of the account.
	alertsMinPreview = 3
)

// AlertsRun is one failing run of an episode, already resolved to what the
// screen draws.
type AlertsRun struct {
	// Turn is the turn the run was in, and Line the command line that ran —
	// an episode is one command's name, so its runs can be several lines.
	Turn int64
	Line string
	// Outcome is how the run ended, in the word its own row states: the exit
	// code, or what ended it where nothing let it exit.
	Outcome string
	// Duration is how long it ran, as its row states it; empty says nothing.
	Duration string
	// Evidence is the id the run's output was kept under where the result
	// was reduced or trimmed, so the whole of it is one read away. Empty
	// where the output was never cut.
	Evidence string
}

// AlertsAnswer is what answered an episode: a clean run of the command, or
// the repository's own suite passing over the tree it failed on.
type AlertsAnswer struct {
	// What is the answer in words — `make check came back clean`.
	What string
	// Turn is the turn the answer ran in.
	Turn int64
}

// AlertsItem is one episode.
type AlertsItem struct {
	// Alert is the rail's own alert for the episode, not a copy of its
	// figures made again: the screen and the block read one walk.
	Alert InspectorAlert
	// Answer is what answered it; nil on a standing one.
	Answer *AlertsAnswer
	// Runs are its failing runs, oldest first.
	Runs []AlertsRun
}

// AlertsResult is how the screen closed: with a run's kept output to open, or
// with nothing.
type AlertsResult struct {
	// Open is `[enter]` on a run whose output was kept; Evidence names the
	// entry and Line the command that wrote it. The host does the opening,
	// because the store is the session's and not this screen's.
	Open     bool
	Evidence string
	Line     string
}

// AlertsScreen is `/alerts`: a takeover in the chat, full width, owning the
// keyboard for as long as it is up.
type AlertsScreen struct {
	// The pointer is an index into Alerts.
	listScreen[AlertsItem]
	// Alerts are standing first, then superseded, each newest first — the
	// order the list draws them in.
	Alerts []AlertsItem
	// Open says the runs of the episode under the pointer are showing.
	// Moving the pointer off the episode closes them, since they are one
	// episode's.
	Open bool
	// runAt is the run under the pointer while the runs are showing, an index
	// into the episode's Runs.
	runAt int
	// Notice is the line a key left behind. The host clears it on the next
	// keystroke.
	Notice string
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int
}

// Update is the screen's whole keyboard: it moves, it shows an episode's
// runs, it opens a run's kept output, it shows its keys, and it leaves. It
// reports whether the screen is done, and with what.
func (s *AlertsScreen) Update(msg tea.KeyPressMsg) (done bool, result AlertsResult) {
	pressed := msg.String()
	switch {
	case s.walked(pressed):
	case keys.Is(pressed, keys.Screen.Take):
		if s.current(s.Alerts) == nil {
			break
		}
		if !s.Open {
			s.Open, s.runAt = true, 0
			break
		}
		r := s.run()
		if r == nil {
			break
		}
		if r.Evidence == "" {
			// Nothing was kept because nothing was cut: the row in the
			// transcript is the whole of it, and saying so is the answer.
			s.Notice = "This run's output was never cut, so nothing was kept — its row in the transcript holds all of it."
			break
		}
		return true, AlertsResult{Open: true, Evidence: r.Evidence, Line: r.Line}
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Quit):
		return true, AlertsResult{}
	}
	return false, AlertsResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *AlertsScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *AlertsScreen) View(width int) string { return s.view(width, s) }

// chrome is the header over the panes, the keys under them, and the line the
// last key left.
func (s *AlertsScreen) chrome(width int) screenChrome {
	field := s.footField()
	return screenChrome{
		header:   s.header(),
		foot:     s.footer(s.offers(width, field), s.keyList(), field).rows(width),
		notice:   s.Notice,
		maxLines: s.maxLines,
	}
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go): on the left the episodes, standing first.
func (s *AlertsScreen) panes() screenPanes {
	return screenPanes{
		stackAt: alertsStackWidth, listMin: alertsListMin,
		listMax: alertsListMax, minPreview: alertsMinPreview,
		list: func(width, budget int) []string {
			return s.listRows(s.Alerts, "nothing this session ran has come back broken", width, budget)
		},
		preview: s.previewRows,
	}
}

// previewRows is the right pane: the episode under the pointer, its account
// in the rail's own words and what answered it, and its runs where they are
// open.
func (s *AlertsScreen) previewRows(width int) []string {
	a := s.current(s.Alerts)
	if a == nil {
		return []string{sty.dim.Render(Clip("no alert selected", width))}
	}
	word, _ := alertStanding(*a)
	rows := []string{paneTitle(brightStyle().Render(a.Alert.Label), sty.dim.Render(word), width), ""}
	// A flaky check has no runs in this session and nothing answers it: its
	// account is the ledger's, and that is the one line it has.
	if a.Alert.Flaky {
		return append(rows, alertsField("ledger", sty.body.Render(a.Alert.Note), width))
	}
	runs := fmt.Sprintf("%d", max(a.Alert.Runs, 1))
	if a.Alert.Turns > 1 {
		runs += fmt.Sprintf(", in %d turns", a.Alert.Turns)
	}
	answered := sty.err.Render("not yet")
	if a.Answer != nil {
		answered = sty.body.Render(joinTurn(a.Answer.What, a.Answer.Turn))
	}
	fields := []struct{ label, value string }{
		{"last run", sty.body.Render(a.Alert.Note)},
		{"runs", sty.body.Render(runs)},
	}
	// A command run before any turn — the reader's own, at an idle prompt —
	// has no turn to name, and the rail's row names none for it either.
	if a.Alert.Turn > 0 {
		fields = append(fields, struct{ label, value string }{"broke", sty.body.Render(fmt.Sprintf("turn %d", a.Alert.Turn))})
	}
	fields = append(fields, struct{ label, value string }{"answered", answered})
	for _, f := range fields {
		rows = append(rows, alertsField(f.label, f.value, width))
	}
	if !s.Open {
		return rows
	}
	rows = append(rows, "", "  "+sty.status.Render("each run"))
	for i, r := range a.Runs {
		rows = append(rows, alertsRunRows(r, i == s.runAt, width)...)
	}
	return rows
}

// alertsLabelWidth is the account's label column, wide enough for its
// longest label and the gap after it.
const alertsLabelWidth = 10

// alertsField is one line of the account: a dim label in a fixed column and
// the value beside it, clipped to the pane.
func alertsField(label, value string, width int) string {
	lead := "  " + sty.status.Render(fmt.Sprintf("%-*s", alertsLabelWidth, label))
	return Clip(lead+value, width)
}

// alertsRunRows is one run: the mark, its turn and how it ended and how long
// it took, with the evidence id at the far end where the output was kept,
// and the command line under it — two runs of one episode can be two lines.
// The run under the pointer takes the pointer in the columns the others
// leave blank, so walking the runs moves nothing sideways.
func alertsRunRows(r AlertsRun, pointed bool, width int) []string {
	text := r.Outcome
	if r.Turn > 0 {
		text = fmt.Sprintf("turn %d · %s", r.Turn, r.Outcome)
	}
	if r.Duration != "" {
		text += " · " + r.Duration
	}
	lead := "  "
	if pointed {
		lead = sty.focusPointer.Render("❯") + " "
	}
	left := lead + sty.err.Render("✗") + " " + sty.body.Render(text)
	rows := []string{Clip(left, width), Clip("    "+sty.dimmer.Render(r.Line), width)}
	if r.Evidence == "" {
		return rows
	}
	// The id is what makes the output one read away, so where it will not
	// stand at the end of the run's row it takes a row of its own rather
	// than being dropped.
	right := sty.dim.Render(r.Evidence)
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 {
		rows[0] = left + strings.Repeat(" ", pad) + right
		return rows
	}
	return append(rows, Clip("    "+sty.dim.Render("output kept as "+r.Evidence), width))
}

// joinTurn is a fact and the turn it happened in, `… · turn 3`, and the fact
// alone where there was no turn to name.
func joinTurn(fact string, turn int64) string {
	if turn <= 0 {
		return fact
	}
	return fmt.Sprintf("%s · turn %d", fact, turn)
}

// alertStanding is how an episode stands, in the word the list and the
// preview both say, and the tone the list reads it in.
func alertStanding(a AlertsItem) (string, FieldTone) {
	if a.Alert.Flaky {
		return "flaky", ToneOpen
	}
	if a.Alert.Superseded {
		return "superseded", ToneQuiet
	}
	return "standing", ToneRisk
}

// alertGlyph is the row's leading mark: the rail row's ✗ for one still
// standing, ✓ for one something has answered, and the rail's ~ for a check
// that keeps flaking. It is plain rather than painted for the steps screen's
// reason, and the word beside it says the same thing (invariant 1).
func alertGlyph(a AlertsItem) string {
	if a.Alert.Flaky {
		return "~"
	}
	if a.Alert.Superseded {
		return "✓"
	}
	return "✗"
}

// header names the surface and what it counts: the standing and the answered,
// two fields because they are two facts, as the rail's marker says them.
func (s *AlertsScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/alerts")}, keys: s.headerKeys(keys.Screen.List, keys.Screen.Quit)}
	standing := 0
	for _, a := range s.Alerts {
		if !a.Alert.Superseded {
			standing++
		}
	}
	// Nothing standing is not a field: the rail's marker leads with the
	// answered count in that case too, rather than with a zero.
	if standing > 0 {
		h.left = append(h.left, screenField(fmt.Sprintf("%d standing", standing)))
	}
	if superseded := len(s.Alerts) - standing; superseded > 0 {
		h.left = append(h.left, screenField(fmt.Sprintf("%d superseded", superseded)))
	}
	return h
}

// offers is the key row: the pointer's keys, the runs, and the way out, and
// the last two alone where the field leaves no room for all three.
func (s *AlertsScreen) offers(width int, field string) []KeyOffer {
	var acts []KeyOffer
	switch {
	case s.current(s.Alerts) != nil && !s.Open:
		acts = append(acts, keyOfferAs(keys.Screen.Take, "show each run"))
	case s.run() != nil && s.run().Evidence != "":
		// Only a run whose output was kept offers the key: on one that was
		// not, it answers with a sentence and opens nothing (invariant 5).
		acts = append(acts, keyOfferAs(keys.Screen.Take, "open its output"))
	}
	acts = append(acts, wayOut(backToPrompt))
	return offersBeside(keyOffer(keys.Screen.Move), acts, field, width)
}

// keyList is every key the screen has, for `[?]`.
func (s *AlertsScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between alerts, or between the runs where they are out"),
		keyOfferAs(keys.Screen.Take, "show the alert's runs, or open a run's kept output"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Screen.Quit, backToPrompt),
	}
}

// footField annotates the key row with where the episodes come from: the
// rail's own reading, so a row here and the rail's row are one alert.
func (s *AlertsScreen) footField() string {
	if len(s.Alerts) == 0 {
		return ""
	}
	return "every command this session broke, as the rail reads it"
}

// sync rebuilds the list from Alerts. It runs before every View because the
// host may replace Alerts, and the pointer has to survive that.
func (s *AlertsScreen) sync() {
	s.clamp(len(s.Alerts))
	opts := make([]SelectOption, 0, len(s.Alerts))
	for _, a := range s.Alerts {
		opt := SelectOption{Label: alertGlyph(a) + " " + a.Alert.Label,
			metaTone: ToneQuiet}
		if a.Alert.Turn > 0 {
			opt.Meta = alertTurn(a.Alert)
		}
		if note := alertNote(a.Alert); note != "" {
			opt.Detail = []DetailSpan{{Text: note, Tone: ToneQuiet}}
		}
		opt.Value, opt.valueTone = alertStanding(a)
		opts = append(opts, opt)
	}
	s.show(opts, 0, s.Focus)
}

// walked walks the pointer: between an open episode's runs, and between
// episodes otherwise — or past either end of the runs, which leaves the
// episode and puts them away.
func (s *AlertsScreen) walked(pressed string) bool {
	if len(s.Alerts) == 0 {
		return false
	}
	if a := s.current(s.Alerts); s.Open && a != nil && keys.Is(pressed, keys.Screen.Move) {
		if next := s.runAt + keys.Step(pressed, keys.Screen.Move); next >= 0 && next < len(a.Runs) {
			s.runAt = next
			return true
		}
	}
	from := s.Focus
	if !s.moved(s.Alerts, pressed, keys.Screen.Move) {
		return false
	}
	if s.Focus != from {
		s.Open = false
	}
	return true
}

// run is the run under the pointer while an episode's runs are out, or nil.
func (s *AlertsScreen) run() *AlertsRun {
	a := s.current(s.Alerts)
	if a == nil || !s.Open || s.runAt < 0 || s.runAt >= len(a.Runs) {
		return nil
	}
	return &a.Runs[s.runAt]
}
