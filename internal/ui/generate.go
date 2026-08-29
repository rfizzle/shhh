package ui

// The one-shot result surface (S-113,
// docs/interface/surfaces.md#the-one-shot-result) — the whole interface of
// command generation (docs/capabilities/generation.md). Most people meet shhh
// here rather than in the agent, and until now this screen printed a command
// and waited for a keystroke: no explanation unless you remembered `-e`, no
// statement of what the command could reach, and the same default key whether
// the answer was `ls` or `rm -rf`. Safe-if-you-remember-a-flag is not safe by
// default.
//
// Four things changed. The explanation is on by default, as one line — the
// flag now buys the long form. A containment line states the command's reach
// from the same resolver the approval cards use (internal/radius), so the
// front door and the session agree about what a command is. The action bar is
// a row of keys rather than a menu. And on a destructive command the safe
// default moves: enter spends itself saying what would be affected, `[d]`
// runs the command's own no-op form where one exists, and running takes a
// deliberate `y`.
//
// S-114 added the fifth: the commands the generator did not pick. It weighed
// lsof against netstat before answering; `[a]` is where that goes instead of
// being thrown away, each one carrying the phrase that says why you might
// take it. They are an offer, never a requirement — a response without them
// is the surface exactly as it was.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/dryrun"
	"github.com/rfizzle/shhh/internal/preflight"
	"github.com/rfizzle/shhh/internal/proposal"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

type phase int

const (
	phaseStreaming phase = iota
	phaseAction
	phaseRevise
	phaseEdit
	phaseExplain
	phaseSave
	phaseDryRun
	phasePick
	phaseDone
)

// ExplainMode is how much explaining this run does. The default is Brief:
// a command you do not understand is a command you should not run, and one
// line is what fits under it without pushing the keys off the screen.
type ExplainMode int

const (
	// ExplainNone is `--silent` and `behavior.silent_mode`.
	ExplainNone ExplainMode = iota
	// ExplainBrief is the default — one line under the command.
	ExplainBrief
	// ExplainLong is `-e`/`--explain`, and what `[x]` asks for on demand.
	ExplainLong
)

type NewStreamFunc func(messages []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error)

// ExplainStreamFunc streams an explanation of command. long selects the
// flag's paragraph form over the default one-liner.
type ExplainStreamFunc func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error)

// DryRunFunc runs a command that has already been rewritten into its no-op
// form and reports what it said. It is a field rather than a call so the
// tests never reach the shell.
type DryRunFunc func(command string) (output string, exitCode int)

const maxPreflightRetries = 2

// dryRunTimeout bounds a no-op form that turns out not to be one — a `make
// -n` over a generated Makefile can still take a while, and the surface has
// to come back.
const dryRunTimeout = 30 * time.Second

// dryRunLines is how much of a dry run's output the surface keeps. What is
// past it is counted, never dropped silently.
const dryRunLines = 20

type GenerateModel struct {
	stream           StreamModel
	actionBar        ActionBarModel
	reviseInput      textinput.Model
	editInput        textinput.Model
	saveInput        textinput.Model
	explainStream    StreamModel
	messages         []provider.Message
	newStream        NewStreamFunc
	newExplain       ExplainStreamFunc
	runDry           DryRunFunc
	phase            phase
	result           GenerateResult
	shell            string
	preflightRetries int
	explainMode      ExplainMode
	// shown is the form of the explanation currently on screen, which is not
	// always the configured one: `[x]` asks for the long form of a run that
	// defaulted to brief.
	shown ExplainMode
	// explaining is a brief explanation still arriving under the action bar.
	// The keys stay live while it does — the bar is not worth blocking for
	// one sentence.
	explaining bool
	// checking is a preflight check that is out. The stream it is a check of
	// stays done while it runs, so without this the surface would ask again
	// on every message that arrives.
	checking bool
	// opening is a stream that has been asked for and has not come back. The
	// spinner turns on it: it is the surface saying it is waiting on a round
	// trip rather than on the reader.
	opening bool
	// gen counts the times this surface has asked for a stream. Opening one
	// is a request that outlives the keystroke that asked for it, and the
	// screen can move on while it is out: a revise, a step back, a different
	// alternative. The answer carries the gen it was asked under, and one
	// that no longer matches is an answer about a command nobody is looking
	// at.
	gen int
	// reach is the resolved radius of the command on screen (S-101).
	reach radius.Command
	// dryCommand is the command's no-op form, and dryAvailable whether it has
	// one at all. Without one, `[d]` is not offered.
	dryCommand   string
	dryAvailable bool
	dryOutput    string
	dryFailed    bool
	// affected is whether enter has been spent on the radius block, which is
	// what a destructive command's enter does instead of running.
	affected bool
	// danger is whether the safe default has moved for this command.
	danger bool
	// past is the revise chain, most recent last: what `[u]` steps back to.
	past []pastCommand
	// choices is every command this generation offered, the one it led with
	// first (S-114). It always holds at least the command on screen, so the
	// picker and the key row count from the same place.
	choices []proposal.Choice
	// chosen is which of them the surface is showing.
	chosen int
	// pick is the alternatives picker while it is open — the same select card
	// the session pickers use (S-078).
	pick *components.Select
}

