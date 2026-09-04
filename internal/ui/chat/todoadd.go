package chat

// /todo add with nothing after it: the session read into proposed backlog
// items, shown on a card, and only what is accepted written. The rule is
// the memory prompt's: a session may propose, the person decides, and
// nothing lands on disk before they do
// (docs/capabilities/todo.md#a-session-proposes-you-accept).
//
// The reading is a background command like the summarizer's: nothing on
// screen waits for it, and a result arriving after the session moved on is
// dropped by its run number rather than shown over whatever is there now.
// /clear moves on: it cancels the reading and retires its number, since
// proposals from a conversation that no longer exists are nobody's.

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// todoProposalsMsg carries a finished reading back to the model.
type todoProposalsMsg struct {
	runID  int
	result todo.ExtractResult
}

// todoExtractEnabled reports whether the session can read itself into items.
func (m *Model) todoExtractEnabled() bool {
	return m.todosEnabled() && m.todos.Extractor.Enabled()
}

// startTodoExtract takes the reading. The transcript is digested here, on
// the model, so the command closes over plain strings and never touches
// the Model from another goroutine.
func (m Model) startTodoExtract() (tea.Model, tea.Cmd) {
	if !m.todoExtractEnabled() {
		return m.systemNotice("No model is configured to read the session into items. /todo add <text> adds one by hand.")
	}
	if m.todoExtracting {
		return m.systemNotice("Still reading the session — the proposals card opens when it is done.")
	}
	if len(m.transcript) == 0 {
		return m.systemNotice("Nothing to read yet: the session has no conversation.")
	}
	m.todoExtracting = true
	m.todoExtractRun++
	runID := m.todoExtractRun
	extractor := m.todos.Extractor
	req := m.todoExtractRequest()
	ctx, cancel := context.WithCancel(context.Background())
	m.todoExtractCancel = cancel
	model, _ := m.systemNotice("Reading the session for backlog items…")
	return model, func() tea.Msg {
		defer cancel()
		return todoProposalsMsg{runID: runID, result: extractor.Extract(ctx, req)}
	}
}

// dropTodoExtract retires a reading in flight: /clear calls it, so the
// result cannot land as proposals for a conversation that is gone.
func (m *Model) dropTodoExtract() {
	if m.todoExtractCancel != nil {
		m.todoExtractCancel()
		m.todoExtractCancel = nil
	}
	m.todoExtracting = false
	m.todoExtractRun++
}

// todoExtractRequest is the digest: what was said on each side, the tool
// rows the summarizer already treats as content-free, the changeset in
// words, and what already stands — the backlog, and the notes this session's
// agents wrote for each other. No tool output, no file contents.
func (m Model) todoExtractRequest() todo.ExtractRequest {
	req := todo.ExtractRequest{
		Activity: m.summaryActivity(),
		Changes:  m.summaryChanges(),
	}
	for _, e := range m.transcript {
		text := strings.TrimSpace(e.text)
		if text == "" {
			continue
		}
		switch e.kind {
		case entryUser:
			req.Instructions = append(req.Instructions, text)
		case entryAssistant:
			req.Assistant = append(req.Assistant, text)
		}
	}
	req.Existing = m.todoDraftExisting()
	return req
}

// finishTodoExtract applies a reading: a failed one is a sentence in the
// transcript, a good one is the proposals card with everything checked,
// because accepting the lot is the common case and dropping one is the
// exception worth a keystroke.
func (m Model) finishTodoExtract(msg todoProposalsMsg) (tea.Model, tea.Cmd) {
	if !m.todoExtracting || msg.runID != m.todoExtractRun {
		return m, nil
	}
	m.todoExtracting = false
	m.todoExtractCancel = nil
	r := msg.result
	if r.Failed {
		return m.systemNotice("The session could not be read into items — " + r.Err + ". /todo add <text> adds one by hand.")
	}
	return m.openTodoProposals(r.Proposals, plural(len(r.Proposals), "backlog item")+" proposed")
}

