package components

// The process manager (docs/interface/surfaces.md#the-process-manager): `/ps`,
// and the rail's PROCESSES and CALLS headings. It is the agent manager's shape
// for the two other things a session leaves running: the processes it started,
// and the tool calls of the round in flight. A row opens onto what it is doing
// — a process's output and its errors, a call's arguments and what it has
// printed so far — and `[d]` stops it after asking, with the agent manager's
// two answers, stop and kill. Stopping one call leaves the rest of its round
// running. This is a renderer; the rows and every act on them are the host's.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// managerStackWidth is where the panes stack. A row is a mark, a name,
	// a state word and an age; the preview is a command and a line of
	// output, about as wide as the tools screen's.
	managerStackWidth = 88
	managerListMin    = 30
	managerListMax    = 56
	managerMinPreview = 3
	// managerLabelWidth is the account's label column.
	managerLabelWidth = 11
)

// ManagerSection is which of the manager's two lists a row is in. The order of
// the constants is the order on the screen.
type ManagerSection int

const (
	// ManagerProcesses are the processes the session started.
	ManagerProcesses ManagerSection = iota
	// ManagerCalls are the tool calls running now.
	ManagerCalls
)

// heading is the section's rail, and what it reads with nothing in it.
func (s ManagerSection) heading() string {
	if s == ManagerCalls {
		return "tools"
	}
	return "processes"
}

// ManagerState is where a row's thing stands. The words are the rail's.
type ManagerState int

const (
	// ManagerRunning is a process alive or a call not yet returned.
	ManagerRunning ManagerState = iota
	// ManagerExited is a process that ended by itself, with its code.
	ManagerExited
	// ManagerStopped is a process whose end was asked for.
	ManagerStopped
	// ManagerStoppedByYou is a call a person stopped here, still owed its
	// place in the round until the round ends.
	ManagerStoppedByYou
)

// ManagerRow is one process or one call, already resolved to what the screen
// draws.
type ManagerRow struct {
	Section ManagerSection
	// Key is what the host acts on: the process's name, the call's id.
	Key string
	// Name is the process's name or the tool's.
	Name string
	// Summary is the command a process runs, or a call's arguments in a line
	// in the receipt's words.
	Summary string
	State   ManagerState
	// Code is an exited process's exit code.
	Code int
	// Age is how long the thing has run: a process's uptime, or how long it
	// ran once it has ended, and a call's elapsed.
	Age time.Duration
	// Ended is when a process ended, zero while it runs.
	Ended time.Time
	// Where is where it runs: the host, or the container.
	Where string
	// Whole is what opening a call shows first: its arguments whole.
	Whole string
	// Output is what it has printed: the process tool's read of stdout, or
	// a command-shaped call's live output so far. Errors is stderr's read.
	Output string
	Errors string
}

// Word is the row's state in the words the rail draws it in.
func (r ManagerRow) Word() string {
	switch r.State {
	case ManagerExited:
		if r.Code < 0 {
			return OutcomeKilled
		}
		return fmt.Sprintf("exited %d", r.Code)
	case ManagerStopped:
		return OutcomeCancelled
	case ManagerStoppedByYou:
		return StateCancelled.WordBy("you")
	}
	return StateRunning.Word()
}

// Live reports whether the row can still be stopped.
func (r ManagerRow) Live() bool { return r.State == ManagerRunning }

// Failed reports a process that ended by itself and not well.
func (r ManagerRow) Failed() bool { return r.State == ManagerExited && r.Code != 0 }

// managerGlyph is the row's mark: the rail's for the same state.
func managerGlyph(r ManagerRow) string {
	switch {
	case r.Live():
		return StateRunning.Glyph()
	case r.Failed():
		return StateFailed.Glyph()
	case r.State == ManagerExited:
		return StateDone.Glyph()
	}
	return StateCancelled.Glyph()
}

// ManagerAct is what a confirmed stop asks the host to do.
type ManagerAct int

const (
	// ManagerNothing is no act.
	ManagerNothing ManagerAct = iota
	// ManagerStop ends the row's thing the gentle way: a process's
	// signal, grace and kill; a call's stop through its context.
	ManagerStop
	// ManagerKill ends it at once.
	ManagerKill
)

// ManagerResult is an act the screen hands the host, and the row it is on.
type ManagerResult struct {
	Act ManagerAct
	Row ManagerRow
}

