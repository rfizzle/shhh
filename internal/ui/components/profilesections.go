package components

// The profile drafter's second widget: the section editor. It is the draft's
// sections, one block each, with the pointer on one of them, and the note a
// section — or the whole draft — is refined with. The wizard draws it on the
// draft step and on a redraft's wait, and hands it the keys while the
// sections, or a note under one, hold the keyboard.

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// profileSections is the section editor. It owns the draft's sections, the
// pointer, the note and whether it is open, and the sections' scroll. The
// wizard owns whether the card has the keyboard, and lends it for each draw
// in a profileFlow; it owns the text field too, and lends it for the note.
// See docs/architecture.md#the-profile-drafters-widgets.
type profileSections struct {
	// draft is the profile the card is about.
	draft ProfileDraftView
	// section is the block the pointer is on
	// (docs/interface/surfaces.md#the-profile-drafter).
	section int
	// refining is the note open under the selected section: the field has
	// the keyboard, and enter sends it.
	refining bool
	// whole says the note, or the wait after it, is about the whole draft
	// rather than the selected section, and is drawn under the section list;
	// include is its second press, and sent the note as it went to the
	// drafter, which the wait keeps on screen above itself.
	whole   bool
	include bool
	sent    string
	// pane is the offset into the sections' rows, held as a Pager like every
	// other scrolling body's here, so the fold under it counts through the
	// same arithmetic the rest of the package folds through
	// (docs/interface/principles.md#fold-never-hide). reveal asks the next
	// draw to bring the selected section inside it: a move puts it there,
	// and a scroll the reader asked for does not snap back.
	pane   Pager
	reveal bool
	field  *textarea.Model
}

// profileFlow is where the wizard stands, lent to the section editor for one
// draw: whether the card has the keyboard, whether a wait is running and
// whether it is a redraft of the editor's own sections, and the wait's label
// in motion.
type profileFlow struct {
	card, working, redrafting bool
	label                     func(after string) string
}

// land takes a finished draft. It keeps where the pointer is: a revision
// that sent the pointer back to Purpose would make the next revision of the
// same section cost the walk down to it again.
func (p *profileSections) land(draft ProfileDraftView) {
	p.draft = draft
	p.refining = false
	p.whole, p.include, p.sent = false, false, ""
	p.section = min(max(p.section, 0), max(len(draft.Sections)-1, 0))
	p.reveal = true
	p.field.Blur()
}

// wait closes the note for a wait: the drafter is writing, and nothing is
// typed into the sections while it does.
func (p *profileSections) wait() {
	p.refining = false
	p.field.Blur()
}

// scroll moves the sections by a row, held inside them against the rows the
// pane last drew them in.
func (p *profileSections) scroll(by int) {
	p.pane.Offset = Pager{Offset: p.pane.Offset + by, Height: p.pane.Height, total: p.pane.total}.Held()
}

// updateSections answers the sections while they have the keyboard. What
// enter, e and x do is the selected section's: a prose section is revised,
// and a field block hands enter to the host for the selector that picks it.
func (p *profileSections) updateSections(msg tea.KeyPressMsg, migratable bool) (bool, profileResult) {
	sec, ok := p.selected()
	if !ok {
		if keys.Is(msg.String(), keys.Profile.Back) {
			return true, profileResult{Action: profileDiscard}
		}
		return false, profileResult{}
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.Move):
		p.section = min(max(p.section+keys.Step(pressed, keys.Profile.Move), 0), len(p.draft.Sections)-1)
		p.reveal = true
	case keys.Is(pressed, keys.Profile.Refine):
		if !sec.revisable() {
			if sec.Pick == "" {
				return false, profileResult{}
			}
			return true, profileResult{Action: ProfilePick, Index: p.section}
		}
		p.refining = true
		p.field.Reset()
		p.field.Placeholder = "what to change in " + sec.Name
		p.field.Focus()
		p.reveal = true
	case keys.Is(pressed, keys.Profile.RefineAll):
		// One note for every section, whichever block the pointer is on: it
		// opens under the section list, since it is about all of them.
		p.refining, p.whole, p.include = true, true, false
		p.field.Reset()
		p.field.Placeholder = "what to change across the draft"
		p.field.Focus()
	case keys.Is(pressed, keys.Profile.Edit) && sec.revisable():
		return true, profileResult{Action: ProfileEdit, Index: p.section}
	case keys.Is(pressed, keys.Profile.Clear) && sec.revisable() && strings.TrimSpace(sec.Body) != "":
		return true, profileResult{Action: ProfileClear, Index: p.section}
	case keys.Is(pressed, keys.Profile.Migrate) && migratable:
		return true, profileResult{Action: ProfileMigrate, Index: p.section}
	case keys.Is(pressed, keys.Profile.Back):
		// esc takes back the selected section's last revision while it has
		// one, and is the step's own esc on a section with none: every
		// revision is kept for the life of the flow, so the way back through
		// them is the key that is always the safe answer.
		if sec.Revised || sec.Whole {
			return true, profileResult{Action: ProfileUndo, Index: p.section}
		}
		return true, profileResult{Action: profileDiscard}
	}
	return false, profileResult{}
}

