package components

// The steps screen (docs/interface/surfaces.md#the-supporting-screens): the
// session's whole working list, the one the rail's STEPS block reads as a
// count and the step it is on — or, while an approved plan is being executed,
// that plan's checklist, the one the rail's PLAN block draws. The two blocks
// are never up together, so whichever list is the checklist is the one this
// screen draws, and its header names which (Plan).
//
// Two things are joined here and they are not the same thing. The list is
// the session's working checklist — the steps the agent said it would take,
// with the paths it named and the ones it has marked finished. The
// transcript's step blocks are the model's titled runs of calls. They usually
// share titles, and where one does its block is drawn under the step; where
// none does, the step says `not started`, because nothing in the transcript
// is filed under it. The host makes the join; this is a renderer.
//
// It is re-cut from parts that already exist, like every screen in the
// family: the selector window for the list, the preview pane beside it, the
// shared chrome around both, and — for the calls under a step — the activity
// row's own renderer, so a call reads here exactly as it read in the
// transcript.

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// stepsStackWidth is the width below which the panes stack. The preview
	// draws activity rows, which want the transcript's own grid, so two
	// columns want as much room as the sources ledger's.
	stepsStackWidth = 96
	// stepsListMin / stepsListMax bound the list column. A row carries a
	// number, a title, the state word and a count, and past the ceiling the
	// extra columns are worth more to the rows in the preview.
	stepsListMin = 30
	stepsListMax = 56
	// stepsMinPreview is the smallest preview the stacked layout leaves
	// standing: the step's title, and the line saying what the transcript
	// holds for it.
	stepsMinPreview = 3
)

// StepsItem is one step of the list, already resolved to what the screen
// draws.
type StepsItem struct {
	// Number is the number the list gives the step, which is not always its
	// position: a revised list numbers its new steps after the finished ones.
	Number int
	Title  string
	// Paths are the files the step said it would touch. Empty means it did
	// not say.
	Paths []string
	// Done is a step a progress line has marked finished; Current is the
	// first one not marked, the step the rail's block names.
	Done    bool
	Current bool
	// Failed is a plan's step whose run ended on a failure: finished, and
	// not cleanly. The working list has no such mark.
	Failed bool
	// Started says the transcript has a step titled for this one. Count and
	// Duration are what that step's header states — `3 tools`, `4.1s` — and
	// Rows are its calls. The list carries the count alone; the duration is
	// the preview's, where there is room for it.
	Started  bool
	Count    string
	Duration string
	Rows     []ActivityRow
}

// StepsScreen is `/steps`: a takeover in the chat, full width, owning the
// keyboard for as long as it is up.
type StepsScreen struct {
	// Steps are the list in its own order.
	Steps []StepsItem
	// Focus is an index into Steps.
	Focus int
	// Subject is what the header says the screen is over — `2 of 7`, the
	// rail block's own count.
	Subject string
	// Plan says the list is an approved plan's checklist rather than the
	// agent's own working list: the header is /plan's, a step's state is the
	// transcript's reading of its run rather than a mark the agent made, and
	// Drift is drawn under the list.
	Plan bool
	// Drift is what the run has done that the plan did not say, one clause
	// each, as /plan has always reported it. Empty on a plan the run has
	// followed, which says so.
	Drift []string
	// MaxLines bounds the screen height. 0 is unbounded.
	MaxLines int

	list Select
	keys bool
}

// Update is the screen's whole keyboard: it moves, it shows its keys, and it
// leaves. It reports whether the screen is done.
func (s *StepsScreen) Update(msg tea.KeyPressMsg) (done bool) {
	pressed := msg.String()
	switch {
	case s.moved(pressed):
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Quit):
		return true
	}
	return false
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *StepsScreen) SetSize(_, height int) { s.MaxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *StepsScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	s.sync()
	return ScreenChrome{
		Header:   s.header(),
		Foot:     s.footer(width).Rows(width),
		MaxLines: s.MaxLines,
	}.View(width, func(budget int) []string { return s.panes().rows(width, budget) })
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go).
func (s *StepsScreen) panes() screenPanes {
	return screenPanes{
		stackAt: stepsStackWidth, listMin: stepsListMin,
		listMax: stepsListMax, minPreview: stepsMinPreview,
		list:    s.listRows,
		preview: s.previewRows,
	}
}