// pastCommand is one rung of the revise ladder — the command that was on
// screen, the feedback that replaced it, and enough of the conversation to
// put both back.
type pastCommand struct {
	command  string
	feedback string
	explain  string
	shown    ExplainMode
	// messages is how long the conversation was before the revise added to
	// it, so stepping back drops exactly what the revise appended.
	messages int
	// choices and chosen are the offers that came with that command. A
	// revise generated its own set, so stepping back has to put the old ones
	// back or `[a]` would open on commands nobody is looking at.
	choices []proposal.Choice
	chosen  int
}

type GenerateResult struct {
	Command  string
	Action   Action
	Feedback string
	SaveName string
	// Confirmed is set when the surface already took a deliberate `y` for a
	// destructive command, so the caller does not ask the same question a
	// second time.
	Confirmed bool
	Cancelled bool
	Err       error
}

func NewGenerateModel(events <-chan provider.StreamEvent, cancel context.CancelFunc, messages []provider.Message, newStream NewStreamFunc, newExplain ExplainStreamFunc, shell string) GenerateModel {
	ti := textinput.New()
	ti.Placeholder = "Describe what to change…"
	ti.CharLimit = 500
	ei := textinput.New()
	ei.CharLimit = 1000
	si := textinput.New()
	si.Placeholder = "Snippet name…"
	si.CharLimit = 100
	msgs := make([]provider.Message, len(messages))
	copy(msgs, messages)
	return GenerateModel{
		stream:      NewStreamModel(events, cancel),
		actionBar:   NewActionBarModel(),
		reviseInput: ti,
		editInput:   ei,
		saveInput:   si,
		messages:    msgs,
		newStream:   newStream,
		newExplain:  newExplain,
		runDry:      shellDryRun,
		phase:       phaseStreaming,
		shell:       shell,
		explainMode: ExplainBrief,
	}
}

// WithExplain sets how much explaining this run does.
func (m GenerateModel) WithExplain(mode ExplainMode) GenerateModel {
	m.explainMode = mode
	return m
}

// WithDryRun replaces how `[d]` executes a no-op form.
func (m GenerateModel) WithDryRun(f DryRunFunc) GenerateModel {
	m.runDry = f
	return m
}

func (m GenerateModel) Result() GenerateResult       { return m.result }
func (m GenerateModel) Phase() phase                 { return m.phase }
func (m GenerateModel) Messages() []provider.Message { return m.messages }

// Reach is the resolved radius of the command on screen, for callers that
// need what the containment line states.
func (m GenerateModel) Reach() radius.Command { return m.reach }

func (m GenerateModel) Init() tea.Cmd {
	return m.stream.Init()
}

func (m GenerateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A stream that has finished opening is answered wherever the surface
	// has got to, not only in the phase that asked: the whole point of not
	// waiting is that the screen was free to move.
	switch msg := msg.(type) {
	case explainReadyMsg:
		return m.explainReady(msg)
	case streamReadyMsg:
		return m.streamReady(msg)
	case preflightDoneMsg:
		return m.preflightDone(msg)
	}

	switch m.phase {
	case phaseStreaming:
		return m.updateStreaming(msg)
	case phaseAction:
		return m.updateAction(msg)
	case phaseRevise:
		return m.updateRevise(msg)
	case phaseEdit:
		return m.updateEdit(msg)
	case phaseSave:
		return m.updateSave(msg)
	case phaseExplain:
		return m.updateExplain(msg)
	case phaseDryRun:
		return m.updateDryRun(msg)
	case phasePick:
		return m.updatePick(msg)
	}
	return m, nil
}

func (m GenerateModel) updateSave(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyEnter:
			name := strings.TrimSpace(m.saveInput.Value())
			if name == "" {
				return m, nil
			}
			m.phase = phaseDone
			m.result = GenerateResult{
				Command:  m.stream.Output(),
				Action:   ActionSave,
				SaveName: name,
			}
			return m, tea.Quit
		case tea.KeyEscape:
			m.saveInput.Blur()
			m.phase = phaseAction
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.saveInput, cmd = m.saveInput.Update(msg)
	return m, cmd
}