// ManagerScreen is `/ps`: a take-over, full width, owning the keyboard while
// it is up.
type ManagerScreen struct {
	// The pointer is an index into Rows.
	listScreen[ManagerRow]
	// Rows are the processes, then the calls. The host hands them over again
	// at every paint, so the screen is live.
	Rows []ManagerRow
	// Opened is the key of the row open onto its output, empty while the
	// list is up.
	Opened string
	// offset is the first row of the opened view the pane shows.
	offset int
	// confirm is the question between `[d]` and the stop, and asking the
	// row it was asked about: a row that moves under the question is still
	// the one stopped.
	confirm *StopConfirm
	asking  ManagerRow
	// maxLines bounds the screen height. 0 is unbounded.
	maxLines int
	optAt    map[int]int
}

// Update is the screen's keyboard. It reports whether the screen is done and
// any stop a confirm let through.
func (s *ManagerScreen) Update(msg tea.KeyPressMsg) (done bool, result ManagerResult) {
	if s.confirm != nil {
		answered, answer := s.confirm.Update(msg)
		if !answered {
			return false, ManagerResult{}
		}
		s.confirm = nil
		switch answer {
		case StopCancel:
			return false, ManagerResult{Act: ManagerStop, Row: s.asking}
		case StopKill:
			return false, ManagerResult{Act: ManagerKill, Row: s.asking}
		}
		return false, ManagerResult{}
	}
	pressed := msg.String()
	if s.Opened != "" {
		switch {
		case keys.Is(pressed, keys.Agent.Back):
			s.Opened, s.offset = "", 0
		case keys.Is(pressed, keys.Agent.Move):
			s.offset = max(s.offset+keys.Step(pressed, keys.Agent.Move), 0)
		case keys.Is(pressed, keys.Agent.Stop):
			s.ask(s.opened())
		case keys.Is(pressed, keys.Screen.List):
			s.keys = !s.keys
		}
		return false, ManagerResult{}
	}
	switch {
	case s.moved(s.Rows, pressed, keys.Agent.Move):
	case keys.Is(pressed, keys.Agent.Attach):
		if row := s.current(s.Rows); row != nil {
			s.Opened, s.offset = row.Key, 0
		}
	case keys.Is(pressed, keys.Agent.Stop):
		s.ask(s.current(s.Rows))
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Agent.Back):
		return true, ManagerResult{}
	}
	return false, ManagerResult{}
}

// ask arms the stop's question over a row that can still be stopped, and is
// silent over one that cannot, the way the agent manager's `[d]` is over a
// row that is not an agent.
func (s *ManagerScreen) ask(row *ManagerRow) {
	if row == nil || !row.Live() {
		return
	}
	s.asking = *row
	what := "process"
	if row.Section == ManagerCalls {
		what = "call"
	}
	s.confirm = &StopConfirm{Prompt: "stop the " + what + " " + row.Name + "?", Yes: "stop"}
}

// opened is the row the opened view is on, nil once it has left the list.
func (s *ManagerScreen) opened() *ManagerRow {
	for i := range s.Rows {
		if s.Rows[i].Key == s.Opened && s.Opened != "" {
			return &s.Rows[i]
		}
	}
	return nil
}

// Reading is the key of the row whose output the screen shows: the one
// opened, or the one under the pointer.
func (s *ManagerScreen) Reading() string {
	if s.Opened != "" {
		return s.Opened
	}
	if row := s.current(s.Rows); row != nil {
		return row.Key
	}
	return ""
}

// FocusSection puts the pointer on the first row of a section, and reports whether
// there was one — the rail's CALLS heading opens on the calls.
func (s *ManagerScreen) FocusSection(sec ManagerSection) bool {
	for i, r := range s.Rows {
		if r.Section == sec {
			s.Focus = i
			return true
		}
	}
	return false
}

// SetSize gives the screen the terminal's rectangle.
func (s *ManagerScreen) SetSize(_, height int) { s.maxLines = height }

// View renders the screen: the list and its preview, or the row opened.
func (s *ManagerScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	if s.Opened == "" {
		return s.view(width, s)
	}
	s.sync()
	return s.chrome(width).view(width, func(budget int) []string { return s.openedRows(width, budget) })
}

// chrome is the header over the body and the keys under it; the confirm
// takes the foot row while it is up.
func (s *ManagerScreen) chrome(width int) screenChrome {
	foot := s.footer(s.offers(width), s.keyList(), s.footField())
	if s.confirm != nil {
		foot.taken = s.confirm.View(width)
	}
	return screenChrome{header: s.header(), foot: foot.rows(width), maxLines: s.maxLines}
}