// openTodoProposals shows proposals on the card, everything checked.
func (m Model) openTodoProposals(proposals []todo.Proposal, what string) (tea.Model, tea.Cmd) {
	m.todoProposals = proposals
	opts := make([]components.SelectOption, len(proposals))
	for i, p := range proposals {
		// A multi-select draws the label and the right-hand meta and nothing
		// else, so the facts the reader decides on go in the meta.
		opts[i] = components.SelectOption{
			Label: p.Title,
			Meta:  todoProposalMeta(m.todos.Profile, p),
		}
	}
	card := components.NewMultiSelect(fmt.Sprintf("%s — %s toggles, %s all or none, %s writes the checked ones, %s writes nothing",
		what, keys.Shown(keys.Select.Toggle), keys.Shown(keys.Select.All), keys.Shown(keys.Select.Take), keys.Shown(keys.Select.Cancel)), opts)
	for i := range card.Checked {
		card.Checked[i] = true
	}
	card.MaxLines = m.maxConfirmPanelHeight()
	// The way into one proposal's header. A reading grades and connects the
	// items it proposes, and this is where a reader who disagrees says so
	// before anything is written — the alternative being a file written with
	// the reading's answers and then edited back.
	card.Actions = []string{keys.Shown(keys.Backlog.Edit) + " its header"}
	m.todoPropose = card
	m.enterSurface(stateTodoPropose)
	m.syncViewport()
	return m, nil
}

// todoProposalMeta is the row's right-hand field: the header fields the
// reading answered and what the item waits on, so the reader can judge it
// without opening it.
func todoProposalMeta(profile todo.Profile, p todo.Proposal) string {
	words := make([]string, 0, len(profile.Fields))
	for _, f := range profile.Fields {
		// A field the reading did not answer is left out rather than left
		// blank: two separators with nothing between them read as a row
		// that lost a word rather than as a proposal that has none.
		if word := p.Fields[f.Name]; word != "" {
			words = append(words, word)
		}
	}
	meta := strings.Join(words, " · ")
	if len(p.DependsOn) > 0 {
		meta += " · after " + strings.Join(p.DependsOn, ", ")
	}
	return meta
}

// answerTodoPropose routes keys while the proposals card shows.
func (m *Model) answerTodoPropose(msg tea.KeyPressMsg) (bool, overlayAction) {
	// The focused proposal's header, on the card the draft uses. It is the
	// one key here that does not answer this card.
	if keys.Is(msg.String(), keys.Backlog.Edit) &&
		m.todoPropose.Focus < len(m.todoProposals) {
		m.openTodoDraft(m.todoProposals[m.todoPropose.Focus], m.todoPropose.Focus)
		return false, overlayAction{}
	}
	done, res := m.todoPropose.Update(msg)
	if !done {
		return false, overlayAction{}
	}
	proposals := m.todoProposals
	// The row a blocked run left is claimed here whatever the answer was: a
	// card the reader declined wrote nothing to name on it, and leaving the
	// claim standing would put the next card's first item on a run that
	// never asked for it.
	followUp := m.todoRunner.followUpRow
	m.todoPropose = nil
	m.todoProposals = nil
	m.todoRunner.followUpRow = 0
	if res.Canceled {
		return true, overlayAction{close: true, note: "Nothing written; the proposals are dropped."}
	}
	note, written := m.writeProposals(proposals, res.Indices)
	if len(written) > 0 {
		m.signal(observe.SignalTodo, observe.TodoAdd)
	}
	m.nameFollowUpOnRun(followUp, written)
	return true, overlayAction{close: true, note: note}
}

