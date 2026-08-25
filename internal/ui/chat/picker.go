package chat

// Interactive slash-command pickers (S-078). Bare /model and /mode open a
// components.Select in the bottom panel instead of printing usage text: ↑↓
// moves, enter applies, esc cancels. The argument forms (/model <name>,
// /mode <name>) keep their direct handleSlashCommand paths. Both share one
// generic statePick surface, so the session pickers built on it (/load,
// /chats, /branches — S-080) only need options and an apply function.
//
// The session pickers (S-080) and the /run code-block picker (S-081) open
// only when there is something to pick: no database, a read error, an empty
// list, or a lone code block falls through to the text message
// handleSlashCommand has always printed.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// WithModelOptions sets the models offered by the bare /model picker,
// normally the provider's curated catalog (provider.KnownModels). The
// session's current model is merged in when missing.
func (m Model) WithModelOptions(names []string) Model {
	m.modelOptions = names
	return m
}

// openPicker shows a select card in the bottom panel; apply consumes the
// chosen index and returns the transcript note.
func (m Model) openPicker(title string, opts []components.SelectOption, focus int, apply func(*Model, int) string) (tea.Model, tea.Cmd) {
	m.picker = &components.Select{
		Title:    title,
		Options:  opts,
		Focus:    focus,
		MaxLines: m.maxConfirmPanelHeight(),
	}
	m.pickerApply = apply
	m.state = statePick
	m.syncViewportHeight()
	return m, nil
}

// updatePick routes keys while a picker is showing.
func (m Model) updatePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+d" {
		m.quitting = true
		return m, m.quitCmd()
	}
	done, result := m.picker.Update(msg)
	if !done {
		return m, nil
	}
	sel := result.(components.SelectResult)
	apply := m.pickerApply
	m.picker = nil
	m.pickerApply = nil
	m.state = stateInput
	if sel.Canceled {
		m.syncViewportHeight()
		return m, nil
	}
	// An apply that hands the session to another surface — the /run picker
	// into the confirm prompt (S-081) — returns no note and keeps the state
	// it set instead of stateInput.
	if note := apply(&m, sel.Index); note != "" {
		m.appendEntry(entry{kind: entrySystem, text: note})
	}
	m.syncViewportHeight()
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, nil
}

// pickerLines is the rendered picker, one row per line.
func (m Model) pickerLines() []string {
	if m.picker == nil {
		return nil
	}
	return strings.Split(m.picker.View(m.contentWidth()), "\n")
}

