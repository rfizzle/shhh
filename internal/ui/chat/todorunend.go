package chat

// The two ends a run has: done, which archives the item with its report, and
// blocked, which leaves the evidence on the row and proposes the follow-up.
// It is its own file because both endings write the same account of the run
// into the notebook, and that account is what outlives the session.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoRunDoneNote is the row a finished run closes with: what happened to
// the work, and where the item went.
//
// A run that made no commit says so rather than saying nothing about it.
// "done" beside an uncommitted tree reads as a commit that was made, and
// the reader's next act is to go looking for one — so the row names the
// files instead, which is the only place the change now is.
func todoRunDoneNote(st *run.State, to string) string {
	files := plural(len(st.Files), "file")
	// A run whose product is the write-up says where the write-up is.
	// Counting the files it never set out to change would say nothing about
	// what it did, and "0 files in the working tree" reads as a failure.
	if ending, ok := st.Pipeline.Ending(); ok && ending == run.FinishNote {
		return fmt.Sprintf("✓ todo run %s done — the report is in the session notebook, and the item is archived to %s.", st.Slug, to)
	}
	if st.NoCommit {
		return fmt.Sprintf("✓ todo run %s done — not committed; %s in the working tree, and the item is archived to %s.", st.Slug, files, to)
	}
	return fmt.Sprintf("✓ todo run %s done — committed %s and archived the item to %s.", st.Slug, files, to)
}

// todoRunDone archives the item with its report and ends the run.
func (m Model) todoRunDone() (tea.Model, tea.Cmd) {
	st := m.todoRunner.state
	to, err := m.fileTodoRun(st)
	note := todoRunDoneNote(st, to) + m.closeFinishedSprint()
	if err != nil {
		// The work is finished and the run package has already put the item
		// back to open with the report on it; what is left is telling the
		// reader what to do about it.
		did := "committed " + plural(len(st.Files), "file")
		if st.NoCommit {
			did = fmt.Sprintf("made no commit and left %s in the working tree", plural(len(st.Files), "file"))
		}
		note = fmt.Sprintf("✓ todo run %s %s, but the item could not be archived — %v. The report is on the item and it is open; /todo done %s archives it once that is settled.", st.Slug, did, err, st.Slug)
	}
	sprinting, slug := st.Sprinting(), st.Slug
	m.endTodoRun()
	// The report is the row's final state and opens from it; a copy of it
	// under the notice would be the same paragraphs twice, once where they
	// can be folded and once where they cannot.
	model, _ := m.systemNotice(note)
	if sprinting {
		return model.(Model).advanceSprint(slug)
	}
	return model, nil
}

// fileTodoRun archives the finished item with its report, through the
// notebook where the run's ending is the write-up. A run that ends in a note
// has the write-up read in the session rather than only in the archive,
// which is the whole reason it spent a turn producing one.
func (m Model) fileTodoRun(st *run.State) (string, error) {
	if ending, ok := st.Pipeline.Ending(); ok && ending == run.FinishNote && m.notebook != nil {
		return run.FileNote(m.todos.Root, st, m.todoRunner.item, m.writeRunNote)
	}
	return run.File(m.todos.Root, st, m.todoRunner.item)
}

// writeRunNote puts a run's write-up in the session's notebook and answers
// with the number /notes lists it under.
func (m Model) writeRunNote(author, title, body string) (string, error) {
	n, _, err := m.notebook.Write(author, title, runNoteBody(body))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("n%d", n.ID), nil
}

// runNoteBody is a report as a note: whole where it fits, and cut to the
// notebook's bound with a line saying so where it does not.
//
// A note is a paragraph by design — every agent in the session is handed the
// whole notebook in one block, so a store that could hold a document would
// spend a context window on one run's leavings. A report in the shape the
// finish asks for is a document: a summary, the decisions, a row per file,
// the deviations and the follow-ups. Refusing it would leave the finish
// saying it could not write the note it exists to write, so it is cut here
// instead, and the line points at the archived item, which carries the whole
// of it either way. See docs/capabilities/chat.md#what-they-share.
func runNoteBody(report string) string {
	body := strings.TrimSpace(report)
	if len(body) <= notebook.MaxBodyLen {
		return body
	}
	const cut = "\n\n… cut to fit a note; the archived item carries the whole report."
	body = body[:notebook.MaxBodyLen-len(cut)]
	if i := strings.LastIndexByte(body, '\n'); i > 0 {
		body = body[:i]
	}
	return strings.ToValidUTF8(strings.TrimRight(body, "\n "), "") + cut
}

