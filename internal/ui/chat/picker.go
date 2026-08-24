package chat

// Interactive slash-command pickers (S-078). Bare /model and /mode open a
// components.Select in the bottom panel instead of printing usage text: ↑↓
// moves, enter applies, esc cancels. The argument forms (/model <name>,
// /mode <name>) keep their direct handleSlashCommand paths. Both share one
// generic statePick surface, so future pickers (/load, /branches — S-080)
// only need options and an apply function.

import (
	"fmt"
	"strings"

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
	note := apply(&m, sel.Index)
	m.appendEntry(entry{kind: entrySystem, text: note})
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