// writeProposals writes the accepted proposals as items and says what it
// wrote. Dependencies are resolved here, once the accepted set is known: a
// proposal's title becomes that proposal's slug, an existing slug stays,
// and anything else — a dropped proposal, a name nothing matches — is
// removed and named, because a dependency on nothing would hold the item
// back forever.
// It also reports the slugs it wrote, in the order it wrote them, because a
// caller that offered the proposals for a reason — a blocked run's follow-up
// — has to be able to name what came of them.
func (m *Model) writeProposals(proposals []todo.Proposal, accepted []int) (string, []string) {
	s := m.todoStore
	taken := map[string]bool{}
	if s != nil {
		for _, it := range s.Items {
			taken[it.Slug] = true
		}
		for _, it := range s.Done {
			taken[it.Slug] = true
		}
	}
	// Each accepted proposal carries its own slug; the title index is only
	// for resolving what other proposals named, and two proposals with one
	// title resolve to whichever came first — a dependency on an ambiguous
	// name has to land somewhere, and first is the one the reader saw first.
	type accept struct {
		todo.Proposal
		slug string
	}
	slugFor := map[string]string{}
	var chosen []accept
	for _, i := range accepted {
		if i < 0 || i >= len(proposals) {
			continue
		}
		p := proposals[i]
		slug := uniqueSlug(todo.Slugify(p.Title), taken)
		taken[slug] = true
		if _, dup := slugFor[strings.ToLower(p.Title)]; !dup {
			slugFor[strings.ToLower(p.Title)] = slug
		}
		chosen = append(chosen, accept{Proposal: p, slug: slug})
	}
	created := time.Now().Format("2006-01-02")
	var b strings.Builder
	var dropped []string
	var slugs []string
	written := 0
	for _, a := range chosen {
		var deps []string
		for _, d := range a.DependsOn {
			switch {
			case slugFor[strings.ToLower(d)] != "":
				deps = append(deps, slugFor[strings.ToLower(d)])
			case s != nil && has(s, d):
				deps = append(deps, d)
			default:
				dropped = append(dropped, fmt.Sprintf("%s → %q", a.slug, d))
			}
		}
		a.DependsOn = deps
		it := a.Item(m.todos.Profile, a.slug, created, m.sessionName)
		if _, err := todo.Create(m.todos.Profile, m.todos.Root, it); err != nil {
			fmt.Fprintf(&b, "\ncould not write %s: %v", a.slug, err)
			continue
		}
		slugs = append(slugs, a.slug)
		written++
		fmt.Fprintf(&b, "\n  %s  %s · %s · %s", a.slug, it.Priority, gradeOrDash(it), it.Title)
	}
	m.reloadTodos()
	head := fmt.Sprintf("Wrote %s to %s.", plural(written, "backlog item"), todo.Dir(m.todos.Root))
	if written == 0 {
		head = "Wrote nothing."
	}
	out := head + b.String()
	if len(dropped) > 0 {
		out += "\nDropped dependencies that name nothing in the backlog: " + strings.Join(dropped, "; ") + "."
	}
	if written > 0 {
		out += "\n/todo edit <slug> opens one to refine it."
	}
	return out, slugs
}

// nameFollowUpOnRun puts the follow-up a blocked run offered onto the row at
// idx, which is the row that blocked, so the block and what was written about it are read in one
// place rather than as two rows a scroll apart. Only the first written slug
// is named: a block offers one proposal, and the reader who added others to
// the card was adding items, not answering the block.
func (m *Model) nameFollowUpOnRun(idx int, written []string) {
	if idx <= 0 || idx > len(m.transcript) || len(written) == 0 {
		return
	}
	if r := m.transcript[idx-1].todorun; r != nil {
		r.followUp = written[0]
		m.invalidateRenderCache()
	}
}

func has(s *todo.Store, slug string) bool {
	_, ok := s.Find(slug)
	return ok
}

// gradeOrDash is the item's grade, or a dash for one nobody has graded: a
// listing that left the column blank would read as a field the row forgot.
func gradeOrDash(it todo.Item) string {
	if grade := it.Grade(); grade != "" {
		return grade
	}
	return "-"
}

// uniqueSlug suffixes a slug that is already taken, so two proposals with
// one title — or one that repeats an archived item — both get a file.
func uniqueSlug(slug string, taken map[string]bool) string {
	if !taken[slug] {
		return slug
	}
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s-%d", slug, n)
		if len(cand) > todo.MaxSlugLen {
			prefix := strings.TrimRight(slug[:todo.MaxSlugLen-len(fmt.Sprint(n))-1], "-")
			cand = fmt.Sprintf("%s-%d", prefix, n)
		}
		if !taken[cand] {
			return cand
		}
	}
}

// todoProposeLines renders the proposals card for the bottom panel.
func (m Model) todoProposeLines() []string {
	if m.todoPropose == nil {
		return nil
	}
	return strings.Split(m.todoPropose.View(m.contentWidth()), "\n")
}

// /todo new <sentence>: the same proposal machinery entered from the other
// end. A reading of the session proposes the work it left behind; a sentence
// proposes the work somebody has just thought of. Both land on a card that
// writes nothing until it is accepted
// (docs/capabilities/todo.md#a-session-proposes-you-accept).
//
// The card is also where a header stops needing an editor. An item's header
// is a closed set of answers — what sort of work, how soon, how big, what has
// to land first — and a closed set is a row you step through, not a line you
// retype. The sections under it stay prose: the card renders them, and `e`
// hands the whole item to the editor the way an item file is handed to it.

// todoNewUsage is the one place the command's shape is written, so a refusal
// and the help cannot come to describe different commands.
const todoNewUsage = "Usage: /todo new <what the work is, in a sentence>"

