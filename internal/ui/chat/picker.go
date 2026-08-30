package chat

// Interactive slash-command pickers. Bare /model and /permissions
// open a components.Select in the bottom panel instead of printing usage
// text: ↑↓ moves, enter applies, esc cancels. The argument forms (/model
// <name>, /permissions <name>) keep their direct handleSlashCommand paths.
// Both share one generic statePick surface, so the session pickers built on
// it (/load, /chats, /branches) only need options and an apply
// function.
//
// The session pickers and the /run code-block picker open
// only when there is something to pick: no database, a read error, an empty
// list, or a lone code block falls through to the text message
// handleSlashCommand has always printed.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// keys.Select.Alt is the /model picker's second key: take this option and
// make it the default, rather than taking it for this session. A bare letter
// like the card's own j/k, so it is text while the filter row is open.

// WithModelOptions sets the models offered by the bare /model picker,
// normally the provider's curated catalog (provider.KnownModels). The
// session's current model is merged in when missing.
func (m Model) WithModelOptions(names []string) Model {
	m.modelOptions = names
	return m
}

// openPicker shows a select card in the bottom panel; apply consumes the
// chosen index — always an index into the list the picker opened over, never
// into whatever a filter left of it — and returns the transcript note.
//
// Every picker opened this way carries the filter row: the card
// offers [/], and the match rule lives here rather than inside the component.
func (m Model) openPicker(title string, opts []components.SelectOption, focus int, apply func(*Model, int) string) (tea.Model, tea.Cmd) {
	return m.openPickerWith(title, opts, focus, pickerAlt{}, func(m *Model, idx int, _ bool) string {
		return apply(m, idx)
	})
}

// pickerAlt is a picker's second reading of the same choice: the key
// that takes it, what that key buys, and what plain enter buys once the two
// have to be told apart. The zero value is a card with enter alone, which is
// every picker but /model's.
type pickerAlt struct {
	Key   string
	Label string
	Enter string
}

// openPickerWith is openPicker for a card whose choice has two readings;
// apply is told which key took it.
func (m Model) openPickerWith(title string, opts []components.SelectOption, focus int, alt pickerAlt, apply func(*Model, int, bool) string) (tea.Model, tea.Cmd) {
	m.picker = &components.Select{
		Title:      title,
		Options:    opts,
		Focus:      focus,
		MaxLines:   m.maxConfirmPanelHeight(),
		Filterable: true,
		QueryHint:  "type to filter",
		Total:      selectableOptions(opts),
		AltKey:     alt.Key,
		AltLabel:   alt.Label,
		EnterLabel: alt.Enter,
	}
	m.pickerAll = opts
	m.pickerIndex = identityIndex(len(opts))
	m.pickerApply = apply
	m.enterSurface(statePick)
	m.syncViewport()
	return m, nil
}

// selectableOptions counts what a key can land on, which is what the card's
// counts are about: a group rail is a label for options and is not one.
func selectableOptions(opts []components.SelectOption) int {
	n := 0
	for _, o := range opts {
		if !o.Header {
			n++
		}
	}
	return n
}

// identityIndex is the row-to-option map of an unfiltered list.
func identityIndex(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// refilterPicker re-runs the match rule after the card's query line changed.
// The component does not filter: it reports the query, this decides
// what matches it, and the card is handed the matches, the catalog they came
// out of, and the nearest option there is when nothing matched at all.
func (m *Model) refilterPicker() {
	matches, index := pickerMatches(m.pickerAll, m.picker.Query)
	m.picker.Options = matches
	m.pickerIndex = index
	m.picker.Closest = ""
	if len(matches) == 0 {
		m.picker.Closest = closestOption(m.pickerAll, m.picker.Query)
	}
	m.picker.Focus = m.picker.FirstSelectable()
}

// pickerMatches is the picker's match rule: a case-insensitive run of the
// option's label. It is a substring and not the palette's looser subsequence
// because the card bolds the run it matched — a rule the row cannot
// show is a rule the reader cannot check. It returns the matches and, beside
// them, where each came from, so an apply still receives the index it was
// written against.
func pickerMatches(all []components.SelectOption, query string) ([]components.SelectOption, []int) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all, identityIndex(len(all))
	}
	var (
		matches []components.SelectOption
		index   []int
	)
	for i, opt := range all {
		if opt.Header {
			continue
		}
		if strings.Contains(strings.ToLower(opt.Label), q) {
			matches = append(matches, opt)
			index = append(index, i)
		}
	}
	return matches, index
}

