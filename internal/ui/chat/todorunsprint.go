package chat

// A sprint is the same runner over more than one item: `/todo run --all`
// works the ready list — the sprint file's set where the backlog holds one —
// starting each item in a session of its own, and stops when the list is
// empty, when the cap is reached, or on the first block.
//
// What makes it a sprint rather than a loop is that nothing about an item's
// end is inferred from what the model said. An item is finished when the
// machine reached done, which is after a real commit and an archive with a
// report; an item is blocked when the machine reached blocked. The sprint
// reads those two transitions and nothing else.
// See docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them.
//
// It is a file of its own because nothing in it is a stage: it sits above
// the run rather than inside one, and choosing a set and watching it is a
// surface again (todosprint.go).

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// startTodoSprint is `/todo run --all`. A sprint left behind by a process
// that died is continued rather than replaced: its checkpoint names the item
// it was on, and that item's own checkpoint names the stage.
func (m Model) startTodoSprint(opt todoRunArgs) (tea.Model, tea.Cmd) {
	if m.todoRunner.state != nil && !m.todoRunner.state.Over() {
		return m.systemNotice(fmt.Sprintf("A run is already going: %s. /todo status shows it; /todo stop ends it.", m.todoRunner.state.Summary()))
	}
	if m.todoStore == nil {
		return m.systemNotice("No backlog to run from.")
	}
	if m.turnState() != stateInput {
		return m.systemNotice("Answer the open decision first; a sprint starts from an idle session.")
	}
	noCommit := opt.noCommit || m.todos.NoCommit
	steps := run.Options{NoCommit: noCommit, Pipeline: m.todos.Pipeline, Notebook: m.notebook != nil}.Steps()
	if !steps.Runs() {
		return m.systemNotice(fmt.Sprintf("The %s profile has no run, so there is no set to work: its items are worked by hand.", m.todos.Profile.Name))
	}
	if ref, refused := steps.Refuse(m.todoRunCan(project.InRepo(m.todos.Root))); refused {
		return m.systemNotice(m.todoRunRefusal(ref, ""))
	}
	if sp, live := run.Live(m.todos.Root); live {
		// The invocation's answers stand over the checkpoint's, the same way
		// a continued run's do: asking for the sprint again is asking for it
		// under the answers given now, and the session it is picked up in is
		// this one.
		sp.Session, sp.PrevMode, sp.NoCommit = m.sessionName, m.policy.mode.String(), noCommit
		if opt.max > 0 {
			sp.Max = opt.max
		}
		model, _ := m.systemNotice("Continuing the sprint from its checkpoint — " + sp.Summary() + ".")
		next := model.(Model)
		if slug, ok := sp.Resume(); ok {
			if err := sp.Save(next.todos.Root); err != nil {
				return next.systemNotice("The sprint's checkpoint could not be written — " + err.Error())
			}
			return next.sprintRun(sp, slug)
		}
		return next.sprintNext(sp)
	}
	sp := run.StartSprint(m.sessionName, m.policy.mode.String(), opt.max, noCommit)
	m.signal(observe.SignalRun, "sprint")
	model, _ := m.systemNotice(todoSprintStartNote(sp, len(m.todoStore.Ready())))
	return model.(Model).sprintNext(sp)
}

// todoSprintStartNote says what the sprint is about to work and how it ends,
// because `--all` over a backlog is the one command here whose scope the
// person cannot see from the command they typed.
func todoSprintStartNote(sp *run.Sprint, ready int) string {
	scope := plural(ready, "item") + " ready"
	if sp.Max > 0 {
		scope += fmt.Sprintf(", at most %d of them", sp.Max)
	}
	note := "Sprint started — " + scope + ", one item per session. /todo stop ends it."
	if sp.NoCommit {
		note += " No run in it makes a commit."
	}
	return note
}

// sprintNext starts the sprint's next item, or ends the sprint when there is
// none.
func (m Model) sprintNext(sp *run.Sprint) (tea.Model, tea.Cmd) {
	it, ok := sp.Next(m.todoStore)
	if !ok {
		return m.endTodoSprint(sp)
	}
	if err := sp.Save(m.todos.Root); err != nil {
		sp.Stop()
		model, _ := m.systemNotice("The sprint's checkpoint could not be written — " + err.Error())
		return model.(Model).endTodoSprint(sp)
	}
	return m.sprintRun(sp, it.Slug)
}

// sprintRun starts one item under the sprint. A run the session refuses —
// an item another surface put in an impossible state between the two
// readings — ends the sprint rather than leaving a checkpoint nothing is
// driving and a command that refuses the same way every time it is retried.
func (m Model) sprintRun(sp *run.Sprint, slug string) (tea.Model, tea.Cmd) {
	next, cmd := m.beginTodoRun(slug, sp.NoCommit, true)
	started := next.(Model)
	if started.todoRunner.state == nil || started.todoRunner.state.Over() {
		sp.Blocks(slug, "the run could not be started; the notice above says why")
		ended, _ := started.endTodoSprint(sp)
		return ended, cmd
	}
	return started, cmd
}

