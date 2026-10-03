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
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// keys.Select.Alt is the /model picker's second key: take this option and
// make it the default, rather than taking it for this session. A bare letter
// like the card's own j/k, so it is text while the filter row is open — and
// that card opens with the row open, which is why everything that names the
// key names the [ctrl+u] that closes the row first.

// WithModelOptions sets the models offered by the bare /model picker,
// normally the provider's curated catalog (provider.KnownModels). The
// session's current model is merged in when missing.
func (m Model) WithModelOptions(names []string) Model {
	m.picker.models.options = names
	return m
}

// openPicker shows a select card in the bottom panel; apply consumes the
// chosen index — always an index into the list the picker opened over, never
// into whatever a filter left of it — and returns the transcript note.
//
// Every picker opened this way carries the filter row: the card
// offers [/], and the match rule lives here rather than inside the component.
// The row starts closed, which is the reading a fixed set of answers wants:
// its rows are numbered, and its letters are keys.
func (m Model) openPicker(title string, opts []components.SelectOption, focus int, apply func(*Model, int) string) (tea.Model, tea.Cmd) {
	return m.openPickerWith(title, opts, focus, pickerAlt{}, false, func(m *Model, idx int, _ bool) (string, tea.Cmd) {
		return apply(m, idx), nil
	})
}

