package chat

// Grooming in the session: one read-only turn per item, and the verdicts it
// answers with shown as a diff of single lines the person accepts one at a
// time (docs/capabilities/todo.md#an-item-is-checked-before-it-is-worked).
//
// The rule is the proposals card's and the memory prompt's before it: a
// session may propose, the person decides. Rewriting a `path:line` is
// mechanical, but restating what the code does today is a claim, and the
// claim is what is put to the reader — a groomer that rewrote the item on
// its own would be the runner writing the backlog rather than working it.
//
// The reading is the session's own turn rather than a background command,
// because reading the tree needs the tools a turn has. It runs in plan mode:
// the whole pass changes nothing until a card is accepted.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// todoGroomUsage is the one place the command's shape is written, so the
// refusal and the help cannot describe different commands.
const todoGroomUsage = "Usage: /todo groom [<slug>|--all]"

// todoGroomState is a grooming pass while it is going: the items still to
// read, the one being read, and the reading its card is showing.
type todoGroomState struct {
	// queue is the items still to read, in backlog order. A pass over one
	// item is a queue of one, so there is one flow rather than two.
	queue []string
	// slug is the item being read now, empty when no pass is going.
	slug string
	// item is that item as it stood when the turn was sent.
	item todo.Item
	// turn is the session turn the reading was sent as and mark where in
	// the transcript it began, so an answer is read off the turn that was
	// asked for rather than off whatever ended last.
	turn, mark int
	// prevMode is the session's mode before the pass, restored at the end
	// the way a run restores it.
	prevMode string
	// reading is the answer as parsed, while its card is up.
	reading todo.Reading
	// lines and items are what the pass has accepted so far, for the
	// sentence it leaves behind when it stops.
	lines, items int
	// stale is how far behind each item's accepted reading has fallen,
	// taken when the backlog is reloaded rather than per frame: it asks the
	// repository a question per groomed item, and the rail is drawn on
	// every keystroke.
	stale map[string]int
}

// going reports a grooming pass in flight.
func (g todoGroomState) going() bool { return g.slug != "" }

// startTodoGroom is /todo groom. A pass is a queue of items and a card each,
// and the boundary rules a run keeps are kept here for the same reason: the
// reading is a turn, and a turn started under another turn is not this one's.
func (m Model) startTodoGroom(args []string) (tea.Model, tea.Cmd) {
	if !m.todosEnabled() {
		return m.systemNotice("The backlog is unavailable in this session.")
	}
	if m.todoGroomer.going() {
		return m.systemNotice(fmt.Sprintf("Already reading %s; the card opens when the turn is over.", m.todoGroomer.slug))
	}
	if st := m.todoRunner.state; st != nil && !st.Over() {
		return m.systemNotice(fmt.Sprintf("A run is going (%s · %s); /todo stop ends it, and grooming reads files that run is working from.", st.Slug, st.Stage))
	}
	if m.turnState() != stateInput || m.working() {
		return m.systemNotice("A reading starts from an idle session; this turn has to finish first.")
	}
	s := m.todoStore
	if s == nil || s.Len() == 0 {
		return m.systemNotice("No backlog to read.")
	}
	queue, note := todoGroomQueue(s, args)
	if note != "" {
		return m.systemNotice(note)
	}
	m.todoGroomer = todoGroomState{queue: queue, prevMode: m.policy.mode.String(), stale: m.todoGroomer.stale}
	return m.groomNext()
}

// todoGroomQueue is which items the pass reads: the one named, or every
// active item in backlog order. An archived item is left out — a reading
// corrects work still to do, and the archive is a record.
func todoGroomQueue(s *todo.Store, args []string) ([]string, string) {
	switch {
	case len(args) == 0:
		return nil, todoGroomUsage
	case len(args) == 1 && args[0] == "--all":
		var out []string
		for _, it := range s.Items {
			out = append(out, it.Slug)
		}
		if len(out) == 0 {
			return nil, "Nothing to read: the backlog has no active items."
		}
		return out, ""
	case len(args) > 1 || strings.HasPrefix(args[0], "-"):
		return nil, todoGroomUsage
	}
	it, ok := s.Find(args[0])
	if !ok || it.Archived {
		return nil, fmt.Sprintf("No active backlog item %q; /todo lists them.", args[0])
	}
	return []string{it.Slug}, ""
}

