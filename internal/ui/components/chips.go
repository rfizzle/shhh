package components

// The staged attachment strip (
// docs/interface/surfaces.md#the-input-frame). What is waiting to ride on the
// next message, one chip per file: the mark for what kind of thing it is,
// the handle it is pointed at by, what it is called, and how big it is.
//
// It replaces `2 attachments · 4 KB`, which was true and said nothing. The
// count is the one fact about a staging area a reader never has to be told —
// they just attached them — and the names are the ones they do: two
// screenshots and a spec were the same sentence as three screenshots, and a
// file attached by accident looked exactly like one attached on purpose.
//
// No key is printed on a chip, and none ever will be. The strip sits above a
// live draft, so a key written on it would be an offer nothing accepts while
// the draft holds the keyboard, and a `✕` would be a control the keyboard
// cannot reach
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// A chip is a door instead: reading mode's cursor reaches the strip as its
// last row, where the keys are live because the mode holds the keyboard and
// its own bar says so, and a click on a chip is the pointer twin of that
// cursor's enter — the pointer names one thing, and that thing already has a
// key. So a chip can be drawn picked (the reading cursor standing in its mark's
// column) and reports the cells it was drawn in (AttachmentChipsAt), and it
// still carries nothing that asks to be pressed. By name, a chip is `/paste
// drop <handle>` — which is why the handle is the field a chip gives up last,
// and why what does not fit is counted rather than half-drawn.

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// ChipKind is what a staged attachment is, as the strip marks it. Two of the
// three marks are the glyph set's additions and the third is borrowed from
// it, argued in
// docs/interface/departures.md#the-attachment-chips-mark-what-kind-of-file-is-staged;
// they are closed for the same reason the rest of that set is.
type ChipKind int

const (
	// ChipText is text bound for the prompt itself: lines, unbounded.
	ChipText ChipKind = iota
	// ChipImage is a raster image: a frame with a subject inside it.
	ChipImage
	// ChipDocument is a document the model reads whole — a PDF. The same
	// lines as text, inside the boundary that makes it a single artifact.
	ChipDocument
)

// mark is the kind's glyph. Colour reinforces nothing here: the chips
// are drawn in body text and the mark carries the whole distinction, which is
// what makes the strip read the same in mono (invariant 1).
func (k ChipKind) mark() string {
	switch k {
	case ChipImage:
		return "▣"
	case ChipDocument:
		return "▤"
	}
	return "≡"
}

// AttachmentChip is one staged attachment as the strip draws it: a base name,
// never a path, and a size already in the rails' own units.
type AttachmentChip struct {
	Kind ChipKind
	// Handle is what the two verbs take — `Image#2` — and the chip leads
	// with it, ahead of the name, because a name is often nobody's choice
	// and three screenshots all called clipboard.png are told apart by
	// nothing else (docs/interface/surfaces.md#the-input-frame). Empty
	// leaves the name as the only word the chip has, and then the name is
	// never the field given up.
	Handle string
	Name   string
	Size   string
	// Lines is how far the text runs, for the chips that have text in them.
	// A size answers "will this fit"; it does not answer "which of these is
	// the stack trace", and for a paste that arrived without a name of its
	// own the height is the only other thing there is to say about it.
	//
	// Zero draws nothing. A picture and a PDF have no lines to count, and a
	// stat that cannot be reported is left out rather than reported as zero
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	Lines int
}

// chipNameWidth caps a chip's name. A staging area holds a handful of files
// and the strip is one line, so a name long enough to push another chip off
// the row costs more than its tail is worth. The cap is the name's and never
// the handle's: the handle is short by construction, and it is the word that
// tells two screenshots apart.
const chipNameWidth = 20

// chipSeparator joins chips the way every other rail joins its fields.
const chipSeparator = " · "

// chipForm is how much of a chip is drawn, widest first. Each form gives up
// one more field, and the handle is in all of them.
type chipForm int

