package components

// The backlog screen's keyboard: the ladder Update walks, the split between
// keys that change nothing and keys that change a file, and the three modes —
// the confirm, the filter row and the body — that take the keys off it while
// they are up. It is its own file because a surface's whole register is the
// thing the register's rules are checked against, and a key answered from
// inside a renderer is a key no such reading finds.

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Update is the screen's whole keyboard. The confirm answers first while it
// is up — it holds the keyboard, and `y` is not a letter to it
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard,
// invariant 5).
func (b *BacklogScreen) Update(msg tea.KeyPressMsg) (done bool, result BacklogResult) {
	b.Notice = ""
	b.sync()
	if b.confirm != nil {
		return b.updateConfirm(msg)
	}
	pressed := msg.String()
	// The plan card answers every keystroke while it is up, including the
	// way out: a card that is asking for one answer and a screen underneath
	// it that closes on `q` would lose the proposal to a letter.
	if b.planning() {
		return b.updatePlan(pressed)
	}
	// With the query line open the query line is the surface, so every
	// selector letter is a letter — the reading every list in the product
	// makes. ctrl+u clears it, and clearing a filter that is already empty
	// closes it, which is how the row keys are got back without leaving.
	if b.filtering {
		return b.editQuery(msg, pressed)
	}
	if b.reading {
		return b.updateReading(pressed)
	}
	if keys.Is(pressed, keys.Backlog.Back) {
		return true, BacklogResult{Canceled: true}
	}
	if b.readKey(pressed) {
		return false, BacklogResult{}
	}
	return b.stateKey(pressed)
}

// readKey answers the keys that change nothing outside this screen — the
// pointer, the tab, the filters, the register — and reports whether it did.
// They are separated from the keys that change a file because that is the
// line a running turn is drawn along: everything here stays live while the
// model works, and nothing here is offered twice.
func (b *BacklogScreen) readKey(pressed string) bool {
	switch {
	case b.moved(pressed):
	case keys.Is(pressed, keys.Backlog.Read):
		if b.current() != nil {
			b.reading, b.pager.Offset = true, 0
		}
	case keys.Is(pressed, keys.Backlog.Tab):
		b.swapTab()
	case keys.Is(pressed, keys.Backlog.Filter):
		b.filtering = true
	case keys.Is(pressed, keys.Backlog.Status) && !b.archived():
		b.status = (b.status + 1) % len(backlogStatuses)
		b.refilter()
	case keys.Is(pressed, keys.Backlog.Priority):
		b.priority = (b.priority + 1) % len(b.priorityStops())
		b.refilter()
	case keys.Is(pressed, keys.Backlog.Kind):
		b.field = (b.field + 1) % len(fieldStops(b.Fields))
		b.refilter()
	case keys.Is(pressed, keys.Backlog.Ready) && !b.archived():
		b.ready = !b.ready
		b.refilter()
	case keys.Is(pressed, keys.Backlog.Depends):
		b.jumpToDependency()
	case keys.Is(pressed, keys.Backlog.List):
		b.keys = !b.keys
	default:
		return false
	}
	return true
}

// stateKey answers the keys that change a file. Every one of them is inert
// while the session's own turn is running: the model may be reading these
// files this second, and a key that changed one under it would leave the
// turn working from a header that no longer exists. The keys go grey and the
// footer says why, rather than the surface accepting the press and refusing
// it afterwards — a refusal after the fact is a key that looked live
// (invariant 5).
func (b *BacklogScreen) stateKey(pressed string) (bool, BacklogResult) {
	if b.ReadOnly {
		return false, BacklogResult{}
	}
	// Starting an item is about the backlog rather than about a row, so it
	// answers with the pointer on nothing — which is the list it is most
	// needed on. An empty backlog offering a key that did nothing would be
	// the one screen where the offer is the only thing on it.
	if keys.Is(pressed, keys.Backlog.New) {
		return false, BacklogResult{Do: &BacklogCommand{Act: BacklogNew}}
	}
	row := b.current()
	if row == nil {
		return false, BacklogResult{}
	}
	switch {
	case row.State == BacklogUnreadable:
		// Nothing below this line can be done to a file that will not parse:
		// the verbs are line edits on a header this one does not have. The
		// row is still here, and the way to act on it is the editor.
		if keys.Is(pressed, keys.Backlog.Edit) {
			return false, b.act(BacklogEdit, row.Slug)
		}
	case keys.Is(pressed, keys.Backlog.Edit):
		return false, b.act(BacklogEdit, row.Slug)
	case keys.Is(pressed, keys.Backlog.Run) && !b.archived():
		return false, b.act(BacklogRun, row.Slug)
	case keys.Is(pressed, keys.Backlog.Reopen):
		return false, b.act(BacklogReopen, row.Slug)
	case keys.Is(pressed, keys.Backlog.Block) && !b.archived():
		b.ask(BacklogBlock, row.Slug, "Block "+row.Slug+"?")
	case keys.Is(pressed, keys.Backlog.Archive) && !b.archived():
		b.ask(BacklogArchive, row.Slug, "Archive "+row.Slug+"?")
	case keys.Is(pressed, keys.Backlog.Drop) && !b.archived():
		// The one key here that loses information says so in the question,
		// because the answer to "archive or drop" depends entirely on which
		// of the two this is.
		b.ask(BacklogDrop, row.Slug, "Drop "+row.Slug+"? The file is deleted, not archived.")
	case keys.Is(pressed, keys.Backlog.Groom) && !b.archived():
		return false, b.act(BacklogGroom, row.Slug)
	case keys.Is(pressed, keys.Backlog.Sprint) && b.Sprint != "" && !b.archived():
		if row.InSprint {
			return false, b.act(BacklogSprintDrop, row.Slug)
		}
		return false, b.act(BacklogSprintAdd, row.Slug)
	}
	return false, BacklogResult{}
}