// groomNext sends the reading for the next item in the queue, or ends the
// pass when there is none left.
func (m Model) groomNext() (tea.Model, tea.Cmd) {
	g := &m.todoGroomer
	for len(g.queue) > 0 {
		slug := g.queue[0]
		g.queue = g.queue[1:]
		// The store is re-read between items, because a card accepted a
		// moment ago changed a file and the next reading states the item as
		// it now stands.
		it, ok := m.todoStore.Find(slug)
		if !ok || it.Archived {
			continue
		}
		g.slug, g.item = slug, it
		g.turn = int(m.turnCount) + 1
		g.mark = len(m.transcript)
		m.applyMode(agent.ModePlan)
		return m.sendUserMessageAs(run.GroomPrompt(it), "groom "+slug)
	}
	return m.endTodoGroom("")
}

// endTodoGroom closes the pass, restores the mode and says what came of it.
func (m Model) endTodoGroom(why string) (tea.Model, tea.Cmd) {
	g := m.todoGroomer
	if prev, err := agent.ParseMode(g.prevMode); err == nil {
		m.applyMode(prev)
	}
	m.todoGroomer = todoGroomState{stale: g.stale}
	m.reloadTodos()
	var b strings.Builder
	if why != "" {
		b.WriteString(why)
	}
	// The tally is what a pass over several items owes the reader and what
	// a pass over one does not: each card already said what it wrote, and
	// one card's answer restated underneath it is a row that says nothing.
	if g.items > 1 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "Accepted %s across %s.", plural(g.lines, "line"), plural(g.items, "item"))
	}
	if b.Len() == 0 {
		return m, nil
	}
	return m.systemNotice(b.String())
}

// todoGroomAfter is the turn-end hook, derived from the model before against
// the model after the way the runner's is: a reading's turn ending is a
// transition, and no one handler could be trusted to send it.
func (m Model) todoGroomAfter(prev Model) (Model, tea.Cmd) {
	g := m.todoGroomer
	if !g.going() || !prev.working() || m.working() {
		return m, nil
	}
	if m.turnState() != stateInput || m.pausedAtRoundLimit() || m.heldAtBoundary() {
		return m, nil
	}
	if int(m.turnCount) != g.turn {
		// The turn that ended is not the reading's — a compaction, a skill,
		// something a command started. Nothing about the item is wrong, so
		// the pass stops rather than grading an answer that is not its own.
		next, cmd := m.endTodoGroom("The reading's turn was displaced by another message.")
		return next.(Model), cmd
	}
	reading, err := todo.Groom(g.item, m.groomAnswer())
	if err != nil {
		next, cmd := m.endTodoGroom("Could not read " + g.slug + " — " + err.Error() + ".")
		return next.(Model), cmd
	}
	reading.Head = project.Head(m.todos.Root)
	next, cmd := m.openTodoGroomCard(reading)
	return next.(Model), cmd
}

// groomAnswer is the assistant's last message since the reading began.
func (m Model) groomAnswer() string {
	for i := len(m.transcript) - 1; i >= m.todoGroomer.mark && i >= 0; i-- {
		if e := m.transcript[i]; e.kind == entryAssistant {
			return e.text
		}
	}
	return ""
}

// openTodoGroomCard puts the reading up as a diff to accept. A row is one
// line of the file, and the last row is the header's own stamp — so
// accepting the reading is itself one accepted line, and declining the card
// leaves an item nothing has claimed to have read.
func (m Model) openTodoGroomCard(r todo.Reading) (tea.Model, tea.Cmd) {
	changes := r.Changes()
	if len(r.Findings) == 0 {
		// A turn that answered in no shape the reader can act on is worth
		// saying out loud: the alternative is a card of one row that reads
		// as an item nothing was found wrong with.
		m.todoGroomer.slug = ""
		model, _ := m.systemNotice("The reading of " + r.Slug + " answered in no shape that could be read as verdicts; nothing was changed.")
		return model.(Model).groomAfterCard()
	}
	m.todoGroomer.reading = r
	opts := make([]components.SelectOption, 0, len(changes)+1)
	for _, f := range changes {
		opts = append(opts, components.SelectOption{Label: todoGroomRow(f)})
	}
	opts = append(opts, components.SelectOption{Label: todoGroomStampRow(m.todoGroomer.item, r)})
	card := components.NewMultiSelect(todoGroomTitle(r, len(changes)), opts)
	for i := range card.Checked {
		card.Checked[i] = true
	}
	card.MaxLines = m.maxConfirmPanelHeight()
	m.todoGroom = card
	m.enterSurface(stateTodoGroom)
	m.syncViewport()
	return m, nil
}

// groomedLine is the header line a stamp is written as, so the card's last
// row is a diff of the same line the write produces.
func groomedLine(it todo.Item) string {
	if it.Groomed == "" {
		return ""
	}
	return "groomed: " + it.Groomed
}

