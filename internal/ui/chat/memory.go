package chat

// Durable memory. The /memory slash command manages entries, and the
// model's remember tool proposes new ones. The trust rule is absolute:
// agent-proposed memories persist only after explicit user confirmation on
// the memory prompt (docs/interface/surfaces.md#selectors) — no permission
// mode, session grant, or classifier verdict can wave one through, because
// memory an agent writes to itself is an injection surface.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Memory wires durable memory into the chat TUI. The zero value
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
	// EntryText is one entry's stored text, which /memory edit opens the
	// editor on.
	EntryText func(id int64) (string, error)
	// Rewrite replaces one entry's text and returns the line confirming it.
	Rewrite func(id int64, text string) (string, error)
	// Omitted is how many of this session's memories the recall budget left
	// out of the system prompt. It is a value rather than a call because
	// nothing in a session changes it: recall runs once, before the first
	// turn, and an entry edited now is carried by the next session.
	Omitted int
}

// WithMemory enables the /memory command and the remember-tool confirm flow.
// WithSkills hands the model the session's skill catalog and the listing
// /skills prints. Activated skill content is exempted from context
// trimming: the instructions are guidance for every later turn, and a
// trimmed skill fails silently — the model just stops following it.
// See docs/capabilities/skills.md#a-skill-is-read-in-three-tiers.
func (m Model) WithSkills(c *skill.Catalog, list func(*skill.Catalog) string) Model {
	m.skills = c
	m.skillsList = list
	m.agent.KeepResults(skill.IsContent)
	return m
}

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

// openMemoryAsk shows the memory confirm prompt
// (docs/interface/surfaces.md#selectors) for the pending remember proposal:
// save to project or global scope — with an optional note amending the entry
// — or don't save.
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
	m.recordDecision(observe.DecisionAllow, observe.ReasonUser)
	resultText, err := m.memory.Save(scope, req.memoryDraft.Kind, text)
	if err != nil {
		resultText = "error: cannot save memory: " + err.Error()
	}
	m.pendingApproval = nil
	m.agent.ResolveApproval(resultText)
	m.recordToolResult(req.call.Name, time.Duration(0), resultText)
	m.appendEntry(entry{kind: entryTool, toolName: req.call.Name, toolArgs: req.call.Arguments, toolResult: resultText})
	m.viewport.SetLines(m.renderHistoryLines())
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

// memoryEditorDoneMsg is the editor's exit from a memory's text. Like the
// draft's, the file is shhh's own and is removed when it comes back; unlike
// the backlog's, the editor never saw the record itself, so an editor that
// crashed halfway leaves the stored entry exactly as it was.
type memoryEditorDoneMsg struct {
	id   int64
	path string
	err  error
}

// openMemoryEditor hands one entry's text to the reader's editor. It is the
// answer to a memory the recall budget would not carry: a memory is already
// saved with a scope and a kind the user chose, so shortening it has to be an
// edit and not a delete and a re-add.
//
// The same refusals as the draft's editor apply — it takes the terminal, and
// a turn or a decision in flight would be lost behind it (editor.go).
func (m Model) openMemoryEditor(arg string) (tea.Model, tea.Cmd) {
	if m.memory.EntryText == nil || m.memory.Rewrite == nil {
		return m.systemNotice("Durable memory is unavailable in this session.")
	}
	if reason, refused := m.editorRefusal(); refused {
		return m.surfaceNotice(reason)
	}
	id, err := memory.ParseID(arg)
	if err != nil {
		return m.systemNotice("Error: " + err.Error())
	}
	text, err := m.memory.EntryText(id)
	if err != nil {
		return m.systemNotice("Error: " + err.Error())
	}
	path, err := writeDraftFile(text)
	if err != nil {
		return m.surfaceNotice("could not write the memory out — " + err.Error())
	}
	argv := editorArgv(editorCommand(), path, 1, 1)
	proc := exec.Command(argv[0], argv[1:]...)
	return m, tea.ExecProcess(proc, func(err error) tea.Msg {
		return memoryEditorDoneMsg{id: id, path: path, err: err}
	})
}

// memoryEditorFinished saves what came back. Every exit the editor can make
// arrives here, which is what makes this the one place the temp file is
// removed.
func (m Model) memoryEditorFinished(msg memoryEditorDoneMsg) (tea.Model, tea.Cmd) {
	defer func() { _ = os.Remove(msg.path) }()
	if msg.err != nil {
		return m.surfaceNotice("the editor exited with an error, so the memory is as it was — " + msg.err.Error())
	}
	edited, err := os.ReadFile(msg.path)
	if err != nil {
		return m.surfaceNotice("could not read the memory back, so it is as it was — " + err.Error())
	}
	text := strings.TrimSpace(string(edited))
	if text == "" {
		// An emptied file is how an editor says the edit was abandoned, and
		// it is also what a quit on a file the editor never wrote looks like
		// from here. Neither is a request to delete the memory, and there is
		// a command that is.
		return m.systemNotice(fmt.Sprintf("The editor came back empty, so the memory is as it was; /memory forget m%d drops one.", msg.id))
	}
	note, err := m.memory.Rewrite(msg.id, text)
	if err != nil {
		return m.systemNotice("Error: " + err.Error())
	}
	return m.systemNotice(note)
}