// openSearchPicker is openPicker for a card that opens over a catalog — the
// models, the branches, the backlog — where walking is the slow way and the
// first thing a reader does is name what they are after. The query row is
// open when the card arrives, so that first keystroke searches rather than
// being spent opening the row it would have gone into.
// See docs/interface/surfaces.md#selectors.
// Its apply answers with a command as well as a note: a choice that rewrites
// the conversation — the rewind is the one — owes the autosave that follows
// it, and a picker whose apply could only answer with words would leave every
// caller to remember the save on its behalf.
func (m Model) openSearchPicker(title string, opts []components.SelectOption, focus int, apply func(*Model, int) (string, tea.Cmd)) (tea.Model, tea.Cmd) {
	return m.openPickerWith(title, opts, focus, pickerAlt{}, true, func(m *Model, idx int, _ bool) (string, tea.Cmd) {
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
// apply is told which key took it. search opens the query row with the card
// (openSearchPicker).
func (m Model) openPickerWith(title string, opts []components.SelectOption, focus int, alt pickerAlt, search bool, apply func(*Model, int, bool) (string, tea.Cmd)) (tea.Model, tea.Cmd) {
	m.picker.card = &components.Select{
		Title:      title,
		Options:    opts,
		Focus:      focus,
		MaxLines:   m.maxConfirmPanelHeight(),
		Filterable: true,
		Filtering:  search,
		QueryHint:  "type to filter",
		Total:      selectableOptions(opts),
		AltKey:     alt.Key,
		AltLabel:   alt.Label,
		EnterLabel: alt.Enter,
	}
	// The panel places the terminal's own cursor on the filter row, so the
	// card stops painting one (docs/interface/surfaces.md#selectors).
	m.picker.card.SetVirtualCursor(false)
	m.picker.all = opts
	m.picker.index = identityIndex(len(opts))
	m.picker.apply = apply
	m.picker.fromReading = m.state == stateFocus
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
	matches, index := pickerMatches(m.picker.all, m.picker.card.Query)
	// The saved-chat card looks past the names as well, and folds what the
	// store found into the same list (chats.go).
	matches, index = m.withChatMatches(matches, index)
	m.picker.card.Options = matches
	m.picker.index = index
	m.picker.card.Closest = ""
	if len(matches) == 0 {
		m.picker.card.Closest = closestOption(m.picker.all, m.picker.card.Query)
	}
	m.picker.card.Focus = m.picker.card.FirstSelectable()
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
	// And the rewind picker's diff key (rewind.go).
	if model, cmd, handled := m.updateRewindPick(msg); handled {
		return model, cmd
	}
	done, sel := m.picker.card.Update(msg)
	if m.picker.card.QueryChanged() {
		m.refilterPicker()
		m.syncViewport()
		return m, nil
	}
	if !done {
		return m, nil
	}
	apply, index := m.picker.apply, m.picker.index
	m.closePicker()
	if sel.Canceled {
		m.syncViewport()
		if m.state == stateFocus {
			m.refreshFocusView()
		}
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
	note, cmd := apply(&m, sel.Index, sel.Alt)
	if note != "" {
		m.appendEntry(entry{kind: entrySystem, text: note})
	}
	m.syncViewport()
	if m.state == stateFocus {
		// A card opened over reading mode leaves the cursor where it was,
		// and the pane where the reader had scrolled it.
		m.refreshFocusView()
		return m, cmd
	}
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, cmd
}

// pickerLines is the rendered picker, one row per line — under the rule that
// names it, for a card that carries a rail label. Only a picker that is a
// surface in its own right does: a menu a command dropped is named by the
// command that dropped it, and a second rail over one would be chrome saying
// what the card's own title already said
// (docs/interface/surfaces.md#the-rewind).
func (m Model) pickerLines() []string {
	if m.picker.card == nil {
		return nil
	}
	width := m.contentWidth()
	var lines []string
	if m.picker.card.Rail != "" {
		lines = append(lines, keyboardRail(m.picker.card.Rail, width))
	}
	lines = append(lines, strings.Split(m.picker.card.View(width), "\n")...)
	return append(lines, m.chatPickLines()...)
}

// modelPickChoices is the /model picker's option list: the curated catalog
// with the session's current model merged in (first when it isn't listed).
func (m Model) modelPickChoices() []string {
	for _, name := range m.picker.models.options {
		if name == m.modelName {
			return m.picker.models.options
		}
	}
	if m.modelName == "" {
		return m.picker.models.options
	}
	return append([]string{m.modelName}, m.picker.models.options...)
}

// canPickModel reports whether bare /model should open the picker rather
// than fall back to the usage text: either the catalog already offers a
// choice, or the provider can enumerate its endpoint for one.
func (m Model) canPickModel() bool {
	if m.switchFn == nil {
		return false
	}
	return len(m.modelPickChoices()) > 1 || (m.picker.models.lister != nil && !m.picker.models.listed)
}

// WithModelLister wires live model discovery for providers that can
// enumerate their endpoint (provider.ModelLister). Bare /model queries it
// once per session — lazily, so a slow or unreachable endpoint costs nothing
// until the user asks — and the result replaces the curated catalog.
func (m Model) WithModelLister(fn func(context.Context) ([]string, error)) Model {
	m.picker.models.lister = fn
	return m
}

// startModelPick is the bare-/model entry point: it queries the provider for
// its model list when one is available and not yet fetched, and otherwise
// opens the picker straight away.
func (m Model) startModelPick() (tea.Model, tea.Cmd) {
	if m.picker.models.lister == nil || m.picker.models.listed {
		return m.openModelPick()
	}
	lister := m.picker.models.lister
	ctx, cancel := context.WithCancel(context.Background())
	m.picker.models.cancel = cancel
	m.enterSurface(stateModelList)
	m.syncViewport()
	return m, func() tea.Msg {
		names, err := lister(ctx)
		return modelListMsg{names: names, err: err}
	}
}

// answerModelList routes keys while the model list is in flight: esc (or
// ctrl+c) abandons the query, and the host puts the screen back.
func (m *Model) answerModelList(msg tea.KeyPressMsg) (bool, overlayAction) {
	if !keys.Match(msg, keys.Select.Cancel) {
		return false, overlayAction{}
	}
	m.picker.models.stop()
	return true, overlayAction{close: true}
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
	m.picker.models.cancel = nil
	m.leaveSurface()
	switch {
	case msg.err != nil:
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("could not list models: %v", msg.err)})
	case len(msg.names) == 0:
		m.picker.models.listed = true
		m.appendEntry(entry{kind: entrySystem, text: "the provider reported no models"})
	default:
		m.picker.models.listed = true
		m.picker.models.options = msg.names
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
	// read off a list does not have to be typed back to `/model default`. The
	// card opens as a search, so [d] is a letter until [ctrl+u] closes the
	// query row — which is what the key row offers while it is open.
	alt := pickerAlt{Key: keys.Shown(keys.Select.Alt), Label: "and make it default", Enter: "this session"}
	if m.writeConfig == nil {
		alt = pickerAlt{}
	}
	// What esc leaves is the model the session is already on, which is the
	// one thing the word `cancel` cannot say
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	keep := m.modelName
	// The title names what chose the current model in the words the text
	// answer uses, so the two paths `/model` takes tell one story.
	title := "Switch model"
	if by := m.modelChosenBy(); by != "" {
		title += " · current" + by
	}
	updated, cmd := m.openPickerWith(title, opts, focus, alt, true, func(m *Model, idx int, makeDefault bool) (string, tea.Cmd) {
		name := choices[idx]
		switched := name != m.modelName
		if switched {
			m.switchFn(name)
			m.modelName = name
		}
		if !makeDefault {
			if !switched {
				return fmt.Sprintf("already using %s", name), nil
			}
			return fmt.Sprintf("switched to %s for this session. In the picker, [%s] then [%s] makes a choice the default",
				name, keys.Shown(keys.Select.ClearQ), keys.Shown(keys.Select.Alt)), nil
		}
		// setModelDefault owns the writing and everything true about it —
		// the failure wording, and the warning when something outranks the
		// file. Saying any of it a second time here is how the two come to
		// disagree.
		saved := m.setModelDefault("default", []string{name})
		if !switched {
			return saved, nil
		}
		return fmt.Sprintf("switched to %s. %s", name, saved), nil
	})
	next := updated.(Model)
	next.picker.card.CancelLabel = "keep " + keep
	return next, cmd
}

// openModePick opens the interactive /permissions picker over the session's
// mode cycle, focused on the active mode.
//
// A conversation has no picker to open: it answers with the one mode it has.
func (m Model) openModePick() (tea.Model, tea.Cmd) {
	if m.conversation {
		m.noteOneMode()
		return m, nil
	}
	cycle := m.policy.cycle
	if len(cycle) == 0 {
		cycle = agent.DefaultCycle()
	}
	opts := make([]components.SelectOption, len(cycle))
	focus := 0
	for i, mode := range cycle {
		label := mode.String()
		if mode == m.policy.mode {
			label += "  (current)"
			focus = i
		}
		opts[i] = components.SelectOption{Label: label, Desc: mode.Describe()}
	}
	updated, cmd := m.openPicker("Permission mode", opts, focus, func(m *Model, idx int) string {
		mode := cycle[idx]
		m.applyMode(mode)
		return fmt.Sprintf("mode set to %s — %s", mode, mode.Describe())
	})
	// Esc leaves the session on the mode it is in, and says which one
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	next := updated.(Model)
	next.picker.card.CancelLabel = "keep " + m.policy.mode.String()
	return next, cmd
}

// --- session pickers ----------------------------------------------

// sessionDesc is the description row shared by every saved-chat and branch
// listing: how many turns it holds and when it was last written.
func sessionDesc(turns int, updated time.Time) string {
	return plural(turns, "turn") + " · " + updated.Local().Format("Jan 2 15:04")
}

// currentBranchPhrase marks the row the session is already on. It is the
// right-aligned short field rather than a suffix on the name, so the label
// column stays the branch name and nothing else, and it is two words rather
// than a clause so it is still on the row at the narrowest width
// (docs/interface/surfaces.md#selectors).
const currentBranchPhrase = "this one"

// branchLabel is a branch's name with the part it shares with its parent
// elided. A branch is named for the session it was cut from, so a family
// listed in full repeats the same prefix on every row and pushes what
// differs — the turn and the moment of the cut — off the end of it, taking
// the row's description and its marker with it. The card's own elision mark
// stands in for the shared run; a branch whose name is not built on its
// parent's, because it was saved under one of its own, keeps all of it.
func branchLabel(name, parent string) string {
	if parent == "" || !strings.HasPrefix(name, parent) {
		return name
	}
	return "…" + strings.TrimPrefix(name, parent)
}

// branchPickOptions builds the branch picker's rows and says which one to
// focus: the branch the session is on. Each row carries what the family
// knows about a branch — its turn count, when it was last written, and the
// branch it was cut from. Rows act on the whole name whatever the label
// shows.
func (m Model) branchPickOptions(branches []storage.ChatBranch) ([]components.SelectOption, int) {
	opts := make([]components.SelectOption, len(branches))
	focus := 0
	for i, b := range branches {
		desc := sessionDesc(b.Turns, b.UpdatedAt)
		if b.Parent != "" {
			desc += fmt.Sprintf(" · branch of %s", branchLabel(b.Parent, parentOf(branches, b.Parent)))
		}
		opts[i] = components.SelectOption{Label: branchLabel(b.Name, b.Parent), Desc: desc}
		if b.Name == m.sessionName {
			opts[i].Meta = currentBranchPhrase
			focus = i
		}
	}
	return opts, focus
}

// parentOf is a family member's own parent, for naming a row's parent the
// same way that parent's own row is named.
func parentOf(branches []storage.ChatBranch, name string) string {
	for _, b := range branches {
		if b.Name == name {
			return b.Parent
		}
	}
	return ""
}

// openBranchPick opens the branch picker behind bare /branches, focused on
// the current branch. Selecting one switches to it with the usual
// save-the-current-branch-first semantics. It reports false when the session
// has no branch family to pick from.
func (m Model) openBranchPick() (tea.Model, tea.Cmd, bool) {
	branches, _ := m.branchFamily()
	if len(branches) == 0 {
		return m, nil, false
	}
	opts, focus := m.branchPickOptions(branches)
	model, cmd := m.openSearchPicker("Switch branch", opts, focus, func(m *Model, idx int) (string, tea.Cmd) {
		return m.switchToBranch(branches[idx].Name), nil
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
	model, cmd := m.openBlockPick("Run a code block", "take none", blocks, func(m *Model, idx int) (string, tea.Cmd) {
		m.pendingRun = blocks[idx].body
		m.approval.blast = m.resolveRadius(nil)
		m.setTurnState(stateConfirmRun)
		return "", nil
	})
	return model, cmd, true
}

// openBlockPick is the numbered card over a reply's code blocks, which /run
// and /copy code both open: one dressing for one question — which of these
// blocks — whatever is then done with the answer
// (docs/interface/surfaces.md#selectors). The rows are a handful and fixed,
// so the card carries no filter, and each row's number is its own key, so
// the key row spends nothing repeating them; the lit row's whole block rides
// under it, flattened, because a first line alone rarely tells two shell
// blocks apart. cancel is what esc leaves, in the caller's words.
func (m Model) openBlockPick(title, cancel string, blocks []codeBlock, apply func(*Model, int) (string, tea.Cmd)) (tea.Model, tea.Cmd) {
	opts := blockPickOptions(blocks, m.contentWidth())
	next, cmd := m.openPickerWith(title, opts, 0, pickerAlt{}, false, func(m *Model, idx int, _ bool) (string, tea.Cmd) {
		return apply(m, idx)
	})
	pm := next.(Model)
	pm.picker.card.Filterable = false
	pm.picker.card.FocusDesc = true
	pm.picker.card.Tone = components.CardDecision
	pm.picker.card.Chips = []string{plural(len(blocks), "block")}
	pm.picker.card.CancelLabel = cancel
	pm.picker.card.HintKeys = []components.KeyOffer{
		components.Offer(keys.Select.Move), components.Offer(keys.Select.Take),
	}
	pm.syncViewport()
	return pm, cmd
}

// blockPickOptions are the card's rows: a block's first line, then its
// language, then how many lines it holds, with the block flattened under the
// lit one. The first line is what gives way on a narrow card, cut short of
// where the row would overrun, so the language and the count are never the
// part that is lost: they are what tells two blocks apart once the first
// lines start to look alike.
func blockPickOptions(blocks []codeBlock, width int) []components.SelectOption {
	// The numbering column and the space after it, as the card lays them.
	numbered := len(strconv.Itoa(len(blocks))) + 2
	opts := make([]components.SelectOption, len(blocks))
	for i, b := range blocks {
		head := blockHead(b.body)
		if head == "" {
			head = "(empty block)"
		}
		facts := blockWord(b.lang) + " · " + plural(blockLines(b.body), "line")
		room := components.Card{}.Inner(width) - components.GridPointerWidth - numbered - lipgloss.Width(" · "+facts)
		if room > 0 && lipgloss.Width(head) > room {
			head = components.Clip(head, room)
		}
		// A one-line block's preview is just its label again, so it gets no
		// description row.
		desc := runPickPreview(b.body)
		if desc == blockHead(b.body) {
			desc = ""
		}
		opts[i] = components.SelectOption{
			Label: head,
			Detail: []components.DetailSpan{
				{Text: blockWord(b.lang), Tone: components.ToneNeutral},
				{Text: " · " + plural(blockLines(b.body), "line"), Tone: components.ToneQuiet},
			},
			Desc: desc,
		}
	}
	return opts
}

// runPickPreview flattens a block onto the description row: blank lines
// dropped, line breaks shown as ↵, capped at runPreviewMax.
func runPickPreview(body string) string {
	var parts []string
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	preview := strings.Join(parts, " ↵ ")
	if r := []rune(preview); len(r) > runPreviewMax {
		preview = strings.TrimRight(string(r[:runPreviewMax]), " ") + " …"
	}
	return preview
}