func (m GenerateModel) updateStreaming(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.stream, cmd = m.stream.Update(msg)

	if m.stream.Done() {
		if m.stream.Cancelled() || m.stream.Err() != nil {
			m.phase = phaseDone
			m.result = GenerateResult{
				// A cancelled stream reports what it had of the command, not
				// what it had of the response: the section after the sentinel
				// is never something a caller should copy or run.
				Command:   proposal.CommandPart(m.stream.Output()),
				Cancelled: m.stream.Cancelled(),
				Err:       m.stream.Err(),
			}
			return m, tea.Quit
		}

		// The check is already out; the stream stays done and there is
		// nothing to do again until it answers.
		if m.checking {
			return m, cmd
		}

		// What arrived is a proposal, not a bare command: everything before
		// the sentinel is the command, and what follows is the offers. A
		// response without the section parses to one choice, which is the
		// fence-stripping path this always was.
		raw := m.stream.Output()
		choices := proposal.Parse(raw)
		output := choices[0].Command

		if m.shell != "" && m.preflightRetries < maxPreflightRetries && m.newStream != nil {
			// Checking the command spawns a shell and walks PATH, and both
			// happen on whatever machine this is: a crowded PATH, a shell
			// with a startup file, a home directory over a network mount.
			// Inline in Update that is the loop stopped again (S-133), so
			// the check goes where the requests went.
			m.gen++
			m.checking = true
			return m, runPreflight(output, raw, choices, m.shell, m.gen)
		}

		return m.accept(raw, choices)
	}
	return m, cmd
}

// accept takes a response the surface is going to show: the conversation
// keeps the raw response, alternatives and all, because a revise is a reply
// to what the model actually said and the format it answered in is part of
// that.
func (m GenerateModel) accept(raw string, choices []proposal.Choice) (GenerateModel, tea.Cmd) {
	m.messages = append(m.messages, provider.Message{
		Role:    provider.RoleAssistant,
		Content: raw,
	})
	m.choices, m.chosen = choices, 0
	m.stream = m.stream.WithOutput(choices[0].Command)
	return m.arm(choices[0].Command)
}

// preflightDone carries a check that has finished, along with the response it
// was a check of — the surface moved on from Update while it ran and the
// answer has to bring its own subject.
func (m GenerateModel) preflightDone(msg preflightDoneMsg) (GenerateModel, tea.Cmd) {
	if msg.gen != m.gen {
		return m, nil
	}
	m.checking = false
	if msg.result.OK {
		return m.accept(msg.raw, msg.choices)
	}
	m.preflightRetries++
	correction := fmt.Sprintf(
		"That command has errors:\n%s\n\nPlease fix and output only the corrected command(s).",
		strings.Join(msg.result.Errors, "\n"),
	)
	m.messages = append(m.messages,
		provider.Message{Role: provider.RoleAssistant, Content: msg.raw},
		provider.Message{Role: provider.RoleUser, Content: correction},
	)
	m.gen++
	m.stream = pendingStream()
	m.opening = true
	return m, tea.Batch(m.stream.spinner.Tick, openStream(m.newStream, m.messages, m.gen))
}

// arm resolves everything the result surface states about a freshly generated
// command and starts whichever explanation this run asked for. It is the one
// place the surface is built, so a revise, an edit and a first generation all
// land on the same screen.
func (m GenerateModel) arm(output string) (GenerateModel, tea.Cmd) {
	m.gen++
	m.reach = radius.Resolve(output)
	m.danger = m.reach.Level == radius.High
	m.dryCommand, m.dryAvailable = dryrun.Derive(output)
	m.affected = false
	m.dryOutput, m.dryFailed = "", false
	m.explaining = false
	m.shown = ExplainNone
	m.explainStream = StreamModel{}
	m.actionBar = m.actionBar.
		SetMulti(IsMultiCommand(output)).
		SetDanger(m.danger).
		SetDryRun(m.dryAvailable).
		SetAffected(false).
		SetRevision(len(m.past)).
		SetAlternatives(m.others()).
		Reset()
	m.phase = phaseAction
	m.pick = nil

	if m.newExplain == nil || m.explainMode == ExplainNone {
		return m, nil
	}
	long := m.explainMode == ExplainLong
	m.shown = m.explainMode
	m.explainStream = pendingStream()
	if long {
		// The long form is a block, and reading it is the whole point of
		// asking for it: it gets the screen until it is done, as `-e` always
		// did.
		m.phase = phaseExplain
	} else {
		// One line arrives under a live action bar. Blocking the keys for a
		// sentence would make the default worse than the flag — and waiting
		// for the request to open blocks them exactly as hard as waiting for
		// the sentence, which is what the bar used to do.
		m.explaining = true
	}
	m.opening = true
	return m, tea.Batch(m.explainStream.spinner.Tick, openExplain(m.newExplain, output, long, m.gen))
}

