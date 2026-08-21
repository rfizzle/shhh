package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// AutosaveName is the reserved chat-session slot that always mirrors the most
// recent conversation, used by `shhh chat --continue`.
const AutosaveName = "(last session)"

// DefaultMaxToolRounds bounds how many consecutive tool-call rounds one user
// turn may trigger before the loop pauses for fresh input
// (behavior.max_tool_rounds overrides it).
const DefaultMaxToolRounds = agent.DefaultMaxToolRounds

type StreamFunc = agent.StreamFunc
type ToolExecutor = agent.ToolExecutor

type state int

const (
	stateInput state = iota
	stateStreaming
	stateConfirmRun
	stateRunningCmd
	// stateClassifying: the auto-mode permission classifier (S-060) is
	// deciding whether the pending approval may run without a prompt.
	stateClassifying
	// statePlanApprove: a completed planning response is awaiting the user's
	// decision — execute, keep planning, or reject (S-061).
	statePlanApprove
	// stateFocus: focus mode (S-076, DESIGN-TUI.md §7) — j/k moves a
	// selection cursor over expandable transcript rows, enter
	// expands/collapses in place, esc returns to the input.
	stateFocus
)

const inputHeight = 3
const headerHeight = 1
const dividerHeight = 1
const statusBarHeight = 1
const chromeHeight = headerHeight + dividerHeight + dividerHeight + statusBarHeight
const horizontalPadding = 2

type tokenMsg struct {
	text string
	// final carries a terminal event (doneMsg, streamErrMsg, toolCallsMsg)
	// that arrived in the same batch, so it isn't lost when tokens are drained.
	final tea.Msg
}
type doneMsg struct{ usage *provider.Usage }
type streamErrMsg struct{ err error }
type streamStartedMsg struct {
	events <-chan provider.StreamEvent
	cancel context.CancelFunc
}
type toolCallsMsg struct {
	calls []provider.ToolCall
	usage *provider.Usage
}
type toolResultsMsg struct {
	runID   int
	results []agent.ToolResult
}
type cmdDoneMsg struct {
	runID    int
	command  string
	output   string
	exitCode int
	duration time.Duration
}
type initialPromptMsg struct{}

// classifierDoneMsg carries the auto-mode classifier's verdict for the
// pending approval (S-060).
type classifierDoneMsg struct {
	runID   int
	verdict agent.ClassifierVerdict
}

type entryKind int

const (
	entryUser entryKind = iota
	entryAssistant
	entryTool
	entrySystem
	entryError
	entryCommand
)

// entry is one transcript item, stored raw so the history can be re-rendered
// at any width (e.g. after a terminal resize).
type entry struct {
	kind       entryKind
	text       string
	toolName   string
	toolArgs   string
	toolResult string
	exitCode   int
	// expanded shows the full tool/command output instead of the truncated
	// block; toggled from focus mode (S-076).
	expanded bool
}

type Model struct {
	// agent owns the loop state (message list, stream requests, tool
	// dispatch, approval queue, iteration guard); the Model is one front-end
	// driving it (S-056).
	agent    *agent.Agent
	db       *storage.DB
	copyFn   func(string) clipboard.Result
	runFn    func(context.Context, string) (string, int)
	switchFn func(string)

	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	transcript []entry
	// Incremental render cache: entries [0, cachedCount) rendered at cachedWidth.
	cachedRender string
	cachedWidth  int
	cachedCount  int

	// Input recall: inputHistory holds previously submitted inputs;
	// historyIdx == len(inputHistory) means "not browsing".
	inputHistory []string
	historyIdx   int

	streaming  string
	events     <-chan provider.StreamEvent
	cancel     context.CancelFunc
	state      state
	pendingRun string
	runCancel  context.CancelFunc
	// Head of the agent's approval queue while its confirm prompt is showing,
	// with everything needed to preview and execute it.
	pendingApproval *approvalRequest
	gatedTools      map[string]GatedPreviewFunc
	// Session approval policy: the permission mode (S-059) plus the S-054
	// internals it builds on — [a] on a confirm prompt promotes its category
	// to auto-approval, commandAllowlist comes from config. The default is
	// manual mode: everything prompts.
	mode             agent.Mode
	modeCycle        []agent.Mode
	allowAllEdits    bool
	allowAllCommands bool
	commandAllowlist []string
	// Auto mode's LLM permission classifier (S-060): judges gated calls the
	// static policy would ask about; nil falls back to asking the user.
	classifier       *agent.Classifier
	classifierCancel context.CancelFunc
	// lastDenial is the most recent auto-mode denial, shown by /mode why.
	lastDenial string
	// planChoice is the focused row of the plan-approval prompt (S-061).
	planChoice int
	// focusIdx is the transcript index of the row selected in focus mode
	// (S-076).
	focusIdx int
	// containment wraps assistant commands in OS-level process containment
	// when a mechanism is available (S-062).
	containment Containment
	// evidence reduces bulky tool results and keeps the originals
	// retrievable (S-064).
	evidence Evidence
	// compacting marks an in-flight /compact request (S-055): the streamed
	// response is a summary handled by finishCompact, not conversation text.
	compacting bool
	// observer receives content-free session events for observability
	// (S-065); turnCount and toolDefTokens feed it and /stats.
	observer      Observer
	turnCount     int64
	toolDefTokens int64
	// steering holds messages typed while the agent is working (S-058); they
	// are injected as user messages before the next stream request.
	steering      []string
	title         string
	width         int
	height        int
	ready         bool
	atBottom      bool
	quitting      bool
	initialPrompt string

	TotalTokensIn  int64
	TotalTokensOut int64
	contextTokens  int64
	prices         *pricing.Table
	modelName      string
	updateNotice   string
}

func New(initialMessages []provider.Message, stream StreamFunc) Model {
	ta := textarea.New()
	ta.Placeholder = "Ask something... (Enter to send, Alt+Enter for newline)"
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("alt+enter")

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return Model{
		agent:    agent.New(initialMessages, stream),
		input:    ta,
		spinner:  s,
		state:    stateInput,
		atBottom: true,
		copyFn:   clipboard.Copy,
	}
}

