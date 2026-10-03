package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/storage"
)

// compareMinSessions is the fewest sessions a cohort may hold and still have
// its rates compared against another's.
//
// It is ten, and the argument is about what the number is used for rather
// than about a confidence interval this command deliberately does not
// compute. At ten, one sitting is a tenth of the cohort: the afternoon a
// dependency broke and every turn ended in a tool error can move a rate by
// its own share and no further, which is smaller than the difference anybody
// would act on. At six — the size that motivates the rule — one sitting is a
// sixth, and two of them going the same way produce a forty-percent
// difference out of nothing at all. A row that prints such a difference
// invites acting on it, and there is no way to tell from a percentage that
// it was made of two sessions.
//
// A cohort under it prints its count and no rate. That is the whole
// treatment: not a caveat beside a number, because a caveat beside a number
// loses to the number
// (docs/capabilities/sessions-and-memory.md#a-comparison-is-two-cohorts-as-rates).
const compareMinSessions = 10

// observeFigure is how one comparable figure is written down. It travels
// with the figure rather than being decided at the row, so the same number
// reads the same way in the report and in the export.
type observeFigure int

const (
	// observeRate is a count over a denominator — steers per turn, calls per
	// turn. Two decimals, because the ones worth reading are under one.
	observeRate observeFigure = iota
	// observeRounds is rounds per turn, at the precision the dashboard
	// already prints them at.
	observeRounds
	// observeShare is a proportion of a population, written as a percentage.
	observeShare
	// observeMoney is a spend.
	observeMoney
	// observeTokenRate is tokens over a denominator.
	observeTokenRate
)

// text writes one value of this figure.
func (f observeFigure) text(v float64) string {
	switch f {
	case observeRounds:
		return fmt.Sprintf("%.1f", v)
	case observeShare:
		return fmt.Sprintf("%.0f%%", v*100)
	case observeMoney:
		return observeSpend(v)
	case observeTokenRate:
		return tokenCount(int64(math.Round(v)))
	}
	return fmt.Sprintf("%.2f", v)
}

