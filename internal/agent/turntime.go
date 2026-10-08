package agent

import "time"

// Wait is what a running turn is waiting on at a given moment. A turn is
// always waiting on exactly one of the four, which is what lets their sum be
// the turn's own duration rather than an estimate of it: a turn that reads as
// eight minutes in one number reads as six of a command, one of a person
// deciding and one of the model once it is split this way.
// See docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed.
type Wait int

const (
	// WaitModelFirst is a request that has gone out and had nothing back
	// yet: the provider's queue, its time to first token, and any retry
	// waited out before it answered.
	WaitModelFirst Wait = iota
	// WaitModelStream is the model writing, from the request's first stream
	// event to its last.
	WaitModelStream
	// WaitTool is the round's calls being worked through: run, decided by the
	// policy or the classifier, or held by a hook in front of them.
	WaitTool
	// WaitPerson is a card in front of the person — an approval, a question,
	// a plan, a paused round — from the moment it is put to them to the
	// moment they answer it.
	WaitPerson

	waitCount
)

// TurnClock splits one turn's wall time into the four waits, and keeps the
// longest stretch of it in which nothing reached the screen. It holds no
// clock of its own: every mark takes the time its caller already read, so the
// split is summed from the same stamps the turn's duration is, and a test
// drives it with a held clock.
//
// It is a value with no references in it, so a front-end whose model is
// copied on every update carries it the way it carries its other counters.
type TurnClock struct {
	start, at time.Time
	wait      Wait
	split     [waitCount]time.Duration
	// The stretch is the time since something last reached the screen, by
	// what the turn waited on during it, and how many stream events arrived
	// in it that drew nothing.
	drew      time.Time
	stretch   [waitCount]time.Duration
	delivered int
	longest   Quiet
}

// Quiet is the longest stretch of a turn in which nothing reached the screen:
// how long it was, what the turn waited on for most of it, and how many
// stream events arrived during it that drew nothing — a keepalive, a
// reasoning delta on a rung that hides reasoning. The events are counted and
// never kept; the count is what tells a stream that went quiet from one that
// went silent.
type Quiet struct {
	Took      time.Duration
	On        Wait
	Delivered int
}

// TurnSplit is where a turn's time went, as of the moment it was read.
type TurnSplit struct {
	ModelFirst, ModelStream, Tool, Person time.Duration
	Quiet                                 Quiet
}

// Total is the four waits added up, which is the turn's span.
func (s TurnSplit) Total() time.Duration {
	return s.ModelFirst + s.ModelStream + s.Tool + s.Person
}

// Begin starts the clock at the turn's own start stamp, waiting on the model:
// a turn opens with its first request.
func (c *TurnClock) Begin(start time.Time) {
	*c = TurnClock{start: start, at: start, drew: start, wait: WaitModelFirst}
}

// Started is the stamp the clock was begun at, so a front-end can tell
// whether the turn it is timing is still the one the clock holds.
func (c *TurnClock) Started() time.Time { return c.start }

// Request marks a request going out: the turn is waiting on the model again,
// and has heard nothing back from this one.
func (c *TurnClock) Request(t time.Time) { c.to(t, WaitModelFirst) }

// Tool marks the turn working through calls.
func (c *TurnClock) Tool(t time.Time) { c.to(t, WaitTool) }

// Ask marks a card put in front of the person. The card is drawn, so the
// screen moved.
func (c *TurnClock) Ask(t time.Time) {
	c.to(t, WaitPerson)
	c.Drew(t)
}

// Waiting reports what the turn is waiting on now.
func (c *TurnClock) Waiting() Wait { return c.wait }

// Event marks a stream event arriving. The first of a request ends the wait
// for it; drew says whether the event put anything on the screen, and one
// that did not is counted against the stretch it arrived in.
func (c *TurnClock) Event(t time.Time, drew bool) {
	c.advance(t)
	if c.wait == WaitModelFirst {
		c.wait = WaitModelStream
	}
	if drew {
		c.Drew(t)
		return
	}
	c.delivered++
}

// Drew marks something reaching the screen, which ends the stretch running
// up to it.
func (c *TurnClock) Drew(t time.Time) {
	c.advance(t)
	c.closeStretch(t)
	c.drew, c.stretch, c.delivered = t, [waitCount]time.Duration{}, 0
}

// Split reads where the turn's time has gone up to t, counting the stretch
// still open as a candidate for the longest. It moves the clock to t and
// changes nothing else, so a turn read at a pause and read again at its end
// reads the whole span both times.
func (c *TurnClock) Split(t time.Time) TurnSplit {
	c.advance(t)
	longest := c.longest
	if open := c.openStretch(t); open.Took > longest.Took {
		longest = open
	}
	return TurnSplit{
		ModelFirst: c.split[WaitModelFirst], ModelStream: c.split[WaitModelStream],
		Tool: c.split[WaitTool], Person: c.split[WaitPerson], Quiet: longest,
	}
}

func (c *TurnClock) to(t time.Time, w Wait) {
	c.advance(t)
	c.wait = w
}

// advance books the time since the last mark to the wait the turn was in.
// A stamp earlier than the last one books nothing: the clock never runs
// backwards, so the four can never add up to more than the span.
func (c *TurnClock) advance(t time.Time) {
	if c.start.IsZero() || !t.After(c.at) {
		return
	}
	d := t.Sub(c.at)
	c.split[c.wait] += d
	c.stretch[c.wait] += d
	c.at = t
}

func (c *TurnClock) closeStretch(t time.Time) {
	if open := c.openStretch(t); open.Took > c.longest.Took {
		c.longest = open
	}
}

// openStretch is the stretch running since the screen last moved, filed
// under the wait it spent most of its time in.
func (c *TurnClock) openStretch(t time.Time) Quiet {
	if c.start.IsZero() || !t.After(c.drew) {
		return Quiet{}
	}
	on := WaitModelFirst
	for w := Wait(0); w < waitCount; w++ {
		if c.stretch[w] > c.stretch[on] {
			on = w
		}
	}
	return Quiet{Took: t.Sub(c.drew), On: on, Delivered: c.delivered}
}
