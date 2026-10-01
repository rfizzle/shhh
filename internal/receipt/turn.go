package receipt

import "time"

// TurnEnd is how a turn stands: still working, or how it ended.
type TurnEnd int

const (
	TurnWorking TurnEnd = iota
	TurnDone
	TurnCancelled
	TurnFailed
)

// Turn is the turn's own receipt: the facts its total line states while it
// runs and its close row states once it has ended. It is one value so the
// two cannot disagree — a total reading `11 tools` above a close reading 12
// would leave the reader to work out which of them was counting.
//
// A field the session could not measure is left at its zero, and a reader
// leaves it out rather than stating it
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
type Turn struct {
	End     TurnEnd
	Elapsed time.Duration
	// Steps is the steps the turn ran, and Tools the calls it made.
	Steps, Tools int
	// Spend is what the turn cost, already worded by whoever priced it: an
	// amount, or a token count where the model had no price.
	Spend string
	// At is when the turn ended, and zero while it is working.
	At time.Time
}