// renderPick renders the picker padded to the bottom panel height.
func (m Model) renderPick() string {
	lines := m.pickerLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// modelPickChoices is the /model picker's option list: the curated catalog
// with the session's current model merged in (first when it isn't listed).
func (m Model) modelPickChoices() []string {
	for _, name := range m.modelOptions {
		if name == m.modelName {
			return m.modelOptions
		}
	}
	if m.modelName == "" {
		return m.modelOptions
	}
	return append([]string{m.modelName}, m.modelOptions...)
}

// canPickModel reports whether bare /model should open the picker rather
// than fall back to the usage text.
func (m Model) canPickModel() bool {
	return m.switchFn != nil && len(m.modelPickChoices()) > 1
}

// openModelPick opens the interactive /model picker, focused on the current
// model, with per-model pricing when the table knows it.
func (m Model) openModelPick() (tea.Model, tea.Cmd) {
	choices := m.modelPickChoices()
	opts := make([]components.SelectOption, len(choices))
	focus := 0
	for i, name := range choices {
		label := name
		if name == m.modelName {
			label += "  (current)"
			focus = i
		}
		desc := ""
		if m.prices != nil {
			if in, out, ok := m.prices.Cost(name, 1_000_000, 1_000_000); ok {
				desc = fmt.Sprintf("$%.2f in / $%.2f out per Mtok", in, out)
			}
		}
		opts[i] = components.SelectOption{Label: label, Desc: desc}
	}
	return m.openPicker("Switch model", opts, focus, func(m *Model, idx int) string {
		name := choices[idx]
		if name == m.modelName {
			return fmt.Sprintf("Already using %s.", name)
		}
		m.switchFn(name)
		m.modelName = name
		return fmt.Sprintf("Switched model to %s.", name)
	})
}

// openModePick opens the interactive /mode picker over the session's mode
// cycle, focused on the active mode.
func (m Model) openModePick() (tea.Model, tea.Cmd) {
	cycle := m.modeCycle
	if len(cycle) == 0 {
		cycle = agent.DefaultCycle()
	}
	opts := make([]components.SelectOption, len(cycle))
	focus := 0
	for i, mode := range cycle {
		label := mode.String()
		if mode == m.mode {
			label += "  (current)"
			focus = i
		}
		opts[i] = components.SelectOption{Label: label, Desc: mode.Describe()}
	}
	return m.openPicker("Permission mode", opts, focus, func(m *Model, idx int) string {
		mode := cycle[idx]
		m.applyMode(mode)
		return fmt.Sprintf("Mode set to %s — %s.", mode, mode.Describe())
	})
}

// --- session pickers (S-080) ----------------------------------------------

// sessionDesc is the description row shared by every saved-chat and branch
// listing: how many turns it holds and when it was last written.
func sessionDesc(turns int, updated time.Time) string {
	return fmt.Sprintf("%d turns, %s", turns, updated.Local().Format("Jan 2 15:04"))
}

// openChatPick opens the saved-chat picker behind bare /load and /chats:
// enter loads the highlighted chat, esc keeps the current one. It reports
// false when there is nothing to pick, leaving the caller on the text path.
func (m Model) openChatPick() (tea.Model, tea.Cmd, bool) {
	if m.db == nil {
		return m, nil, false
	}
	entries, err := m.db.ListChats()
	if err != nil || len(entries) == 0 {
		return m, nil, false
	}
	opts := make([]components.SelectOption, len(entries))
	focus := 0
	for i, e := range entries {
		label := e.Name
		if e.Name == m.sessionName {
			label += "  (current)"
			focus = i
		}
		opts[i] = components.SelectOption{Label: label, Desc: sessionDesc(e.Turns, e.UpdatedAt)}
	}
	model, cmd := m.openPicker("Load a saved chat", opts, focus, func(m *Model, idx int) string {
		return m.loadChatByName(entries[idx].Name)
	})
	return model, cmd, true
}

// openBranchPick opens the branch picker behind bare /branches, focused on
// the current branch. Selecting one switches to it with the usual
// save-the-current-branch-first semantics. It reports false when the session
// has no branch family to pick from.
func (m Model) openBranchPick() (tea.Model, tea.Cmd, bool) {
	if m.db == nil {
		return m, nil, false
	}
	branches, err := m.db.ListChatBranches(m.sessionName)
	if err != nil || len(branches) < 2 {
		return m, nil, false
	}
	opts := make([]components.SelectOption, len(branches))
	focus := 0
	for i, b := range branches {
		label := b.Name
		if b.Name == m.sessionName {
			label += "  (current)"
			focus = i
		}
		desc := sessionDesc(b.Turns, b.UpdatedAt)
		if b.Parent != "" {
			desc += fmt.Sprintf(" · branch of %q", b.Parent)
		}
		opts[i] = components.SelectOption{Label: label, Desc: desc}
	}
	model, cmd := m.openPicker("Switch branch", opts, focus, func(m *Model, idx int) string {
		return m.switchToBranch(branches[idx].Name)
	})
	return model, cmd, true
}

// --- run picker (S-081) ---------------------------------------------------

// runPreviewMax bounds the description row's flattened block preview so a
// long block does not build a string the card only clips away.
const runPreviewMax = 160

// openRunPick opens the code-block picker behind bare /run when the last
// response holds more than one block. Selecting a block hands off to the
// existing confirm-run flow — safety warnings and y/n/a semantics unchanged.
// It reports false when there is nothing to pick (no runner, no blocks, or a
// single block), leaving the caller on the direct startRun path.
func (m Model) openRunPick() (tea.Model, tea.Cmd, bool) {
	if m.runFn == nil {
		return m, nil, false
	}
	blocks := extractCodeBlockInfo(m.lastAssistantText())
	if len(blocks) < 2 {
		return m, nil, false
	}
	opts := make([]components.SelectOption, len(blocks))
	for i, b := range blocks {
		// A one-line block's preview is just its label again, so it gets no
		// description row.
		desc := runPickPreview(b.body)
		if desc == blockHead(b.body) {
			desc = ""
		}
		opts[i] = components.SelectOption{Label: runPickLabel(b), Desc: desc}
	}
	model, cmd := m.openPicker("Run a code block", opts, 0, func(m *Model, idx int) string {
		m.pendingRun = blocks[idx].body
		m.state = stateConfirmRun
		return ""
	})
	return model, cmd, true
}

// runPickLabel is a block's picker row: its first line, then the fence's
// language tag when it carried one, then how many lines it holds.
func runPickLabel(b codeBlock) string {
	head := blockHead(b.body)
	if head == "" {
		head = "(empty block)"
	}
	n := blockLines(b.body)
	meta := fmt.Sprintf("%d lines", n)
	if n == 1 {
		meta = "1 line"
	}
	if b.lang != "" {
		meta = b.lang + " · " + meta
	}
	return head + "  ·  " + meta
}

// runPickPreview flattens a block onto the description row: blank lines
// dropped, line breaks shown as ⏎, capped at runPreviewMax.
func runPickPreview(body string) string {
	var parts []string
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	preview := strings.Join(parts, " ⏎ ")
	if r := []rune(preview); len(r) > runPreviewMax {
		preview = strings.TrimRight(string(r[:runPreviewMax]), " ") + " …"
	}
	return preview
}