// explainReady installs an explanation stream that has finished opening.
func (m GenerateModel) explainReady(msg explainReadyMsg) (GenerateModel, tea.Cmd) {
	if msg.gen != m.gen {
		// Asked for a command that is no longer on screen. Nothing here
		// wants it, and the request behind it should stop.
		if msg.cancel != nil {
			msg.cancel()
		}
		return m, nil
	}
	m.opening = false
	if msg.err != nil {
		// A surface that cannot explain itself still has to be usable; the
		// keys are what the reader came for.
		m.explaining = false
		if msg.long {
			m.explainStream = m.explainStream.WithOutput("Error: " + msg.err.Error())
			m.explainStream.done = true
			m.phase = phaseAction
			return m, nil
		}
		m.shown = ExplainNone
		m.explainStream = StreamModel{}
		return m, nil
	}
	m.explainStream = NewStreamModel(msg.events, msg.cancel)
	return m, m.explainStream.Init()
}

// streamReady installs a command stream that has finished opening. Both the
// paths that ask for one — a preflight correction and a revise — end a
// failure the same way, so there is one answer to it.
func (m GenerateModel) streamReady(msg streamReadyMsg) (GenerateModel, tea.Cmd) {
	if msg.gen != m.gen {
		if msg.cancel != nil {
			msg.cancel()
		}
		return m, nil
	}
	m.opening = false
	if msg.err != nil {
		m.phase = phaseDone
		m.result = GenerateResult{Err: msg.err}
		return m, tea.Quit
	}
	m.stream = NewStreamModel(msg.events, msg.cancel)
	m.phase = phaseStreaming
	return m, m.stream.Init()
}

func (m GenerateModel) updateAction(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A brief explanation is still arriving: everything that is not a
	// keystroke belongs to it, and the keys stay the action bar's.
	if _, isKey := msg.(tea.KeyPressMsg); !isKey && m.explaining {
		var cmd tea.Cmd
		m.explainStream, cmd = m.explainStream.Update(msg)
		if m.explainStream.Done() {
			m.explaining = false
		}
		return m, cmd
	}

	var cmd tea.Cmd
	m.actionBar, cmd = m.actionBar.Update(msg)

	switch m.actionBar.Selected() {
	case ActionAffected:
		m.affected = true
		m.actionBar = m.actionBar.SetAffected(true).Reset()
		return m, nil

	case ActionDryRun:
		if !m.dryAvailable || m.runDry == nil {
			m.actionBar = m.actionBar.Reset()
			return m, nil
		}
		m.actionBar = m.actionBar.Reset()
		m.phase = phaseDryRun
		return m, m.dryRunCmd()

	case ActionBack:
		if len(m.past) == 0 {
			m.actionBar = m.actionBar.Reset()
			return m, nil
		}
		return m.stepBack()

	case ActionAlternatives:
		m.actionBar = m.actionBar.Reset()
		if m.others() == 0 {
			return m, nil
		}
		m = m.hushExplain()
		return m.openAlternatives()

	case ActionEdit:
		m = m.hushExplain()
		m.phase = phaseEdit
		m.editInput.SetValue(m.stream.Output())
		m.editInput.CursorEnd()
		blink := m.editInput.Focus()
		m.actionBar = m.actionBar.Reset()
		return m, blink

	case ActionExplain:
		m = m.hushExplain()
		m.actionBar = m.actionBar.Reset()
		if m.newExplain == nil {
			return m, nil
		}
		m.gen++
		m.explainStream = pendingStream()
		m.shown = ExplainLong
		m.phase = phaseExplain
		m.opening = true
		return m, tea.Batch(m.explainStream.spinner.Tick, openExplain(m.newExplain, m.stream.Output(), true, m.gen))

	case ActionSave:
		m = m.hushExplain()
		m.phase = phaseSave
		m.saveInput.Reset()
		blink := m.saveInput.Focus()
		m.actionBar = m.actionBar.Reset()
		return m, blink

	case ActionRevise:
		m = m.hushExplain()
		m.phase = phaseRevise
		m.reviseInput.Reset()
		blink := m.reviseInput.Focus()
		m.actionBar = m.actionBar.Reset()
		return m, blink
	}

	sel := m.actionBar.Selected()
	if sel == ActionRun || sel == ActionRunAll || sel == ActionRunStep ||
		sel == ActionCopy || sel == ActionCancel {
		m = m.hushExplain()
		m.phase = phaseDone
		m.result = GenerateResult{
			Command: m.stream.Output(),
			Action:  sel,
			// In danger mode enter is spent on the radius, so a run is a run
			// only because `y` was pressed. Step-by-step comes from `[t]`,
			// which asked nothing, and keeps the caller's own prompt.
			Confirmed: m.danger && (sel == ActionRun || sel == ActionRunAll),
		}
		return m, tea.Quit
	}
	return m, cmd
}