// todoGroomTitle says what the reading found before the diff says what it
// would do about it: the verdicts that propose nothing are the ones that
// would otherwise be invisible, and "everything else holds" is the answer a
// reader most wants to be able to trust.
func todoGroomTitle(r todo.Reading, changes int) string {
	var parts []string
	for _, v := range []todo.Verdict{todo.VerdictHolds, todo.VerdictUnknown} {
		if n := r.Count(v); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, v))
		}
	}
	if n := r.Unplaced(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d matched no line", n))
	}
	head := fmt.Sprintf("%s read · %s proposed", r.Slug, plural(changes, "change"))
	if len(parts) > 0 {
		head += " · " + strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%s — %s toggles, %s all or none, %s writes the checked lines, %s writes nothing",
		head, keys.Shown(keys.Select.Toggle), keys.Shown(keys.Select.All),
		keys.Shown(keys.Select.Take), keys.Shown(keys.Select.Cancel))
}

// todoGroomRow is one proposed line as the card draws it: the verdict, the
// line as it would read, the line it replaces struck through beside it, and
// the evidence behind all three.
//
// The order is the order the fields give ground in as the card narrows, and
// it is the order they are decided in. The verdict leads because it is the
// smallest fact and the one being accepted; the evidence goes last because
// it is the reason, and a reason clipped at the end still reads as one.
// Putting the verdict in the row's right-hand field — where a staging list
// states its counts — was the version this replaced: a diff of a real line
// is long, and the field that gives ground first took the verdict with it.
func todoGroomRow(f todo.Finding) string {
	row := components.Toned(todoGroomTone(f.Verdict), groomField(string(f.Verdict))) + components.LineChange(f.Claim, f.Now)
	if f.Evidence != "" {
		row += "  " + components.DimText(f.Evidence)
	}
	return row
}

// todoGroomStampRow is the card's last row: the header line an accepted
// reading writes, drawn as the diff it is. It is a row like any other
// because it is a line like any other, which is what makes "the stamp is
// written only when the person accepts" a fact about the card.
func todoGroomStampRow(before todo.Item, r todo.Reading) string {
	return components.DimText(groomField("the reading")) +
		components.LineChange(groomedLine(before), groomedLine(todo.Item{Groomed: r.Stamp()}))
}

// groomField is the row's left column, wide enough for the longest word in
// the closed set so the diffs beside it line up.
func groomField(word string) string {
	return fmt.Sprintf("%-*s", groomFieldWidth+2, word)
}

// groomFieldWidth is the longest verdict, measured rather than written down:
// a word added to the set must not be able to break the column.
var groomFieldWidth = func() int {
	n := len("the reading")
	for _, v := range todo.Verdicts() {
		n = max(n, len(string(v)))
	}
	return n
}()

// todoGroomTone reads a verdict the way a card field is read. Only the two
// that say the item has fallen behind carry weight; a correction and a
// criterion the tree already met are statements of fact.
func todoGroomTone(v todo.Verdict) components.FieldTone {
	switch v {
	case todo.VerdictGone, todo.VerdictChanged:
		return components.ToneRisk
	}
	return components.ToneNeutral
}

// todoGroomLines renders the card for the bottom panel.
func (m Model) todoGroomLines() []string {
	if m.todoGroom == nil {
		return nil
	}
	return strings.Split(m.todoGroom.View(m.contentWidth()), "\n")
}

// updateTodoGroom routes keys while the card shows. Enter writes the lines
// that are checked; esc writes nothing and stops the pass, keeping what
// earlier cards already accepted.
//
// It hands the session back itself rather than describing its answer to the
// register, because what an answer here starts is the next item's reading —
// a turn, which is work the session owns and not something a mode should be
// reporting as a row and a close.
func (m Model) updateTodoGroom(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	done, res := m.todoGroom.Update(msg)
	if !done {
		return m, nil
	}
	r := m.todoGroomer.reading
	m.todoGroom = nil
	m.todoGroomer.slug = ""
	m.todoGroomer.reading = todo.Reading{}
	m.leaveSurface()
	m.syncViewport()
	if res.Canceled {
		return m.endTodoGroom("Nothing was written for " + r.Slug + ".")
	}
	// The write is its own statement: it changes this model — the tally the
	// pass keeps and the store it reloaded — and folding it into the call
	// below would leave which of the two the notice was built from up to
	// evaluation order.
	note := m.writeGrooming(r, res.Indices)
	noted, _ := m.systemNotice(note)
	return noted.(Model).groomAfterCard()
}