func (m Model) WithToolExecutor(executor ToolExecutor) Model {
	m.agent.SetExecutor(executor)
	return m
}

// WithRunner enables /run with the given command executor.
func (m Model) WithRunner(run func(context.Context, string) (string, int)) Model {
	m.runFn = run
	return m
}

// WithModelSwitcher enables /model <name>; fn must make subsequent stream
// requests use the given model.
func (m Model) WithModelSwitcher(fn func(string)) Model {
	m.switchFn = fn
	return m
}

// WithTitle overrides the header title (default "shhh chat"), so `shhh code`
// can reuse the TUI under its own name.
func (m Model) WithTitle(title string) Model {
	m.title = title
	return m
}

func (m Model) WithDB(db *storage.DB) Model {
	m.db = db
	return m
}

func (m Model) WithInitialPrompt(prompt string) Model {
	m.initialPrompt = prompt
	return m
}

func (m Model) WithPricing(prices *pricing.Table, modelName string) Model {
	m.prices = prices
	m.modelName = modelName
	return m
}

func (m Model) WithUpdateNotice(notice string) Model {
	m.updateNotice = notice
	return m
}

// WithClassifier enables auto mode's LLM permission classifier (S-060):
// gated calls the static policy would ask about are judged by it instead;
// its failures fall back to asking the user.
func (m Model) WithClassifier(c *agent.Classifier) Model {
	m.classifier = c
	return m
}

// WithMaxToolRounds overrides the per-turn tool-round cap; n <= 0 keeps
// DefaultMaxToolRounds.
func (m Model) WithMaxToolRounds(n int) Model {
	m.agent.SetMaxRounds(n)
	return m
}

func (m Model) effectiveMaxToolRounds() int {
	return m.agent.MaxRounds()
}

// WithResumedMessages replaces the conversation with a previously saved one
// and rebuilds the transcript from it.
func (m Model) WithResumedMessages(msgs []provider.Message) Model {
	m.loadConversation(msgs)
	return m
}

// autosaveCmd persists the conversation to the autosave slot in the
// background. Returns nil when there is no DB or nothing beyond the system
// prompt to save.
func (m Model) autosaveCmd() tea.Cmd {
	if m.db == nil || len(m.agent.Messages()) <= 1 {
		return nil
	}
	db := m.db
	msgs := m.agent.RequestMessages()
	return func() tea.Msg {
		_ = db.SaveChat(AutosaveName, msgs)
		return nil
	}
}

// quitCmd quits, autosaving first when possible.
func (m Model) quitCmd() tea.Cmd {
	if save := m.autosaveCmd(); save != nil {
		return tea.Sequence(save, tea.Quit)
	}
	return tea.Quit
}