// hushExplain stops a brief explanation that is still arriving. Two live
// streams would answer to the same message types, so the one nobody is
// waiting for goes first.
func (m GenerateModel) hushExplain() GenerateModel {
	if m.explaining {
		// A stream still being opened has nothing to cancel yet; the gen it
		// was asked under is what stops its answer from landing.
		if m.explainStream.cancel != nil {
			m.explainStream.cancel()
		}
		m.explaining = false
	}
	return m
}

// stepBack undoes one revise: the command, the explanation that went with it
// and the conversation all return to where they were.
func (m GenerateModel) stepBack() (GenerateModel, tea.Cmd) {
	m = m.hushExplain()
	m.gen++
	last := m.past[len(m.past)-1]
	m.past = m.past[:len(m.past)-1]
	if last.messages <= len(m.messages) {
		m.messages = m.messages[:last.messages]
	}
	m.choices, m.chosen = last.choices, last.chosen
	m.stream = m.stream.WithOutput(last.command)
	m.reach = radius.Resolve(last.command)
	m.danger = m.reach.Level == radius.High
	m.dryCommand, m.dryAvailable = dryrun.Derive(last.command)
	m.affected = false
	m.dryOutput, m.dryFailed = "", false
	// The explanation is restored rather than asked for again: it was said
	// about this exact command and nothing about it has changed.
	m.explainStream = StreamModel{}.WithOutput(last.explain)
	m.explainStream.done = last.explain != ""
	m.shown = last.shown
	m.actionBar = m.actionBar.
		SetMulti(IsMultiCommand(last.command)).
		SetDanger(m.danger).
		SetDryRun(m.dryAvailable).
		SetAffected(false).
		SetRevision(len(m.past)).
		SetAlternatives(m.others()).
		Reset()
	m.phase = phaseAction
	m.pick = nil
	return m, nil
}

// others is how many commands are on offer beside the one showing.
func (m GenerateModel) others() int {
	if len(m.choices) < 2 {
		return 0
	}
	return len(m.choices) - 1
}

// alternativesWidth is what the picker renders at. Like the failure report,
// the one-shot has no layout of its own to measure.
const alternativesWidth = 88

// openAlternatives shows every command this generation offered, the one on
// screen marked. It is the generic select card (S-078) rather than a list
// this surface draws itself, so moving, choosing and backing out are the keys
// they are everywhere else.
func (m GenerateModel) openAlternatives() (GenerateModel, tea.Cmd) {
	opts := make([]components.SelectOption, 0, len(m.choices))
	for i, c := range m.choices {
		label := "  " + oneLine(c.Command)
		if i == m.chosen {
			// The mark is a glyph in the label, not the focus bar: the reader
			// has to be able to find the current command without moving the
			// pointer onto it (invariant 1).
			label = "◆ " + oneLine(c.Command)
		}
		desc := c.Tradeoff
		if i == m.chosen && desc == "" {
			desc = "the command on screen"
		}
		opts = append(opts, components.SelectOption{Label: label, Desc: desc})
	}
	m.pick = &components.Select{
		Title: "Alternatives",
		// Numbers would be a third way to say the same thing on a list of
		// three rows, and the artboard's row is ↑↓ and enter.
		Unnumbered: true,
		Options:    opts,
		Focus:      m.chosen,
		Hint:       "↑↓ move · enter choose · esc back",
	}
	m.phase = phasePick
	return m, nil
}