// todoNewPrefix is what the backlog screen's `n` leaves in the draft box. A
// draft is made from a sentence and the screen has nowhere to type one, so
// the key hands the keyboard back with the question already asked rather than
// opening a card with nothing in it.
const todoNewPrefix = "/todo new "

// todoDraftMsg carries a finished drafting back to the model.
type todoDraftMsg struct {
	runID  int
	result todo.ExtractResult
}

// todoDraftEditorDoneMsg is the editor's exit from a draft. The file it was
// given is temporary — the item does not exist yet, and esc must still leave
// nothing behind — so it is removed here the way the input draft's is.
type todoDraftEditorDoneMsg struct {
	path string
	err  error
}

// The header rows are the profile's own fields, in the order it declares
// them, and then the dependency row. Each field row steps a closed scale in
// place; the last is a list rather than a scale, so the key that changes a
// field opens the backlog on it instead of stepping one.
func (d *todoDraft) dependsRow() int { return len(d.profile.Fields) }

// todoDraftUngraded is what the grade row says, and what it means: an item
// nobody has graded, which is not the same as one at the bottom of the
// scale. It is on the scale the row steps through because a grade set by
// mistake needs a way back off it.
const todoDraftUngraded = "ungraded"

// todoDraftNone is the dependency row on an item that waits on nothing.
const todoDraftNone = "nothing"

// todoDraft is one proposed item on the card: the proposal as it stands, the
// header rows drawn from it, and the dependency picker while that is open.
//
// The proposal is the truth and the rows are a drawing of it. Every key that
// changes a row is answered by reading all of them back and redrawing, so a
// header changed on a row and a header changed in the editor arrive by the
// same door.
type todoDraft struct {
	proposal todo.Proposal
	// profile is the vocabulary the rows are drawn from and the item will
	// be written in: which fields there are, what each may say, and which
	// of them is the grade.
	profile todo.Profile
	// body is the item's prose as it will be written. It is kept beside the
	// proposal because the editor rewrites prose and the proposal's sections
	// cannot hold what comes back: rebuilding the body from them would throw
	// away whatever the editor did to it.
	body   string
	fields *components.Select
	// picker is the dependency list while it is open. It takes the card's
	// rows and the card's keys until it is answered, which is why it is a
	// field here rather than a surface of its own: it is a question about
	// this card, and a second overlay would put the card it is about behind
	// it.
	picker *components.MultiSelect
	// deps is the active backlog as the picker offers it, slug and title.
	deps []components.SelectOption
	// known is every name a dependency may resolve to: the backlog's slugs,
	// and — for a draft opened from the proposals card — the titles of the
	// other proposals on it, which become slugs if they are accepted too.
	known []string
	// from is the row of the proposals card this draft was opened from, or
	// -1 for a draft that started from a sentence. It decides what enter
	// buys: a file, or the header the row it came from now carries.
	from int
}

// newTodoDraft builds the card around one proposal.
func newTodoDraft(profile todo.Profile, p todo.Proposal, deps []components.SelectOption, known []string, from int) *todoDraft {
	// The proposal's fields are copied rather than shared: the card writes
	// every keystroke back into its own copy, and a draft that esc dropped
	// would otherwise have already changed the row it was opened from.
	p.Fields = maps.Clone(p.Fields)
	d := &todoDraft{
		proposal: p,
		profile:  profile,
		body:     p.Item(profile, "", "", "").Body,
		fields:   &components.Select{Unnumbered: true},
		deps:     deps,
		known:    known,
		from:     from,
	}
	d.sync()
	return d
}