// advanceSprint is the step between two items: the finished one is recorded,
// the session boundary is crossed, and the next item's run starts in the
// conversation on the other side.
//
// The boundary is the point of the loop. The previous item's conversation is
// cost and noise to the next one — the checkpoint already carries everything
// a stage needs — and a session per item is also what makes the record one
// row per item rather than one row for the night.
func (m Model) advanceSprint(done string) (tea.Model, tea.Cmd) {
	sp, live := run.Live(m.todos.Root)
	if !live {
		return m, nil
	}
	sp.Finished(done)
	// What the item cost is added to the set's total here, on the last line
	// before the boundary resets the ledger it is read from: the checkpoint
	// is the only thing that outlives the session it was spent in.
	sp.Spent(int(m.turnCount), m.sessionSpend().Cost)
	if err := sp.Save(m.todos.Root); err != nil {
		sp.Stop()
		model, _ := m.systemNotice("The sprint's checkpoint could not be written — " + err.Error())
		return model.(Model).endTodoSprint(sp)
	}
	// The same boundary /new crosses, through the same function: one
	// definition of what a session ending and another beginning resets
	// (model.go).
	note, save := m.startNewSession()
	// The new session's first row says which item comes next. Everything
	// else the boundary carried is gone by design, so a reader who comes
	// back to a fresh transcript would otherwise have to open the board to
	// find out what the sprint is about to work.
	note += sprintNextNote(sp, m.todoStore)
	model, _ := m.systemNotice(note)
	next, cmd := model.(Model).sprintNext(sp)
	return next, tea.Batch(save, cmd)
}

// sprintNextNote is the line the first row on the far side of a session
// boundary carries: which item the sprint takes next, or that there is none
// left. It reads the choice rather than making it, so the row and the run
// that follows it cannot name different items.
func sprintNextNote(sp *run.Sprint, store *todo.Store) string {
	if next, ok := sp.Peek(store); ok {
		return "\nNext in the sprint: " + next.Slug + " · " + next.Title
	}
	return "\nNothing is left that the sprint can start; it ends here."
}

// endTodoSprint retires the sprint: the mode the session was in before it
// goes back, the checkpoint is removed, and the row says which of the closed
// reasons stopped it.
//
// The checkpoint goes rather than being kept with its ending in it. A sprint
// that has stopped has nothing left to continue — the item it stopped on
// keeps its own checkpoint, and the note names the command that picks that
// one up — and a file left behind saying "ended" is a file the next `--all`
// has to decide about.
func (m Model) endTodoSprint(sp *run.Sprint) (tea.Model, tea.Cmd) {
	if sp.Ended == "" {
		sp.Stop()
	}
	run.DiscardSprint(m.todos.Root)
	if prev, err := agent.ParseMode(sp.PrevMode); err == nil {
		m.applyMode(prev)
	}
	m.signal(observe.SignalRun, "sprint-"+sp.Ended)
	return m.systemNotice(todoSprintEndNote(sp))
}

// todoSprintEndNote is what a finished sprint says: how much it got through,
// and which of the four endings it was. The word matters more than the
// sentence — a sprint that ran out of ready items and one that stopped on a
// block leave the same quiet screen, and only one of them is finished.
func todoSprintEndNote(sp *run.Sprint) string {
	note := fmt.Sprintf("Sprint over — %s · %s: %s", sp.Count(), sp.Ended, sp.Reason)
	if sp.Ended == run.SprintBlocked {
		note += "\nNothing further was attempted: a sprint stops on the first block, because what comes next may rest on the work that did not land."
	}
	return note
}

// sprintCap is the sprint's wall-clock cap on one item, read at the boundary
// between two stages rather than by a clock of its own. A stage is the
// smallest thing the runner can judge, so it is also the smallest thing the
// cap can end: cutting a turn in half would leave a tree nothing has read.
func (m Model) sprintCap(step run.Step) (run.Step, bool) {
	st := m.todoRunner.state
	if m.todos.ItemTimeout <= 0 || !st.Sprinting() || st.Over() {
		return step, false
	}
	switch step.Action {
	case run.ActionBlocked, run.ActionDone:
		return step, false
	}
	sp, live := run.Live(m.todos.Root)
	if !live || !sp.Expired(m.todos.ItemTimeout) {
		return step, false
	}
	return st.Block(run.TimedOut(m.todos.ItemTimeout)), true
}

// sprintCloseWords name the item a sprint's turn was spent on and how far the
// sprint has got, for the notification a finished turn raises. A reader who
// left a sprint running and came back to one line about a turn would have to
// go and look up which of thirty items it was.
func (m Model) sprintCloseWords() string {
	st := m.todoRunner.state
	if !st.Sprinting() {
		return ""
	}
	// An item that reached done is named as finished rather than as being
	// at a stage called done: the reader being called back is asking which
	// item ended, and "done" as a stage word reads as one more step.
	words := st.Slug + " · " + string(st.Stage)
	if st.Stage == run.StageDone {
		words = "finished " + st.Slug
	}
	if sp, live := run.Live(m.todos.Root); live {
		words += " · sprint " + sp.Count()
	}
	return words
}