// updatePick routes keys while the alternatives are showing. Choosing one
// makes it the command on screen and hands the surface back to the key row —
// it does not run: an alternative deserves the same explanation, containment
// line and default the primary got.
func (m GenerateModel) updatePick(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || m.pick == nil {
		return m, nil
	}
	done, result := m.pick.Update(key)
	if !done {
		return m, nil
	}
	sel := result.(components.SelectResult)
	m.pick = nil
	if sel.Canceled || sel.Index < 0 || sel.Index >= len(m.choices) {
		m.phase = phaseAction
		return m, nil
	}
	if sel.Index == m.chosen {
		m.phase = phaseAction
		return m, nil
	}
	m.chosen = sel.Index
	command := m.choices[m.chosen].Command
	m.stream = m.stream.WithOutput(command)
	// The conversation follows the screen: a revise from here is a revise of
	// the command the user chose, not of the one the model led with.
	if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == provider.RoleAssistant {
		m.messages[len(m.messages)-1].Content = command
	}
	return m.arm(command)
}

// oneLine folds a multi-command answer onto the single row a picker has for
// it. The rows are what is being compared, and the command itself is on the
// screen the picker returns to.
func oneLine(command string) string {
	return strings.Join(SplitCommands(command), " ; ")
}

// Opening a stream is not free and it is not instant. For every provider in
// the OpenAI family, StreamCompletion sends the request and blocks until the
// model has started answering — the whole time-to-first-token — before it
// returns a channel. Doing that inline in Update stops the event loop, and a
// loop that is stopped cannot paint: the command sat alone on screen for the
// length of the explanation's round trip, and the action bar arrived when
// that request did. So the surface asks, says so, and carries on (S-132).
type explainReadyMsg struct {
	gen    int
	long   bool
	events <-chan provider.StreamEvent
	cancel context.CancelFunc
	err    error
}

type streamReadyMsg struct {
	gen    int
	events <-chan provider.StreamEvent
	cancel context.CancelFunc
	err    error
}

func openExplain(f ExplainStreamFunc, command string, long bool, gen int) tea.Cmd {
	return func() tea.Msg {
		events, cancel, err := f(command, long)
		return explainReadyMsg{gen: gen, long: long, events: events, cancel: cancel, err: err}
	}
}

// openStream copies the conversation because the model that asked keeps
// mutating its own copy — a revise appends to it the moment this returns.
type preflightDoneMsg struct {
	gen     int
	raw     string
	choices []proposal.Choice
	result  preflight.Result
}

// runPreflight checks a command off the event loop. It carries the response
// it checked so the answer needs nothing from the model it left behind.
func runPreflight(command, raw string, choices []proposal.Choice, shell string, gen int) tea.Cmd {
	return func() tea.Msg {
		return preflightDoneMsg{
			gen:     gen,
			raw:     raw,
			choices: choices,
			result:  preflight.Check(command, shell),
		}
	}
}

func openStream(f NewStreamFunc, messages []provider.Message, gen int) tea.Cmd {
	msgs := make([]provider.Message, len(messages))
	copy(msgs, messages)
	return func() tea.Msg {
		events, cancel, err := f(msgs)
		return streamReadyMsg{gen: gen, events: events, cancel: cancel, err: err}
	}
}

// pendingStream is what stands in for a stream that has been asked for and
// has not arrived. It has no events to wait on — the ready message brings
// those — but it has the spinner, so the wait says it is a wait.
func pendingStream() StreamModel {
	streamSeq++
	return StreamModel{id: streamSeq, spinner: components.NewSpinnerModel()}
}

type dryRunDoneMsg struct {
	output string
	code   int
}

// dryRunCmd runs the derived no-op form off the UI goroutine.
func (m GenerateModel) dryRunCmd() tea.Cmd {
	run, command := m.runDry, m.dryCommand
	return func() tea.Msg {
		out, code := run(command)
		return dryRunDoneMsg{output: out, code: code}
	}
}

// shellDryRun is the default execution of a no-op form: the user's own shell,
// output captured rather than inherited, bounded in time.
func shellDryRun(command string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), dryRunTimeout)
	defer cancel()
	return runner.RunCapture(ctx, command)
}

func (m GenerateModel) updateDryRun(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dryRunDoneMsg:
		m.dryOutput = strings.TrimRight(msg.output, "\n")
		m.dryFailed = msg.code != 0
		if strings.TrimSpace(m.dryOutput) == "" {
			m.dryOutput = "it reported nothing — the dry run found no work to do"
		}
		m.phase = phaseAction
		return m, nil
	case tea.KeyPressMsg:
		// The dry run is already running as its own process; esc only stops
		// waiting for it on screen once it lands.
		return m, nil
	}
	return m, nil
}