// listRows is the left pane: the steps, numbered as the list numbers them.
func (s *StepsScreen) listRows(width, budget int) []string {
	if len(s.Steps) == 0 {
		return []string{sty.Dim.Render(Clip("the session has declared no steps", width))}
	}
	// A plan's departures are about the whole run rather than one step, so
	// they stand under the list the way /plan printed them under its
	// checklist. The steps come first: where the rows cannot hold every step
	// and the departures too, the departures give way, and the rail's PLAN
	// block still counts them.
	trail := s.driftRows(width)
	if budget > 0 && len(s.Steps)+len(trail)+1 > budget {
		trail = nil
	}
	if len(trail) > 0 && budget > 0 {
		budget -= len(trail) + 1
	}
	body, _ := s.list.visibleRows(cardWidthFor(width), budget, false)
	if len(trail) > 0 {
		body = append(append(body, ""), trail...)
	}
	return body
}

// driftRows is the plan's departures, laid out in the list's width: each
// clause on a line of its own in the warning's treatment, or the one line
// that says there are none. The working list has no plan to depart from.
func (s *StepsScreen) driftRows(width int) []string {
	if !s.Plan {
		return nil
	}
	if len(s.Drift) == 0 {
		return wrapDim("no drift · every step so far is one the plan named, in the order it named them", width)
	}
	var rows []string
	for _, d := range s.Drift {
		rows = append(rows, wrapWarn("⚠ "+d, width)...)
	}
	return rows
}

