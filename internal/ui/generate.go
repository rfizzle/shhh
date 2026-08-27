package ui

// The one-shot result surface (S-113, DESIGN-TUI.md §18b). Most people meet
// shhh here rather than in the agent, and until now this screen printed a
// command and waited for a keystroke: no explanation unless you remembered
// `-e`, no statement of what the command could reach, and the same default
// key whether the answer was `ls` or `rm -rf`. Safe-if-you-remember-a-flag is
// not safe by default.
//
// Four things changed. The explanation is on by default, as one line — the
// flag now buys the long form. A containment line states the command's reach
// from the same resolver the approval cards use (internal/radius), so the
// front door and the session agree about what a command is. The action bar is
// a row of keys rather than a menu. And on a destructive command the safe
// default moves: enter spends itself saying what would be affected, `[d]`
// runs the command's own no-op form where one exists, and running takes a
// deliberate `y`.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/dryrun"
	"github.com/rfizzle/shhh/internal/preflight"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/runner"
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
	}
	return m, nil
}

func (m GenerateModel) updateSave(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
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
	updated, cmd := m.stream.Update(msg)
	m.stream = updated.(StreamModel)

	if m.stream.Done() {
		if m.stream.Cancelled() || m.stream.Err() != nil {
			m.phase = phaseDone
			m.result = GenerateResult{
				Command:   m.stream.Output(),
				Cancelled: m.stream.Cancelled(),
				Err:       m.stream.Err(),
			}
			return m, tea.Quit
		}

		output := m.stream.Output()

		if m.shell != "" && m.preflightRetries < maxPreflightRetries && m.newStream != nil {
			check := preflight.Check(output, m.shell)
			if !check.OK {
				m.preflightRetries++
				correction := fmt.Sprintf(
					"That command has errors:\n%s\n\nPlease fix and output only the corrected command(s).",
					strings.Join(check.Errors, "\n"),
				)
				m.messages = append(m.messages,
					provider.Message{Role: provider.RoleAssistant, Content: output},
					provider.Message{Role: provider.RoleUser, Content: correction},
				)
				events, cancel, err := m.newStream(m.messages)
				if err != nil {
					m.phase = phaseDone
					m.result = GenerateResult{Err: err}
					return m, tea.Quit
				}
				m.stream = NewStreamModel(events, cancel)
				return m, m.stream.Init()
			}
		}

		m.messages = append(m.messages, provider.Message{
			Role:    provider.RoleAssistant,
			Content: output,
		})
		return m.arm(output)
	}
	return m, cmd
}

// arm resolves everything the result surface states about a freshly generated
// command and starts whichever explanation this run asked for. It is the one
// place the surface is built, so a revise, an edit and a first generation all
// land on the same screen.
func (m GenerateModel) arm(output string) (GenerateModel, tea.Cmd) {
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
		Reset()
	m.phase = phaseAction

	if m.newExplain == nil || m.explainMode == ExplainNone {
		return m, nil
	}
	long := m.explainMode == ExplainLong
	events, cancel, err := m.newExplain(output, long)
	if err != nil {
		// A surface that cannot explain itself still has to be usable; the
		// keys are what the reader came for.
		return m, nil
	}
	m.explainStream = NewStreamModel(events, cancel)
	m.shown = m.explainMode
	if long {
		// The long form is a block, and reading it is the whole point of
		// asking for it: it gets the screen until it is done, as `-e` always
		// did.
		m.phase = phaseExplain
		return m, m.explainStream.Init()
	}
	// One line arrives under a live action bar. Blocking the keys for a
	// sentence would make the default worse than the flag.
	m.explaining = true
	return m, m.explainStream.Init()
}