func (m GenerateModel) updateRevise(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyEnter:
			feedback := m.reviseInput.Value()
			if feedback == "" {
				return m, nil
			}
			m.past = append(m.past, pastCommand{
				command:  m.stream.Output(),
				feedback: feedback,
				explain:  m.explainStream.Output(),
				shown:    m.shown,
				messages: len(m.messages),
				choices:  m.choices,
				chosen:   m.chosen,
			})
			m.messages = append(m.messages, provider.Message{
				Role:    provider.RoleUser,
				Content: feedback,
			})
			if m.newStream == nil {
				m.phase = phaseDone
				m.result = GenerateResult{
					Command:  m.stream.Output(),
					Action:   ActionRevise,
					Feedback: feedback,
				}
				return m, tea.Quit
			}
			m.gen++
			m.stream = pendingStream()
			m.phase = phaseStreaming
			m.opening = true
			return m, tea.Batch(m.stream.spinner.Tick, openStream(m.newStream, m.messages, m.gen))
		case tea.KeyEscape:
			m.phase = phaseAction
			m.reviseInput.Blur()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.reviseInput, cmd = m.reviseInput.Update(msg)
	return m, cmd
}

func (m GenerateModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyEnter:
			edited := m.editInput.Value()
			if edited == "" {
				return m, nil
			}
			m.stream = m.stream.WithOutput(edited)
			if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == provider.RoleAssistant {
				m.messages[len(m.messages)-1].Content = edited
			}
			m.editInput.Blur()
			// An edit rewrites the choice it started from, and takes its
			// tradeoff with it: the phrase was said about a command that no
			// longer exists. The other offers stand — they were alternatives
			// to the request, not to the typo.
			if m.chosen < len(m.choices) {
				m.choices[m.chosen] = proposal.Choice{Command: edited}
			}
			// An edited command is a different command: its radius, its dry
			// run and its explanation are all re-read rather than carried
			// over from the one it replaced.
			return m.arm(edited)
		case tea.KeyEscape:
			m.editInput.Blur()
			m.phase = phaseAction
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.editInput, cmd = m.editInput.Update(msg)
	return m, cmd
}

func (m GenerateModel) updateExplain(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.explainStream.Done() {
			m.phase = phaseAction
			return m, nil
		}
		switch pressed := msg.String(); {
		case keys.Is(pressed, keys.Screen.Quit):
			if m.explainStream.cancel != nil {
				m.explainStream.cancel()
			}
			m.phase = phaseAction
			return m, nil
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.explainStream, cmd = m.explainStream.Update(msg)
	if m.explainStream.Done() {
		m.phase = phaseAction
		return m, nil
	}
	return m, cmd
}

func IsMultiCommand(output string) bool {
	return len(SplitCommands(output)) > 1
}

func SplitCommands(output string) []string {
	raw := strings.TrimSpace(output)
	if raw == "" {
		return nil
	}
	var cmds []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			cmds = append(cmds, line)
		}
	}
	return cmds
}

func formatMultiCommand(output string) string {
	cmds := SplitCommands(output)
	if len(cmds) <= 1 {
		return output
	}
	var b strings.Builder
	for i, cmd := range cmds {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, cmd))
	}
	return strings.TrimRight(b.String(), "\n")
}

// streamingView is the command as it arrives. It stops at the sentinel, so
// the alternatives section never appears on screen on its way to the picker —
// the reason the response is command-first and line-oriented rather than a
// JSON envelope.
func (m GenerateModel) streamingView() string {
	if m.stream.Err() != nil || m.stream.Output() == "" {
		return m.stream.View()
	}
	return sty.Command.Render(proposal.CommandPart(m.stream.Output()))
}

// commandView draws the command itself, numbered when there is more than one.
func (m GenerateModel) commandView() string {
	if IsMultiCommand(m.stream.Output()) {
		return sty.Command.Render(formatMultiCommand(m.stream.Output()))
	}
	return m.stream.View()
}

// pastView is the revise ladder's top rung: the command being compared
// against and the feedback that replaced it, dimmed above the new one. It is
// not history you scroll for — it is the thing the new command is an answer
// to, so it stays on screen while the answer does.
func (m GenerateModel) pastView() string {
	if len(m.past) == 0 {
		return ""
	}
	last := m.past[len(m.past)-1]
	var b strings.Builder
	b.WriteString(sty.PastCommand.Render("$ "+strings.ReplaceAll(last.command, "\n", " ; ")) + "\n")
	b.WriteString(sty.PastCommand.Render("❯ "+last.feedback) + "\n")
	return b.String()
}