func (m Model) Messages() []provider.Message { return m.agent.Messages() }

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.spinner.Tick}
	if m.initialPrompt != "" {
		cmds = append(cmds, func() tea.Msg { return initialPromptMsg{} })
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		contentWidth := msg.Width - horizontalPadding*2
		m.input.SetWidth(contentWidth)
		vpHeight := m.viewportHeight()

		if !m.ready {
			m.viewport = viewport.New(contentWidth, vpHeight)
			m.viewport.MouseWheelEnabled = true
			m.viewport.SetContent(m.renderHistory())
			m.ready = true
		} else {
			m.viewport.Width = contentWidth
			m.viewport.Height = vpHeight
			m.viewport.SetContent(m.renderHistory())
		}
		return m, nil

	case tea.KeyMsg:
		if m.state == stateConfirmRun {
			return m.updateConfirmRun(msg)
		}
		if m.state == statePlanApprove {
			return m.updatePlanApprove(msg)
		}
		if m.state == stateFocus {
			return m.updateFocus(msg)
		}
		switch msg.String() {
		case "ctrl+d":
			m.quitting = true
			if m.cancel != nil {
				m.cancel()
			}
			if m.runCancel != nil {
				m.runCancel()
			}
			if m.classifierCancel != nil {
				m.classifierCancel()
			}
			return m, m.quitCmd()
		case "ctrl+c":
			if m.state == stateClassifying {
				// Skip the classifier check and ask the user directly.
				if m.classifierCancel != nil {
					m.classifierCancel()
					m.classifierCancel = nil
				}
				m.state = stateConfirmRun
				m.syncViewportHeight()
				return m, nil
			}
			if m.state == stateRunningCmd {
				if m.runCancel != nil {
					m.runCancel()
				}
				return m, nil
			}
			if m.state == stateStreaming {
				m.cancelStreaming()
				m.viewport.SetContent(m.renderHistory())
				m.viewport.GotoBottom()
				return m, m.autosaveCmd()
			}
			if strings.TrimSpace(m.input.Value()) != "" {
				m.input.Reset()
				m.historyIdx = len(m.inputHistory)
				return m, nil
			}
			m.quitting = true
			return m, m.quitCmd()
		case "shift+tab":
			// Cycle the permission mode (S-059); the status bar reflects it.
			m.mode = agent.NextMode(m.modeCycle, m.mode)
			return m, nil
		case "ctrl+e":
			// Focus mode (S-076): navigate and expand transcript rows.
			if m.state == stateInput {
				return m.enterFocusMode()
			}
		case "esc":
			// The input is live in every non-confirm state (S-058), so esc
			// always clears the draft.
			m.input.Reset()
			m.historyIdx = len(m.inputHistory)
			return m, nil
		case "up":
			if m.state == stateInput && len(m.inputHistory) > 0 &&
				(m.browsingHistory() || strings.TrimSpace(m.input.Value()) == "") {
				if m.historyIdx > 0 {
					m.historyIdx--
					m.input.SetValue(m.inputHistory[m.historyIdx])
				}
				return m, nil
			}
		case "down":
			if m.state == stateInput && m.browsingHistory() {
				m.historyIdx++
				if m.historyIdx >= len(m.inputHistory) {
					m.historyIdx = len(m.inputHistory)
					m.input.Reset()
				} else {
					m.input.SetValue(m.inputHistory[m.historyIdx])
				}
				return m, nil
			}
		case "enter":
			if m.state == stateStreaming || m.state == stateRunningCmd || m.state == stateClassifying {
				return m.queueSteering()
			}
			if m.state == stateInput {
				text := strings.TrimSpace(m.input.Value())
				if text == "" {
					return m, nil
				}
				m.recordInput(text)
				if text == "/exit" || text == "/quit" || text == "/q" {
					m.quitting = true
					if m.cancel != nil {
						m.cancel()
					}
					return m, m.quitCmd()
				}
				if parts := strings.Fields(text); len(parts) > 0 && parts[0] == "/run" {
					m.input.Reset()
					result, entersConfirm := m.startRun(parts)
					if !entersConfirm {
						m.appendEntry(entry{kind: entrySystem, text: result})
					}
					m.viewport.SetContent(m.renderHistory())
					m.viewport.GotoBottom()
					return m, nil
				}
				if text == "/compact" {
					m.input.Reset()
					return m.startCompact()
				}
				if handled, result := m.handleSlashCommand(text); handled {
					m.input.Reset()
					m.appendEntry(entry{kind: entrySystem, text: result})
					m.viewport.SetContent(m.renderHistory())
					m.viewport.GotoBottom()
					return m, nil
				}
				m.input.Reset()
				return m.sendUserMessage(text)
			}
		}

	case initialPromptMsg:
		text := m.initialPrompt
		m.initialPrompt = ""
		return m.sendUserMessage(text)

	case streamStartedMsg:
		m.events = msg.events
		m.cancel = msg.cancel
		return m, waitForEvent(m.events)

	case tokenMsg:
		m.streaming += msg.text
		m.viewport.SetContent(m.renderHistory())
		if m.atBottom {
			m.viewport.GotoBottom()
		}
		if msg.final != nil {
			return m.Update(msg.final)
		}
		return m, waitForEvent(m.events)

	case doneMsg:
		m.accumulateUsage(msg.usage)
		if m.compacting {
			return m.finishCompact()
		}
		hadText := m.streaming != ""
		m.finishStreaming()
		// A steering message queued while the model was responding becomes the
		// next user turn immediately (S-058).
		if cmd := m.dispatchSteering(); cmd != nil {
			return m, cmd
		}
		// A completed planning response gets the plan-approval prompt (S-061).
		if m.mode == agent.ModePlan && hadText {
			m.state = statePlanApprove
			m.planChoice = 0
			m.syncViewportHeight()
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd()

	case toolCallsMsg:
		m.accumulateUsage(msg.usage)
		if m.compacting {
			return m.abortCompact()
		}
		auto, _ := m.agent.BeginToolRound(m.streaming, msg.calls, m.requiresApproval)
		if m.streaming != "" {
			m.appendEntry(entry{kind: entryAssistant, text: m.streaming})
		}
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if len(auto) > 0 {
			return m, m.execToolsCmd(auto)
		}
		return m.advanceApprovalQueue()

	case toolResultsMsg:
		if msg.runID != m.agent.RunID() || m.state != stateStreaming {
			return m, nil
		}
		m.agent.RecordAutoResults(msg.results)
		for _, r := range msg.results {
			m.recordToolEvent(r.Call.Name, r.Duration, outcomeFromResult(r.Result))
			m.appendEntry(entry{kind: entryTool, toolName: r.Call.Name, toolArgs: r.Call.Arguments, toolResult: r.Result})
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if m.agent.QueuedApprovals() > 0 {
			return m.advanceApprovalQueue()
		}
		return m.resumeToolLoop()

	case cmdDoneMsg:
		if msg.runID != m.agent.RunID() || m.state != stateRunningCmd {
			return m, nil
		}
		m.runCancel = nil
		out := strings.TrimRight(msg.output, "\n")
		// Assistant command output goes through the reduction pipeline
		// (S-064) before both the transcript entry and the tool result, so
		// the user sees exactly what the model got. /run — the user's own
		// command — stays unreduced.
		if m.pendingApproval != nil {
			out = m.reduceResult(tools.ExecCommandName, out)
			outcome := outcomeOK
			if msg.exitCode != 0 {
				outcome = outcomeError
			}
			m.recordToolEvent(tools.ExecCommandName, msg.duration, outcome)
		}
		m.appendEntry(entry{kind: entryCommand, text: msg.command, toolResult: out, exitCode: msg.exitCode})
		if m.pendingApproval != nil {
			m.pendingApproval = nil
			m.agent.ResolveApproval(execToolResult(out, msg.exitCode))
			m.viewport.SetContent(m.renderHistory())
			m.viewport.GotoBottom()
			return m.advanceApprovalQueue()
		}
		m.state = stateInput
		m.agent.Append(provider.Message{
			Role:    provider.RoleUser,
			Content: commandContextMessage(msg.command, out, msg.exitCode),
		})
		// A message typed while the /run command executed is sent now, with
		// the command context already in the conversation (S-058).
		if cmd := m.dispatchSteering(); cmd != nil {
			return m, cmd
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd()

	case approvedToolDoneMsg:
		if msg.runID != m.agent.RunID() || m.state != stateRunningCmd || m.pendingApproval == nil {
			return m, nil
		}
		req := m.pendingApproval
		m.pendingApproval = nil
		m.agent.ResolveApproval(msg.result)
		m.recordToolEvent(req.call.Name, msg.duration, outcomeFromResult(msg.result))
		m.appendEntry(entry{kind: entryTool, toolName: req.call.Name, toolArgs: req.call.Arguments, toolResult: msg.result})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()

	case classifierDoneMsg:
		if msg.runID != m.agent.RunID() || m.state != stateClassifying || m.pendingApproval == nil {
			return m, nil
		}
		m.classifierCancel = nil
		return m.finishClassifierCheck(msg.verdict)

	case streamErrMsg:
		m.appendEntry(entry{kind: entryError, text: msg.err.Error()})
		m.compacting = false
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.state = stateInput
		m.restoreSteering()
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd()

	case spinner.TickMsg:
		if (m.state == stateStreaming && m.streaming == "") || m.state == stateRunningCmd || m.state == stateClassifying {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	var cmds []tea.Cmd
	// The input stays live while the agent streams or runs tools so the user
	// can type a steering message (S-058); only the confirm and plan-approval
	// prompts take over.
	if m.state != stateConfirmRun && m.state != statePlanApprove {
		// Any other keypress while browsing input history turns the recalled
		// text into a fresh draft.
		if _, ok := msg.(tea.KeyMsg); ok {
			m.historyIdx = len(m.inputHistory)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	m.atBottom = m.viewport.AtBottom()

	return m, tea.Batch(cmds...)
}

func (m Model) browsingHistory() bool {
	return m.historyIdx < len(m.inputHistory)
}

func (m *Model) recordInput(text string) {
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
	}
	m.historyIdx = len(m.inputHistory)
}

func (m Model) sendUserMessage(text string) (tea.Model, tea.Cmd) {
	m.turnCount++
	m.agent.StartTurn(text)
	m.appendEntry(entry{kind: entryUser, text: text})
	m.trimForRequest()
	m.state = stateStreaming
	m.streaming = ""
	m.atBottom = true
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, m.requestStream()
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "Initializing…"
	}

	contentWidth := m.width - horizontalPadding*2

	title := m.title
	if title == "" {
		title = "shhh chat"
	}
	header := headerStyle.Render(" "+title) +
		headerHintStyle.Render("  Ctrl+D to exit")
	if m.updateNotice != "" {
		header += "  " + updateNoticeStyle.Render(m.updateNotice)
	}
	header += strings.Repeat(" ", max(0, contentWidth-lipgloss.Width(header)))

	topDivider := dividerStyle(contentWidth)

	var body string
	switch {
	case m.state == stateStreaming && m.streaming == "":
		label := "Thinking…"
		switch {
		case m.compacting:
			label = "Compacting…"
		case m.agent.Executing():
			label = "Running tools…"
		}
		body = m.viewport.View() + "\n" + m.spinner.View() + " " + label
	case m.state == stateRunningCmd:
		label := "Running command…"
		if m.pendingApproval != nil && m.pendingApproval.kind != approvalExec {
			label = "Applying changes…"
		}
		body = m.viewport.View() + "\n" + m.spinner.View() + " " + label
	case m.state == stateClassifying:
		body = m.viewport.View() + "\n" + m.spinner.View() + " Checking permission…"
	default:
		body = m.viewport.View()
	}

	bottomDivider := dividerStyle(contentWidth)
	statusBar := m.renderStatusBar(contentWidth)

	inputView := m.input.View()
	switch m.state {
	case stateConfirmRun:
		inputView = m.renderConfirm()
	case statePlanApprove:
		inputView = m.renderPlanApprove()
	case stateFocus:
		inputView = m.renderFocusHint()
	}

	content := header + "\n" + topDivider + "\n" + body + "\n" + bottomDivider + "\n" + statusBar + "\n" + inputView
	return lipgloss.NewStyle().Padding(0, horizontalPadding).Render(content)
}

// startRun resolves which code block from the last response to execute.
// It returns either a message for the transcript, or entersConfirm=true after
// switching to the confirmation state.
func (m *Model) startRun(parts []string) (result string, entersConfirm bool) {
	if m.runFn == nil {
		return "Command execution is not available in this session.", false
	}
	blocks := extractCodeBlocks(m.lastAssistantText())
	if len(blocks) == 0 {
		return "No code blocks in the last response to run.", false
	}
	idx := 0
	if len(parts) > 1 {
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 || n > len(blocks) {
			return fmt.Sprintf("Usage: /run [1-%d]", len(blocks)), false
		}
		idx = n - 1
	} else if len(blocks) > 1 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "The last response has %d code blocks:\n", len(blocks))
		for i, b := range blocks {
			fmt.Fprintf(&sb, "  %d. %s\n", i+1, firstLine(b))
		}
		sb.WriteString("Pick one with /run <n>.")
		return sb.String(), false
	}
	m.pendingRun = blocks[idx]
	m.state = stateConfirmRun
	return "", true
}

// updateConfirmRun routes confirm-prompt keys through the approval card
// (S-076); the card's y/n/esc semantics match the original prompt, and [a]
// (S-054) is offered only where a session grant is allowed.
func (m Model) updateConfirmRun(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+d" {
		m.quitting = true
		return m, m.quitCmd()
	}
	done, result := m.approvalCard().Update(msg)
	if !done {
		return m, nil
	}
	switch result {
	case components.ApprovalApprove:
		if m.pendingApproval != nil {
			m.recordDecision(decisionAllow, "user")
		}
		if m.pendingApproval != nil && m.pendingApproval.kind != approvalExec {
			return m.executeApprovedTool()
		}
		return m.executeRun()
	case components.ApprovalAlways:
		// Approve and auto-allow this category for the session (S-054).
		// Safety-flagged commands, generic gated tools, and /run keep asking
		// (the card offers [a] only where a grant is allowed).
		if req := m.pendingApproval; req != nil {
			switch req.kind {
			case approvalExec:
				m.recordDecision(decisionAllow, "user-always")
				m.allowAllCommands = true
				return m.executeRun()
			case approvalDiff:
				m.recordDecision(decisionAllow, "user-always")
				m.allowAllEdits = true
				return m.executeApprovedTool()
			}
		}
	case components.ApprovalDeny:
		if m.pendingApproval != nil {
			return m.declineApproval()
		}
		m.pendingRun = ""
		m.state = stateInput
		m.syncViewportHeight()
		m.appendEntry(entry{kind: entrySystem, text: "Run cancelled."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	}
	return m, nil
}

func execToolResult(output string, exitCode int) string {
	return tools.FormatExecResult(output, exitCode)
}

func (m Model) executeRun() (tea.Model, tea.Cmd) {
	command := m.pendingRun
	m.pendingRun = ""
	m.state = stateRunningCmd
	m.syncViewportHeight()
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	runID := m.agent.RunID()
	runFn := m.runFn
	// Assistant commands run contained when a mechanism is available (S-062);
	// /run — the user's own command — stays on the plain runner.
	if m.pendingApproval != nil && m.containment.Run != nil {
		runFn = m.containment.Run
	}
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		start := time.Now()
		out, code := runFn(ctx, command)
		return cmdDoneMsg{runID: runID, command: command, output: out, exitCode: code, duration: time.Since(start)}
	})
}

// commandContextMessage is appended to the conversation (as the user) so the
// model can see what a /run produced, without triggering a response.
func commandContextMessage(command, output string, exitCode int) string {
	if cut, truncated := tools.TruncateOutput(output, tools.MaxExecOutputBytes); truncated {
		output = cut + "\n… (output truncated)"
	}
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return fmt.Sprintf("I ran this command:\n```\n%s\n```\nExit code: %d\nOutput:\n```\n%s\n```", command, exitCode, output)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// resumeToolLoop requests the next model response after a round of tool
// results — unless this turn has hit the tool-round cap, in which case the
// loop pauses and control returns to the user (a fresh message continues the
// conversation and resets the counter).
func (m Model) resumeToolLoop() (tea.Model, tea.Cmd) {
	// Steering messages queued mid-turn join the conversation here, between
	// tool rounds, so the model sees them on its next request (S-058). They
	// count as fresh user input, so they also lift a hit round cap.
	if m.injectSteering() {
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
	}
	if m.agent.CapReached() {
		m.state = stateInput
		m.syncViewportHeight()
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
			"Paused after %d tool rounds this turn. Send a message (e.g. \"continue\") to keep going.",
			m.agent.Rounds())})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd()
	}
	m.state = stateStreaming
	m.streaming = ""
	m.trimForRequest()
	m.syncViewportHeight()
	return m, m.requestStream()
}