// groomAfterCard carries the pass on to the next item, or ends it. It is
// its own step because the card and the queue are separate facts: the reader
// answering one card is not the reader asking for the pass to end.
func (m Model) groomAfterCard() (tea.Model, tea.Cmd) {
	if len(m.todoGroomer.queue) > 0 {
		return m.groomNext()
	}
	return m.endTodoGroom("")
}

// writeGrooming writes the accepted lines and says what it wrote. The stamp
// is the card's last row, so an accepted reading is recorded exactly when
// the reader said so and never otherwise.
func (m *Model) writeGrooming(r todo.Reading, accepted []int) string {
	changes := r.Changes()
	var take []todo.Finding
	stamp := ""
	for _, i := range accepted {
		switch {
		case i >= 0 && i < len(changes):
			take = append(take, changes[i])
		case i == len(changes):
			stamp = r.Stamp()
		}
	}
	it := m.todoGroomer.item
	n, skipped, err := todo.Accept(it.Path, take, stamp)
	if err != nil {
		return "Could not write " + r.Slug + " — " + err.Error() + "."
	}
	m.todoGroomer.lines += n
	if n > 0 {
		m.todoGroomer.items++
	}
	// The reading is kept only where the person accepted it, because what a
	// run is handed later has to be the reading they agreed to rather than
	// the one they declined.
	if stamp != "" {
		if err := todo.SaveReading(m.todos.Root, r); err != nil {
			m.appendEntry(entry{kind: entrySystem, text: "The reading of " + r.Slug + " could not be written down — " + err.Error()})
		}
	}
	m.signal(observe.SignalTodo, observe.GroomReason(n))
	m.reloadTodos()
	var b strings.Builder
	fmt.Fprintf(&b, "Wrote %s to %s.", plural(n, "line"), it.Path)
	for _, f := range take {
		if wasSkipped(skipped, f) {
			continue
		}
		fmt.Fprintf(&b, "\n  %s  %s", f.Verdict, strings.TrimSpace(orLine(f.Now, f.Claim)))
	}
	// And what the file's own lines would not take, with the reason: a
	// correction the reader checked and cannot find in the file afterwards
	// is worse than one that was refused out loud.
	for _, u := range skipped {
		fmt.Fprintf(&b, "\n  not written  %s — %s", strings.TrimSpace(orLine(u.Now, u.Claim)), u.Why)
	}
	if r.Finished() {
		// Proposed, never carried out: an item the tree has already
		// finished is exactly the case where somebody has to agree that it
		// is the same work, and the evidence is what they read to decide.
		fmt.Fprintf(&b, "\nEvery acceptance criterion of %s reads already done. /todo done %s archives it, with this as its report:\n%s",
			r.Slug, r.Slug, r.Report())
	}
	return b.String()
}

// wasSkipped reports the accepted finding is one the file would not take, so
// the sentence naming what was written names only what was.
func wasSkipped(skipped []todo.Unwritten, f todo.Finding) bool {
	for _, u := range skipped {
		if u.Finding == f {
			return true
		}
	}
	return false
}

// orLine is the line a finding would leave behind: what it would write, or
// what it would delete when it writes nothing.
func orLine(now, claim string) string {
	if now == "" {
		return claim
	}
	return now
}

// dropTodoGroom retires a pass in flight. The session boundary calls it, so
// a card cannot come up for a conversation that is gone — and the mode goes
// back with it, the way a run let go of at the boundary puts it back: plan
// mode was the reading's, and the reading is over.
func (m *Model) dropTodoGroom() {
	if !m.todoGroomer.going() && m.todoGroom == nil {
		return
	}
	if prev, err := agent.ParseMode(m.todoGroomer.prevMode); err == nil {
		m.applyMode(prev)
	}
	if m.todoGroom != nil {
		m.todoGroom = nil
		m.leaveSurface()
	}
	m.todoGroomer = todoGroomState{stale: m.todoGroomer.stale}
}

// refreshGroomStale re-reads how far behind each item's accepted reading has
// fallen. It asks the repository one question per groomed item, so it is
// called where the backlog is loaded and nowhere near a frame.
func (m *Model) refreshGroomStale() {
	if m.todoStore == nil {
		m.todoGroomer.stale = nil
		return
	}
	m.todoGroomer.stale = todo.Stale(m.todos.Root, m.todoStore.Items, m.todos.GroomStale)
}

// groomStaleNote is what a surface says about an item whose reading has
// fallen behind, and empty for one whose has not — or one nobody has read
// that way, because absence is not staleness.
func (m Model) groomStaleNote(slug string) string {
	n, ok := m.todoGroomer.stale[slug]
	if !ok {
		return ""
	}
	return fmt.Sprintf("groomed %d commits ago", n)
}
