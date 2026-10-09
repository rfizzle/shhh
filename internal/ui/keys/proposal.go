package keys

// ProposalKeys are the two answers the proposal card gives where every other
// summoned card gives one. A proposal made from what repeats across sessions
// can be refused for now or for good, and the two differ in what outlives the
// card: not now leaves it to be offered again next time, and never writes the
// person's no down so no surface raises it again
// (docs/capabilities/sessions-and-memory.md#memory-is-what-shhh-knows-about-your-project).
//
// They are Refuse split in two, so the pair keeps Refuse's spelling: the
// letter is the no, and the shifted letter is the same no, more of it — the
// way the approval card's [N] is its [n] with a sentence. The yes is
// Write below, and esc is Select.Cancel, the way out that answers nothing.
//
// The yes is the letter alone. The card is opened with enter on a row of
// /patterns, so a second enter from the same double tap would write a line
// that widens what runs without asking, on a card its reader had not read.
// Every other summoned card takes enter as a yes because what it writes is
// the thing the reader asked for by name; here the reader asked to look.
//
// The scaffold, toolchain and toolchain-draft cards are opened the same way,
// by enter on a row or a typed command, and take the same Write: what they
// write or install is the reader's to answer on the card, not on the key that
// opened it.
type ProposalKeys struct {
	Write Binding
	Later Binding
	Never Binding
}

var Proposal = ProposalKeys{
	Write: bind("y", "write it", "y", "Y"),
	Later: bind("n", "not now — offered again next time", "n"),
	Never: bind("N", "never — not offered again", "N"),
}