func (m GenerateModel) updateAction(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A brief explanation is still arriving: everything that is not a
	// keystroke belongs to it, and the keys stay the action bar's.
	if _, isKey := msg.(tea.KeyMsg); !isKey && m.explaining {
		updated, cmd := m.explainStream.Update(msg)
		m.explainStream = updated.(StreamModel)
		if m.explainStream.Done() {
			m.explaining = false
		}
		return m, cmd
	}

	updated, cmd := m.actionBar.Update(msg)
	m.actionBar = updated.(ActionBarModel)

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

	case ActionEdit:
		m = m.hushExplain()
		m.phase = phaseEdit
		m.editInput.SetValue(m.stream.Output())
		m.editInput.CursorEnd()
		m.editInput.Focus()
		m.actionBar = m.actionBar.Reset()
		return m, m.editInput.Cursor.BlinkCmd()

	case ActionExplain:
		m = m.hushExplain()
		m.actionBar = m.actionBar.Reset()
		if m.newExplain == nil {
			return m, nil
		}
		events, cancel, err := m.newExplain(m.stream.Output(), true)
		if err != nil {
			return m, func() tea.Msg { return explainErrMsg{err: err} }
		}
		m.explainStream = NewStreamModel(events, cancel)
		m.shown = ExplainLong
		m.phase = phaseExplain
		return m, m.explainStream.Init()

	case ActionSave:
		m = m.hushExplain()
		m.phase = phaseSave
		m.saveInput.Reset()
		m.saveInput.Focus()
		m.actionBar = m.actionBar.Reset()
		return m, m.saveInput.Cursor.BlinkCmd()

	case ActionRevise:
		m = m.hushExplain()
		m.phase = phaseRevise
		m.reviseInput.Reset()
		m.reviseInput.Focus()
		m.actionBar = m.actionBar.Reset()
		return m, m.reviseInput.Cursor.BlinkCmd()
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
		m.explainStream.cancel()
		m.explaining = false
	}
	return m
}

// stepBack undoes one revise: the command, the explanation that went with it
// and the conversation all return to where they were.
func (m GenerateModel) stepBack() (GenerateModel, tea.Cmd) {
	m = m.hushExplain()
	last := m.past[len(m.past)-1]
	m.past = m.past[:len(m.past)-1]
	if last.messages <= len(m.messages) {
		m.messages = m.messages[:last.messages]
	}
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
		Reset()
	m.phase = phaseAction
	return m, nil
}

type reviseErrMsg struct{ err error }
type explainErrMsg struct{ err error }
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
	case tea.KeyMsg:
		// The dry run is already running as its own process; esc only stops
		// waiting for it on screen once it lands.
		return m, nil
	}
	return m, nil
}

func (m GenerateModel) updateRevise(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
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
			events, cancel, err := m.newStream(m.messages)
			if err != nil {
				return m, func() tea.Msg { return reviseErrMsg{err: err} }
			}
			m.stream = NewStreamModel(events, cancel)
			m.phase = phaseStreaming
			return m, m.stream.Init()
		case tea.KeyEscape:
			m.phase = phaseAction
			m.reviseInput.Blur()
			return m, nil
		}
	case reviseErrMsg:
		m.phase = phaseDone
		m.result = GenerateResult{Err: msg.err}
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.reviseInput, cmd = m.reviseInput.Update(msg)
	return m, cmd
}

func (m GenerateModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
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
	case tea.KeyMsg:
		if m.explainStream.Done() {
			m.phase = phaseAction
			return m, nil
		}
		switch msg.String() {
		case "q", "esc":
			m.explainStream.cancel()
			m.phase = phaseAction
			return m, nil
		}
		return m, nil
	case explainErrMsg:
		m.explainStream = m.explainStream.WithOutput("Error: " + msg.err.Error())
		m.explainStream.done = true
		m.phase = phaseAction
		return m, nil
	}
	updated, cmd := m.explainStream.Update(msg)
	m.explainStream = updated.(StreamModel)
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