// panes is the list beside the row under the pointer.
func (s *ManagerScreen) panes() screenPanes {
	return screenPanes{
		stackAt: managerStackWidth, listMin: managerListMin,
		listMax: managerListMax, minPreview: managerMinPreview,
		list: func(width, budget int) []string {
			body, _ := s.list.visibleRows(cardWidthFor(width), budget, false)
			return body
		},
		preview: s.previewRows,
	}
}

// sync rebuilds the list from Rows: each section under its heading, and a
// section with nothing in it saying so on its heading, so an empty list of
// calls is read as nothing running rather than as a list that failed to load.
func (s *ManagerScreen) sync() {
	s.clamp(len(s.Rows))
	s.optAt = make(map[int]int, len(s.Rows))
	opts := make([]SelectOption, 0, len(s.Rows)+2)
	for _, sec := range []ManagerSection{ManagerProcesses, ManagerCalls} {
		n := 0
		for _, r := range s.Rows {
			if r.Section == sec {
				n++
			}
		}
		label := sec.heading()
		if n == 0 {
			label += " · nothing running"
		}
		opts = append(opts, SelectOption{Label: label, Header: true})
		for i, r := range s.Rows {
			if r.Section != sec {
				continue
			}
			s.optAt[i] = len(opts)
			opts = append(opts, r.option())
		}
	}
	at := 0
	if len(s.Rows) > 0 {
		at = s.optAt[s.Focus]
	}
	s.show(opts, len(s.Rows), at)
}

// option is a row as the list draws it: the mark and the name, the state
// word, and the age and where it runs.
func (r ManagerRow) option() SelectOption {
	tone := ToneQuiet
	switch {
	case r.Live():
		tone = ToneNeutral
	case r.Failed():
		tone = ToneRisk
	}
	opt := SelectOption{Label: managerGlyph(r) + " " + r.Name, Value: r.Word(), valueTone: tone}
	detail := []DetailSpan{{Text: FormatElapsed(r.Age), Tone: ToneQuiet}}
	if r.Where != "" {
		detail = append(detail, DetailSpan{Text: " · " + r.Where, Tone: ToneQuiet})
	}
	opt.Detail = detail
	return opt
}

// previewRows is the right pane: the row under the pointer, accounted for.
func (s *ManagerScreen) previewRows(width int) []string {
	row := s.current(s.Rows)
	if row == nil {
		return []string{sty.dim.Render(Clip("nothing selected", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(row.Name), sty.dim.Render(row.Word()), width), ""}
	rows = append(rows, s.account(*row, width)...)
	if last := lastLine(row.Output); last != "" {
		rows = append(rows, managerField("last line", last, width)...)
	}
	return rows
}

// account is what the row is, field by field: the same whichever view it is
// read in.
func (s *ManagerScreen) account(row ManagerRow, width int) []string {
	var rows []string
	if row.Section == ManagerProcesses {
		rows = append(rows, managerField("command", row.Summary, width)...)
	} else {
		rows = append(rows, managerField("call", row.Summary, width)...)
	}
	age := "up"
	if row.Section == ManagerCalls {
		age = "running for"
	}
	if !row.Live() {
		age = "ran for"
	}
	rows = append(rows, managerField(age, FormatElapsed(row.Age), width)...)
	if row.Where != "" {
		rows = append(rows, managerField("runs on", row.Where, width)...)
	}
	return rows
}

// openedRows is the opened view: the account, then what the thing has said,
// windowed and scrolled like a reading.
func (s *ManagerScreen) openedRows(width, budget int) []string {
	row := s.opened()
	if row == nil {
		return []string{sty.dim.Render(Clip("it has left the list", width))}
	}
	rows := []string{paneTitle(brightStyle().Render(row.Name), sty.dim.Render(row.Word()), width), ""}
	rows = append(rows, s.account(*row, width)...)
	if row.Section == ManagerCalls {
		rows = append(rows, "", "  "+sty.status.Render("arguments"))
		rows = append(rows, managerBody(row.Whole, "no arguments", width)...)
		rows = append(rows, "", "  "+sty.status.Render("output so far"))
		rows = append(rows, managerBody(row.Output, "this tool shows nothing until it returns", width)...)
	} else {
		rows = append(rows, "", "  "+sty.status.Render("stdout"))
		rows = append(rows, managerBody(row.Output, "nothing yet", width)...)
		rows = append(rows, "", "  "+sty.status.Render("stderr"))
		rows = append(rows, managerBody(row.Errors, "nothing yet", width)...)
	}
	if budget <= 0 || len(rows) <= budget {
		s.offset = 0
		return rows
	}
	p := Pager{Offset: s.offset, Height: max(budget-1, 1)}
	shown := p.Window(rows)
	s.offset = p.Offset
	if below := len(rows) - p.Offset - len(shown); below > 0 {
		shown = append(shown, sty.dim.Render(Clip(fmt.Sprintf("↓ %d more %s", below, plainPlural(below, "row", "rows")), width)))
	} else if p.Offset > 0 {
		shown = append([]string{sty.dim.Render(Clip(fmt.Sprintf("↑ %d more %s", p.Offset, plainPlural(p.Offset, "row", "rows")), width))}, shown[1:]...)
	}
	return shown
}

// managerField is one entry of the account: a dim label in a fixed column and
// the value beside it, wrapped under itself, as the tools screen lays its
// account out.
func managerField(label, value string, width int) []string {
	lead := "  " + sty.status.Render(fmt.Sprintf("%-*s", managerLabelWidth+1, label))
	indent := strings.Repeat(" ", 3+managerLabelWidth)
	var rows []string
	for i, part := range wrapPlain(value, max(width-len(indent), 8)) {
		if i == 0 {
			rows = append(rows, Clip(lead+sty.body.Render(part), width))
			continue
		}
		rows = append(rows, Clip(indent+sty.body.Render(part), width))
	}
	return rows
}

// managerBody is a block of output, each line clipped under an indent, or a
// dim sentence where there is none.
func managerBody(text, none string, width int) []string {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return []string{Clip("    "+sty.dim.Render(none), width)}
	}
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		rows = append(rows, Clip("    "+sty.body.Render(line), width))
	}
	return rows
}