// todoRunBlocked ends the run with its evidence on the item. The work
// already done stays in the tree, uncommitted, and the note says so.
func (m Model) todoRunBlocked() (tea.Model, tea.Cmd) {
	st := m.todoRunner.state
	it := m.todoRunner.item
	_ = todo.SetStatus(it.Path, todo.StatusBlocked)
	_ = todo.Append(it.Path, fmt.Sprintf("## Blocked\n%s\n\n_run in session %s, stage %s, %s_", st.Blocked, st.Session, st.Stage, time.Now().Format("2006-01-02 15:04")))
	paths := []string{}
	if m.changes != nil {
		paths = m.todoRunPaths()
	}
	blockedRow := m.todoRunner.rowIdx
	m.endTodoRun()
	// The proposal card that follows writes the follow-up item; the row that
	// blocked is where it belongs, so the reader finds the block and what
	// was written about it in one place.
	m.todoRunner.followUpRow = blockedRow
	note := fmt.Sprintf("✗ todo run %s blocked — %s", it.Slug, st.Blocked)
	if len(paths) > 0 {
		note += "\nWork so far stays in the tree, uncommitted: " + strings.Join(paths, ", ")
	}
	note += fmt.Sprintf("\nThe evidence is on the item; /todo open %s reopens it when it is settled.", it.Slug)
	model, _ := m.systemNotice(note)
	// A sprint stops here and does not go on to the next ready item: the
	// blocked item is about to be offered a follow-up, and what comes after
	// it in the backlog may be resting on the work that did not land. The
	// end is said before the follow-up card opens, because the card takes
	// the screen and a sentence behind it is a sentence nobody read.
	if st.Sprinting() {
		if sp, live := run.Live(m.todos.Root); live {
			sp.Blocks(it.Slug, st.Blocked)
			ended, _ := model.(Model).endTodoSprint(sp)
			model = ended
		}
	}
	// What is left is offered as a follow-up item, after this one; accepting
	// it is what lets the blocked item be archived once the rest lands.
	return model.(Model).openTodoProposals([]todo.Proposal{todoFollowUp(it, st)}, "a follow-up for "+it.Slug)
}

// todoFollowUp is the item proposed when a run blocks: what is left of the
// blocked item, after it. It is a proposal, not a file — the person
// accepts it on the same card the session's own proposals use.
func todoFollowUp(it todo.Item, st *run.State) todo.Proposal {
	var criteria []string
	in := false
	for _, line := range strings.Split(it.Body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			in = strings.EqualFold(strings.TrimSpace(t[3:]), "acceptance criteria")
			continue
		}
		if in && strings.HasPrefix(t, "- [ ] ") {
			criteria = append(criteria, strings.TrimSpace(t[6:]))
		}
	}
	if len(criteria) == 0 {
		criteria = []string{"What " + it.Slug + " left undone is done"}
	}
	// The follow-up carries the blocked item's own header words, with the
	// grade the run was working at rather than the one the file still says:
	// research may have raised it, and what is left to do is the size of
	// the work as the run found it.
	fields := map[string]string{todo.PriorityField().Name: string(it.Priority)}
	for name, value := range it.Fields {
		fields[name] = value
	}
	if st.Profile.Grade != "" {
		fields[st.Profile.Grade] = st.Grade
	}
	return todo.Proposal{
		Title:     "Follow up " + it.Slug + ": " + it.Title,
		Fields:    fields,
		Story:     "Continue " + it.Slug + ", which blocked at " + string(st.Stage) + ".",
		Criteria:  criteria,
		Notes:     []string{"Blocked because: " + strings.ReplaceAll(st.Blocked, "\n", " ")},
		DependsOn: []string{it.Slug},
	}
}