// sync redraws the header rows from the proposal, one row per field the
// profile declares. The grading field carries an extra stop for an item
// nobody has graded, because a grade set by mistake needs a way back off
// the scale; every other field falls back to its own default.
func (d *todoDraft) sync() {
	waits := todoDraftNone
	if len(d.proposal.DependsOn) > 0 {
		waits = strings.Join(d.proposal.DependsOn, ", ")
	}
	d.fields.Options = nil
	for _, f := range d.profile.Fields {
		value, words := d.proposal.Fields[f.Name], f.Words()
		if f.Name == d.profile.Grade {
			words = append(words, todoDraftUngraded)
			if value == "" {
				value = todoDraftUngraded
			}
		}
		if value == "" {
			value = f.Default
		}
		// A field with no default still has to show a word: a row drawn
		// blank is one the reader cannot tell from a scale with nothing on
		// it, and stepping it would be the first thing that ever set it.
		if value == "" && len(words) > 0 {
			value = words[0]
		}
		d.fields.Options = append(d.fields.Options,
			components.SelectOption{Label: f.Name, Value: value, Values: words})
	}
	d.fields.Options = append(d.fields.Options,
		components.SelectOption{Label: "waits on", Value: waits})
	d.fields.Title = d.proposal.Title
	d.fields.Chips = []string{todo.Slugify(d.proposal.Title)}
	d.fields.HintKeys = d.hint()
	d.fields.Warning = ""
	if missing := d.missing(); len(missing) > 0 {
		d.fields.Warning = "nothing in the backlog is named " + strings.Join(missing, " or ") +
			" — dropped rather than written as a dependency on nothing"
	}
}

// read takes the rows back into the proposal.
func (d *todoDraft) read() {
	if d.proposal.Fields == nil {
		d.proposal.Fields = map[string]string{}
	}
	for i, f := range d.profile.Fields {
		value := d.fields.Options[i].Value
		if f.Name == d.profile.Grade && value == todoDraftUngraded {
			value = ""
		}
		d.proposal.Fields[f.Name] = value
	}
}

// hint is the card's key row, as segments so a narrow terminal stacks it
// rather than clipping one. A draft from a sentence writes a file and can go
// out to the editor; one opened from the proposals card is setting a header
// on a row that is still a proposal, so enter keeps the header and there is
// no file for an editor to be given.
func (d *todoDraft) hint() []string {
	segs := []string{
		keys.Shown(keys.Select.Move) + " move",
		keys.Shown(keys.Select.Toggle) + " change",
	}
	if d.from >= 0 {
		return append(segs,
			keys.Shown(keys.Select.Take)+" keep it",
			keys.Shown(keys.Select.Cancel)+" leave it")
	}
	return append(segs,
		keys.Shown(keys.Backlog.Edit)+" the editor",
		keys.Shown(keys.Select.Take)+" write it",
		keys.Shown(keys.Select.Cancel)+" drop it")
}

// missing names the dependencies the draft gave that nothing answers. They
// are a warning and not a refusal: what a model got wrong about the backlog
// is the reader's to fix, and the item is still worth writing without them.
func (d *todoDraft) missing() []string {
	var out []string
	for _, dep := range d.proposal.DependsOn {
		if !slices.Contains(d.known, dep) {
			out = append(out, dep)
		}
	}
	return out
}

// openPicker puts the backlog up as the dependency list with the ones the
// draft already names checked. Checking nothing is an answer here — it is how
// an item's dependencies are cleared — rather than the slip it is on a card
// that is choosing what to do with a list.
func (d *todoDraft) openPicker(maxLines int) {
	if len(d.deps) == 0 {
		return
	}
	// The title says what the list is and nothing about the keys: this card
	// keeps the multi-select's own key row, which already names them, and a
	// title that repeated them would be the one thing on the card a narrow
	// terminal has to clip.
	card := components.NewMultiSelect("what has to land first", d.deps)
	for i, opt := range d.deps {
		card.Checked[i] = slices.Contains(d.proposal.DependsOn, opt.Label)
	}
	card.AllowNone = true
	card.MaxLines = maxLines
	d.picker = card
}

// setDepends takes the picker's answer. What the picker offers is what the
// backlog holds, so a name the draft invented and the reader kept is not in
// it and goes here — which is the other half of the warning the card made
// about it.
func (d *todoDraft) setDepends(indices []int) {
	var out []string
	for _, i := range indices {
		if i >= 0 && i < len(d.deps) {
			out = append(out, d.deps[i].Label)
		}
	}
	d.proposal.DependsOn = out
}

// todoDraftEnabled reports whether the session can draft an item.
func (m *Model) todoDraftEnabled() bool {
	return m.todosEnabled() && m.todos.Drafter.Enabled()
}

