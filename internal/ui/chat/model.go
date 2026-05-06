package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

type StreamFunc func([]provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error)
type ToolExecutor func(name string, args json.RawMessage) (string, error)

type state int

const (
	stateInput state = iota
	stateStreaming
)

const inputHeight = 3
const headerHeight = 1
const dividerHeight = 1
const statusBarHeight = 1
const chromeHeight = headerHeight + dividerHeight + dividerHeight + statusBarHeight
const horizontalPadding = 2

type tokenMsg string
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
type initialPromptMsg struct{}

type Model struct {
	messages []provider.Message
	stream   StreamFunc
	executor ToolExecutor
	db       *storage.DB

	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	history       *strings.Builder
	streaming     string
	events        <-chan provider.StreamEvent
	cancel        context.CancelFunc
	state         state
	width         int
	height        int
	ready         bool
	atBottom      bool
	quitting      bool
	initialPrompt string

	TotalTokensIn  int64
	TotalTokensOut int64
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
		history:  &strings.Builder{},
		state:    stateInput,
		atBottom: true,
	}
}

func (m Model) WithToolExecutor(executor ToolExecutor) Model {
	m.executor = executor
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
		switch msg.String() {
		case "ctrl+d":
			m.quitting = true
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "ctrl+c":
			if m.state == stateStreaming {
				if m.cancel != nil {
					m.cancel()
				}
				m.finishStreaming()
				m.viewport.SetContent(m.renderHistory())
				m.viewport.GotoBottom()
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if m.state == stateInput {
				text := strings.TrimSpace(m.input.Value())
				if text == "" {
					return m, nil
				}
				if text == "/exit" || text == "/quit" || text == "/q" {
					m.quitting = true
					if m.cancel != nil {
						m.cancel()
					}
					return m, tea.Quit
				}
				if handled, result := m.handleSlashCommand(text); handled {
					m.input.Reset()
					m.history.WriteString(systemMsgStyle.Render(result) + "\n\n")
					m.viewport.SetContent(m.renderHistory())
					m.viewport.GotoBottom()
					return m, nil
				}
				m.input.Reset()
				m.messages = append(m.messages, provider.Message{
					Role:    provider.RoleUser,
					Content: text,
				})
				m.history.WriteString(userStyle.Render("You") + "\n")
				m.history.WriteString(m.wordWrap(text, m.contentWidth()) + "\n\n")
				m.state = stateStreaming
				m.streaming = ""
				m.atBottom = true
				m.viewport.SetContent(m.renderHistory())
				m.viewport.GotoBottom()
				return m, m.requestStream()
			}
		}

	case initialPromptMsg:
		text := m.initialPrompt
		m.initialPrompt = ""
		m.messages = append(m.messages, provider.Message{
			Role:    provider.RoleUser,
			Content: text,
		})
		m.history.WriteString(userStyle.Render("You") + "\n")
		m.history.WriteString(m.wordWrap(text, m.contentWidth()) + "\n\n")
		m.state = stateStreaming
		m.streaming = ""
		m.atBottom = true
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.requestStream()

	case streamStartedMsg:
		m.events = msg.events
		m.cancel = msg.cancel
		return m, waitForEvent(m.events)

	case tokenMsg:
		m.streaming += string(msg)
		m.viewport.SetContent(m.renderHistory())
		if m.atBottom {
			m.viewport.GotoBottom()
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
		m.executeToolCalls(msg.calls)
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.requestStream()

	case streamErrMsg:
		m.history.WriteString(errorStyle.Render("Error: "+msg.err.Error()) + "\n\n")
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.state = stateInput
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil

	case spinner.TickMsg:
		if m.state == stateStreaming && m.streaming == "" {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	var cmds []tea.Cmd
	if m.state == stateInput {
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
	if m.state == stateStreaming && m.streaming == "" {
		body = m.viewport.View() + "\n" + m.spinner.View() + " Thinking…"
	} else {
		body = m.viewport.View()
	}

	bottomDivider := dividerStyle(contentWidth)
	statusBar := m.renderStatusBar(contentWidth)

	content := header + "\n" + topDivider + "\n" + body + "\n" + bottomDivider + "\n" + statusBar + "\n" + m.input.View()
	return lipgloss.NewStyle().Padding(0, horizontalPadding).Render(content)
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

func waitForEvent(events <-chan provider.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return doneMsg{}
		}
		if ev.Err != nil {
			return streamErrMsg{err: ev.Err}
		}
		if len(ev.ToolCalls) > 0 {
			return toolCallsMsg{calls: ev.ToolCalls, usage: ev.Usage}
		}
		if ev.Done {
			return doneMsg{usage: ev.Usage}
		}
		return tokenMsg(ev.Token)
	}
}

func (m *Model) executeToolCalls(calls []provider.ToolCall) {
	assistantMsg := provider.Message{
		Role:      provider.RoleAssistant,
		Content:   m.streaming,
		ToolCalls: calls,
	}
	m.messages = append(m.messages, assistantMsg)

	if m.streaming != "" {
		m.history.WriteString(assistantStyle.Render("Assistant") + "\n")
		m.history.WriteString(renderMarkdown(m.streaming, m.contentWidth()) + "\n\n")
	}

	for _, tc := range calls {
		var result string
		if m.executor != nil {
			out, err := m.executor(tc.Name, json.RawMessage(tc.Arguments))
			if err != nil {
				result = "error: " + err.Error()
			} else {
				result = out
			}
		} else {
			result = "error: no tool executor configured"
		}

		m.messages = append(m.messages, provider.Message{
			Role:       provider.RoleTool,
			Content:    result,
			ToolCallID: tc.ID,
		})

		m.history.WriteString(m.renderToolBlock(tc.Name, tc.Arguments, result))
	}

	m.streaming = ""
	m.events = nil
	m.cancel = nil
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
	}
}

func (m *Model) finishStreaming() {
	if m.streaming != "" {
		m.messages = append(m.messages, provider.Message{
			Role:    provider.RoleAssistant,
			Content: m.streaming,
		})
		m.history.WriteString(assistantStyle.Render("Assistant") + "\n")
		m.history.WriteString(renderMarkdown(m.streaming, m.contentWidth()) + "\n\n")
	}
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.state = stateInput
}

func (m Model) renderStatusBar(width int) string {
	if m.TotalTokensIn == 0 && m.TotalTokensOut == 0 {
		return statusBarStyle.Render(strings.Repeat(" ", width))
	}

	tokens := fmt.Sprintf("↑%d ↓%d", m.TotalTokensIn, m.TotalTokensOut)

	var cost string
	if m.prices != nil && m.modelName != "" {
		inCost, outCost, found := m.prices.Cost(m.modelName, m.TotalTokensIn, m.TotalTokensOut)
		if found {
			total := inCost + outCost
			if total < 0.01 {
				cost = fmt.Sprintf("  $%.4f", total)
			} else {
				cost = fmt.Sprintf("  $%.2f", total)
			}
		}
	}

	info := tokens + cost
	pad := width - lipgloss.Width(info)
	if pad < 0 {
		pad = 0
	}
	return statusBarStyle.Render(info + strings.Repeat(" ", pad))
}

func (m Model) renderHistory() string {
	if m.history.Len() == 0 && m.state != stateStreaming {
		return welcomeStyle.Render("Type a message to start chatting.")
	}
	s := m.history.String()
	if m.state == stateStreaming && m.streaming != "" {
		s += assistantStyle.Render("Assistant") + "\n"
		s += renderMarkdown(m.streaming, m.contentWidth())
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

	if m.db == nil {
		return false, ""
	}

	switch parts[0] {
	case "/save":
		name := "unnamed"
		if len(parts) > 1 {
			name = strings.Join(parts[1:], " ")
		}
		if err := m.db.SaveChat(name, m.messages); err != nil {
			return true, "Error saving: " + err.Error()
		}
		return true, fmt.Sprintf("Chat saved as %q", name)

	case "/load":
		if len(parts) < 2 {
			return true, "Usage: /load <name>"
		}
		name := strings.Join(parts[1:], " ")
		msgs, err := m.db.LoadChat(name)
		if err != nil {
			return true, "Error: " + err.Error()
		}
		m.messages = msgs
		m.history.Reset()
		for i, msg := range msgs {
			switch msg.Role {
			case provider.RoleUser:
				m.history.WriteString(userStyle.Render("You") + "\n")
				m.history.WriteString(m.wordWrap(msg.Content, m.contentWidth()) + "\n\n")
			case provider.RoleAssistant:
				if msg.Content != "" {
					m.history.WriteString(assistantStyle.Render("Assistant") + "\n")
					m.history.WriteString(renderMarkdown(msg.Content, m.contentWidth()) + "\n\n")
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
					m.history.WriteString(m.renderToolBlock(tc.Name, tc.Arguments, result))
				}
			}
		}
		return true, fmt.Sprintf("Loaded chat %q (%d messages)", name, len(msgs))

	case "/chats":
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
		return false, ""
	}
}