const (
	// chipFull is the mark, the handle, the name and the counts.
	chipFull chipForm = iota
	// chipNameless gives up the name, which the handle already stands for.
	chipNameless
	// chipBare gives up the counts as well: the mark and the handle.
	chipBare
)

// AttachmentChips renders the staged set as one row, or "" when nothing is
// staged. It is AttachmentChipsAt with no chip picked and the cells thrown
// away, which is the strip above a draft that holds the keyboard.
func AttachmentChips(chips []AttachmentChip, width int) string {
	row, _ := AttachmentChipsAt(chips, width, -1)
	return row
}

// ChipHit is where one chip landed on a row AttachmentChipsAt drew: its index
// in the chips it was handed, and the columns it covers, From inclusive and
// To exclusive, counted from the row's first cell. A chip the row gave up has
// none, and neither does the count of the ones it gave up: a number of files
// nobody is looking at names no one of them to open.
type ChipHit struct {
	Index    int
	From, To int
}

// AttachmentChipsAt renders the staged set as one row with the chip at picked
// drawn under the reading cursor — a negative picked draws none — and reports
// the cells each drawn chip covers, which is what a click on the strip is
// resolved against.
//
// What does not fit is given up in an order, and the handle is the last of
// it, because the handle is what `/paste show` and `/paste drop` take. Every
// chip gives up its name first: the handle already says which chip it is,
// and a row of three `clipboard.png`s is the one thing a name adds. Then
// chips are dropped whole from the end rather than clipped, because half a
// handle is a chip that cannot be named, and what was dropped is counted
// where it stood — the number of files you are not looking at is the one
// thing the row cannot otherwise say. The one chip that is always kept gives
// up its counts, and only then clips.
//
// A picked chip is never the one given up. Where the chips in front of it
// fill the row, the ones before it are dropped instead and counted where they
// stood, at the front: a cursor that walked onto a chip the row is not
// drawing is a cursor nobody can see.
func AttachmentChipsAt(chips []AttachmentChip, width, picked int) (string, []ChipHit) {
	if len(chips) == 0 || width <= 0 {
		return "", nil
	}
	if picked >= len(chips) {
		picked = -1
	}
	for _, form := range []chipForm{chipFull, chipNameless} {
		run := stripRun{chips: chips, form: form, to: len(chips), picked: picked}
		if row, hits := run.draw(); lipgloss.Width(row) <= width {
			return row, hits
		}
	}
	// Give the row back one chip at a time until it fits alongside the count
	// of the ones it gave up. The last rung keeps one chip whatever happens:
	// a strip that is only a number has lost the thing it is for.
	for kept := len(chips) - 1; kept >= 1; kept-- {
		from := 0
		if picked >= kept {
			from = picked - kept + 1
		}
		run := stripRun{chips: chips, form: chipNameless, from: from, to: from + kept, picked: picked}
		if row, hits := run.draw(); lipgloss.Width(row) <= width {
			return row, hits
		}
	}
	// The count goes before the handle does: a clip through the tail would
	// cut the number off the one word the row still has.
	one := max(picked, 0)
	run := stripRun{chips: chips, form: chipBare, from: one, to: one + 1, picked: picked}
	if row, hits := run.draw(); lipgloss.Width(row) <= width {
		return row, hits
	}
	bare := Clip(chips[one].render(chipBare, one == picked), width)
	return bare, []ChipHit{{Index: one, From: 0, To: lipgloss.Width(bare)}}
}

// stripRun is the chips from one index up to another drawn in one form, with
// the ones either side of the run counted rather than drawn.
type stripRun struct {
	chips    []AttachmentChip
	form     chipForm
	from, to int
	picked   int
}