// updateRefine answers the note open under a section: enter sends it, esc
// closes it leaving the section as it is, and everything else is text.
func (p *profileSections) updateRefine(msg tea.KeyPressMsg) (bool, profileResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.Back):
		p.refining, p.whole, p.include = false, false, false
		p.field.Blur()
		return false, profileResult{}
	case p.toggles(pressed):
		p.include = !p.include
		return false, profileResult{}
	case keys.Is(pressed, keys.Profile.Refine):
		// A note is what a refine is made of, so enter over an empty one does
		// nothing rather than asking the drafter to guess.
		note := strings.TrimSpace(p.field.Value())
		if note == "" {
			return false, profileResult{}
		}
		if p.whole {
			p.sent = note
			return true, profileResult{Action: ProfileRefineAll, Text: note, Include: p.include}
		}
		return true, profileResult{Action: ProfileRefine, Index: p.section, Text: note}
	}
	*p.field, _ = p.field.Update(msg)
	return false, profileResult{}
}

// toggles reports a keystroke that is the whole-draft note's second press
// rather than a letter of it: the note's own key, while nothing has been
// typed and some section is the person's to keep or include. Once the note
// has text in it the key is text, since a note is a sentence.
func (p *profileSections) toggles(pressed string) bool {
	return p.whole && keys.Is(pressed, keys.Profile.RefineAll) &&
		p.field.Value() == "" && len(p.mine()) > 0
}

// mine is the names of the sections the person wrote themselves, in order.
func (p *profileSections) mine() []string {
	var out []string
	for _, sec := range p.draft.Sections {
		if sec.Mine {
			out = append(out, sec.Name)
		}
	}
	return out
}

// sectionCount is the draft's sections, leaving out the row a whole-draft
// note left under them.
func (p *profileSections) sectionCount() int {
	n := 0
	for _, sec := range p.draft.Sections {
		if !sec.Whole {
			n++
		}
	}
	return n
}

// selected is the section the pointer is on.
func (p *profileSections) selected() (ProfileSection, bool) {
	if p.section < 0 || p.section >= len(p.draft.Sections) {
		return ProfileSection{}, false
	}
	return p.draft.Sections[p.section], true
}

// sectionHint is the draft step's own key row while the sections or a note
// under one have the keyboard. The card draws its own row, and while it has
// the keyboard this one is not drawn: two rows of live keys for one keyboard
// would be offering it twice.
func (p *profileSections) sectionHint(idle, migratable bool, wayOut string) []KeyOffer {
	if p.refining && p.whole {
		segments := []KeyOffer{keyOfferAs(keys.Profile.Refine, "redraft every section")}
		if len(p.mine()) > 0 && p.field.Value() == "" {
			again := "again to include the sections you edited"
			if p.include {
				again = "again to keep the sections you edited"
			}
			segments = append(segments, keyOfferAs(keys.Profile.RefineAll, again))
		}
		return append(segments, keyOfferAs(keys.Profile.Back, "leave it as it is"))
	}
	if p.refining {
		return []KeyOffer{
			keyOfferAs(keys.Profile.Refine, "refine it"),
			keyOfferAs(keys.Profile.Back, "leave it as it is"),
		}
	}
	if idle {
		return nil
	}
	sec, ok := p.selected()
	segments := []KeyOffer{keyOfferAs(keys.Profile.Move, "section")}
	if ok && !sec.revisable() && sec.Pick != "" {
		segments = append(segments, keyOfferAs(keys.Profile.Refine, sec.Pick))
	}
	if ok && sec.revisable() {
		segments = append(segments,
			keyOfferAs(keys.Profile.Refine, "refine it with a note"),
			keyOfferAs(keys.Profile.Edit, "edit it yourself"))
		if strings.TrimSpace(sec.Body) != "" {
			segments = append(segments, keyOfferAs(keys.Profile.Clear, "clear it"))
		}
	}
	if migratable {
		segments = append(segments, keyOffer(keys.Profile.Migrate))
	}
	segments = append(segments,
		keyOfferAs(keys.Profile.RefineAll, "all"),
		keyOfferAs(keys.Profile.Note, "the card"))
	if ok && sec.Whole {
		return append(segments, keyOfferAs(keys.Profile.Back, "take it back from every section it changed"))
	}
	if ok && sec.Revised {
		return append(segments, keyOfferAs(keys.Profile.Back, "take back its last revision"))
	}
	return append(segments, keyOfferAs(keys.Profile.Back, wayOut))
}

