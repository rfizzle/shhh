package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/chat/store"
)

type StreamFunc func([]provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error)

type state int

const (
	stateInput state = iota
	stateStreaming
)

const inputHeight = 3
const headerHeight = 1
const dividerHeight = 1
const chromeHeight = headerHeight + dividerHeight + dividerHeight

type tokenMsg string
type doneMsg struct{}
type streamErrMsg struct{ err error }
type streamStartedMsg struct {
	events <-chan provider.StreamEvent
	cancel context.CancelFunc
}

type Model struct {
	messages []provider.Message
	stream   StreamFunc

	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	history   strings.Builder
	streaming string
	events    <-chan provider.StreamEvent
	cancel    context.CancelFunc
	state     state
	width     int
	height    int
	ready     bool
	atBottom  bool
	quitting  bool
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
	}
}

func (m Model) Messages() []provider.Message { return m.messages }

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(msg.Width)
		vpHeight := m.viewportHeight()

		if !m.ready {
			m.viewport = viewport.New(msg.Width, vpHeight)
			m.viewport.MouseWheelEnabled = true
			m.viewport.SetContent(m.renderHistory())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
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
				m.history.WriteString(m.wordWrap(text, m.width) + "\n\n")
				m.state = stateStreaming
				m.streaming = ""
				m.atBottom = true
				m.viewport.SetContent(m.renderHistory())
				m.viewport.GotoBottom()
				return m, m.requestStream()
			}
		}

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
		m.finishStreaming()
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil

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

	header := headerStyle.Render(" shhh chat") +
		headerHintStyle.Render("  Ctrl+D to exit")
	header += strings.Repeat(" ", max(0, m.width-lipgloss.Width(header)))

	topDivider := dividerStyle(m.width)

	var body string
	if m.state == stateStreaming && m.streaming == "" {
		body = m.viewport.View() + "\n" + m.spinner.View() + " Thinking…"
	} else {
		body = m.viewport.View()
	}

	bottomDivider := dividerStyle(m.width)

	return header + "\n" + topDivider + "\n" + body + "\n" + bottomDivider + "\n" + m.input.View()
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
		if ev.Done {
			return doneMsg{}
		}
		return tokenMsg(ev.Token)
	}
}

func (m *Model) finishStreaming() {
	if m.streaming != "" {
		m.messages = append(m.messages, provider.Message{
			Role:    provider.RoleAssistant,
			Content: m.streaming,
		})
		m.history.WriteString(assistantStyle.Render("Assistant") + "\n")
		m.history.WriteString(highlightCode(m.wordWrap(m.streaming, m.width)) + "\n\n")
	}
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.state = stateInput
}

func (m Model) renderHistory() string {
	if m.history.Len() == 0 && m.state != stateStreaming {
		return welcomeStyle.Render("Type a message to start chatting.")
	}
	s := m.history.String()
	if m.state == stateStreaming && m.streaming != "" {
		s += assistantStyle.Render("Assistant") + "\n"
		s += highlightCode(m.wordWrap(m.streaming, m.width))
	}
	return s
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
	case "/save":
		name := "unnamed"
		if len(parts) > 1 {
			name = strings.Join(parts[1:], " ")
		}
		if err := store.Save(name, m.messages); err != nil {
			return true, "Error saving: " + err.Error()
		}
		return true, fmt.Sprintf("Chat saved as %q", name)

	case "/load":
		if len(parts) < 2 {
			return true, "Usage: /load <name>"
		}
		name := strings.Join(parts[1:], " ")
		msgs, err := store.Load(name)
		if err != nil {
			return true, "Error: " + err.Error()
		}
		m.messages = msgs
		m.history.Reset()
		for _, msg := range msgs {
			switch msg.Role {
			case provider.RoleUser:
				m.history.WriteString(userStyle.Render("You") + "\n")
				m.history.WriteString(m.wordWrap(msg.Content, m.width) + "\n\n")
			case provider.RoleAssistant:
				m.history.WriteString(assistantStyle.Render("Assistant") + "\n")
				m.history.WriteString(highlightCode(m.wordWrap(msg.Content, m.width)) + "\n\n")
			}
		}
		return true, fmt.Sprintf("Loaded chat %q (%d messages)", name, len(msgs))

	case "/chats":
		entries, err := store.List()
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

