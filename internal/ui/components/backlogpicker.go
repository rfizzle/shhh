package components

// The backlog screen's two pickers: where an item stands, and what to spend
// a turn on it for. Each is one key that opens a short list rather than a
// letter per act, which is what lets the screen's register be a handful: the
// acts behind `s` are three and the acts behind `r` are two, and a reader
// holding six letters for them was holding five more than they press.
// They are the selector family's list drawn in the pane beside the backlog,
// so the row they are about stays on screen under the pointer.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// backlogPicker is one picker over the row under the pointer: the line
// saying what it sets, the list, and the act each option asks for. A nil
// act is the option the item already stands at, which closes the picker
// and changes nothing.
type backlogPicker struct {
	what string
	card Select
	acts []*BacklogCommand
}

// openStatus is `[s]`: open, blocked with a reason, done. On the archive
// the one move there is is back into the backlog, so that is the one row.
func (b *BacklogScreen) openStatus(row BacklogRow) {
	p := &backlogPicker{what: "set the status of " + row.Slug}
	add := func(word, desc string, a backlogAct) {
		opt := SelectOption{Label: word, Desc: desc}
		cmd := &BacklogCommand{Act: a, Slug: row.Slug}
		if word == row.Status {
			opt.Meta, cmd = "now", nil
		}
		p.card.Options = append(p.card.Options, opt)
		p.acts = append(p.acts, cmd)
	}
	if b.archived() {
		add("open", "put it back in the backlog", BacklogReopen)
	} else {
		add("open", "back in the queue", BacklogReopen)
		add("blocked", "say why, in the draft", BacklogBlock)
		add("done", "archive it", BacklogArchive)
	}
	b.picker = p
}

// openRun is `[r]`: work the item through to a commit, or read it against
// the tree first. Both spend a turn, which is why they are one key that asks
// rather than two that act.
func (b *BacklogScreen) openRun(row BacklogRow) {
	b.picker = &backlogPicker{
		what: "run " + row.Slug,
		card: Select{Options: []SelectOption{
			{Label: "run it", Desc: "work it through to a commit"},
			{Label: "groom it", Desc: "read it against the tree"},
		}},
		acts: []*BacklogCommand{
			{Act: BacklogRun, Slug: row.Slug},
			{Act: BacklogGroom, Slug: row.Slug},
		},
	}
}

// updatePicker hands the keystroke to the list. esc closes the picker and
// not the screen: it is one level in, and esc is a step back.
func (b *BacklogScreen) updatePicker(msg tea.KeyPressMsg) backlogResult {
	done, res := b.picker.card.Update(msg)
	if !done {
		return backlogResult{}
	}
	p := b.picker
	b.picker = nil
	if res.Canceled || res.Index < 0 || res.Index >= len(p.acts) || p.acts[res.Index] == nil {
		return backlogResult{}
	}
	return backlogResult{Do: p.acts[res.Index]}
}

// rows is the picker as the pane draws it: what it sets, then the options,
// numbered, the way every list in the family is.
func (p *backlogPicker) rows(width int) []string {
	rows := []string{brightStyle().Render(Clip(p.what, width)), ""}
	body, _ := p.card.visibleRows(cardWidthFor(width), 0, true)
	return append(rows, body...)
}

// offers is the key row while a picker holds the keyboard: the family's.
func (p *backlogPicker) offers() []KeyOffer {
	return []KeyOffer{
		keyOffer(keys.Select.MoveJK),
		keyOffer(keys.Select.Take),
		{Key: keys.Bracketed(fmt.Sprintf("1–%d", len(p.card.Options))), Label: "jump"},
		keyOfferAs(keys.Select.Cancel, "back to the list"),
	}
}