// draw lays the run on one row and notes the cells each chip took.
func (r stripRun) draw() (string, []ChipHit) {
	var b strings.Builder
	var hits []ChipHit
	col := 0
	write := func(s string) {
		b.WriteString(s)
		col += lipgloss.Width(s)
	}
	if r.from > 0 {
		write(sty.dim.Render(chipTail(r.from) + chipSeparator))
	}
	for i := r.from; i < r.to; i++ {
		if i > r.from {
			write(sty.dim.Render(chipSeparator))
		}
		at := col
		write(r.chips[i].render(r.form, i == r.picked))
		hits = append(hits, ChipHit{Index: i, From: at, To: col})
	}
	if r.to < len(r.chips) {
		write(sty.dim.Render(chipSeparator + chipTail(len(r.chips)-r.to)))
	}
	return b.String(), hits
}

// PasteToken is the fold anything that arrives at the cursor leaves in the
// sentence: `⟨Paste#1 · 214 lines⟩` for a paste, `⟨Image#1 · 1440×900⟩` for a
// picture, `⟨File#1 · 3.2 MB⟩` for anything else — the handle, which carries
// the kind, and the one figure that kind is counted by. The draft holds it
// where the bytes would otherwise have gone and the transcript keeps it once
// the message is sent (docs/interface/surfaces.md#the-input-frame).
//
// The angle quotes are two marks the guideline pages do not carry, taken
// deliberately because square brackets are how this product writes a key and
// a fold inside a live draft must not be readable as an offer
// (docs/interface/departures.md#the-paste-fold-is-written-in-angle-quotes).
//
// It is here beside the chip because the strip and the token are the same
// attachment said twice — what is riding, and where in the sentence it goes.
// The figure is the caller's, read off provider.Attachment.Figure, because
// the line that leads the bytes in the request states the same one and the
// word in the sentence must resolve to it.
func PasteToken(handle, figure string) string {
	return string(PasteFoldOpen) + handle + chipSeparator + figure + string(PasteFoldClose)
}

// PasteFoldOpen and PasteFoldClose are the two marks, exported because
// finding a fold is the other half of writing one: the draft and the
// transcript paint the run between them, and a surface spelling the pair
// again would be a second place the mark is decided.
const (
	PasteFoldOpen  = '⟨'
	PasteFoldClose = '⟩'
)

// countedLines is a line count as the rails write one. It is here rather than
// beside either caller because the strip and the preview card are the two
// surfaces that report a text attachment's height, and two spellings of the
// same count on two surfaces describing the same file is the kind of drift
// nobody notices until it is on a screenshot.
func countedLines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return strconv.Itoa(n) + " lines"
}

// chipTail counts the chips the row could not take.
func chipTail(hidden int) string {
	return "+" + strconv.Itoa(hidden) + " more"
}

// render lays one chip in a form: the kind's mark, the handle and the name in
// body text, the counts dim beside them. The counts read like every other
// count on the rails; the handle and the name are the content, and are the
// only parts drawn as such. A chip with no handle keeps its name in every
// form, since then the name is the word the verbs take.
//
// A picked chip is drawn the way reading mode draws the row under its
// cursor: the pointer in the chip's first column, where its mark was, and the
// rest of it lit — so nothing on the strip moves sideways to say where the
// cursor is. The mark is what gives way because the handle still says what
// kind of thing the chip is, the way a lit row gives up its fold mark
// (docs/interface/surfaces.md#reading-mode).
func (c AttachmentChip) render(form chipForm, picked bool) string {
	head := ""
	if c.Handle != "" {
		head += " " + c.Handle
	}
	if c.Handle == "" || form == chipFull {
		head += " " + Clip(c.Name, chipNameWidth)
	}
	counts := ""
	if form != chipBare {
		if c.Size != "" {
			counts += sty.dim.Render(" " + c.Size)
		}
		if c.Lines > 0 {
			counts += sty.dim.Render(" " + countedLines(c.Lines))
		}
	}
	if picked {
		rest := sty.body.Render(head) + counts
		return sty.focusPointer.Render("❯") + LitRow(rest, 0, lipgloss.Width(rest))
	}
	return sty.body.Render(c.Kind.mark()+head) + counts
}
