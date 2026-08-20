package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
)

type StreamFunc func([]provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error)
type ToolExecutor func(name string, args json.RawMessage) (string, error)

type state int

const (
	stateInput state = iota
	stateStreaming
	stateConfirmRun
	stateRunningCmd
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
type toolExecution struct {
	call   provider.ToolCall
	result string
}
type toolResultsMsg struct {
	runID   int
	results []toolExecution
}
type cmdDoneMsg struct {
	runID    int
	command  string
	output   string
	exitCode int
}
type initialPromptMsg struct{}

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
}

type Model struct {
	messages []provider.Message
	stream   StreamFunc
	executor ToolExecutor
	db       *storage.DB
	copyFn   func(string) clipboard.Result
	runFn    func(context.Context, string) (string, int)

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

	streaming    string
	events       <-chan provider.StreamEvent
	cancel       context.CancelFunc
	state        state
	runID        int
	runningTools bool
	pendingCalls []provider.ToolCall
	pendingRun   string
	runCancel    context.CancelFunc
	// Queue of execute_command tool calls awaiting user approval; the head is
	// mirrored in pendingExecCall while its confirm prompt is showing.
	execQueue       []provider.ToolCall
	pendingExecCall *provider.ToolCall
	width           int
	height          int
	ready           bool
	atBottom        bool
	quitting        bool
	initialPrompt   string

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
		messages: initialMessages,
		stream:   stream,
		input:    ta,
		spinner:  s,
		state:    stateInput,
		atBottom: true,
		copyFn:   clipboard.Copy,
	}
}

func (m Model) WithToolExecutor(executor ToolExecutor) Model {
	m.executor = executor
	return m
}