// lastLine is the last line with anything on it.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}

// header names the surface and counts what is running in each section.
func (s *ManagerScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/ps")}, keys: s.headerKeys(keys.Screen.List, keys.Agent.Back)}
	procs, calls := 0, 0
	for _, r := range s.Rows {
		if !r.Live() {
			continue
		}
		if r.Section == ManagerCalls {
			calls++
		} else {
			procs++
		}
	}
	h.left = append(h.left, screenField(plainPlural(procs, "1 process running", fmt.Sprintf("%d processes running", procs))),
		screenField(plainPlural(calls, "1 call running", fmt.Sprintf("%d calls running", calls))))
	return h
}

// offers is the key row: open and stop where the row takes them, and the way
// out, which in the opened view is back to the list.
func (s *ManagerScreen) offers(width int) []KeyOffer {
	if s.Opened != "" {
		var acts []KeyOffer
		if row := s.opened(); row != nil && row.Live() {
			acts = append(acts, keyOfferAs(keys.Agent.Stop, "stop"))
		}
		acts = append(acts, keyOfferAs(keys.Agent.Back, "back to the list"))
		return offersBeside(keyOfferAs(keys.Agent.Move, "scroll"), acts, "", width)
	}
	var acts []KeyOffer
	if row := s.current(s.Rows); row != nil {
		acts = append(acts, keyOfferAs(keys.Agent.Attach, "open"))
		if row.Live() {
			acts = append(acts, keyOfferAs(keys.Agent.Stop, "stop"))
		}
	}
	acts = append(acts, wayOut(backToPrompt))
	return offersBeside(keyOffer(keys.Agent.Move), acts, s.footField(), width)
}

// keyList is every key the screen has, for `[?]`.
func (s *ManagerScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Agent.Move, "move between rows, or scroll an opened one"),
		keyOfferAs(keys.Agent.Attach, "open the row: a process's output and errors, a call's arguments and output"),
		keyOfferAs(keys.Agent.Stop, "stop it — after asking, where "+keys.Bracket(keys.Agent.Kill)+" kills it instead"),
		keyOfferAs(keys.Agent.Back, EscBack),
	}
}

// footField says what stopping one thing leaves alone.
func (s *ManagerScreen) footField() string {
	if s.Opened != "" {
		return ""
	}
	for _, r := range s.Rows {
		if r.Section == ManagerCalls {
			return "a stopped call leaves the rest of its round running"
		}
	}
	return ""
}