// startTodoDraft takes the drafting. Like the reading, it is a background
// command: nothing on screen waits for it, and an answer arriving after the
// session moved on is dropped by its run number.
func (m Model) startTodoDraft(sentence string) (tea.Model, tea.Cmd) {
	sentence = strings.TrimSpace(sentence)
	if sentence == "" {
		return m.systemNotice(todoNewUsage)
	}
	if !m.todoDraftEnabled() {
		return m.systemNotice("No model is configured to draft an item. /todo add <text> adds one by hand.")
	}
	if m.todoDrafting {
		return m.systemNotice("Still drafting — the card opens when it is done.")
	}
	m.todoDrafting = true
	m.todoDraftRun++
	runID, drafter := m.todoDraftRun, m.todos.Drafter
	req := todo.DraftRequest{Sentence: sentence, Existing: m.todoDraftExisting()}
	ctx, cancel := context.WithCancel(context.Background())
	m.todoDraftStop = cancel
	model, _ := m.systemNotice("Drafting the item…")
	return model, func() tea.Msg {
		defer cancel()
		return todoDraftMsg{runID: runID, result: drafter.Draft(ctx, req)}
	}
}

// todoDraftExisting is what already stands, as a reading and a drafting are
// both told it: the backlog "slug — title" per line, so a dependency the
// answer names is a slug that is there, and the session's notes after it.
func (m Model) todoDraftExisting() []string {
	var out []string
	if s := m.todoStore; s != nil {
		for _, it := range s.Items {
			out = append(out, it.Slug+" — "+it.Title)
		}
	}
	// The notebook is what this session already wrote down, and an item
	// proposing work a note has already recorded is the same repetition the
	// backlog is given to avoid. It is titles only, for the same reason the
	// backlog is: what a note says is the session's working state, and the
	// reading needs to know it exists rather than what is in it.
	// See docs/capabilities/chat.md#what-they-share.
	for _, n := range m.notebook.List() {
		out = append(out, "note "+n.Title)
	}
	return out
}

// finishTodoDraft applies a drafting: a failed one is a sentence in the
// transcript, a good one is the card.
func (m Model) finishTodoDraft(msg todoDraftMsg) (tea.Model, tea.Cmd) {
	if !m.todoDrafting || msg.runID != m.todoDraftRun {
		return m, nil
	}
	m.todoDrafting = false
	m.todoDraftStop = nil
	if msg.result.Failed {
		return m.systemNotice("The item could not be drafted — " + msg.result.Err +
			". /todo add <text> adds one by hand.")
	}
	m.openTodoDraft(msg.result.Proposals[0], -1)
	return m, nil
}

// dropTodoDraft retires a drafting in flight. A sentence is not a
// conversation, so this is not the reading's reason — it is that the request
// went out against this session and its answer would otherwise open a card in
// the next one, and be written with that session's name on it.
func (m *Model) dropTodoDraft() {
	if m.todoDraftStop != nil {
		m.todoDraftStop()
		m.todoDraftStop = nil
	}
	m.todoDrafting = false
	m.todoDraftRun++
}

// openTodoDraft puts one proposal on the card.
func (m *Model) openTodoDraft(p todo.Proposal, from int) {
	m.todoDraft = newTodoDraft(m.todos.Profile, p, m.todoDraftDeps(), m.todoDraftKnown(from), from)
	m.enterSurface(stateTodoDraft)
	m.syncViewport()
}

// todoDraftDeps is the active backlog as the picker offers it.
func (m Model) todoDraftDeps() []components.SelectOption {
	s := m.todoStore
	if s == nil {
		return nil
	}
	out := make([]components.SelectOption, 0, len(s.Items))
	for _, it := range s.Items {
		// A multi-select draws the label and the right-hand meta and nothing
		// else, so the title that says which item a slug is goes in the meta.
		out = append(out, components.SelectOption{Label: it.Slug, Meta: it.Title})
	}
	return out
}

// todoDraftKnown is every name a dependency may resolve to. On a draft opened
// from the proposals card the other proposals' titles count: they become
// slugs when they are accepted, which is the resolution the writing already
// makes, and warning about them here would warn about something that is
// about to exist.
func (m Model) todoDraftKnown(from int) []string {
	var out []string
	if s := m.todoStore; s != nil {
		for _, it := range s.Items {
			out = append(out, it.Slug)
		}
		for _, it := range s.Done {
			out = append(out, it.Slug)
		}
	}
	if from < 0 {
		return out
	}
	for i, p := range m.todoProposals {
		if i != from {
			out = append(out, p.Title)
		}
	}
	return out
}