func (m Model) requestStream() tea.Cmd {
	msgs := m.agent.RequestMessages()
	// Plan mode injects planning instructions into the request's system
	// prompt (S-061); the stored conversation stays untouched, so leaving
	// plan mode stops the injection.
	if m.mode == agent.ModePlan && len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		msgs[0].Content += "\n\n" + prompt.PlanModeInstructions
	}
	return m.requestStreamFor(msgs)
}

// requestStreamFor starts a stream over an explicit message list (callers
// pass a copy so in-flight requests are immune to later mutation).
func (m Model) requestStreamFor(msgs []provider.Message) tea.Cmd {
	a := m.agent
	return func() tea.Msg {
		events, cancel, err := a.Stream(msgs)
		if err != nil {
			return streamErrMsg{err: err}
		}
		return streamStartedMsg{events: events, cancel: cancel}
	}
}

func (m Model) execToolsCmd(calls []provider.ToolCall) tea.Cmd {
	a := m.agent
	runID := a.RunID()
	return func() tea.Msg {
		return toolResultsMsg{runID: runID, results: a.ExecuteCalls(calls)}
	}
}

// waitForEvent reads the next stream event. If it is a token, any further
// tokens already buffered on the channel are drained into a single batch so
// the UI re-renders once per batch instead of once per token.
func waitForEvent(events <-chan provider.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return doneMsg{}
		}
		if final := terminalMsg(ev); final != nil {
			return final
		}
		var batch strings.Builder
		batch.WriteString(ev.Token)
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return tokenMsg{text: batch.String(), final: doneMsg{}}
				}
				if final := terminalMsg(ev); final != nil {
					return tokenMsg{text: batch.String(), final: final}
				}
				batch.WriteString(ev.Token)
			default:
				return tokenMsg{text: batch.String()}
			}
		}
	}
}