// closestOption is the nearest option to a query nothing matched: the first
// option carrying the longest leading run of the query. It is the same
// substring test as the match rule, tried on shorter and shorter prefixes, so
// "sonnet-5" finds claude-sonnet-4.6 through the "sonnet-" the two share. A
// query with nothing at all in common names nothing rather than guessing.
func closestOption(all []components.SelectOption, query string) string {
	q := []rune(strings.ToLower(strings.TrimSpace(query)))
	for n := len(q); n > 0; n-- {
		prefix := string(q[:n])
		for _, opt := range all {
			if opt.Header {
				continue
			}
			if strings.Contains(strings.ToLower(opt.Label), prefix) {
				return opt.Label
			}
		}
	}
	return ""
}

// updatePick routes keys while a picker is showing.
func (m Model) updatePick(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	// The palette is this surface with a query on it: same card, same panel
	// accounting, but every key that is not movement or dispatch is text
	// (palette.go).
	if m.palette != nil {
		return m.updatePalette(msg)
	}
	// The saved-chat picker's housekeeping keys, and the confirm or rename
	// row one of them opened (chats.go), come before the card sees the key.
	if model, cmd, handled := m.updateChatOps(msg); handled {
		return model, cmd
	}
	done, result := m.picker.Update(msg)
	if m.picker.QueryChanged() {
		m.refilterPicker()
		m.syncViewport()
		return m, nil
	}
	if !done {
		return m, nil
	}
	sel := result.(components.SelectResult)
	apply, index := m.pickerApply, m.pickerIndex
	m.closePicker()
	if sel.Canceled {
		m.syncViewport()
		return m, nil
	}
	// The card answers with a row of what it was showing; the apply was
	// written against the list the picker opened over, so a filtered choice
	// is mapped back before it is spent.
	if sel.Index >= 0 && sel.Index < len(index) {
		sel.Index = index[sel.Index]
	}
	// An apply that hands the session to another surface — the /run picker
	// into the confirm prompt — returns no note and keeps the state
	// it set instead of stateInput.
	if note := apply(&m, sel.Index, sel.Alt); note != "" {
		m.appendEntry(entry{kind: entrySystem, text: note})
	}
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
}

// pickerLines is the rendered picker, one row per line.
func (m Model) pickerLines() []string {
	if m.picker == nil {
		return nil
	}
	lines := strings.Split(m.picker.View(m.contentWidth()), "\n")
	return append(lines, m.chatPickLines()...)
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
// than fall back to the usage text: either the catalog already offers a
// choice, or the provider can enumerate its endpoint for one.
func (m Model) canPickModel() bool {
	if m.switchFn == nil {
		return false
	}
	return len(m.modelPickChoices()) > 1 || (m.modelLister != nil && !m.modelListed)
}

// WithModelLister wires live model discovery for providers that can
// enumerate their endpoint (provider.ModelLister). Bare /model queries it
// once per session — lazily, so a slow or unreachable endpoint costs nothing
// until the user asks — and the result replaces the curated catalog.
func (m Model) WithModelLister(fn func(context.Context) ([]string, error)) Model {
	m.modelLister = fn
	return m
}

// startModelPick is the bare-/model entry point: it queries the provider for
// its model list when one is available and not yet fetched, and otherwise
// opens the picker straight away.
func (m Model) startModelPick() (tea.Model, tea.Cmd) {
	if m.modelLister == nil || m.modelListed {
		return m.openModelPick()
	}
	lister := m.modelLister
	ctx, cancel := context.WithCancel(context.Background())
	m.modelListCancel = cancel
	m.enterSurface(stateModelList)
	m.syncViewport()
	return m, func() tea.Msg {
		names, err := lister(ctx)
		return modelListMsg{names: names, err: err}
	}
}

// updateModelList routes keys while the model list is in flight: esc (or
// ctrl+c) abandons the query and returns to the input.
func (m Model) updateModelList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Draft.Quit):
		m.quitting = true
		if m.modelListCancel != nil {
			m.modelListCancel()
			m.modelListCancel = nil
		}
		return m, m.quitCmd()
	case keys.Is(pressed, keys.Select.Cancel):
		if m.modelListCancel != nil {
			m.modelListCancel()
			m.modelListCancel = nil
		}
		m.leaveSurface()
		m.syncViewport()
		return m, nil
	}
	return m, nil
}