// todoDraftLines renders the card for the bottom panel.
func (m Model) todoDraftLines() []string {
	d := m.todoDraft
	if d == nil {
		return nil
	}
	width := m.contentWidth()
	if d.picker != nil {
		d.picker.MaxLines = m.maxConfirmPanelHeight()
		return strings.Split(d.picker.View(width), "\n")
	}
	d.fields.MaxLines = m.maxConfirmPanelHeight()
	// The body is laid out here rather than kept, because the width it is
	// laid out at is the panel's and the panel's width moves with the
	// terminal. It goes through the renderer the transcript lays an answer
	// out with: an item's sections are prose somebody wrote, and a second
	// renderer for them would be a second design system.
	d.fields.Body = todoProse(d.body, components.Card{}.Inner(width))
	return strings.Split(d.fields.View(width), "\n")
}

// answerTodoDraft routes keys while the draft card shows.
func (m *Model) answerTodoDraft(msg tea.KeyPressMsg) (bool, overlayAction) {
	d := m.todoDraft
	if d == nil {
		return true, overlayAction{close: true}
	}
	if d.picker != nil {
		done, res := d.picker.Update(msg)
		if !done {
			return false, overlayAction{}
		}
		if !res.Canceled {
			d.setDepends(res.Indices)
		}
		d.picker = nil
		d.sync()
		return false, overlayAction{}
	}
	pressed := msg.String()
	switch {
	case keys.Is(pressed, keys.Select.Toggle) && d.fields.Focus == d.dependsRow():
		if len(d.deps) == 0 {
			// A key that cannot act says why rather than doing nothing: an
			// empty backlog is the one state where this row has no list to
			// open, and silence there reads as a broken key.
			return true, overlayAction{note: "Nothing in the backlog to wait on yet. The item is written without dependencies."}
		}
		d.openPicker(m.maxConfirmPanelHeight())
		return false, overlayAction{}
	case keys.Is(pressed, keys.Backlog.Edit) && d.from < 0:
		return true, m.editTodoDraft()
	}
	done, res := d.fields.Update(msg)
	d.read()
	d.sync()
	switch {
	case !done:
		return false, overlayAction{}
	case res.Canceled && d.from >= 0:
		return true, m.leaveTodoDraft()
	case res.Canceled:
		m.todoDraft = nil
		return true, overlayAction{close: true, note: "Nothing written; the draft is dropped."}
	}
	return true, m.takeTodoDraft()
}

// takeTodoDraft is enter: a file, or — for a draft opened from the proposals
// card — the header the row it came from now carries.
func (m *Model) takeTodoDraft() overlayAction {
	d := m.todoDraft
	if d.from >= 0 {
		if d.from < len(m.todoProposals) {
			m.todoProposals[d.from] = d.proposal
			if m.todoPropose != nil && d.from < len(m.todoPropose.Options) {
				m.todoPropose.Options[d.from].Meta = todoProposalMeta(d.profile, d.proposal)
			}
		}
		return m.leaveTodoDraft()
	}
	note, written := m.writeTodoDraft(d)
	m.todoDraft = nil
	// Only a file that landed is a backlog that grew: a write that failed
	// counted in the record would put a rate over a number that includes the
	// times nothing happened.
	if written {
		m.signal(observe.SignalTodo, observe.TodoNew)
	}
	return overlayAction{close: true, note: note}
}

// leaveTodoDraft hands the screen back to the card this draft was opened
// from. leaveSurface would hand it to the turn instead — it is the way out of
// the last surface — and the proposals card is still standing under this one.
func (m *Model) leaveTodoDraft() overlayAction {
	m.todoDraft = nil
	if m.todoPropose == nil {
		// Nothing to go back to. It cannot happen through the keyboard —
		// the card that opened this one is what is under it — and a guard
		// that fails closed costs nothing, where going to a nil card would
		// be a surface that draws and cannot be answered.
		return overlayAction{close: true}
	}
	m.state = stateTodoPropose
	m.syncViewport()
	return overlayAction{}
}