// terminalMsg converts a non-token stream event into its message, or returns
// nil for a plain token event.
func terminalMsg(ev provider.StreamEvent) tea.Msg {
	if ev.Err != nil {
		return streamErrMsg{err: ev.Err}
	}
	if len(ev.ToolCalls) > 0 {
		return toolCallsMsg{calls: ev.ToolCalls, usage: ev.Usage}
	}
	if ev.Done {
		return doneMsg{usage: ev.Usage}
	}
	return nil
}

const maxToolResultLines = 8

func (m Model) renderToolBlock(name, args, result string) string {
	return m.renderToolBlockLimited(name, args, result, maxToolResultLines)
}

// renderToolBlockLimited renders a tool block truncated to limit result
// lines; limit <= 0 shows everything (focus-mode expansion, S-076).
func (m Model) renderToolBlockLimited(name, args, result string, limit int) string {
	border := toolBorderStyle.Render("│")

	var block strings.Builder
	block.WriteString(border + " " + toolStyle.Render("⚙ "+name) + " " + toolArgsStyle.Render(formatToolArgs(args)) + "\n")

	display := result
	if limit > 0 {
		display = truncateLines(result, limit)
	}
	for _, line := range strings.Split(display, "\n") {
		block.WriteString(border + " " + toolResultStyle.Render(line) + "\n")
	}
	block.WriteString("\n")
	return block.String()
}

func (m Model) renderCommandBlock(e entry) string {
	border := toolBorderStyle.Render("│")

	var block strings.Builder
	block.WriteString(border + " " + toolStyle.Render("$ "+firstLine(e.text)) + " " + toolArgsStyle.Render(fmt.Sprintf("(exit %d)", e.exitCode)) + "\n")
	if strings.TrimSpace(e.toolResult) != "" {
		display := e.toolResult
		if !e.expanded {
			display = truncateLines(e.toolResult, maxToolResultLines)
		}
		for _, line := range strings.Split(display, "\n") {
			block.WriteString(border + " " + toolResultStyle.Render(line) + "\n")
		}
	}
	block.WriteString("\n")
	return block.String()
}