// foldedSections is the sections windowed to room rows, with a counted marker
// for what is folded above and below. The markers are paid for out of the
// room, so the window never draws a row past it.
func (p *profileSections) foldedSections(width, room int, blocks []string, starts []int) []string {
	if room <= 0 {
		return nil
	}
	height := max(room-1, 1)
	p.pane.Height, p.pane.total = height, len(blocks)
	if p.reveal {
		first, last := p.selectedRows(starts, len(blocks))
		p.pane.reveal(last)
		p.pane.reveal(first)
		p.reveal = false
	}
	p.pane.Offset = p.pane.Held()
	if p.pane.Offset > 0 && room > 2 {
		// A second marker costs a row of its own, so the window is shortened
		// by one more and the selected section brought back into it.
		height = room - 2
		p.pane.Height = height
		first, last := p.selectedRows(starts, len(blocks))
		if first < p.pane.Offset || last >= p.pane.Offset+height {
			p.pane.reveal(last)
			p.pane.reveal(first)
		}
		p.pane.Offset = p.pane.Held()
	}
	var rows []string
	if above := sectionsBefore(starts, p.pane.Offset); p.pane.Offset > 0 && room > 2 {
		rows = append(rows, Clip(indent(sty.dim.Render(fmt.Sprintf("⋮ %s above · %s",
			plural(above, "more section"), words(keys.Profile.ScrollUp, "scroll up")))), width))
	}
	window := p.pane.Window(blocks)
	end := p.pane.Offset + len(window)
	// A section the window would cut off after its heading is folded whole
	// rather than shown as a heading with nothing under it — unless it is the
	// selected one, which the window is there to show.
	for i, start := range starts {
		last := len(blocks) - 1
		if i+1 < len(starts) {
			last = starts[i+1] - 2
		}
		if start > p.pane.Offset && start < end && last >= end && i != p.section {
			window = window[:start-1-p.pane.Offset]
			end = start
			break
		}
	}
	rows = append(rows, window...)
	if below := sectionsFrom(starts, end); below > 0 {
		rows = append(rows, Clip(indent(sty.dim.Render(fmt.Sprintf("⋮ %s · %s",
			plural(below, "more section"), words(keys.Profile.ScrollDown, "scroll the profile")))), width))
	}
	if len(rows) > room {
		rows = rows[:room]
	}
	return rows
}

// selectedRows is the first and last row of the selected section.
func (p *profileSections) selectedRows(starts []int, total int) (int, int) {
	if p.section < 0 || p.section >= len(starts) {
		return 0, 0
	}
	last := total - 1
	if p.section+1 < len(starts) {
		// The blank row between two sections belongs to neither.
		last = starts[p.section+1] - 2
	}
	return starts[p.section], max(last, starts[p.section])
}

// sectionsBefore counts the sections whose heading is above row, which is
// what a marker over a scrolled pane states.
func sectionsBefore(starts []int, row int) int {
	n := 0
	for _, s := range starts {
		if s < row {
			n++
		}
	}
	return n
}

// sectionsFrom counts the sections whose heading is at or below row, which
// is what the marker under a folded pane states.
func sectionsFrom(starts []int, row int) int {
	n := 0
	for _, s := range starts {
		if s >= row {
			n++
		}
	}
	return n
}

// sectionRows is every section as a block — the heading with its mark, then
// its prose or its field line — with a blank row between blocks, and the row
// each block starts at. The selected section takes the pointer and the focus
// tone on its heading while the sections have the keyboard; a note being
// written, or a redraft being waited on, is drawn under it.
func (p *profileSections) sectionRows(width int, f profileFlow) ([]string, []int) {
	var rows []string
	starts := make([]int, 0, len(p.draft.Sections))
	// A whole-draft note is about every section, so no one of them is lit
	// while it is open or being waited on.
	lit := (!f.card || f.working) && !p.whole
	for i, sec := range p.draft.Sections {
		if i > 0 {
			rows = append(rows, "")
		}
		starts = append(starts, len(rows))
		selected := i == p.section && lit
		rows = append(rows, p.headingRow(sec, selected, width))
		rows = append(rows, p.sectionBody(sec, width)...)
		if i != p.section || p.whole {
			continue
		}
		switch {
		case p.refining:
			rows = append(rows, "")
			rows = append(rows, p.refineRows(sec, width)...)
		case f.redrafting:
			rows = append(rows, "", Clip(bodyIndent(f.label("the other sections stand")), width))
		}
	}
	return rows, starts
}