// writeTodoDraft writes the drafted item and says what it wrote. It writes
// its one file here rather than through the proposals path because the draft
// carries a body the editor may have rewritten, and a body rebuilt from the
// proposal's sections would throw that away.
//
// A dependency that names nothing is dropped and named, as it is on the
// proposals card: a dependency on nothing would hold the item back forever,
// and the card warned about it before enter was pressed.
func (m *Model) writeTodoDraft(d *todoDraft) (string, bool) {
	taken := map[string]bool{}
	if s := m.todoStore; s != nil {
		for _, it := range s.Items {
			taken[it.Slug] = true
		}
		for _, it := range s.Done {
			taken[it.Slug] = true
		}
	}
	slug := uniqueSlug(todo.Slugify(d.proposal.Title), taken)
	it := d.proposal.Item(d.profile, slug, time.Now().Format("2006-01-02"), m.sessionName)
	it.Body = d.body
	var deps, dropped []string
	for _, dep := range d.proposal.DependsOn {
		if m.todoStore != nil && has(m.todoStore, dep) {
			deps = append(deps, dep)
			continue
		}
		dropped = append(dropped, dep)
	}
	it.DependsOn = deps
	path, err := todo.Create(d.profile, m.todos.Root, it)
	if err != nil {
		return fmt.Sprintf("Could not write %s: %v", slug, err), false
	}
	m.reloadTodos()
	out := fmt.Sprintf("Wrote %s to %s.\n  %s  %s · %s · %s",
		slug, path, slug, it.Priority, gradeOrDash(it), it.Title)
	if len(dropped) > 0 {
		out += "\nDropped dependencies that name nothing in the backlog: " + strings.Join(dropped, ", ") + "."
	}
	return out + "\n/todo edit " + slug + " opens it to refine it.", true
}

// editTodoDraft hands the item as it stands to the editor. It is the handoff
// /todo edit makes — shhh writes the file, the editor is given the path, and
// whatever is in the file when it exits is the item — over a temporary file,
// because the item does not exist yet and esc must still leave nothing on
// disk.
//
// The card comes down for the handoff and goes back up holding what the
// editor wrote: the editor suspends the program, and a surface left drawn
// under a program nobody is rendering comes back as a ghost.
func (m *Model) editTodoDraft() overlayAction {
	if m.working() || m.frameWorking() {
		return overlayAction{note: "Not while the turn is running — the editor takes the terminal with it. The draft is still on the card."}
	}
	path, err := writeTodoDraftFile(m.todoDraft.profile, m.todoDraft.proposal, m.todoDraft.body)
	if err != nil {
		return overlayAction{note: "Could not write the draft out — " + err.Error() + ". The draft is still on the card."}
	}
	argv := editorArgv(editorCommand(), path, 1, 1)
	proc := exec.Command(argv[0], argv[1:]...)
	return overlayAction{close: true, run: tea.ExecProcess(proc, func(err error) tea.Msg {
		return todoDraftEditorDoneMsg{path: path, err: err}
	})}
}

// writeTodoDraftFile puts the draft where an editor can reach it, in the file
// format an item has: what comes back is read by the same parser that reads
// the backlog, so an editor that broke the header says so here rather than
// after the file has been written into the project.
func writeTodoDraftFile(profile todo.Profile, p todo.Proposal, body string) (string, error) {
	it := p.Item(profile, todo.Slugify(p.Title), time.Now().Format("2006-01-02"), "")
	it.Body = body
	f, err := os.CreateTemp("", "shhh-item-*.md")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(todo.Render(profile, it)); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// todoDraftEditorFinished takes the file back onto the card. Every exit the
// editor can make arrives here, which is what makes this the one place the
// temporary file is removed.
func (m Model) todoDraftEditorFinished(msg todoDraftEditorDoneMsg) (tea.Model, tea.Cmd) {
	defer func() { _ = os.Remove(msg.path) }()
	d := m.todoDraft
	if d == nil {
		return m, nil
	}
	back := func(note string) (tea.Model, tea.Cmd) {
		m.enterSurface(stateTodoDraft)
		m.syncViewport()
		if note == "" {
			return m, nil
		}
		return m.systemNotice(note)
	}
	if msg.err != nil {
		return back("The editor exited with an error, so the draft is as you left it — " + msg.err.Error())
	}
	content, err := os.ReadFile(msg.path)
	if err != nil {
		return back("Could not read the draft back, so it is as you left it — " + err.Error())
	}
	it, err := todo.Parse(d.profile, msg.path, string(content))
	if err != nil {
		return back("That does not read as an item — " + err.Error() + ". The draft is as you left it.")
	}
	if it.Title != "" {
		d.proposal.Title = it.Title
	}
	d.proposal.Fields = map[string]string{todo.PriorityField().Name: string(it.Priority)}
	for name, value := range it.Fields {
		d.proposal.Fields[name] = value
	}
	d.proposal.DependsOn = it.DependsOn
	d.body = it.Body
	d.sync()
	note := ""
	for _, w := range it.Warnings {
		note += "\nwarning: " + w
	}
	return back(strings.TrimPrefix(note, "\n"))
}