func formatToolArgs(raw string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	var parts []string
	for k, v := range m {
		switch val := v.(type) {
		case string:
			parts = append(parts, k+"="+val)
		default:
			b, _ := json.Marshal(val)
			parts = append(parts, k+"="+string(b))
		}
	}
	return strings.Join(parts, " ")
}

func truncateLines(s string, max int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	truncated := strings.Join(lines[:max], "\n")
	remaining := len(lines) - max
	truncated += fmt.Sprintf("\n… (%d more lines)", remaining)
	return truncated
}

func (m *Model) accumulateUsage(u *provider.Usage) {
	if u != nil {
		m.TotalTokensIn += int64(u.PromptTokens)
		m.TotalTokensOut += int64(u.CompletionTokens)
		// The latest request's prompt plus its completion is what the next
		// request will roughly carry as context.
		m.contextTokens = int64(u.PromptTokens) + int64(u.CompletionTokens)
		m.notifyUsage()
	}
}

func (m *Model) finishStreaming() {
	if m.compacting {
		// A cancelled compaction discards the partial summary and keeps the
		// conversation unchanged (the success path goes through finishCompact).
		m.compacting = false
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.state = stateInput
		m.appendEntry(entry{kind: entrySystem, text: "Compaction cancelled; conversation unchanged."})
		return
	}
	if m.streaming != "" {
		m.agent.Append(provider.Message{
			Role:    provider.RoleAssistant,
			Content: m.streaming,
		})
		m.appendEntry(entry{kind: entryAssistant, text: m.streaming})
	}
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.state = stateInput
}

// cancelStreaming aborts an in-flight response or tool run. Any pending tool
// calls get synthetic error results so the conversation stays well-formed for
// the next request.
func (m *Model) cancelStreaming() {
	if m.cancel != nil {
		m.cancel()
	}
	for _, tc := range m.agent.CancelTurn() {
		m.appendEntry(entry{kind: entryTool, toolName: tc.Name, toolArgs: tc.Arguments, toolResult: "cancelled by user"})
	}
	m.pendingApproval = nil
	m.finishStreaming()
	m.restoreSteering()
}

// queueSteering handles Enter while the agent is working (S-058): the typed
// text is queued and injected as a user message before the next stream
// request. Quit commands still quit; other slash commands cannot run mid-turn.
func (m Model) queueSteering() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	if text == "/exit" || text == "/quit" || text == "/q" {
		m.quitting = true
		if m.cancel != nil {
			m.cancel()
		}
		if m.runCancel != nil {
			m.runCancel()
		}
		return m, m.quitCmd()
	}
	// Same mistyped-command heuristic as handleSlashCommand: a lone "/word"
	// is a command, which cannot run while the agent is working; a path like
	// /etc/hosts contains another slash and queues as steering text.
	if parts := strings.Fields(text); strings.HasPrefix(parts[0], "/") && !strings.Contains(parts[0][1:], "/") {
		m.appendEntry(entry{kind: entrySystem, text: "Commands can't run while the agent is working; " + parts[0] + " was not queued."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	}
	m.recordInput(text)
	m.input.Reset()
	m.steering = append(m.steering, text)
	return m, nil
}

// injectSteering appends queued steering messages to the conversation and
// transcript as user messages, reporting whether any were queued. Steering is
// fresh user input, so it resets the tool-round counter (S-053 semantics).
func (m *Model) injectSteering() bool {
	if len(m.steering) == 0 {
		return false
	}
	for _, text := range m.steering {
		m.agent.Append(provider.Message{Role: provider.RoleUser, Content: text})
		m.appendEntry(entry{kind: entryUser, text: text})
	}
	m.turnCount += int64(len(m.steering))
	m.steering = nil
	m.agent.ResetRounds()
	return true
}

// dispatchSteering turns queued steering messages into a fresh user turn once
// the current turn has ended: it injects them and opens the next stream.
// Returns nil when nothing was queued.
func (m *Model) dispatchSteering() tea.Cmd {
	if !m.injectSteering() {
		return nil
	}
	m.state = stateStreaming
	m.streaming = ""
	m.atBottom = true
	m.trimForRequest()
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return tea.Batch(m.spinner.Tick, m.requestStream(), m.autosaveCmd())
}

// restoreSteering returns queued-but-uninjected steering messages to the input
// when a turn ends abnormally (cancel, stream error), so nothing typed is
// silently lost.
func (m *Model) restoreSteering() {
	if len(m.steering) == 0 {
		return
	}
	parts := m.steering
	if cur := m.input.Value(); strings.TrimSpace(cur) != "" {
		parts = append(parts, cur)
	}
	m.input.SetValue(strings.Join(parts, "\n"))
	m.steering = nil
}

func (m *Model) appendEntry(e entry) {
	m.transcript = append(m.transcript, e)
}

func (m *Model) resetTranscript() {
	m.transcript = nil
	m.invalidateRenderCache()
}

// invalidateRenderCache forces the next renderHistory to re-render every
// entry (used when an entry's rendering changes in place, e.g. focus-mode
// expansion).
func (m *Model) invalidateRenderCache() {
	m.cachedRender = ""
	m.cachedCount = 0
}

func (m Model) renderEntry(e entry, width int) string {
	switch e.kind {
	case entryUser:
		return userStyle.Render("You") + "\n" + m.wordWrap(e.text, width) + "\n\n"
	case entryAssistant:
		return assistantStyle.Render("Assistant") + "\n" + renderMarkdown(e.text, width) + "\n\n"
	case entryTool:
		limit := maxToolResultLines
		if e.expanded {
			limit = 0
		}
		return m.renderToolBlockLimited(e.toolName, e.toolArgs, e.toolResult, limit)
	case entryCommand:
		return m.renderCommandBlock(e)
	case entrySystem:
		return systemMsgStyle.Render(e.text) + "\n\n"
	case entryError:
		return errorStyle.Render("Error: "+e.text) + "\n\n"
	}
	return ""
}

func (m Model) renderStatusBar(width int) string {
	// The active permission mode is always visible (S-059).
	parts := []string{m.modeSegment()}
	if m.TotalTokensIn != 0 || m.TotalTokensOut != 0 {
		usage := fmt.Sprintf("↑%d ↓%d", m.TotalTokensIn, m.TotalTokensOut)

		if m.prices != nil && m.modelName != "" {
			inCost, outCost, found := m.prices.Cost(m.modelName, m.TotalTokensIn, m.TotalTokensOut)
			if found {
				total := inCost + outCost
				if total < 0.01 {
					usage += fmt.Sprintf("  $%.4f", total)
				} else {
					usage += fmt.Sprintf("  $%.2f", total)
				}
			}
		}
		parts = append(parts, statusBarStyle.Render(usage))
		if m.contextTokens > 0 {
			parts = append(parts, m.renderContextIndicator())
		}
	}

	// Round counter shows only mid-turn, so long tool loops are visible.
	if m.agent.Rounds() > 0 && m.state != stateInput {
		parts = append(parts, statusBarStyle.Render(fmt.Sprintf("round %d", m.agent.Rounds())))
	}

	// Steering messages waiting to be injected (S-058).
	if n := len(m.steering); n > 0 {
		parts = append(parts, statusBarStyle.Render(fmt.Sprintf("queued %d", n)))
	}

	// Active approval policy (S-054); absent in the default ask-everything state.
	if p := m.policyLabel(); p != "" {
		parts = append(parts, statusBarStyle.Render(p))
	}

	left := strings.Join(parts, "  ")
	right := ""
	if m.modelName != "" {
		right = statusBarStyle.Render(m.modelName)
	}
	pad := width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		right = ""
		pad = width - lipgloss.Width(left)
	}
	if pad < 0 {
		pad = 0
	}
	return left + strings.Repeat(" ", pad) + right
}

