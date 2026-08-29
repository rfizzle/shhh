package chat

// Durable memory (S-070). The /memory slash command manages entries, and the
// model's remember tool proposes new ones. The trust rule is absolute:
// agent-proposed memories persist only after explicit user confirmation on
// the memory prompt (DESIGN-TUI.md §4c) — no permission mode, session grant,
// or classifier verdict can wave one through, because memory an agent writes
// to itself is an injection surface.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Memory wires durable memory (S-070) into the chat TUI. The zero value
// disables it.
type Memory struct {
	// Manage backs the /memory slash command (list | add | forget).
	Manage func(args []string) string
	// Save persists a user-confirmed agent proposal and returns the tool
	// result text. It is only ever called after the user explicitly approved
	// the entry on the memory prompt.
	Save func(scope, kind, text string) (string, error)
	// ProjectScope is the per-project scope key behind the "Save (project)"
	// option.
	ProjectScope string
}

// WithMemory enables the /memory command and the remember-tool confirm flow.
func (m Model) WithMemory(mem Memory) Model {
	m.memory = mem
	return m
}

// buildMemoryApproval turns a remember tool call into its approval request.
func (m Model) buildMemoryApproval(tc provider.ToolCall) (*approvalRequest, error) {
	if m.memory.Save == nil {
		return nil, fmt.Errorf("tool %s cannot be approved in this session", tc.Name)
	}
	draft, err := memory.ParseRemember(json.RawMessage(tc.Arguments))
	if err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return &approvalRequest{
		call:        tc,
		kind:        approvalMemory,
		title:       "remember (" + draft.Kind + ")",
		summary:     firstLine(draft.Text),
		memoryDraft: draft,
	}, nil
}

// openMemoryAsk shows the memory confirm prompt (DESIGN-TUI.md §4c) for the
// pending remember proposal: save to project or global scope — with an
// optional note amending the entry — or don't save.
func (m *Model) openMemoryAsk(req *approvalRequest) {
	ns := components.NewNoteSelect("Remember this?", []components.SelectOption{
		{Label: "Save (project)", Desc: m.memory.ProjectScope},
		{Label: "Save (global)", Desc: "applies in every workspace"},
		{Label: "Don't save"},
	})
	if req.memoryDraft.Scope == memory.GlobalScope {
		ns.Select.Focus = 1
	}
	ns.Select.MaxLines = m.maxConfirmPanelHeight() - 1
	m.memoryAsk = ns
}

// updateMemoryAsk routes confirm-prompt keys while the memory prompt shows.
// Saving resolves the remember call with the saved entry as its result;
// anything else declines it.
func (m Model) updateMemoryAsk(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	done, result := m.memoryAsk.Update(msg)
	if !done {
		return m, nil
	}
	res := result.(components.NoteSelectResult)
	m.memoryAsk = nil
	if res.Canceled || (res.Index != 0 && res.Index != 1) {
		return m.declineApproval()
	}
	scope := m.memory.ProjectScope
	if res.Index == 1 {
		scope = memory.GlobalScope
	}
	req := m.pendingApproval
	text := req.memoryDraft.Text
	if res.Note != "" {
		text += " (" + res.Note + ")"
	}
	m.recordDecision(decisionAllow, "user")
	resultText, err := m.memory.Save(scope, req.memoryDraft.Kind, text)
	if err != nil {
		resultText = "error: cannot save memory: " + err.Error()
	}
	m.pendingApproval = nil
	m.agent.ResolveApproval(resultText)
	m.recordToolEvent(req.call.Name, time.Duration(0), outcomeFromResult(resultText))
	m.appendEntry(entry{kind: entryTool, toolName: req.call.Name, toolArgs: req.call.Arguments, toolResult: resultText})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m.advanceApprovalQueue()
}

// memoryAskLines renders the memory confirm prompt: the proposed entry above
// the scope selector card.
func (m Model) memoryAskLines() []string {
	var lines []string
	if req := m.pendingApproval; req != nil {
		head := fmt.Sprintf("Assistant proposes a %s memory: %q", req.memoryDraft.Kind, firstLine(req.memoryDraft.Text))
		for _, l := range strings.Split(m.wordWrap(head, m.contentWidth()), "\n") {
			lines = append(lines, sty.User.Render(l))
		}
	}
	return append(lines, strings.Split(m.memoryAsk.View(m.contentWidth()), "\n")...)
}
