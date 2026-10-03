package components

// The backlog screen's foot: every key the surface offers, the words each is
// offered in, and the sentence over them while a turn holds them inert. It is
// its own file because the key row, `[?]` and the inert reading all draw from
// this one set — a key shown in one of the three and missing from another is
// the failure collecting them here avoids.

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

// footRows is the key row and, while a turn is running, the run of keys that
// is not live and the sentence saying why.
func (b *BacklogScreen) footRows(width int) []string {
	f := keyFooter{
		offers:   b.offers(width),
		register: b.keyList(),
		showing:  b.keys,
		legend:   b.lettersLegend(),
	}
	if b.confirm != nil {
		f.taken = b.confirm.View(width)
	}
	rows := f.rows(width)
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
	rows = append(rows, sty.dim.Render(Clip(b.whyInert(), width)))
	return append(rows, packOffersIn(b.stateOffers(), width, false)...)
}

// whyInert is the sentence over the grey keys. The host's is used where
// there is one, because the session knows what it is doing and this does
// not.
func (b *BacklogScreen) whyInert() string {
	if b.Why != "" {
		return b.Why
	}
	return "the turn is running; these change the files it may be working from"
}

// offers is the key row for whichever surface holds the keyboard. While the
// query line is open the row keys are letters, so they are not offered: a
// key that cannot act is not an offer (invariant 5).
func (b *BacklogScreen) offers(width int) []KeyOffer {
	if b.planning() {
		return sprintOffers(b.Plan)
	}
	if b.filtering {
		return filterOffers(width)
	}
	if b.reading {
		return []KeyOffer{
			keyOfferAs(keys.Backlog.Move, "scroll"),
			keyOffer(keys.Backlog.Page),
			// esc and not `q`: the header already spells `[q] back`, and
			// both answer this step back to the list.
			wayOut("back to the list"),
		}
	}
	return b.listOffers(width)
}

// filterOffers is the query row's key row, and it is one row at every width
// the way the list's is. Where the full words do not fit, esc gives up its
// words first — it is the way out, and its tone says so — and then the clear
// key its clause, down to the two things it does in turn. No offer is shed:
// these three are the whole of what a row being typed into answers.
func filterOffers(width int) []KeyOffer {
	move := keyOffer(keys.Backlog.Move)
	clearQ := keyOfferAs(keys.Backlog.ClearQ, "clear the filter, then close it")
	rungs := [][]KeyOffer{
		// esc and not the letter: a row being typed into keeps every letter
		// as text, so the two keystrokes no sentence produces are the whole
		// of what closes it (invariant 5).
		{move, clearQ, wayOut("close it")},
		{move, clearQ, wayOut("")},
		{move, keyOfferAs(keys.Backlog.ClearQ, "clear, then close"), wayOut("")},
	}
	for _, rung := range rungs {
		if lipgloss.Width(keyOffers(rung)) <= width {
			return rung
		}
	}
	return rungs[len(rungs)-1]
}

// listOffers is the list's key row, and it is one row at every width: the
// keys a reader presses every time the screen is open — move, read, filter,
// edit, a new item and the way out. Every other key is behind the header's
// `[?] keys`, which the header keeps at every width, so a foot that also
// listed the filters, the tabs and every verb was offering the register twice
// (docs/interface/departures.md#the-backlog-screens-layout-was-decided-in-the-binary).
//
// Where even those will not fit, whole segments give ground, the way the
// history browser's row does (invariant 4): the way out first, because the
// header states it at every width; then the two verbs, which `[?]` carries in
// full. On the archive the row's verb is putting an item back, and its words
// shorten before it is shed, since it is the one thing that tab is for. The
// pointer's keys are never shed — this list moves on the arrows alone, which
// no other list in the product teaches.
func (b *BacklogScreen) listOffers(width int) []KeyOffer {
	out := []KeyOffer{keyOffer(keys.Backlog.Move)}
	if b.current() != nil {
		out = append(out, keyOfferAs(keys.Backlog.Read, "read"))
	}
	out = append(out, keyOfferAs(keys.Backlog.Filter, "filter"))
	if !b.ReadOnly {
		// While a turn works these two are in the grey run under the
		// sentence instead: a key that cannot act is not an offer.
		out = append(out, b.fileOffers()...)
	}
	out = append(out, wayOut("back"))

	way := keys.Bracket(keys.Select.Cancel)
	edit, fresh := keys.Bracket(keys.Backlog.Edit), keys.Bracket(keys.Backlog.New)
	reopen := keys.Bracket(keys.Backlog.Reopen)
	rungs := [][]KeyOffer{
		out,
		without(out, way),
		without(out, way, fresh),
		reworded(without(out, way, fresh), reopen, "put it back"),
		without(out, way, fresh, edit, reopen),
	}
	for _, rung := range rungs {
		if lipgloss.Width(keyOffers(rung)) <= width {
			return rung
		}
	}
	return rungs[len(rungs)-1]
}