// act is a key that asked for something with nothing to confirm.
func (b *BacklogScreen) act(a BacklogAct, slug string) BacklogResult {
	return BacklogResult{Do: &BacklogCommand{Act: a, Slug: slug}}
}

// ask arms the inline confirm in front of a key that changes a file. The
// prompt names the item rather than saying "this item": the row moves under
// the reader as the filters narrow, and a question that does not say what it
// is about is one that gets answered yes by reflex.
func (b *BacklogScreen) ask(a BacklogAct, slug, prompt string) {
	b.confirm = &Confirm{Prompt: sty.Body.Render(prompt)}
	b.pending = &BacklogCommand{Act: a, Slug: slug}
}

// updateConfirm resolves the armed question. Declining leaves the screen
// exactly as it was, which is what esc promises everywhere else
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func (b *BacklogScreen) updateConfirm(msg tea.KeyPressMsg) (bool, BacklogResult) {
	answered, yes := confirmed(&b.confirm, msg)
	if !answered {
		return false, BacklogResult{}
	}
	// The armed command goes down with the question either way: it was armed
	// for this question, and a decline that left it behind would hand it to
	// whatever is asked next.
	cmd := b.pending
	b.pending = nil
	if yes && cmd != nil {
		return false, BacklogResult{Do: cmd}
	}
	return false, BacklogResult{}
}

// editQuery is the keyboard while the filter row is open. Every letter is a
// letter here, the arrows still move the pointer, and the two keys that are
// not letters close the row.
func (b *BacklogScreen) editQuery(msg tea.KeyPressMsg, pressed string) (bool, BacklogResult) {
	switch {
	case b.movedTyping(pressed):
		// The list under the row is still a list, which is why the movement
		// binding is the arrows and not j/k: a query being typed into has
		// no letters to spare, and this screen would have had to break the
		// pair here as well as on the list.
		return false, BacklogResult{}
	case keys.Is(pressed, keys.Backlog.ClearQ):
		// An empty filter has nothing left to clear, so the same key closes
		// the row and hands the letters back — the rule every selector in
		// the product answers to.
		if b.query == "" {
			b.filtering = false
			return false, BacklogResult{}
		}
		b.query = ""
	case keys.Is(pressed, keys.Backlog.Back) && pressed != keys.Shown(keys.Backlog.Back):
		// The way out answers to three keystrokes and one of them is `q`.
		// Here `q` is a letter, so only the two that no sentence produces
		// close the row.
		b.filtering, b.query = false, ""
	case keys.Is(pressed, keys.Query.Rub):
		if r := []rune(b.query); len(r) > 0 {
			b.query = string(r[:len(r)-1])
		}
	default:
		b.query += typedRunes(msg)
	}
	b.refilter()
	return false, BacklogResult{}
}

// updateReading is the keyboard while the body has it: the pager, and the
// way back to the list. Back goes to the list rather than out of the screen,
// because the reader is one level in and esc is a step back rather than an
// exit.
func (b *BacklogScreen) updateReading(pressed string) (bool, BacklogResult) {
	switch {
	case keys.Is(pressed, keys.Backlog.Move):
		b.pager.Offset += keys.Step(pressed, keys.Backlog.Move)
	case keys.Is(pressed, keys.Backlog.Page):
		b.pager.Offset += keys.Step(pressed, keys.Backlog.Page) * max(b.pager.Height, 1)
	case keys.Is(pressed, keys.Backlog.Read), keys.Is(pressed, keys.Backlog.Back):
		b.reading = false
	case keys.Is(pressed, keys.Backlog.List):
		b.keys = !b.keys
	}
	return false, BacklogResult{}
}

// jumpToDependency puts the pointer on the first item the row is waiting on,
// which is what makes the edge something you can follow rather than a slug
// to remember. A dependency the backlog does not hold is the case the row's
// own warning is about, and this says so rather than moving nowhere.
func (b *BacklogScreen) jumpToDependency() {
	row := b.current()
	if row == nil || len(row.Waits) == 0 {
		return
	}
	want := row.Waits[0]
	for i, r := range b.rows() {
		if r.Slug != want {
			continue
		}
		// The pointer may be moving to a row the filters are hiding, so they
		// come off: a jump that landed on nothing would be the filter
		// swallowing the answer to the key that was just pressed.
		if !b.showing(i) {
			b.clearFilters()
		}
		b.focus[b.tab] = i
		b.reading, b.pager.Offset = false, 0
		b.sync()
		return
	}
	b.Notice = "the backlog has no item named " + want
}

// clearFilters puts every filter back to showing everything.
func (b *BacklogScreen) clearFilters() {
	b.query, b.filtering = "", false
	b.status, b.priority, b.field, b.ready = 0, 0, 0, false
}