// explanationView is the one line the surface now leads with, or the block
// the flag asked for.
func (m GenerateModel) explanationView() string {
	text := strings.TrimSpace(m.explainStream.Output())
	switch {
	case m.shown == ExplainNone:
		return ""
	case m.shown == ExplainLong && text != "":
		return "\n" + sty.ExplainLabel.Render("Explanation:") + "\n" + sty.ExplainBody.Render(text)
	case text == "":
		return ""
	}
	return "\n" + indent(sty.ExplainBody.Render(text))
}

// reachView is the containment line: what the command writes, whether it
// leaves the machine, and whose privileges it runs with, from the resolver
// the approval cards use. The risks above it come from the same read.
func (m GenerateModel) reachView() string {
	var b strings.Builder
	for _, risk := range m.reach.Risks {
		b.WriteString("\n" + indent(sty.Risk.Render("⚠ "+risk)))
	}
	b.WriteString("\n" + indent(sty.Reach.Render("⛨ "+m.reach.Reach())))
	return b.String()
}

// affectedView is what enter buys on a destructive command: the paths the
// resolver found, described as the filesystem holds them now. A command whose
// paths it could not resolve says that instead of showing an empty list.
func (m GenerateModel) affectedView() string {
	if !m.affected {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n" + indent(sty.Dim.Render("would affect")))
	if len(m.reach.Writes) == 0 {
		reason := "shhh could not resolve what this writes"
		if len(m.reach.Unresolved) > 0 {
			reason = m.reach.Unresolved[0]
		}
		b.WriteString("\n" + indent2(sty.Risk.Render(reason)))
		return b.String()
	}
	for _, w := range m.reach.Writes {
		b.WriteString("\n" + indent2(sty.ExplainBody.Render(w.Path)+sty.Dim.Render(" — "+w.Describe())))
	}
	for _, u := range m.reach.Unresolved {
		b.WriteString("\n" + indent2(sty.Risk.Render("and unresolved: "+u)))
	}
	return b.String()
}

// dryRunView is what `[d]` came back with, bounded and counted.
func (m GenerateModel) dryRunView() string {
	if m.phase == phaseDryRun {
		return "\n" + indent(sty.Dim.Render("⟳ dry run — "+m.dryCommand))
	}
	if m.dryOutput == "" {
		return ""
	}
	label := "dry run — " + m.dryCommand
	if m.dryFailed {
		label += " (it exited non-zero; what it printed is below)"
	}
	var b strings.Builder
	b.WriteString("\n" + indent(sty.Dim.Render(label)))
	lines := strings.Split(m.dryOutput, "\n")
	kept := lines
	if len(kept) > dryRunLines {
		kept = kept[:dryRunLines]
	}
	for _, line := range kept {
		b.WriteString("\n" + indent2(sty.ExplainBody.Render(line)))
	}
	if n := len(lines) - len(kept); n > 0 {
		b.WriteString("\n" + indent2(sty.Dim.Render(fmt.Sprintf("… %d more lines", n))))
	}
	return b.String()
}

func indent(s string) string  { return prefixLines(s, "  ") }
func indent2(s string) string { return prefixLines(s, "    ") }

func prefixLines(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n")
}

// View is the frame. The one-shot generate UI draws inline under the prompt
// it was typed at and asks the terminal for nothing, so the view carries
// content and no state (S-155).
func (m GenerateModel) View() tea.View {
	return tea.NewView(m.screen())
}

func (m GenerateModel) screen() string {
	switch m.phase {
	case phaseStreaming:
		return m.streamingView()
	case phasePick:
		if m.pick == nil {
			return m.stream.View()
		}
		return m.pick.View(alternativesWidth)
	case phaseAction, phaseDryRun:
		return m.pastView() + m.commandView() +
			m.explanationView() + m.reachView() +
			m.affectedView() + m.dryRunView() +
			m.actionBar.View()
	case phaseEdit:
		return sty.EditPrompt.Render("Edit: ") + m.editInput.View()
	case phaseSave:
		return m.stream.View() + "\n" + sty.RevisePrompt.Render("Snippet name: ") + m.saveInput.View()
	case phaseRevise:
		return m.stream.View() + "\n" + sty.RevisePrompt.Render("Feedback: ") + m.reviseInput.View()
	case phaseExplain:
		view := m.stream.View() + "\n" + sty.ExplainLabel.Render("Explanation:")
		if m.explainStream.output == "" && !m.explainStream.done {
			view += " " + m.explainStream.spinner.View()
		} else {
			view += "\n" + sty.ExplainBody.Render(m.explainStream.output)
		}
		return view
	default:
		return m.stream.View()
	}
}