// fileOffers are the two verbs the list's row carries: the row's own — the
// editor, or on the archive putting the item back, which is what that tab is
// for — and starting a new item, which is about the backlog rather than the
// row, so an empty list offers it too.
func (b *BacklogScreen) fileOffers() []KeyOffer {
	fresh := keyOfferAs(keys.Backlog.New, "new")
	row := b.current()
	if row == nil {
		return []KeyOffer{fresh}
	}
	verb := keyOfferAs(keys.Backlog.Edit, "edit")
	switch {
	case row.State == BacklogUnreadable:
		// None of the verbs is a line edit this file's header could take.
		verb = keyOfferAs(keys.Backlog.Edit, "fix the header")
	case b.archived():
		verb = keyOfferAs(keys.Backlog.Reopen, "put it back in the backlog")
	}
	return []KeyOffer{verb, fresh}
}

// reworded is a rung with one offer's words replaced, for a verb that
// shortens before it is shed. A rung without that offer comes back as it
// was.
func reworded(offers []KeyOffer, key, label string) []KeyOffer {
	out := append([]KeyOffer(nil), offers...)
	for i := range out {
		if out[i].Key == key {
			out[i].Label = label
		}
	}
	return out
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
		keyOfferAs(keys.Backlog.Tab, "the backlog, the sprint, or what shipped"),
		keyOfferAs(keys.Backlog.Filter, "filter by slug or title"),
		keyOfferAs(keys.Backlog.ClearQ, "clear the filter; clear it again to close it"),
		keyOfferAs(keys.Query.Rub, "delete a character from the filter"),
		keyOfferAs(keys.Backlog.Status, "cycle the status filter"),
		keyOfferAs(keys.Backlog.Priority, "cycle the priority filter"),
	}
	if len(b.Fields) > 0 {
		out = append(out, keyOfferAs(keys.Backlog.Kind, "cycle the "+b.fieldNames()+" filter"))
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

// fieldNames is the fields the field-filter key cycles through, named: `kind
// or size`. The names are the profile's, so a second profile's key says what
// it narrows with nothing written here.
func (b *BacklogScreen) fieldNames() string {
	names := make([]string, len(b.Fields))
	for i, f := range b.Fields {
		names[i] = f.Name
	}
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// lettersLegend is what the letters on every row stand for, built from the
// profile's own words and glyphs so a second profile's letters explain
// themselves: `letters  priority H high · M medium · L low — size S M L — -
// unset`, one clause a field. A field whose letter is its word says the
// letters alone. Nil where no field draws a letter, because then the rows
// draw none (docs/interface/surfaces.md#the-backlog-screen).
func (b *BacklogScreen) lettersLegend() []string {
	var parts []string
	for _, f := range append([]BacklogField{b.Priority}, b.Fields...) {
		if f.lettered() {
			parts = append(parts, f.legend())
		}
	}
	if len(parts) == 0 {
		return nil
	}
	parts[0] = "letters  " + parts[0]
	return append(parts, "- unset")
}

// legend is one field's half of the letters legend: its name, then each
// letter with the word it stands for.
func (f BacklogField) legend() string {
	var glyphs []string
	plain := true
	for _, v := range f.Values {
		switch v.Glyph {
		case "":
		case v.Word:
			glyphs = append(glyphs, v.Glyph)
		default:
			plain = false
			glyphs = append(glyphs, v.Glyph+" "+v.Word)
		}
	}
	sep := " · "
	if plain {
		sep = " "
	}
	return f.Name + " " + strings.Join(glyphs, sep)
}