// finishModelList opens the picker over the discovered models. A failed or
// empty query keeps the curated catalog, and says so — for an
// openai-compatible endpoint that catalog is empty, so the note is the whole
// answer and there is no picker to open.
func (m Model) finishModelList(msg modelListMsg) (tea.Model, tea.Cmd) {
	if m.state != stateModelList {
		// The query was abandoned (esc) or the session moved on.
		return m, nil
	}
	m.modelListCancel = nil
	m.leaveSurface()
	switch {
	case msg.err != nil:
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("Could not list models: %v", msg.err)})
	case len(msg.names) == 0:
		m.modelListed = true
		m.appendEntry(entry{kind: entrySystem, text: "The provider reported no models."})
	default:
		m.modelListed = true
		m.modelOptions = msg.names
	}
	if len(m.modelPickChoices()) > 1 {
		return m.openModelPick()
	}
	// Nothing to pick from: fall back to the text /model has always printed.
	if ok, note := m.handleSlashCommand("/model"); ok {
		m.appendEntry(entry{kind: entrySystem, text: note})
	}
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
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
	// The picker is where a model gets chosen, so it is where the choice has
	// to be able to stick. Enter switches the session, as it always
	// did; [d] switches it and writes provider.model, so the name you just
	// read off a list does not have to be typed back to `/model default`.
	alt := pickerAlt{Key: keys.Shown(keys.Select.Alt), Label: "and make it default", Enter: "this session"}
	if m.writeConfig == nil {
		alt = pickerAlt{}
	}
	return m.openPickerWith("Switch model", opts, focus, alt, func(m *Model, idx int, makeDefault bool) string {
		name := choices[idx]
		switched := name != m.modelName
		if switched {
			m.switchFn(name)
			m.modelName = name
		}
		if !makeDefault {
			if !switched {
				return fmt.Sprintf("Already using %s.", name)
			}
			return fmt.Sprintf("Switched to %s for this session. [d] in the picker makes a choice the default.", name)
		}
		// setModelDefault owns the writing and everything true about it —
		// the failure wording, and the warning when something outranks the
		// file. Saying any of it a second time here is how the two come to
		// disagree.
		saved := m.setModelDefault("default", []string{name})
		if !switched {
			return saved
		}
		return fmt.Sprintf("Switched to %s. %s", name, saved)
	})
}

// openModePick opens the interactive /permissions picker over the session's
// mode cycle, focused on the active mode.
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

// --- session pickers ----------------------------------------------

// sessionDesc is the description row shared by every saved-chat and branch
// listing: how many turns it holds and when it was last written.
func sessionDesc(turns int, updated time.Time) string {
	return fmt.Sprintf("%d turns, %s", turns, updated.Local().Format("Jan 2 15:04"))
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

// --- run picker ---------------------------------------------------

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
		m.pendingBlast = m.resolveRadius(nil)
		m.setTurnState(stateConfirmRun)
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
