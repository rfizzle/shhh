package components

// The backlog screen's foot: every key the surface offers, the words each is
// offered in, and the sentence over them while a turn holds them inert. It is
// its own file because the key row, `[?]` and the inert reading all draw from
// this one set — a key shown in one of the three and missing from another is
// the failure collecting them here avoids.

import (
	"strings"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

// footRows is the key row and, while a turn is running, the run of keys that
// is not live and the sentence saying why.
func (b *BacklogScreen) footRows(width int) []string {
	f := KeyFooter{
		Offers:   b.offers(),
		Register: b.keyList(),
		Showing:  b.keys,
	}
	if b.confirm != nil {
		f.Taken = b.confirm.View(width)
	}
	rows := f.Rows(width)
	if b.confirm != nil || b.keys || !b.ReadOnly {
		return rows
	}
	// The keys that change a file, in the treatment a surface that cannot
	// press them draws: grey, with their words, under the sentence saying
	// why. It is the approval card's not-yet-live row over a whole key set.
	//
	// The sentence is a row of its own rather than the annotation beside the
	// offers, which is what a footer's field is. A field gives ground to the
	// keys as the terminal narrows, and this one may not: a row of grey keys
	// with nothing left saying why they are grey is a surface that looks
	// broken (invariant 5).
	rows = append(rows, sty.Dim.Render(Clip(b.whyInert(), width)))
	return append(rows, packOffersIn(b.stateOffers(), width, false)...)
}

// whyInert is the sentence over the grey keys. The host's is used where
// there is one, because the session knows what it is doing and this does
// not.
func (b *BacklogScreen) whyInert() string {
	if b.Why != "" {
		return b.Why
	}
	return "a turn is running; these change files it may be working from"
}

// offers is the key row for whichever surface holds the keyboard. While the
// query line is open the row keys are letters, so they are not offered: a
// key that cannot act is not an offer (invariant 5).
func (b *BacklogScreen) offers() []KeyOffer {
	if b.planning() {
		return sprintOffers(b.Plan)
	}
	if b.filtering {
		return []KeyOffer{
			keyOffer(keys.Backlog.Move),
			keyOfferAs(keys.Backlog.ClearQ, "clear the filter, then close it"),
			// esc and not the letter: a row being typed into keeps every
			// letter as text, so the two keystrokes no sentence produces are
			// the whole of what closes it (invariant 5).
			wayOut("close it"),
		}
	}
	if b.reading {
		return []KeyOffer{
			keyOfferAs(keys.Backlog.Move, "scroll"),
			keyOffer(keys.Backlog.Page),
			keyOfferAs(keys.Backlog.Back, "back to the list"),
		}
	}
	out := []KeyOffer{keyOffer(keys.Backlog.Move)}
	if b.current() != nil {
		out = append(out, keyOffer(keys.Backlog.Read))
	}
	out = append(out, keyOffer(keys.Backlog.Filter), b.narrowOffer(), b.tabOffer())
	if !b.ReadOnly {
		out = append(out, b.stateOffers()...)
	}
	return append(out, wayOut(backToPrompt))
}

// stateOffers are the keys that change a file: the run the footer greys out
// while a turn is working, and offers live otherwise. They are one list so
// the two treatments cannot come to disagree about which keys they are.
func (b *BacklogScreen) stateOffers() []KeyOffer {
	row := b.current()
	if row == nil {
		// A list with nothing on it still has one act: starting the item
		// that would fill it.
		return []KeyOffer{keyOffer(keys.Backlog.New)}
	}
	var out []KeyOffer
	switch {
	case row.State == BacklogUnreadable:
		// None of the verbs is a line edit this file's header could take;
		// the way to act on it is the editor.
		out = []KeyOffer{keyOfferAs(keys.Backlog.Edit, "fix the header")}
	case b.archived():
		out = []KeyOffer{
			keyOfferAs(keys.Backlog.Reopen, "put it back in the backlog"),
			keyOffer(keys.Backlog.Edit),
		}
	default:
		out = []KeyOffer{keyOffer(keys.Backlog.Edit), keyOffer(keys.Backlog.Run), keyOffer(keys.Backlog.Groom)}
		if row.State == BacklogBlocked {
			out = append(out, keyOffer(keys.Backlog.Reopen))
		} else {
			out = append(out, keyOffer(keys.Backlog.Block))
		}
		out = append(out, keyOffer(keys.Backlog.Archive), keyOffer(keys.Backlog.Drop))
		if b.Sprint != "" {
			out = append(out, b.sprintOffer(*row))
		}
	}
	// Starting an item is about the backlog rather than about the row, so it
	// is offered wherever the pointer is standing — including on the archive
	// and on a file that will not load, which is where a reader who has just
	// found something missing is.
	return append(out, keyOffer(keys.Backlog.New))
}

// narrowOffer is the four cycle keys as one offer. They are one segment
// because four offers reading "cycle the … filter" would be most of the key
// row for four keys that do one thing, and they are an offer rather than the
// footer's annotation because an annotation gives ground as the terminal
// narrows: a screen whose filters are only findable behind `[?]` is a screen
// whose filters nobody finds.
//
// The archive drops two of them. Every item there has the same status and
// none of them is ready, so those two keys would narrow a list to nothing —
// and a key that cannot act is not an offer (invariant 5).
func (b *BacklogScreen) narrowOffer() KeyOffer {
	shown := []string{keys.Shown(keys.Backlog.Priority)}
	if !b.archived() {
		shown = []string{keys.Shown(keys.Backlog.Status), keys.Shown(keys.Backlog.Priority)}
	}
	// A project whose items carry nothing but a priority has no field
	// cycle, and a key that cannot narrow anything is not an offer
	// (invariant 5).
	if len(b.Fields) > 0 {
		shown = append(shown, keys.Shown(keys.Backlog.Kind))
	}
	if !b.archived() {
		shown = append(shown, keys.Shown(keys.Backlog.Ready))
	}
	return KeyOffer{Key: "[" + strings.Join(shown, "/") + "]", Label: "narrow it"}
}

// keyList is every key the screen has, for `[?]`. While the plan card holds
// the keyboard it is the card's keys and only those: a register listing keys
// the surface in front of the reader does not answer is worse than no
// register.
func (b *BacklogScreen) keyList() []KeyOffer {
	if b.planning() {
		return sprintOffers(b.Plan)
	}
	out := []KeyOffer{
		keyOfferAs(keys.Backlog.Move, "move between items"),
		keyOfferAs(keys.Backlog.Read, "read the body in the pane"),
		keyOfferAs(keys.Backlog.Page, "page the body while reading it"),
		keyOfferAs(keys.Backlog.Tab, "the backlog, or what shipped"),
		keyOfferAs(keys.Backlog.Filter, "filter by slug or title"),
		keyOfferAs(keys.Backlog.ClearQ, "clear the filter; clear it again to close it"),
		keyOfferAs(keys.Query.Rub, "take a rune back out of the filter"),
		keyOfferAs(keys.Backlog.Status, "cycle the status filter"),
		keyOfferAs(keys.Backlog.Priority, "cycle the priority filter"),
	}
	if len(b.Fields) > 0 {
		out = append(out, keyOfferAs(keys.Backlog.Kind, "cycle the next field filter"))
	}
	out = append(out, []KeyOffer{
		keyOfferAs(keys.Backlog.Ready, "only what can be started now"),
		keyOfferAs(keys.Backlog.Depends, "jump to what this one waits on"),
		keyOfferAs(keys.Backlog.Edit, "open the file in your editor"),
		keyOfferAs(keys.Backlog.Run, "work it through to a commit"),
		keyOfferAs(keys.Backlog.Block, "mark it blocked, after confirming it"),
		keyOfferAs(keys.Backlog.Reopen, "reopen it, from the archive as well"),
		keyOfferAs(keys.Backlog.Archive, "archive it, after confirming it"),
		keyOfferAs(keys.Backlog.Drop, "delete the file, after confirming it"),
		keyOfferAs(keys.Backlog.New, "start a new item"),
	}...)
	if b.Sprint != "" {
		out = append(out, keyOfferAs(keys.Backlog.Sprint, "add it to "+b.Sprint+", or drop it"))
	}
	return append(out, wayOut(backToPrompt), keyOfferAs(keys.Backlog.Back, backToPrompt))
}