// renderContextIndicator shows the estimated context size, changing color as
// it approaches the trim threshold (S-055).
func (m Model) renderContextIndicator() string {
	s := "ctx ~" + formatTokenCount(m.contextTokens)
	switch m.contextSeverity() {
	case 2:
		return ctxAlertStyle.Render(s)
	case 1:
		return ctxWarnStyle.Render(s)
	}
	return statusBarStyle.Render(s)
}

func formatTokenCount(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func (m *Model) renderHistory() string {
	if len(m.transcript) == 0 && m.state != stateStreaming {
		return welcomeStyle.Render("Type a message to start chatting.")
	}
	if m.state == stateFocus {
		// Focus mode renders fresh with the selection gutter, bypassing the
		// incremental cache.
		content, _, _ := m.renderFocusHistory()
		return content
	}
	w := m.contentWidth()
	if w != m.cachedWidth {
		m.cachedWidth = w
		m.cachedRender = ""
		m.cachedCount = 0
	}
	for ; m.cachedCount < len(m.transcript); m.cachedCount++ {
		m.cachedRender += m.renderEntry(m.transcript[m.cachedCount], w)
	}
	s := m.cachedRender
	if m.state == stateStreaming && m.streaming != "" {
		s += assistantStyle.Render("Assistant") + "\n"
		s += renderMarkdown(m.streaming, w)
	}
	return s
}

func (m Model) contentWidth() int {
	return m.width - horizontalPadding*2
}

func (m Model) viewportHeight() int {
	h := m.height - m.bottomPanelHeight() - chromeHeight
	if h < 1 {
		return 1
	}
	return h
}

func (m Model) wordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if lipgloss.Width(line) <= width {
			result.WriteString(line)
			result.WriteByte('\n')
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			result.WriteByte('\n')
			continue
		}
		lineLen := 0
		for i, word := range words {
			wLen := lipgloss.Width(word)
			if i > 0 && lineLen+1+wLen > width {
				result.WriteByte('\n')
				lineLen = 0
			} else if i > 0 {
				result.WriteByte(' ')
				lineLen++
			}
			result.WriteString(word)
			lineLen += wLen
		}
		result.WriteByte('\n')
	}
	return strings.TrimRight(result.String(), "\n")
}

func dividerStyle(width int) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Render(strings.Repeat("─", width))
}

