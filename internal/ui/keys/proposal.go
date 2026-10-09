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
// Decision.Accept, and esc is Select.Cancel, the way out that answers nothing.
type ProposalKeys struct {
	Later Binding
	Never Binding
}

var Proposal = ProposalKeys{
	Later: bind("n", "not now — offered again next time", "n"),
	Never: bind("N", "never — not offered again", "N"),
}
