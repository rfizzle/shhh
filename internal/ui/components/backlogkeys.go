package components

// The backlog screen's keyboard: the ladder Update walks, the split between
// keys that change nothing and keys that change a file, and the modes — the
// confirm, a picker, the filter row and the body — that take the keys off it
// while they are up. The picker, the filter row and the body answer their own
// keys (backlogpicker.go, backlogfilter.go, backlogitem.go); the ladder is
// what hands them over.
// It is its own file because a surface's whole register is the thing the
// register's rules are checked against, and a key answered from inside a
// renderer is a key no such reading finds.

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Update is the screen's whole keyboard. The confirm answers first while it
// is up — it holds the keyboard, and `y` is not a letter to it
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard,
// invariant 5).
func (b *BacklogScreen) Update(msg tea.KeyPressMsg) (done bool, result backlogResult) {
	b.Notice = ""
	b.sync()
	if b.confirm != nil {
		return b.updateConfirm(msg)
	}
	pressed := msg.String()
	// The plan card answers every keystroke while it is up, including the
	// way out: a card that is asking for one answer and a screen underneath
	// it that closes on esc would lose the proposal to the reflex.
	if b.planning() {
		var res backlogResult
		res, b.Notice = b.Plan.update(pressed)
		return false, res
	}
	// A picker holds the keyboard the same way, one level in: its esc is
	// the step back to the list.
	if b.picker != nil {
		return false, b.updatePicker(msg)
	}
	// With the query line open the query line is the surface, so every
	// letter is a letter — the reading every list in the product makes.
	// esc clears it, and on a filter that is already empty it closes the
	// row; enter closes it keeping the words, which is how the row keys are
	// got back over the list the words narrowed.
	if b.filter.filtering {
		// The list under the row is still a list, so its arrows still move:
		// only the halves of the pair no sentence types.
		if !b.movedTyping(pressed) && b.filter.edit(msg, pressed) {
			b.refilter()
		}
		return false, backlogResult{}
	}
	if b.reader.reading {
		if b.reader.update(pressed) {
			b.keys = !b.keys
		}
		return false, backlogResult{}
	}
	if keys.Is(pressed, keys.Backlog.Back) {
		// esc clears a query before it leaves: the words narrowing the list
		// are one level in, and the step back from them is the whole list
		// (docs/interface/principles.md#esc-is-always-the-safe-answer).
		if b.filter.query != "" {
			b.filter.clear()
			b.refilter()
			return false, backlogResult{}
		}
		return true, backlogResult{canceled: true}
	}
	if b.readKey(pressed) {
		return false, backlogResult{}
	}
	return b.stateKey(pressed)
}

// readKey answers the keys that change nothing outside this screen — the
// pointer, the tab, the filter, the register — and reports whether it did.
// They are separated from the keys that change a file because that is the
// line a running turn is drawn along: everything here stays live while the
// model works, and nothing here is offered twice.
func (b *BacklogScreen) readKey(pressed string) bool {
	switch {
	case b.moved(pressed):
	case keys.Is(pressed, keys.Backlog.Read):
		if b.current() != nil {
			b.reader.open()
		}
	case keys.Is(pressed, keys.Backlog.Tab):
		b.swapTab()
	case keys.Is(pressed, keys.Backlog.Filter):
		b.filter.open()
	case keys.Is(pressed, keys.Backlog.List):
		b.keys = !b.keys
	default:
		return false
	}
	return true
}

// stateKey answers the keys that change a file, or open a picker that will.
// Every one of them is inert while the session's own turn is running: the
// model may be reading these files this second, and a key that changed one
// under it would leave the turn working from a header that no longer
// exists. The keys go grey and the footer says why, rather than the surface
// accepting the press and refusing it afterwards — a refusal after the fact
// is a key that looked live (invariant 5).
func (b *BacklogScreen) stateKey(pressed string) (bool, backlogResult) {
	if b.ReadOnly {
		return false, backlogResult{}
	}
	// Starting an item is about the backlog rather than about a row, so it
	// answers with the pointer on nothing — which is the list it is most
	// needed on. An empty backlog offering a key that did nothing would be
	// the one screen where the offer is the only thing on it.
	if keys.Is(pressed, keys.Backlog.New) {
		return false, backlogResult{Do: &BacklogCommand{Act: BacklogNew}}
	}
	row := b.current()
	if row == nil {
		return false, backlogResult{}
	}
	switch {
	case keys.Is(pressed, keys.Backlog.Edit):
		return false, act(BacklogEdit, row.Slug)
	case row.State == BacklogUnreadable:
		// Nothing below this line can be done to a file that will not parse:
		// the verbs are line edits on a header this one does not have. The
		// row is still here, and the way to act on it is the editor.
	case keys.Is(pressed, keys.Backlog.Status):
		b.openStatus(*row)
	case keys.Is(pressed, keys.Backlog.Run) && !b.archived():
		b.openRun(*row)
	case keys.Is(pressed, keys.Backlog.Drop) && !b.archived():
		// The one key here that loses information says so in the question,
		// because the answer to "archive or drop" depends entirely on which
		// of the two this is.
		b.ask(BacklogDrop, row.Slug, "Drop "+row.Slug+"? The file is deleted, not archived.")
	case keys.Is(pressed, keys.Backlog.Sprint) && b.Sprint != "" && !b.archived():
		if row.InSprint {
			return false, act(BacklogSprintDrop, row.Slug)
		}
		return false, act(BacklogSprintAdd, row.Slug)
	}
	return false, backlogResult{}
}

// ask arms the inline confirm in front of a key that changes a file. The
// prompt names the item rather than saying "this item": the row moves under
// the reader as the filters narrow, and a question that does not say what it
// is about is one that gets answered yes by reflex.
func (b *BacklogScreen) ask(a backlogAct, slug, prompt string) {
	b.confirm = &Confirm{Prompt: sty.body.Render(prompt)}
	b.pending = &BacklogCommand{Act: a, Slug: slug}
}

// updateConfirm resolves the armed question. Declining leaves the screen
// exactly as it was, which is what esc promises everywhere else
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func (b *BacklogScreen) updateConfirm(msg tea.KeyPressMsg) (bool, backlogResult) {
	answered, yes := confirmed(&b.confirm, msg)
	if !answered {
		return false, backlogResult{}
	}
	// The armed command goes down with the question either way: it was armed
	// for this question, and a decline that left it behind would hand it to
	// whatever is asked next.
	cmd := b.pending
	b.pending = nil
	if yes && cmd != nil {
		return false, backlogResult{Do: cmd}
	}
	return false, backlogResult{}
}

// act is a key that asked for something with nothing to confirm.
func act(a backlogAct, slug string) backlogResult {
	return backlogResult{Do: &BacklogCommand{Act: a, Slug: slug}}
}