func (m *Model) handleSlashCommand(text string) (handled bool, result string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false, ""
	}

	switch parts[0] {
	case "/help":
		return true, helpText() + "\n\n" + m.policyHelp()

	case "/clear", "/new":
		m.clearConversation()
		return true, "Started a new conversation."

	case "/model":
		if len(parts) < 2 {
			if m.modelName != "" {
				return true, fmt.Sprintf("Current model: %s\nUsage: /model <name>", m.modelName)
			}
			return true, "Usage: /model <name>"
		}
		if m.switchFn == nil {
			return true, "Model switching is not available in this session."
		}
		if len(parts) > 2 {
			return true, "Model names cannot contain spaces. Usage: /model <name>"
		}
		name := parts[1]
		if name == m.modelName {
			return true, fmt.Sprintf("Already using %s.", name)
		}
		m.switchFn(name)
		m.modelName = name
		return true, fmt.Sprintf("Switched model to %s.", name)

	case "/mode":
		if len(parts) < 2 {
			return true, m.modeStatus()
		}
		if len(parts) > 2 {
			return true, "Usage: /mode [manual|accept-edits|auto|plan|why]"
		}
		if parts[1] == "why" {
			if m.lastDenial == "" {
				return true, "No auto-mode denials this session."
			}
			return true, "Last auto-mode denial:\n  " + m.lastDenial
		}
		mode, err := agent.ParseMode(parts[1])
		if err != nil {
			return true, "Error: " + err.Error()
		}
		m.mode = mode
		return true, fmt.Sprintf("Mode set to %s — %s.", mode, mode.Describe())

	case "/stats":
		return true, m.statsReport()

	case "/sandbox":
		args := parts[1:]
		if len(args) == 0 {
			args = []string{"doctor"}
		}
		if m.containment.Manage != nil {
			return true, m.containment.Manage(args)
		}
		// No manager wired (older sessions/tests): doctor falls back to the
		// static report; everything else is unavailable.
		if len(args) == 1 && args[0] == "doctor" {
			if m.containment.Report == "" {
				return true, "Command containment is not configured in this session."
			}
			return true, m.containment.Report
		}
		return true, "Container sandbox management is unavailable in this session."

	case "/evidence":
		if m.evidence.Manage == nil {
			return true, "The evidence store is unavailable in this session."
		}
		return true, m.evidence.Manage(parts[1:])

	case "/plan":
		if len(parts) < 2 || parts[1] != "save" {
			return true, "Usage: /plan save [name]"
		}
		planText := m.lastAssistantText()
		if strings.TrimSpace(planText) == "" {
			return true, "No plan to save yet — there is no assistant response."
		}
		path, err := savePlan(planText, strings.Join(parts[2:], "-"))
		if err != nil {
			return true, "Error saving plan: " + err.Error()
		}
		return true, "Plan saved to " + path

	case "/copy":
		text := m.lastAssistantText()
		if text == "" {
			return true, "Nothing to copy yet."
		}
		what := "response"
		if len(parts) > 1 && parts[1] == "code" {
			blocks := extractCodeBlocks(text)
			if len(blocks) == 0 {
				return true, "No code blocks in the last response."
			}
			text = strings.Join(blocks, "\n")
			what = "code"
		}
		cr := m.copyFn(text)
		if cr.Warning != "" {
			return true, cr.Warning
		}
		return true, "Copied last " + what + " to clipboard."

	case "/save":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		name := "unnamed"
		if len(parts) > 1 {
			name = strings.Join(parts[1:], " ")
		}
		if err := m.db.SaveChat(name, m.agent.Messages()); err != nil {
			return true, "Error saving: " + err.Error()
		}
		return true, fmt.Sprintf("Chat saved as %q", name)

	case "/load":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		if len(parts) < 2 {
			_, listing := m.handleSlashCommand("/chats")
			return true, listing + "\n\nUsage: /load <name>"
		}
		name := strings.Join(parts[1:], " ")
		msgs, err := m.db.LoadChat(name)
		if err != nil {
			return true, "Error: " + err.Error()
		}
		m.loadConversation(msgs)
		return true, fmt.Sprintf("Loaded chat %q (%d messages)", name, len(msgs))

	case "/chats":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		entries, err := m.db.ListChats()
		if err != nil {
			return true, "Error: " + err.Error()
		}
		if len(entries) == 0 {
			return true, "No saved chats."
		}
		var sb strings.Builder
		sb.WriteString("Saved chats:\n")
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("  %s  (%d turns, %s)\n",
				e.Name, e.Turns, e.UpdatedAt.Format("Jan 2 15:04")))
		}
		return true, strings.TrimRight(sb.String(), "\n")

	default:
		// A lone "/word" is almost certainly a mistyped command; a path like
		// /etc/hosts contains another slash and falls through to the LLM.
		if strings.HasPrefix(parts[0], "/") && !strings.Contains(parts[0][1:], "/") {
			return true, fmt.Sprintf("Unknown command %s. Type /help for available commands.", parts[0])
		}
		return false, ""
	}
}

// lastAssistantText returns the content of the most recent assistant message
// that has any text.
func (m Model) lastAssistantText() string {
	msgs := m.agent.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleAssistant && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}

func helpText() string {
	return strings.TrimSpace(`Commands:
  /help          Show this help
  /clear         Start a new conversation (also /new)
  /copy [code]   Copy the last response (or just its code blocks)
  /run [n]       Run a code block from the last response (with confirmation)
  /model [name]  Show or switch the model for this session
  /mode [name]   Show or set the permission mode (manual, accept-edits, auto, plan)
  /mode why      Show the latest auto-mode denial's reason
  /stats         Context occupancy breakdown and cumulative session spend
  /sandbox       Containment status and container sandboxes (doctor|list|status|destroy <id>|prune)
  /evidence      Tool-output evidence store: reduction stats and size (purge to clear)
  /plan save [name]  Save the last plan/response to .shhh/plans/
  /compact       Summarize the conversation and continue from the summary
  /save [name]   Save this chat
  /load <name>   Load a saved chat
  /chats         List saved chats
  /exit          Quit (also /quit, /q)

Keys:
  Enter          Send message        Alt+Enter    Insert newline
  Shift+Tab      Cycle the permission mode
                 (while the agent is working, Enter queues a steering message
                  that joins the conversation before the next model request)
  Up/Down        Recall previous inputs (when the input is empty)
  Ctrl+E         Focus mode: select tool/command rows (j/k), expand/collapse (Enter), Esc back
  Esc            Clear the input
  Ctrl+C         Cancel response / clear input / quit
  Ctrl+D         Quit
  PgUp/PgDn      Scroll history
  y/n/a          Approval prompts: allow / deny / always allow this session`)
}

// clearConversation drops everything except the system prompt.
func (m *Model) clearConversation() {
	msgs := m.agent.Messages()
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		m.agent.SetMessages(msgs[:1:1])
	} else {
		m.agent.SetMessages(nil)
	}
	m.resetTranscript()
	m.contextTokens = 0
	m.agent.ResetRounds()
}

// loadConversation replaces the current conversation and rebuilds the
// transcript from the stored messages.
func (m *Model) loadConversation(msgs []provider.Message) {
	m.agent.SetMessages(msgs)
	m.resetTranscript()
	for i, msg := range msgs {
		switch msg.Role {
		case provider.RoleUser:
			m.appendEntry(entry{kind: entryUser, text: msg.Content})
		case provider.RoleAssistant:
			if msg.Content != "" {
				m.appendEntry(entry{kind: entryAssistant, text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				var result string
				if i+1 < len(msgs) {
					for _, next := range msgs[i+1:] {
						if next.Role == provider.RoleTool && next.ToolCallID == tc.ID {
							result = next.Content
							break
						}
						if next.Role != provider.RoleTool {
							break
						}
					}
				}
				m.appendEntry(entry{kind: entryTool, toolName: tc.Name, toolArgs: tc.Arguments, toolResult: result})
			}
		}
	}
}
