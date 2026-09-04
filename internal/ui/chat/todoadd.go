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
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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
// words, and the backlog as it stands. No tool output, no file contents.
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
	if s := m.todoStore; s != nil {
		for _, it := range s.Items {
			req.Existing = append(req.Existing, it.Slug+" — "+it.Title)
		}
	}
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
			Meta:  todoProposalMeta(p),
		}
	}
	card := components.NewMultiSelect(fmt.Sprintf("%s — %s toggles, %s all or none, %s writes the checked ones, %s writes nothing",
		what, keys.Shown(keys.Select.Toggle), keys.Shown(keys.Select.All), keys.Shown(keys.Select.Take), keys.Shown(keys.Select.Cancel)), opts)
	for i := range card.Checked {
		card.Checked[i] = true
	}
	card.MaxLines = m.maxConfirmPanelHeight()
	m.todoPropose = card
	// The sprint plan shares this card. Whichever opens it owns both
	// fields, because the answer is read from whichever is set — a reading
	// that landed over a sprint plan would otherwise write the sprint's
	// slugs against this card's rows.
	m.todoSprintPlan = nil
	m.enterSurface(stateTodoPropose)
	m.syncViewport()
	return m, nil
}

// todoProposalMeta is the row's right-hand field: kind, priority, size and
// what the item waits on, so the reader can judge it without opening it.
func todoProposalMeta(p todo.Proposal) string {
	meta := fmt.Sprintf("%s · %s · %s", p.Kind, p.Priority, p.Size)
	if len(p.DependsOn) > 0 {
		meta += " · after " + strings.Join(p.DependsOn, ", ")
	}
	return meta
}

// updateTodoPropose routes keys while the proposals card shows.
func (m Model) updateTodoPropose(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	done, result := m.todoPropose.Update(msg)
	if !done {
		return m, nil
	}
	res := result.(components.MultiSelectResult)
	proposals, sprint := m.todoProposals, m.todoSprintPlan
	// The row a blocked run left is claimed here whatever the answer was: a
	// card the reader declined wrote nothing to name on it, and leaving the
	// claim standing would put the next card's first item on a run that
	// never asked for it.
	followUp := m.todoFollowUpRow
	m.todoPropose = nil
	m.todoProposals = nil
	m.todoSprintPlan = nil
	m.todoFollowUpRow = 0
	m.leaveSurface()
	m.syncViewport()
	if res.Canceled {
		if sprint != nil {
			return m.systemNotice("Nothing written; no sprint was planned.")
		}
		return m.systemNotice("Nothing written; the proposals are dropped.")
	}
	if sprint != nil {
		return m.systemNotice(m.writeSprintPlan(sprint, res.Indices))
	}
	note, written := m.writeProposals(proposals, res.Indices)
	m.nameFollowUpOnRun(followUp, written)
	return m.systemNotice(note)
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
		it := a.Item(a.slug, created, m.sessionName)
		if _, err := todo.Create(m.todos.Root, it); err != nil {
			fmt.Fprintf(&b, "\ncould not write %s: %v", a.slug, err)
			continue
		}
		slugs = append(slugs, a.slug)
		written++
		fmt.Fprintf(&b, "\n  %s  %s · %s · %s", a.slug, it.Priority, sizeOrDash(it.Size), it.Title)
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

func sizeOrDash(s todo.Size) string {
	if s == "" {
		return "-"
	}
	return string(s)
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