// WithRunner enables /run with the given command executor.
func (m Model) WithRunner(run func(context.Context, string) (string, int)) Model {
	m.runFn = run
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

func (m Model) Messages() []provider.Message { return m.messages }

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
		switch msg.String() {
		case "ctrl+d":
			m.quitting = true
			if m.cancel != nil {
				m.cancel()
			}
			if m.runCancel != nil {
				m.runCancel()
			}
			return m, tea.Quit
		case "ctrl+c":
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
				return m, nil
			}
			if strings.TrimSpace(m.input.Value()) != "" {
				m.input.Reset()
				m.historyIdx = len(m.inputHistory)
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "esc":
			if m.state == stateInput {
				m.input.Reset()
				m.historyIdx = len(m.inputHistory)
				return m, nil
			}
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
					return m, tea.Quit
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
		m.finishStreaming()
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil

	case toolCallsMsg:
		m.accumulateUsage(msg.usage)
		m.messages = append(m.messages, provider.Message{
			Role:      provider.RoleAssistant,
			Content:   m.streaming,
			ToolCalls: msg.calls,
		})
		if m.streaming != "" {
			m.appendEntry(entry{kind: entryAssistant, text: m.streaming})
		}
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		var readonly, execs []provider.ToolCall
		for _, tc := range msg.calls {
			if tc.Name == tools.ExecCommandName && m.runFn != nil {
				execs = append(execs, tc)
			} else {
				readonly = append(readonly, tc)
			}
		}
		m.execQueue = execs
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if len(readonly) > 0 {
			m.runningTools = true
			m.pendingCalls = msg.calls
			return m, m.execToolsCmd(readonly)
		}
		m.pendingCalls = execs
		return m.advanceExecQueue()

	case toolResultsMsg:
		if msg.runID != m.runID || m.state != stateStreaming {
			return m, nil
		}
		m.runningTools = false
		for _, r := range msg.results {
			m.messages = append(m.messages, provider.Message{
				Role:       provider.RoleTool,
				Content:    r.result,
				ToolCallID: r.call.ID,
			})
			m.appendEntry(entry{kind: entryTool, toolName: r.call.Name, toolArgs: r.call.Arguments, toolResult: r.result})
		}
		m.pendingCalls = m.execQueue
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if len(m.execQueue) > 0 {
			return m.advanceExecQueue()
		}
		return m, m.requestStream()

	case cmdDoneMsg:
		if msg.runID != m.runID || m.state != stateRunningCmd {
			return m, nil
		}
		m.runCancel = nil
		out := strings.TrimRight(msg.output, "\n")
		m.appendEntry(entry{kind: entryCommand, text: msg.command, toolResult: out, exitCode: msg.exitCode})
		if m.pendingExecCall != nil {
			tc := *m.pendingExecCall
			m.pendingExecCall = nil
			m.messages = append(m.messages, provider.Message{
				Role:       provider.RoleTool,
				Content:    execToolResult(out, msg.exitCode),
				ToolCallID: tc.ID,
			})
			m.execQueue = m.execQueue[1:]
			m.pendingCalls = m.execQueue
			m.viewport.SetContent(m.renderHistory())
			m.viewport.GotoBottom()
			return m.advanceExecQueue()
		}
		m.state = stateInput
		m.messages = append(m.messages, provider.Message{
			Role:    provider.RoleUser,
			Content: commandContextMessage(msg.command, out, msg.exitCode),
		})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil

	case streamErrMsg:
		m.appendEntry(entry{kind: entryError, text: msg.err.Error()})
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.state = stateInput
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil

	case spinner.TickMsg:
		if (m.state == stateStreaming && m.streaming == "") || m.state == stateRunningCmd {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	var cmds []tea.Cmd
	if m.state == stateInput {
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
	m.messages = append(m.messages, provider.Message{
		Role:    provider.RoleUser,
		Content: text,
	})
	m.appendEntry(entry{kind: entryUser, text: text})
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

	header := headerStyle.Render(" shhh chat") +
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
		if m.runningTools {
			label = "Running tools…"
		}
		body = m.viewport.View() + "\n" + m.spinner.View() + " " + label
	case m.state == stateRunningCmd:
		body = m.viewport.View() + "\n" + m.spinner.View() + " Running command…"
	default:
		body = m.viewport.View()
	}

	bottomDivider := dividerStyle(contentWidth)
	statusBar := m.renderStatusBar(contentWidth)

	inputView := m.input.View()
	if m.state == stateConfirmRun {
		inputView = m.renderRunConfirm()
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

func (m Model) updateConfirmRun(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.executeRun()
	case "n", "N", "esc", "ctrl+c":
		command := m.pendingRun
		m.pendingRun = ""
		if m.pendingExecCall != nil {
			tc := *m.pendingExecCall
			m.pendingExecCall = nil
			m.messages = append(m.messages, provider.Message{
				Role:       provider.RoleTool,
				Content:    "error: the user declined to run this command",
				ToolCallID: tc.ID,
			})
			m.execQueue = m.execQueue[1:]
			m.pendingCalls = m.execQueue
			m.appendEntry(entry{kind: entrySystem, text: "Declined: " + firstLine(command)})
			m.viewport.SetContent(m.renderHistory())
			m.viewport.GotoBottom()
			return m.advanceExecQueue()
		}
		m.state = stateInput
		m.appendEntry(entry{kind: entrySystem, text: "Run cancelled."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	case "ctrl+d":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// advanceExecQueue shows the confirm prompt for the next queued
// execute_command call, or resumes the model stream once the queue is empty.
func (m Model) advanceExecQueue() (tea.Model, tea.Cmd) {
	if len(m.execQueue) == 0 {
		m.pendingCalls = nil
		m.state = stateStreaming
		m.streaming = ""
		return m, m.requestStream()
	}
	tc := m.execQueue[0]
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || strings.TrimSpace(args.Command) == "" {
		m.execQueue = m.execQueue[1:]
		m.pendingCalls = m.execQueue
		m.messages = append(m.messages, provider.Message{
			Role:       provider.RoleTool,
			Content:    "error: invalid command arguments",
			ToolCallID: tc.ID,
		})
		m.appendEntry(entry{kind: entrySystem, text: "Skipped a tool call with invalid arguments."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m.advanceExecQueue()
	}
	m.pendingExecCall = &tc
	m.pendingRun = args.Command
	m.state = stateConfirmRun
	return m, nil
}

func execToolResult(output string, exitCode int) string {
	const maxLen = 4000
	if len(output) > maxLen {
		output = output[:maxLen] + "\n… (output truncated)"
	}
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return fmt.Sprintf("exit code: %d\noutput:\n%s", exitCode, output)
}

func (m Model) executeRun() (tea.Model, tea.Cmd) {
	command := m.pendingRun
	m.pendingRun = ""
	m.state = stateRunningCmd
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	runID := m.runID
	runFn := m.runFn
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		out, code := runFn(ctx, command)
		return cmdDoneMsg{runID: runID, command: command, output: out, exitCode: code}
	})
}

func (m Model) renderRunConfirm() string {
	label := "Run: "
	if m.pendingExecCall != nil {
		label = "Assistant wants to run: "
	}
	lines := []string{userStyle.Render(label) + firstLine(m.pendingRun)}
	if warnings := safety.Check(m.pendingRun); len(warnings) > 0 {
		var risks []string
		for _, w := range warnings {
			risks = append(risks, w.Risk)
		}
		lines = append(lines, errorStyle.Render("⚠ "+strings.Join(risks, "; ")))
	}
	lines = append(lines, systemMsgStyle.Render("Run this command? [y/N]"))
	for len(lines) < inputHeight {
		lines = append(lines, "")
	}
	return strings.Join(lines[:inputHeight], "\n")
}

// commandContextMessage is appended to the conversation (as the user) so the
// model can see what a /run produced, without triggering a response.
func commandContextMessage(command, output string, exitCode int) string {
	const maxLen = 4000
	if len(output) > maxLen {
		output = output[:maxLen] + "\n… (output truncated)"
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

func (m Model) requestStream() tea.Cmd {
	msgs := make([]provider.Message, len(m.messages))
	copy(msgs, m.messages)
	stream := m.stream
	return func() tea.Msg {
		events, cancel, err := stream(msgs)
		if err != nil {
			return streamErrMsg{err: err}
		}
		return streamStartedMsg{events: events, cancel: cancel}
	}
}

func (m Model) execToolsCmd(calls []provider.ToolCall) tea.Cmd {
	executor := m.executor
	runID := m.runID
	return func() tea.Msg {
		results := make([]toolExecution, 0, len(calls))
		for _, tc := range calls {
			var result string
			if executor != nil {
				out, err := executor(tc.Name, json.RawMessage(tc.Arguments))
				if err != nil {
					result = "error: " + err.Error()
				} else {
					result = out
				}
			} else {
				result = "error: no tool executor configured"
			}
			results = append(results, toolExecution{call: tc, result: result})
		}
		return toolResultsMsg{runID: runID, results: results}
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
	border := toolBorderStyle.Render("│")

	var block strings.Builder
	block.WriteString(border + " " + toolStyle.Render("⚙ "+name) + " " + toolArgsStyle.Render(formatToolArgs(args)) + "\n")

	display := truncateLines(result, maxToolResultLines)
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
		for _, line := range strings.Split(truncateLines(e.toolResult, maxToolResultLines), "\n") {
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
	}
}

func (m *Model) finishStreaming() {
	if m.streaming != "" {
		m.messages = append(m.messages, provider.Message{
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
	m.runID++
	if m.runningTools {
		for _, tc := range m.pendingCalls {
			m.messages = append(m.messages, provider.Message{
				Role:       provider.RoleTool,
				Content:    "error: cancelled by user",
				ToolCallID: tc.ID,
			})
			m.appendEntry(entry{kind: entryTool, toolName: tc.Name, toolArgs: tc.Arguments, toolResult: "cancelled by user"})
		}
		m.runningTools = false
		m.pendingCalls = nil
	}
	m.execQueue = nil
	m.pendingExecCall = nil
	m.finishStreaming()
}

func (m *Model) appendEntry(e entry) {
	m.transcript = append(m.transcript, e)
}

func (m *Model) resetTranscript() {
	m.transcript = nil
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
		return m.renderToolBlock(e.toolName, e.toolArgs, e.toolResult)
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
	var left string
	if m.TotalTokensIn != 0 || m.TotalTokensOut != 0 {
		left = fmt.Sprintf("↑%d ↓%d", m.TotalTokensIn, m.TotalTokensOut)

		if m.prices != nil && m.modelName != "" {
			inCost, outCost, found := m.prices.Cost(m.modelName, m.TotalTokensIn, m.TotalTokensOut)
			if found {
				total := inCost + outCost
				if total < 0.01 {
					left += fmt.Sprintf("  $%.4f", total)
				} else {
					left += fmt.Sprintf("  $%.2f", total)
				}
			}
		}
		if m.contextTokens > 0 {
			left += "  ctx ~" + formatTokenCount(m.contextTokens)
		}
	}

	right := m.modelName
	pad := width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		right = ""
		pad = width - lipgloss.Width(left)
	}
	if pad < 0 {
		pad = 0
	}
	return statusBarStyle.Render(left + strings.Repeat(" ", pad) + right)
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
	h := m.height - inputHeight - chromeHeight
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
		return true, helpText()

	case "/clear", "/new":
		m.clearConversation()
		return true, "Started a new conversation."

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
		if err := m.db.SaveChat(name, m.messages); err != nil {
			return true, "Error saving: " + err.Error()
		}
		return true, fmt.Sprintf("Chat saved as %q", name)

	case "/load":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		if len(parts) < 2 {
			return true, "Usage: /load <name>"
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
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == provider.RoleAssistant && m.messages[i].Content != "" {
			return m.messages[i].Content
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
  /save [name]   Save this chat
  /load <name>   Load a saved chat
  /chats         List saved chats
  /exit          Quit (also /quit, /q)

Keys:
  Enter          Send message        Alt+Enter    Insert newline
  Up/Down        Recall previous inputs (when the input is empty)
  Esc            Clear the input
  Ctrl+C         Cancel response / clear input / quit
  Ctrl+D         Quit
  PgUp/PgDn      Scroll history`)
}

// clearConversation drops everything except the system prompt.
func (m *Model) clearConversation() {
	if len(m.messages) > 0 && m.messages[0].Role == provider.RoleSystem {
		m.messages = m.messages[:1:1]
	} else {
		m.messages = nil
	}
	m.resetTranscript()
	m.contextTokens = 0
}

// loadConversation replaces the current conversation and rebuilds the
// transcript from the stored messages.
func (m *Model) loadConversation(msgs []provider.Message) {
	m.messages = msgs
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