// headingRow is a section's heading with its mark after it.
func (p *profileSections) headingRow(sec ProfileSection, selected bool, width int) string {
	heading := sty.body.Render(sec.Name)
	lead := PointerColumn()
	if selected {
		heading = sty.info.Render(sec.Name)
		lead = sty.focusPointer.Render("❯") + " "
	}
	if sec.Mark != "" {
		heading += " " + sec.MarkTone.style().Render(sec.Mark)
	}
	return Clip(lead+heading, width)
}

// sectionBody is a section's prose wrapped under its heading, or a field
// block's value and what it means.
func (p *profileSections) sectionBody(sec ProfileSection, width int) []string {
	if !sec.Prose {
		if sec.Value == "" {
			return nil
		}
		row := bodyIndent(sec.Tone.style().Render(sec.Value))
		if sec.Detail == "" {
			return []string{Clip(row, width)}
		}
		if full := row + sty.dim.Render(" · "+sec.Detail); lipgloss.Width(full) <= width {
			return []string{full}
		}
		// What the value means goes under it rather than off the end of the
		// row: on the tools block it is the sentence that says the agent can
		// change things.
		rows := []string{Clip(row, width)}
		for _, line := range wrapPlain(sec.Detail, width-profileBodyIndent-2) {
			rows = append(rows, Clip(bodyIndent(sty.dim.Render(line)), width))
		}
		return rows
	}
	body := strings.TrimSpace(sec.Body)
	if body == "" {
		// An empty section is its heading and the mark that says so; a blank
		// row under it would read as text nobody can see.
		return nil
	}
	var rows []string
	for _, line := range wrapBlock(body, width-profileBodyIndent-2) {
		rows = append(rows, Clip(bodyIndent(sty.status.Render(line)), width))
	}
	return rows
}

// refineRows is the note under the section being refined: what it is for,
// and the field.
func (p *profileSections) refineRows(sec ProfileSection, width int) []string {
	others := p.sectionCount() - 1
	label := fmt.Sprintf("┄ what to change in %s — the other %s are sent as fixed context", sec.Name, spellNumber(others))
	inner := max(width-profileBodyIndent-2, 8)
	p.field.SetWidth(inner)
	StyleTextArea(p.field)
	// Wrapped rather than clipped: the half a clip would cut is the half that
	// says what else goes with the note.
	var rows []string
	for _, line := range wrapPlain(label, inner) {
		rows = append(rows, Clip(bodyIndent(sty.dim.Render(line)), width))
	}
	for _, line := range strings.Split(p.field.View(), "\n") {
		rows = append(rows, Clip(bodyIndent(line), width))
	}
	return rows
}

// wholeRows is a note on the whole draft under the section list: what it is
// sent with — which sections it keeps, when some are the person's — and the
// field, or once it has gone, the note as sent and the wait under it.
func (p *profileSections) wholeRows(width int, f profileFlow) []string {
	if !p.whole || (!p.refining && !f.redrafting) {
		return nil
	}
	label := "┄ a note on the whole draft — every section is redrafted on it; the tiers, tools and fields are sent as fixed context"
	if mine := p.mine(); len(mine) > 0 {
		verb := "is"
		if len(mine) > 1 {
			verb = "are"
		}
		if p.include {
			label += "; " + joinAnd(mine) + ", which you edited, " + verb + " redrafted too"
		} else {
			label += "; " + joinAnd(mine) + ", which you edited, " + verb + " kept"
		}
	}
	inner := max(width-profileIndent-2, 8)
	// Wrapped rather than clipped: the half a clip would cut is the half that
	// says which of the person's own sections the note may touch.
	rows := []string{""}
	for _, line := range wrapPlain(label, inner) {
		rows = append(rows, Clip(indent(sty.dim.Render(line)), width))
	}
	if p.refining {
		p.field.SetWidth(inner)
		StyleTextArea(p.field)
		for _, line := range strings.Split(p.field.View(), "\n") {
			rows = append(rows, Clip(indent("  "+line), width))
		}
		return rows
	}
	for _, line := range wrapPlain(p.sent, inner) {
		rows = append(rows, Clip(indent("  "+sty.dimmer.Render(line)), width))
	}
	return append(rows, "", Clip(indent(f.label("")), width))
}

// joinAnd is names as a sentence lists them.
func joinAnd(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