// observeSpend is a spend inside a comparison. It differs from observeCost
// in one place and for one reason: a cohort that spent nothing here has to
// say so, because the row beside it is a number and an empty half of an
// arrow reads as a rendering fault rather than as an absence.
func observeSpend(v float64) string {
	switch {
	case v <= 0:
		return "none"
	case v < 0.01:
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

// observeChange is one figure taken over both cohorts: what it was, what it
// became, and by how much. The report and the JSON are both rendered from
// this list and from nothing else, so the export cannot come to carry a
// different set of numbers from the screen.
type observeChange struct {
	// Section is the block the row is drawn under, and also the order: the
	// list is already in the order it is read in.
	Section string `json:"section"`
	Name    string `json:"name"`
	// Qualifier is what distinguishes two rows of the same name — which
	// decider allowed a call, which reason a signal fired for — or, where a
	// section holds one row, what the pair of figures on it is a statistic
	// of.
	Qualifier string `json:"qualifier,omitempty"`
	// Unit is what the two values are counted in, in the words the row
	// prints them in.
	Unit   string  `json:"unit"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	// Delta is the difference in the figure's own units, and Change the
	// difference as a proportion of what it was. Change is absent where the
	// earlier cohort's figure is zero: there is no ratio to take, and
	// printing one over a denominator of nothing is the arithmetic this
	// command exists to refuse.
	Delta  float64  `json:"delta"`
	Change *float64 `json:"change,omitempty"`
	// Beside is a second figure the first one only means anything next to.
	// Rounds per turn is the case that demands it: a change that made turns
	// shorter by making them fail is an improvement on the unqualified
	// number, so the share of turns that came out that way is drawn on the
	// same row rather than in a section a reader might not reach.
	Beside *observeChange `json:"beside,omitempty"`
	form   observeFigure
	// digits is how many decimal places a share is written to, settled per
	// column by observeShareColumns rather than per row.
	digits int
	// key is how the same row is found on both sides of the comparison. It
	// is not the name: two decision rows are both `denied` and differ only
	// in who decided.
	key string
}

// figures is the pair the row leads with: `3.2 → 2.7 rounds`.
func (c observeChange) figures() string {
	pair := c.figure(c.Before) + " → " + c.figure(c.After)
	if c.Unit == "" {
		return pair
	}
	return pair + " " + c.Unit
}

// figure writes one of the pair, at the precision its column was settled at.
func (c observeChange) figure(v float64) string {
	if c.form == observeShare {
		return fmt.Sprintf("%.*f%%", c.digits, v*100)
	}
	return c.form.text(v)
}

// observeShareColumns settles how precisely each column of shares is
// written: a tool error rate of 2.5% and one of 1.6% are the same whole
// number and a very different week, while a gate pass rate of 67% gains
// nothing from a decimal and pays a column of width for it.
//
// It is decided per column and not per row. A block whose rows disagree
// about their decimal reads as a rendering fault, and the reason the decimal
// is needed at all — that the difference would otherwise round away — is a
// property of what the column measures rather than of one row in it. A share
// beside another figure is its own column, since it is read down the detail
// field rather than against the values above it.
func observeShareColumns(changes []observeChange) {
	each := func(fn func(*observeChange)) {
		for i := range changes {
			if changes[i].form == observeShare {
				fn(&changes[i])
			}
			if b := changes[i].Beside; b != nil && b.form == observeShare {
				fn(b)
			}
		}
	}
	largest := map[string]float64{}
	each(func(c *observeChange) {
		key := c.Section + " " + c.Unit
		largest[key] = math.Max(largest[key], math.Max(math.Abs(c.Before), math.Abs(c.After)))
	})
	each(func(c *observeChange) {
		if largest[c.Section+" "+c.Unit] < 0.1 {
			c.digits = 1
		}
	})
}

// direction is the size and sign of the change.
//
// A share moves in points and everything else moves in percent. The
// distinction is not pedantry: a gate pass rate going from 75% to 92% is
// seventeen points and twenty-three percent, and a reader who takes one for
// the other is wrong by the whole difference between them.
//
// A change too small to print is called unchanged rather than rendered as a
// signed zero, which reads as a rounding fault and tells the reader nothing
// either way.
func (c observeChange) direction() string {
	if c.form == observeShare {
		points := c.Delta * 100
		if smallest := 0.5 / math.Pow10(c.digits); math.Abs(points) >= smallest {
			return fmt.Sprintf("%+.*f pts", c.digits, points)
		}
		return "unchanged"
	}
	if c.Change == nil {
		// Nothing to divide by: the figure is new in the later cohort, which
		// is a fact worth the row and not a percentage.
		return "none before"
	}
	if pct := *c.Change * 100; math.Abs(pct) >= 0.5 {
		return fmt.Sprintf("%+.0f%%", pct)
	}
	return "unchanged"
}

// observeChangeOf builds one comparable figure.
func observeChangeOf(section, name, unit string, form observeFigure, before, after float64) observeChange {
	c := observeChange{Section: section, Name: name, Unit: unit, form: form,
		Before: before, After: after, Delta: after - before}
	if before != 0 {
		rel := (after - before) / before
		c.Change = &rel
	}
	return c
}

// observeCohortData is one side of the comparison: the sessions that ran
// under one value of the split key, and every aggregate over them.
type observeCohortData struct {
	storage.AgentCohort
	Reading storage.AgentCohortReading
}

// turns is the denominator every per-turn rate is over. It counts turn
// events rather than the sessions table's own column because the numerators
// are events too, and a rate whose halves are counted by different
// mechanisms is a rate that drifts when one of them stops being written.
func (c *observeCohortData) turns() float64 {
	var n int
	for _, t := range c.Reading.Turns {
		n += t.Count
	}
	return float64(n)
}

// calls is the denominator a tool error rate is over.
func (c *observeCohortData) calls() float64 {
	var n int
	for _, t := range c.Reading.Tools {
		n += t.Count
	}
	return float64(n)
}

// completed is how many of the cohort's sessions finished their work. The
// word is a literal for the same reason the outcome renderer's are: this
// reads rows written by every build that ever wrote one.
func (c *observeCohortData) completed() float64 {
	for _, o := range c.Reading.Outcomes {
		if o.Outcome == "completed" {
			return float64(o.Count)
		}
	}
	return 0
}

// firstWriteLooking is the middle session's count of search calls before its
// first write, and the share of the cohort's sessions that reached one. The
// two come back together because the first only means anything beside the
// second: a cohort whose sessions stopped writing altogether would otherwise
// report the looking of whichever few still did as an improvement.
//
// The share is over every session in the cohort, which is the denominator
// the report's header prints — including the ones that called no tool at
// all, since a session that never called one never wrote either. The
// dashboard's own row cannot use that denominator and says so instead: it is
// drawn from the reading alone, which holds only the sessions that called
// something.
func (c *observeCohortData) firstWriteLooking() (median, wrote float64) {
	var searches []int
	for _, f := range c.Reading.FirstWrites {
		if f.Wrote {
			searches = append(searches, f.Searches)
		}
	}
	if c.Sessions > 0 {
		wrote = float64(len(searches)) / float64(c.Sessions)
	}
	return observeMedian(searches), wrote
}

// MarshalJSON writes a cohort as the denominators the report's header
// prints, and not as the aggregates behind them. The figures are in the
// changes; a second copy of the raw counts beside them would be a second
// answer to the same question, and the two would be read against each other.
func (c observeCohortData) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Value     string  `json:"value"`
		Sessions  int     `json:"sessions"`
		Completed int     `json:"completed_sessions"`
		Turns     int     `json:"turns"`
		ToolCalls int     `json:"tool_calls"`
		TokensIn  int64   `json:"tokens_in"`
		TokensOut int64   `json:"tokens_out"`
		Cost      float64 `json:"est_cost"`
		First     string  `json:"first_session"`
		Last      string  `json:"last_session"`
	}{
		Value: c.Value, Sessions: c.Sessions, Completed: int(c.completed()),
		Turns: int(c.turns()), ToolCalls: int(c.calls()),
		TokensIn: c.TokensIn, TokensOut: c.TokensOut, Cost: c.Cost,
		First: c.First.UTC().Format(observeTimeLayout),
		Last:  c.Last.UTC().Format(observeTimeLayout),
	})
}

// observeCompareData is one comparison, as both the report and the export
// read it.
type observeCompareData struct {
	Window string `json:"window"`
	Split  string `json:"split"`
	// Sessions is how many of the window's sessions carry a value for the
	// split key at all.
	Sessions int `json:"sessions"`
	// Earlier and Later are the two cohorts compared, ordered by when each
	// was first seen. They are nil when the window holds fewer than two
	// values to compare.
	Earlier *observeCohortData `json:"earlier,omitempty"`
	Later   *observeCohortData `json:"later,omitempty"`
	// Others are the values left out, largest first.
	Others []string `json:"others,omitempty"`
	// MinSessions is the threshold, carried so a reader of the export knows
	// what Comparable was decided against.
	MinSessions int `json:"min_sessions"`
	// Comparable is false when either cohort is under that threshold, and
	// Changes is then empty: a comparison that cannot be read is not
	// rendered with a caveat, it is not rendered.
	Comparable bool            `json:"comparable"`
	Changes    []observeChange `json:"changes,omitempty"`
}

// readObserveCompare reads both cohorts out of the store. The two compared
// are the two with the most sessions, which is also what the small-n rule
// would leave standing: taking the two most recent instead would hand the
// comparison to a value that appeared twice yesterday, and taking every
// value would print a screen of cohorts too small to read.
func readObserveCompare(db *storage.DB, window, split string, since time.Time) (observeCompareData, error) {
	cohorts, err := db.AgentCohorts(since, split)
	if err != nil {
		return observeCompareData{}, fmt.Errorf("query cohorts: %w", err)
	}
	data := observeCompareData{Window: window, Split: split, MinSessions: compareMinSessions}
	for _, c := range cohorts {
		data.Sessions += c.Sessions
	}
	if len(cohorts) < 2 {
		return data, nil
	}
	// The store returns them largest first, so the two at the head are the
	// pair; which of them is the earlier is what their first sessions say.
	pair := []storage.AgentCohort{cohorts[0], cohorts[1]}
	if pair[1].First.Before(pair[0].First) {
		pair[0], pair[1] = pair[1], pair[0]
	}
	sides := make([]*observeCohortData, len(pair))
	for i, c := range pair {
		reading, err := db.ReadAgentCohort(since, split, c.Value)
		if err != nil {
			return observeCompareData{}, err
		}
		sides[i] = &observeCohortData{AgentCohort: c, Reading: reading}
	}
	for _, c := range cohorts[2:] {
		data.Others = append(data.Others, c.Value)
	}
	data.Earlier, data.Later = sides[0], sides[1]
	return observeCompared(data), nil
}

// observeCompared finishes a comparison once both cohorts have been read:
// whether it can be read at all, and the figures if it can. It is separate
// from the store so the whole screen can be held against a fixture.
func observeCompared(data observeCompareData) observeCompareData {
	if data.Earlier == nil || data.Later == nil {
		return data
	}
	data.Comparable = data.Earlier.Sessions >= compareMinSessions &&
		data.Later.Sessions >= compareMinSessions
	if data.Comparable {
		data.Changes = observeCompareChanges(data.Earlier, data.Later)
	}
	return data
}

// observeCompareChanges is every figure the comparison draws, in the order
// it is read in.
//
// The rates that answer "did the change help" lead, and the descriptions
// follow: how often a turn had to be corrected, how many rounds a turn took
// at equal outcome, how much looking came before the first write, how often
// a tool failed and how, whether the gate passed, and what a finished
// session cost. Each fact appears once — the
// steering signals are dropped from the signal block and the gate from
// nowhere else — because one fact counted twice on one screen reads as two.
//
// Every figure is a rate, and that is the point rather than a presentation
// choice: two cohorts either side of a change are never the same size, and a
// count row would read as a change that is only a change in how much work
// went through
// (docs/capabilities/sessions-and-memory.md#a-comparison-is-two-cohorts-as-rates).
func observeCompareChanges(earlier, later *observeCohortData) []observeChange {
	var out []observeChange
	out = append(out, observeSteeringChanges(earlier, later)...)
	out = append(out, observeInterventionChanges(earlier, later)...)
	out = append(out, observeRoundsChanges(earlier, later)...)
	out = append(out, observeFirstWriteChanges(earlier, later)...)
	out = append(out, observeToolErrorChanges(earlier, later)...)
	out = append(out, observeGateChanges(earlier, later)...)
	out = append(out, observeCostChanges(earlier, later)...)
	out = append(out, observeToolChanges(earlier, later)...)
	out = append(out, observeCommandChanges(earlier, later)...)
	out = append(out, observeDecisionChanges(earlier, later)...)
	out = append(out, observeSignalChanges(earlier, later)...)
	out = append(out, observeOutcomeChanges(earlier, later)...)
	observeShareColumns(out)
	return out
}

// observeTally is one aggregate reduced to a number per row, keyed so the
// same row can be found on both sides and named so it can be drawn.
type observeTally struct {
	order []string
	rows  map[string]observeTallyRow
}

type observeTallyRow struct {
	name, qualifier string
	value           float64
}

func (t *observeTally) add(key, name, qualifier string, v float64) {
	if t.rows == nil {
		t.rows = map[string]observeTallyRow{}
	}
	if _, seen := t.rows[key]; !seen {
		t.order = append(t.order, key)
	}
	row := t.rows[key]
	t.rows[key] = observeTallyRow{name: name, qualifier: qualifier, value: row.value + v}
}

func (t *observeTally) value(key string) float64 { return t.rows[key].value }

// observeTallyRows compares two tallies row by row, each over its own
// cohort's denominator. That division is the whole point of the screen: the
// two cohorts are never the same size, and every count row would otherwise
// read as a change that is only a change in how much work went through.
//
// The later cohort's order leads, because it is the side being asked about,
// and a row present on only one side still gets drawn — a signal that fired
// for the first time after a change is exactly what somebody is looking for.
func observeTallyRows(section, unit string, form observeFigure,
	earlier, later observeTally, earlierDen, laterDen float64) []observeChange {
	if earlierDen <= 0 || laterDen <= 0 {
		// A rate over no denominator is not a small number, it is no number.
		return nil
	}
	var out []observeChange
	for _, key := range append(append([]string{}, later.order...), earlier.order...) {
		if _, drawn := observeChangeIndex(out, key); drawn {
			continue
		}
		before, after := earlier.value(key)/earlierDen, later.value(key)/laterDen
		if before == 0 && after == 0 {
			continue
		}
		row := later.rows[key]
		if row.name == "" {
			row = earlier.rows[key]
		}
		c := observeChangeOf(section, row.name, unit, form, before, after)
		c.Qualifier = row.qualifier
		c.key = key
		out = append(out, c)
	}
	return out
}

// observeChangeIndex finds a key among the rows already built, so the two
// cohorts' orders can be concatenated rather than merged.
func observeChangeIndex(out []observeChange, key string) (int, bool) {
	for i, c := range out {
		if c.key == key {
			return i, true
		}
	}
	return 0, false
}

// observeSteeringChanges is how often a turn was corrected while it ran.
//
// The person's steer and the session's own intervention are separate rows
// and never one figure. A change to the interruption machinery that makes
// the session take stock more often so that the person has to break in less
// is the outcome that machinery exists for, and one number over the two
// nets exactly that improvement out to nothing.
func observeSteeringChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, s := range c.Reading.Signals {
			if s.Signal == "steered" || s.Signal == "intervened" {
				t.add(s.Signal+"/"+s.Reason, s.Signal, s.Reason, float64(s.Count))
			}
		}
		return t
	}
	return observeTallyRows("steering", "per turn", observeRate,
		tally(earlier), tally(later), earlier.turns(), later.turns())
}

// observeInterventionChanges is what those interruptions came to: the share
// of every interruption in the cohort that was followed by each reading
// state. It is the figure a change to the drift thresholds is made for —
// the steering block above says how often the machinery spoke, and only
// this says whether the turn was any different afterwards.
func observeInterventionChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, o := range c.Reading.Interventions {
			t.add(o.Kind+"/"+o.Reading, o.Kind+" → "+o.Reading, "", float64(o.Count))
		}
		return t
	}
	interruptions := func(c *observeCohortData) float64 {
		var n int
		for _, o := range c.Reading.Interventions {
			n += o.Count
		}
		return float64(n)
	}
	return observeTallyRows("intervened", "of interruptions", observeShare,
		tally(earlier), tally(later), interruptions(earlier), interruptions(later))
}

// observeRoundsChanges is rounds per turn, grouped by how the turn came out
// first — the qualification is the metric. Each row carries the share of
// turns that ended that way beside its rounds, so a cohort that got shorter
// turns by failing more of them cannot read as an improvement.
func observeRoundsChanges(earlier, later *observeCohortData) []observeChange {
	rounds := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, o := range c.Reading.Turns {
			t.add(o.Outcome, o.Outcome, "", o.AvgRounds)
		}
		return t
	}
	share := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, o := range c.Reading.Turns {
			t.add(o.Outcome, o.Outcome, "", float64(o.Count))
		}
		return t
	}
	// The share is what the rows are built from, and the rounds are hung on
	// them afterwards. The other way round loses a whole block to a surface
	// that records no rounds at all — a one-shot's single turn is round
	// zero on both sides — and it would take the turn mix down with it,
	// which is the qualification the block exists for.
	earlierRounds, laterRounds := rounds(earlier), rounds(later)
	out := observeTallyRows("rounds per turn", "of turns", observeShare,
		share(earlier), share(later), earlier.turns(), later.turns())
	for i := range out {
		beside := out[i]
		c := observeChangeOf("rounds per turn", beside.Name, "rounds", observeRounds,
			earlierRounds.value(beside.key), laterRounds.value(beside.key))
		c.key, c.Beside = beside.key, &beside
		out[i] = c
	}
	return out
}

// observeFirstWriteChanges is how much looking a cohort does before it
// changes anything, with the share of its sessions that got that far drawn
// beside it — the same qualification the rounds block carries, and for the
// same reason: a cohort that got its looking down by writing less often is
// not a cohort that found its way around faster.
//
// One row and no split by anything, because the question it answers is one
// question: whether a session opening on this tree spends its first rounds
// finding a place to start. A cohort where nothing was ever written draws no
// row at all rather than a pair of zeroes.
// See docs/capabilities/coding-agent.md#where-a-map-would-sit.
func observeFirstWriteChanges(earlier, later *observeCohortData) []observeChange {
	earlierMedian, earlierWrote := earlier.firstWriteLooking()
	laterMedian, laterWrote := later.firstWriteLooking()
	if earlierWrote == 0 && laterWrote == 0 {
		return nil
	}
	c := observeChangeOf("first write", "search calls", "calls", observeRounds,
		earlierMedian, laterMedian)
	beside := observeChangeOf("first write", "search calls", "wrote", observeShare,
		earlierWrote, laterWrote)
	c.key, c.Qualifier, c.Beside = "search calls", "median", &beside
	return []observeChange{c}
}

// observeToolErrorChanges is the tool error rate by class, over every call
// the cohort made. The class is what makes the number actionable: a rate
// that rose on bad arguments is a prompt problem and one that rose on scope
// is a policy problem, and the two want opposite responses.
func observeToolErrorChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, e := range c.Reading.ToolErrors {
			class := e.Class
			if class == "" {
				class = "unclassified"
			}
			t.add(class, class, "", float64(e.Count))
		}
		return t
	}
	return observeTallyRows("tool errors", "of calls", observeShare,
		tally(earlier), tally(later), earlier.calls(), later.calls())
}

// observeGateChanges is each suite's pass rate. A run that was blocked or
// cancelled is out of the rate on both sides, the way it is on the
// dashboard: it is no reading of the code rather than a bad one, and a
// suite with no verdict either way gets no row instead of a rate over
// nothing.
func observeGateChanges(earlier, later *observeCohortData) []observeChange {
	judged := func(c *observeCohortData) (observeTally, observeTally) {
		var passed, ran observeTally
		for _, g := range c.Reading.Gates {
			switch g.Verdict {
			case "pass":
				passed.add(g.Suite, g.Suite, "", float64(g.Count))
				ran.add(g.Suite, g.Suite, "", float64(g.Count))
			case "fail":
				ran.add(g.Suite, g.Suite, "", float64(g.Count))
			}
		}
		return passed, ran
	}
	earlierPassed, earlierRan := judged(earlier)
	laterPassed, laterRan := judged(later)
	var out []observeChange
	for _, key := range append(append([]string{}, laterRan.order...), earlierRan.order...) {
		if _, drawn := observeChangeIndex(out, key); drawn {
			continue
		}
		before, after := earlierRan.value(key), laterRan.value(key)
		if before == 0 || after == 0 {
			// One side never got a verdict for this suite, so there is no
			// pair to compare.
			continue
		}
		c := observeChangeOf("gate", key, "passed", observeShare,
			earlierPassed.value(key)/before, laterPassed.value(key)/after)
		c.key = key
		out = append(out, c)
	}
	return out
}

// observeCostChanges is what the work cost, per session and per session that
// finished. The second is the one that answers the question: a cohort that
// abandoned half its sessions spent the same money for half the work, and
// only the completed denominator says so.
func observeCostChanges(earlier, later *observeCohortData) []observeChange {
	var out []observeChange
	// A cohort whose model the pricing table does not carry spent nothing
	// the record can see, and rows of "none → none" describe the pricing
	// table rather than the change being measured.
	priced := earlier.Cost > 0 || later.Cost > 0
	// The denominator is the unit rather than the name: two rows both about
	// cost line up under one word, and what differs between them is what
	// each was divided by, which is the thing being read.
	if priced && earlier.Sessions > 0 && later.Sessions > 0 {
		out = append(out, observeChangeOf("cost", "cost", "per session", observeMoney,
			earlier.Cost/float64(earlier.Sessions), later.Cost/float64(later.Sessions)))
	}
	if priced && earlier.completed() > 0 && later.completed() > 0 {
		out = append(out, observeChangeOf("cost", "cost", "per completed session", observeMoney,
			earlier.Cost/earlier.completed(), later.Cost/later.completed()))
	}
	if earlier.turns() > 0 && later.turns() > 0 {
		out = append(out, observeChangeOf("cost", "tokens", "per turn", observeTokenRate,
			float64(earlier.TokensIn+earlier.TokensOut)/earlier.turns(),
			float64(later.TokensIn+later.TokensOut)/later.turns()))
	}
	return out
}

// observeToolChanges is how often each tool was reached for, per turn, with
// its own failure rate beside it. A tool called half as often for the same
// work is the shape a prompt change makes.
func observeToolChanges(earlier, later *observeCohortData) []observeChange {
	calls := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, u := range c.Reading.Tools {
			t.add(u.Tool, u.Tool, "", float64(u.Count))
		}
		return t
	}
	// The failure rate is already a rate: the aggregate averages it over the
	// cohort's own calls to that tool, so it is carried across as it stands
	// rather than multiplied back into a count and divided by the same count
	// again.
	failed := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, u := range c.Reading.Tools {
			t.add(u.Tool, u.Tool, "", u.ErrorRate)
		}
		return t
	}
	earlierCalls, laterCalls := calls(earlier), calls(later)
	earlierFailed, laterFailed := failed(earlier), failed(later)
	out := observeTallyRows("tools", "per turn", observeRate,
		earlierCalls, laterCalls, earlier.turns(), later.turns())
	for i := range out {
		key := out[i].key
		// A tool only one cohort ever reached for has no pair of failure
		// rates: the zero on the side that never called it would read as a
		// tool that never failed rather than one that was never used.
		if earlierCalls.value(key) == 0 || laterCalls.value(key) == 0 {
			continue
		}
		rate := observeChangeOf("tools", out[i].Name, "failed", observeShare,
			earlierFailed.value(key), laterFailed.value(key))
		if rate.Before == 0 && rate.After == 0 {
			continue
		}
		out[i].Beside = &rate
	}
	return out
}

// observeCommandChanges is the share of each cohort's commands that did each
// kind of thing, the dashboard's COMMANDS block as a comparison: a change to
// the built-in tools is made to move the share of shell calls that were a
// read (docs/capabilities/sessions-and-memory.md#a-command-is-recorded-by-what-it-was-for).
// A share of commands rather than a rate per turn, because a cohort that ran
// fewer commands at all would otherwise read as one that read less by shell.
// Commands recorded before the record said stay a row of their own, as on
// the dashboard: they are in the denominator either way.
func observeCommandChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) (observeTally, float64) {
		var (
			t     observeTally
			total float64
		)
		for _, p := range c.Reading.Commands {
			name := p.Purpose
			if name == "" {
				name = "unrecorded"
			}
			t.add(name, name, "", float64(p.Count))
			total += float64(p.Count)
		}
		return t, total
	}
	earlierTally, earlierTotal := tally(earlier)
	laterTally, laterTotal := tally(later)
	return observeTallyRows("commands", "of commands", observeShare,
		earlierTally, laterTally, earlierTotal, laterTotal)
}

// observeDecisionChanges is how often the permission policy was asked and
// what it said, per turn. The decider stays on the row: a denial by a person
// is a preference and a denial by a rule is policy, and a change that moved
// work from one to the other is the change worth seeing.
func observeDecisionChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, d := range c.Reading.Decisions {
			t.add(d.Decision+"/"+d.Reason, observeDecisionWord(d.Decision),
				observeDecider(d.Reason), float64(d.Count))
		}
		return t
	}
	out := observeTallyRows("decisions", "per turn", observeRate,
		tally(earlier), tally(later), earlier.turns(), later.turns())
	// The overturn rate is a share of each cohort's own judged denials: it
	// is the figure a change to the classifier is made to move, and per turn
	// it would move with how often the classifier said no at all.
	overturned := func(c *observeCohortData) observeTally {
		var t observeTally
		t.add("overturned", "overturned", "you", float64(c.Reading.Overturns.Overturned))
		return t
	}
	return append(out, observeTallyRows("decisions", "of judged denials", observeShare,
		overturned(earlier), overturned(later),
		float64(earlier.Reading.Overturns.Judged), float64(later.Reading.Overturns.Judged))...)
}

// observeSignalChanges is the rest of the loop's safeguards, per turn. The
// steering signals are left out because they lead the screen, and the gate
// because it has a block of its own.
func observeSignalChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, s := range c.Reading.Signals {
			switch s.Signal {
			case "steered", "intervened", "gate":
				continue
			}
			t.add(s.Signal+"/"+s.Reason, s.Signal, s.Reason, float64(s.Count))
		}
		return t
	}
	return observeTallyRows("signals", "per turn", observeRate,
		tally(earlier), tally(later), earlier.turns(), later.turns())
}

// observeOutcomeChanges is the share of sessions that came out each way. It
// closes the screen because it is the column every rate above it is worth
// correlating against.
func observeOutcomeChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, o := range c.Reading.Outcomes {
			t.add(o.Outcome, o.Outcome, "", float64(o.Count))
		}
		return t
	}
	return observeTallyRows("outcomes", "of sessions", observeShare,
		tally(earlier), tally(later), float64(earlier.Sessions), float64(later.Sessions))
}

// observeCompareReport draws the comparison.
//
// No row carries a tick or a cross. Every figure on this screen is a
// difference, and which direction is the good one is the reader's to decide:
// fewer rounds is an improvement unless the turns got shorter by failing,
// and more interventions is a regression unless they are what stopped the
// person from having to break in. A glyph that guessed would be wrong often
// enough to be worth nothing and confident every time
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
func observeCompareReport(data observeCompareData) report.Report {
	r := report.Report{
		Title: "shhh observe compare",
		Subject: joinDetail(data.Split, joinDetail(
			countOf(data.Sessions, "session", "sessions"), "last "+data.Window)),
	}
	// The way out names no --split: whoever is reading this typed one, and
	// the thing to change is the window.
	const wayOut = "shhh observe compare --window 90d"
	if data.Earlier == nil || data.Later == nil {
		// One value in the window is not a comparison with one side at
		// nothing; it is a window with nothing to compare. Every rate would
		// otherwise read as having appeared or vanished entirely.
		if data.Sessions == 0 {
			return emptyInto(r, "no sessions in the last "+data.Window+" carry a "+data.Split, "shhh chat")
		}
		return emptyInto(r, "only one "+data.Split+" in the last "+data.Window, wayOut)
	}

	r.Sections = append(r.Sections, report.Section{Header: "COHORTS", Rows: []report.Row{
		observeCohortRow("earlier", data.Earlier),
		observeCohortRow("later", data.Later),
	}})

	if !data.Comparable {
		short, side := data.Earlier, "earlier"
		if data.Later.Sessions < short.Sessions {
			short, side = data.Later, "later"
		}
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{{
			State: report.Skip, Subject: "too few sessions to compare",
			Outcome: joinDetail(side, countOf(short.Sessions, "session", "sessions")),
			Consequence: strconv.Itoa(data.MinSessions) +
				" is the fewest a rate is read from: under that, one unusual sitting moves a rate " +
				"further than the change being measured would",
			Fix: []string{wayOut},
		}}})
		return observeCompareNotes(r, data)
	}

	for _, c := range data.Changes {
		// The change list is already in reading order, so the blocks fall
		// out of it: a new section starts wherever the section name does.
		// One list behind both the screen and the export is what stops the
		// two from coming to hold different figures.
		if header := strings.ToUpper(c.Section); r.Sections[len(r.Sections)-1].Header != header {
			r.Sections = append(r.Sections, report.Section{Header: header})
		}
		last := &r.Sections[len(r.Sections)-1]
		last.Rows = append(last.Rows, observeChangeRow(c))
	}
	return observeCompareNotes(r, data)
}

// observeCohortRow is one side of the comparison in the header: what it ran
// under, how long it ran for, and the session count every rate below is
// divided by. The count is the outcome field because it never clips: it is
// the denominator, and a rate whose denominator went missing is a rate
// nobody can weigh.
func observeCohortRow(side string, c *observeCohortData) report.Row {
	return report.Row{State: report.Queue, Name: side, Subject: c.Value,
		Detail: joinDetail(
			c.First.Local().Format("Jan 2")+" – "+c.Last.Local().Format("Jan 2"),
			countOf(int(c.turns()), "turn", "turns")),
		Outcome: countOf(c.Sessions, "session", "sessions")}
}

func observeChangeRow(c observeChange) report.Row {
	detail := c.Qualifier
	if c.Beside != nil {
		detail = joinDetail(detail, c.Beside.figures()+" ("+c.Beside.direction()+")")
	}
	return report.Row{State: report.Queue, Name: c.Name,
		Subject: c.figures(), Detail: detail, Outcome: c.direction()}
}

// observeCompareNotes closes the report with what it left out and what it
// does not claim.
func observeCompareNotes(r report.Report, data observeCompareData) report.Report {
	if len(data.Others) > 0 {
		r.Notes = append(r.Notes, report.Note{State: report.Skip,
			Text: countOf(len(data.Others), "other value", "other values") +
				" in the window: " + strings.Join(data.Others, ", ")})
	}
	// The posture, stated where the numbers are — and only where there are
	// numbers. This is a local store of a few hundred sessions, and a
	// p-value over a sample this shape would lend the screen an authority it
	// has not got, so the rates, the denominators and the direction are the
	// whole answer and the reader draws the conclusion.
	if len(data.Changes) > 0 {
		r.Notes = append(r.Notes, report.Note{State: report.Queue,
			Text: "rates over the denominators above; no significance is claimed"})
	}
	return r
}

// observeSplitKey checks a split key against what the store will group on,
// so a mistyped one is answered with the list rather than with an empty
// screen that looks like a window with nothing in it.
func observeSplitKey(key string) error {
	keys := storage.AgentSplitKeys()
	for _, k := range keys {
		if k == key {
			return nil
		}
	}
	if key == "" {
		// The sentence opens on a word rather than on the flag: the error
		// banner upper-cases its first letter, and `--Split` is a flag
		// nobody can type.
		return fmt.Errorf("a --split is required: one of %s", strings.Join(keys, ", "))
	}
	return fmt.Errorf("cannot split sessions on %q: one of %s", key, strings.Join(keys, ", "))
}