// previewRows is the right pane: the step under the pointer, the paths it
// named, and what the transcript recorded for it.
func (s *StepsScreen) previewRows(width int) []string {
	step := s.current()
	if step == nil {
		return []string{sty.Dim.Render(Clip("no step selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(oneLine(stepLabel(*step))),
		sty.Dim.Render(s.stepState(*step)), width)}
	if len(step.Paths) > 0 {
		rows = append(rows, "")
		const label = "touches "
		for i, p := range step.Paths {
			lead := strings.Repeat(" ", len(label))
			if i == 0 {
				lead = sty.Status.Render(label)
			}
			rows = append(rows, "  "+lead+sty.Body.Render(Clip(p, max(width-len(label)-2, 1))))
		}
	}
	rows = append(rows, "")
	if !step.Started {
		// The join found nothing, and it says so rather than drawing an empty
		// pane: a step the transcript has no run titled for is one nobody has
		// started, as far as the transcript can say. A plan's step is read
		// off its run, so one that finished with no run left is one whose
		// run a compaction took out of the transcript.
		line := outcomeNotStarted + " · no step in the transcript is titled for it"
		switch {
		case s.Plan && (step.Done || step.Failed):
			line = "its run is no longer in the transcript"
		case s.Plan:
			line = outcomeNotStarted + " · nothing in the transcript has carried it out"
		}
		return append(rows, sty.Dim.Render(Clip("  "+line, width)))
	}
	head := "  in the transcript"
	for _, f := range []string{step.Count, step.Duration} {
		if f != "" {
			head += " · " + f
		}
	}
	rows = append(rows, sty.Dimmer.Render(Clip(head, width)))
	for _, row := range step.Rows {
		rows = append(rows, strings.Split(row.View(width), "\n")...)
	}
	return rows
}

// outcomeNotStarted is what a step with no titled run in the transcript
// says, on the list and in the preview.
const outcomeNotStarted = "not started"

// stepLabel is the step as the preview titles it: its number and its title.
func stepLabel(step StepsItem) string {
	return strings.TrimSpace(strconv.Itoa(step.Number) + ". " + step.Title)
}

// stepState is the step's standing on the list, in a word. On the working
// list it is the list's own mark and not the transcript's: whether a step is
// finished is the agent's account of it
// (docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps).
// On a plan it is the transcript's reading of the step's run, in the words
// /plan has always used for it.
func (s *StepsScreen) stepState(step StepsItem) string {
	switch {
	case step.Failed:
		return "failed"
	case step.Done:
		return "done"
	case step.Current && s.Plan:
		return "running"
	case step.Current:
		return "current"
	case s.Plan:
		return OutcomeQueued
	}
	return "to do"
}

// stepGlyph is the row's leading mark, the step header's own: ✓ for a step
// marked finished, ✗ for a plan's step whose run failed, ▸ for the one the
// agent is on, · for one it has not reached. It is plain rather than painted
// for the sources screen's reason — the label runs through the card's
// emphasis — and the word beside it says the same thing, so the mark never
// carries the state alone (invariant 1).
func stepGlyph(step StepsItem) string {
	switch {
	case step.Failed:
		return "✗"
	case step.Done:
		return "✓"
	case step.Current:
		return "▸"
	}
	return "·"
}

// header names the surface and what it is over: /steps over the working list,
// /plan over an approved plan's, since that is the command whose list it is.
func (s *StepsScreen) header() ScreenHeader {
	title := "/steps"
	if s.Plan {
		title = "/plan"
	}
	h := ScreenHeader{Left: []RailSegment{screenTitle(title)}, Keys: s.headerKeys()}
	if s.Subject != "" {
		h.Left = append(h.Left, screenField(s.Subject))
	}
	return h
}

// headerKeys is the pair the header ends with: the key that shows the whole
// register, and the way back in the one word a header field is
// (docs/interface/surfaces.md#the-supporting-screens).
func (s *StepsScreen) headerKeys() string {
	list := keys.Bracket(keys.Screen.List) + " " + keys.Words(keys.Screen.List)
	if s.keys {
		list = keys.Bracket(keys.Screen.List) + " hide the keys"
	}
	return list + " · " + words(keys.Screen.Quit, "back")
}

// footer is the keys the screen offers and the field that annotates them.
func (s *StepsScreen) footer(width int) KeyFooter {
	field := s.footField()
	return KeyFooter{Offers: s.offers(width, field), Register: s.keyList(),
		Showing: s.keys, Field: field}
}

// offers is the key row: the pointer's keys and the way out, and the way out
// alone where the field leaves no room for both.
func (s *StepsScreen) offers(width int, field string) []KeyOffer {
	acts := []KeyOffer{wayOut(backToPrompt)}
	full := append([]KeyOffer{keyOffer(keys.Screen.Move)}, acts...)
	if field == "" || fitsBeside(full, field, width) {
		return full
	}
	return acts
}

// keyList is every key the screen has, for `[?]`.
func (s *StepsScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move between steps"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Screen.Quit, backToPrompt),
	}
}

// footField annotates the key row with the thing the rail's block says too:
// the list is the agent's own and can be revised, so it is not an approved
// plan (docs/interface/surfaces.md#the-inspector-rail). A plan's list is the
// other one, and says that instead.
func (s *StepsScreen) footField() string {
	switch {
	case len(s.Steps) == 0:
		return ""
	case s.Plan:
		return "the approved plan, as the transcript has carried it out"
	}
	return "the agent's own list, not a plan"
}

// sync rebuilds the list from Steps. It runs before every View because the
// host may replace Steps, and the pointer has to survive that.
func (s *StepsScreen) sync() {
	s.Focus = min(max(s.Focus, 0), max(len(s.Steps)-1, 0))
	opts := make([]SelectOption, 0, len(s.Steps))
	for _, step := range s.Steps {
		opt := SelectOption{
			Label:  stepGlyph(step) + " " + oneLine(step.Title),
			Number: step.Number,
		}
		switch {
		case step.Failed:
			opt.Value, opt.ValueTone = s.stepState(step), ToneRisk
		case step.Done:
			opt.Value, opt.ValueTone = "done", ToneSafe
		case step.Current:
			opt.Value = s.stepState(step)
		}
		switch {
		case step.Started:
			opt.Meta = step.Count
		case s.Plan && (step.Done || step.Failed):
			// A plan's finished step with no run left: the state beside it
			// is the reading, and not started would contradict it.
		default:
			opt.Meta, opt.MetaTone = outcomeNotStarted, ToneQuiet
		}
		opts = append(opts, opt)
	}
	s.list.Options = opts
	s.list.Unnumbered = true
	s.list.Focus = s.Focus
}

// moved walks the pointer between steps.
func (s *StepsScreen) moved(pressed string) bool {
	if len(s.Steps) == 0 {
		return false
	}
	l := List[StepsItem]{Items: s.Steps, Focus: s.Focus}
	if !l.Move(pressed, keys.Screen.Move) {
		return false
	}
	s.Focus = l.Focus
	return true
}

// current is the step under the pointer, or nil for an empty list.
func (s *StepsScreen) current() *StepsItem {
	if s.Focus < 0 || s.Focus >= len(s.Steps) {
		return nil
	}
	return &s.Steps[s.Focus]
}