// commandView draws the command itself, numbered when there is more than one.
func (m GenerateModel) commandView() string {
	if IsMultiCommand(m.stream.Output()) {
		return CommandStyle.Render(formatMultiCommand(m.stream.Output()))
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
	b.WriteString(PastCommandStyle.Render("$ "+strings.ReplaceAll(last.command, "\n", " ; ")) + "\n")
	b.WriteString(PastCommandStyle.Render("❯ "+last.feedback) + "\n")
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
		return "\n" + ExplainLabelStyle.Render("Explanation:") + "\n" + ExplainBodyStyle.Render(text)
	case text == "":
		return ""
	}
	return "\n" + indent(ExplainBodyStyle.Render(text))
}

// reachView is the containment line: what the command writes, whether it
// leaves the machine, and whose privileges it runs with, from the resolver
// the approval cards use. The risks above it come from the same read.
func (m GenerateModel) reachView() string {
	var b strings.Builder
	for _, risk := range m.reach.Risks {
		b.WriteString("\n" + indent(RiskStyle.Render("⚠ "+risk)))
	}
	b.WriteString("\n" + indent(ReachStyle.Render("⛨ "+m.reach.Reach())))
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
	b.WriteString("\n" + indent(DimStyle.Render("would affect")))
	if len(m.reach.Writes) == 0 {
		reason := "shhh could not resolve what this writes"
		if len(m.reach.Unresolved) > 0 {
			reason = m.reach.Unresolved[0]
		}
		b.WriteString("\n" + indent2(RiskStyle.Render(reason)))
		return b.String()
	}
	for _, w := range m.reach.Writes {
		b.WriteString("\n" + indent2(ExplainBodyStyle.Render(w.Path)+DimStyle.Render(" — "+w.Describe())))
	}
	for _, u := range m.reach.Unresolved {
		b.WriteString("\n" + indent2(RiskStyle.Render("and unresolved: "+u)))
	}
	return b.String()
}

// dryRunView is what `[d]` came back with, bounded and counted.
func (m GenerateModel) dryRunView() string {
	if m.phase == phaseDryRun {
		return "\n" + indent(DimStyle.Render("⟳ dry run — "+m.dryCommand))
	}
	if m.dryOutput == "" {
		return ""
	}
	label := "dry run — " + m.dryCommand
	if m.dryFailed {
		label += " (it exited non-zero; what it printed is below)"
	}
	var b strings.Builder
	b.WriteString("\n" + indent(DimStyle.Render(label)))
	lines := strings.Split(m.dryOutput, "\n")
	kept := lines
	if len(kept) > dryRunLines {
		kept = kept[:dryRunLines]
	}
	for _, line := range kept {
		b.WriteString("\n" + indent2(ExplainBodyStyle.Render(line)))
	}
	if n := len(lines) - len(kept); n > 0 {
		b.WriteString("\n" + indent2(DimStyle.Render(fmt.Sprintf("… %d more lines", n))))
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

func (m GenerateModel) View() string {
	switch m.phase {
	case phaseStreaming:
		return m.stream.View()
	case phaseAction, phaseDryRun:
		return m.pastView() + m.commandView() +
			m.explanationView() + m.reachView() +
			m.affectedView() + m.dryRunView() +
			m.actionBar.View()
	case phaseEdit:
		return EditPromptStyle.Render("Edit: ") + m.editInput.View()
	case phaseSave:
		return m.stream.View() + "\n" + RevisePromptStyle.Render("Snippet name: ") + m.saveInput.View()
	case phaseRevise:
		return m.stream.View() + "\n" + RevisePromptStyle.Render("Feedback: ") + m.reviseInput.View()
	case phaseExplain:
		view := m.stream.View() + "\n" + ExplainLabelStyle.Render("Explanation:")
		if m.explainStream.output == "" && !m.explainStream.done {
			view += " " + m.explainStream.spinner.View()
		} else {
			view += "\n" + ExplainBodyStyle.Render(m.explainStream.output)
		}
		return view
	default:
		return m.stream.View()
	}
}
