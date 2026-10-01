package chat

// The step card (docs/interface/surfaces.md#the-step): every run of calls
// the transcript holds is drawn as one card on the band — the steps prose
// titled and the runs nothing titled alike — and this file is the adapter
// from a block of entries to components.StepCard. What the card states about
// the step is read once, from the step's receipt (internal/receipt): the
// verb and the rollup, the glyph and the evidence are the receipt's, picked
// by one precedence, so the header and the footer cannot argue. What the
// session knows and the receipt cannot — that a step is still running, how
// a call came to be allowed, what a reading made of the work — is added
// here.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/receipt"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// cardStripMinCalls is how many calls make a step large enough that its
// footer ends in the strip, one glyph per call. Below it the rollup in the
// header is the whole sequence already: the catalogue's step of seven calls
// draws none, and its step of twenty-two does. Eight is where a reader
// stops being able to hold the order from the counts alone.
// See docs/interface/departures.md#the-strip-starts-at-eight-calls.
const cardStripMinCalls = 8

// isCardBlock reports whether a block is drawn as a card: a step that has
// begun, or a run of calls nothing titled. A declared step nobody has
// reached has no calls to state and keeps the plan's outline row.
func isCardBlock(blk transcriptBlock, es []entry) bool {
	if blk.step != nil {
		return !blk.step.queued()
	}
	return blk.end > blk.start && blk.start < len(es) && isActivityEntry(es[blk.start])
}

// cardAnchor is the entry a card is kept on: the prose that titled it, or
// the first call of a run nothing titled. The reader's fold is written
// there and the reading cursor stands there, so the card holds no state of
// its own and re-renders from the entries on a resize.
func cardAnchor(blk transcriptBlock) int {
	if blk.step != nil {
		return blk.step.titleIdx
	}
	return blk.start
}

// cardBlockAt is the card whose anchor is the entry at idx.
func (m Model) cardBlockAt(es []entry, idx int) (transcriptBlock, bool) {
	if idx < 0 || idx >= len(es) {
		return transcriptBlock{}, false
	}
	for _, blk := range m.blocksOf(es) {
		// A block holds the entry that titles it as well as its rows.
		if !blk.holds(idx) {
			continue
		}
		if isCardBlock(blk, es) && cardAnchor(blk) == idx {
			return blk, true
		}
	}
	return transcriptBlock{}, false
}

// cardShape is how much of a card is drawn, and whether the reader folded
// it. A finished card does not fold on its own: it stays as the rung draws
// it — the header alone at low, header, body and footer at normal, open at
// high — until the reader folds or opens it, and their answer outranks the
// rung (docs/interface/surfaces.md#the-step).
func (m Model) cardShape(blk transcriptBlock, es []entry) (density components.CardDensity, folded bool) {
	a := cardAnchor(blk)
	if a < 0 || a >= len(es) {
		return components.CardNormal, false
	}
	switch es[a].stepFold {
	case foldClosed:
		return components.CardLow, true
	case foldOpen, foldSearch:
		// Opened onto its calls, by the reader or by the search reaching a
		// match it counted behind the card.
		return components.CardHigh, false
	case foldCard:
		return components.CardNormal, false
	}
	switch {
	case m.cardDetailOpen(blk, es):
		return components.CardHigh, false
	case m.density(verbosityNormal):
		return components.CardNormal, false
	}
	return components.CardLow, false
}

// cardOpen reports whether the card is drawing its calls.
func (m Model) cardOpen(blk transcriptBlock, es []entry) bool {
	d, folded := m.cardShape(blk, es)
	return !folded && d == components.CardHigh
}

// cardHidesRows reports whether the card stands in for its calls rather
// than drawing them, which is what makes it a fold a search counts behind.
func (m Model) cardHidesRows(blk transcriptBlock, es []entry) bool {
	return !m.cardOpen(blk, es)
}

// cardDetailOpen is whether the card's calls are on screen with their
// bodies: the reader opened the step's detail, or the rung is high. A run
// nothing titled has no detail of its own to open (/step names steps), so
// only the rung opens it.
func (m Model) cardDetailOpen(blk transcriptBlock, es []entry) bool {
	if blk.step != nil {
		return m.stepDetailOpen(blk.step, es)
	}
	return m.density(verbosityHigh)
}

// actOf is one call as the step's receipt counts it: its receipt, and how
// it stands as the row draws it, which is where the session's own knowledge
// — running, refused, stopped — has already been read in.
func (m Model) actOf(e entry) receipt.Act {
	rc := m.receiptOf(e)
	act := receipt.Act{Receipt: rc, Duration: e.duration}
	if e.kind == entryDiff {
		return act
	}
	switch m.activityRowFor(e).State {
	case components.ActivityRunning, components.ActivityQueued, components.ActivityChecking:
		act.State = receipt.StateRunning
	case components.ActivityFailed:
		act.State = receipt.StateFailed
	case components.ActivityDenied:
		act.State = receipt.StateRefused
	}
	return act
}

// cardLive reports whether the block is the step the turn is still adding
// to. A call joins the transcript only once it finishes, so the rows alone
// never say a step is busy; the turn does.
func (m Model) cardLive(blk transcriptBlock) bool {
	return blk.last && m.turnState() != stateInput
}

// cardHoldsCommand reports whether the live card is where the assistant's
// running command is drawn: its tail under the card's body, and its clock
// in the card's duration. A command the reader typed is not the step's.
func (m Model) cardHoldsCommand(blk transcriptBlock) bool {
	return m.cardLive(blk) && m.state == stateRunningCmd && m.runTail != nil &&
		m.pendingApproval != nil && m.pendingApproval.kind == approvalExec
}

// liveCardHoldsCommand reports whether the transcript's live card has taken
// the running command, so the row under the transcript does not draw it a
// second time.
func (m Model) liveCardHoldsCommand() bool {
	es := *m.entries()
	blocks := m.blocksOf(es)
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].last {
			return isCardBlock(blocks[i], es) && m.cardHoldsCommand(blocks[i])
		}
	}
	return false
}

// liveCardTicks reports whether a card on the transcript is moving: the
// live step's spinner and its clock. The transcript repaints on the tick
// for it, as it does for an arriving message.
func (m Model) liveCardTicks() bool {
	if m.turnState() == stateInput || m.attachedTo != "" || !m.spinnerWanted() {
		return false
	}
	es := *m.entries()
	blocks := m.blocksOf(es)
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].last {
			d, folded := m.cardShape(blocks[i], es)
			return isCardBlock(blocks[i], es) && !folded && d != components.CardLow
		}
	}
	return false
}

// cardActs is a card's calls as its receipt counts them, with the entry each
// call is, in the order they were made; and the reading taken into the card,
// where one was.
func (m Model) cardActs(blk transcriptBlock, es []entry) (acts []receipt.Act, at []int, reading *summaryReading) {
	start, end := blk.members()
	for i := start; i < end && i < len(es); i++ {
		switch e := es[i]; {
		case isActivityEntry(e):
			acts = append(acts, m.actOf(e))
			at = append(at, i)
		case e.kind == entrySummary && e.reading != nil:
			reading = e.reading
		}
	}
	return acts, at, reading
}

// stepCardFor builds the card for a block at the width it will be drawn at.
func (m Model) stepCardFor(blk transcriptBlock, es []entry, width int, selected bool) components.StepCard {
	density, folded := m.cardShape(blk, es)
	start, end := blk.members()
	acts, at, reading := m.cardActs(blk, es)
	s := receipt.BuildStep(acts)
	h := s.Header()
	if h.Verb == "" && len(acts) > 0 {
		// Every call was refused, so nothing was done to count: the card
		// names the first one in the form it was asked in, and by the
		// subject its row names it by — a call the queue refused for want
		// of a path says so there.
		h = receipt.Header{Verb: acts[0].Receipt.Verb, Subject: m.activityRowFor(es[at[0]]).Target}
	}
	c := components.StepCard{
		Kind:     activityKind(s.Lead.Kind),
		Rail:     s.Rail,
		Verb:     h.Verb,
		Subject:  h.Subject,
		Rollup:   h.Rollup,
		Bare:     h.Bare,
		Duration: turnDuration(s.Duration),
		Density:  density,
		Folded:   folded,
		Selected: selected,
		Frame:    m.spinFrame,
	}
	switch s.Lead.State {
	case receipt.StateFailed:
		c.State = components.ActivityFailed
	case receipt.StateRefused:
		c.State = components.ActivityDenied
	}
	if s.Running || (m.cardLive(blk) && c.State == components.ActivityDone) {
		c.State = components.ActivityRunning
		c.Spin = m.spinnerWanted()
	}
	if m.cardHoldsCommand(blk) {
		// The duration ticks with the command still running, and the
		// command's last line out stands under the body.
		c.Duration = turnDuration(s.Duration + clock().Sub(m.runStart))
		c.Tail = m.runTail.Line()
	}

	// The step's answer: what broke, what refused it, what a write changed,
	// or how a step of one call came out and how it came to be allowed. A
	// live chord the call's row offered — a rule's refusal sends the reader
	// to its own answer — is the card's to offer now.
	var gate string
	var refused *components.ActivityRow
	var ran []int
	for i, a := range acts {
		if a.State != receipt.StateRefused {
			ran = append(ran, at[i])
			continue
		}
		row := m.activityRowFor(es[at[i]])
		refused = &row
	}
	answer := func(row components.ActivityRow) {
		c.Outcome, c.OutcomeState, gate, c.Keys = row.Outcome, row.State, row.Allowed, row.Keys
		c.ByRule = row.ByRule
	}
	switch {
	case c.State == components.ActivityFailed:
		for i := len(at) - 1; i >= 0; i-- {
			if row := m.activityRowFor(es[at[i]]); row.Failed() {
				answer(row)
				break
			}
		}
	case c.State == components.ActivityDenied:
		// Every call was refused, and the last refusal is the step's answer.
		// Who refused it is part of the answer rather than an account of
		// it, so it stands with the outcome and never drops
		// (docs/interface/principles.md#two-denials-are-not-one-denial).
		answer(*refused)
		c.Outcome, gate = components.OutcomeBy(refused.Outcome, refused.Allowed), ""
		// Nothing ran, so there is no time to state; the rail is the one its
		// call would have carried; and why it was refused is the footer.
		c.Duration, c.Rail = "", true
		c.Evidence = refusalReason(es[at[len(at)-1]])
	case s.Running:
		// A call still in flight says how it stands — running, waiting a
		// host out — and that is the step's answer until it lands: the one
		// with something to say beyond running, where there is one.
		for i := range at {
			row := m.activityRowFor(es[at[i]])
			if row.State != components.ActivityRunning {
				continue
			}
			if c.Outcome == "" || row.Outcome != components.OutcomeRunning {
				c.Outcome, c.OutcomeState = joinNonEmpty(row.Outcome, row.Counts), components.ActivityRunning
			}
		}
	case s.Added+s.Removed > 0:
		c.Outcome = lineChange(s.Added, s.Removed)
	case len(ran) == 1 && es[ran[0]].kind != entryDiff:
		answer(m.activityRowFor(es[ran[0]]))
	}

	// The body is the prose that titled the step, whole; where the model
	// said nothing and a reading stands, the reading's sentence stands in
	// for it.
	if g := blk.step; g != nil && g.titleIdx >= 0 && g.titleIdx < len(es) {
		c.Body = strings.TrimSpace(es[g.titleIdx].text)
	} else if reading != nil {
		c.Body, c.Reading = strings.TrimSpace(reading.verdict.Text), true
	}

	// The footer is the receipt's evidence: the tool's own line, and what
	// the call it came from counted.
	if ev := s.Evidence; ev.Line != "" && c.State != components.ActivityDenied {
		// A line the header already says — the one file a step read, the
		// one query it searched — is not said again under it.
		if !headerSays(h, ev.Line) {
			c.Evidence = ev.Line
		}
		if ev.Path != "" && ev.Path != h.Subject {
			c.Evidence = ev.Path + " · " + ev.Line
		}
		if c.Evidence != "" && ev.Call >= 0 && ev.Call < len(acts) {
			c.EvidenceRight = acts[ev.Call].Receipt.Counts()
		}
	}
	if len(s.Strip) >= cardStripMinCalls {
		for _, mk := range s.Strip {
			c.Strip = append(c.Strip, components.StripCell{Kind: activityKind(mk.Kind), State: markState(mk.State)})
		}
	}
	if refused != nil && c.State != components.ActivityDenied && c.State != components.ActivityFailed {
		// A call refused among calls that ran is the footer: the header
		// counts only what ran, so a refusal anywhere else on the card would
		// read as the answer of the calls it names. The call as it was asked
		// for stands left and who refused it right, the refusal's live chord
		// with it (docs/interface/principles.md#two-denials-are-not-one-denial).
		c.Evidence = refused.Verb + " " + refused.Target
		c.EvidenceRight = components.OutcomeBy(refused.Outcome, refused.Allowed)
		c.EvidenceAlert = refused.ByRule
		c.Keys = refused.Keys
	}
	c.Evidence = evidenceLine(c.Evidence)
	footer := c.Evidence != "" || len(c.Strip) > 0

	// The reading's verdict sits in the footer beside the evidence it
	// judged where there is a footer, and on the header where there is not.
	var verdicts []string
	if gate != "" {
		verdicts = append(verdicts, gate)
	}
	if reading != nil {
		v, alert := cardVerdict(reading.verdict.State)
		if footer {
			c.EvidenceRight = joinNonEmpty(v, c.EvidenceRight)
		} else {
			verdicts = append(verdicts, v)
		}
		c.VerdictAlert = alert && gate == ""
	}
	if !m.cardOpen(blk, es) {
		// A fold states what it is covering while a search is up
		// (docs/interface/principles.md#fold-never-hide).
		// See docs/interface/departures.md#where-the-verdict-a-searchs-count-and-the-window-go.
		if n := m.searchMatchesIn(es, start, end); n > 0 {
			c.Matches = matchesInside(n)
		}
	}
	if start < end && start < len(es) && es[start].outOfWindow {
		// The standing fact about the step, in the footer's right-hand run,
		// which keeps its place however narrow the pane: the model no longer
		// remembers these calls firsthand (context.go).
		c.EvidenceRight = joinNonEmpty(c.EvidenceRight, outOfWindowLabel)
	}
	c.Verdict = strings.Join(verdicts, " · ")

	return c
}

// refusalReason is why a call was refused, in the words of whoever refused
// it: the reader's note, the judge's sentence, the queue's reason. Its first
// line is the refused card's footer.
func refusalReason(e entry) string {
	reason := e.denyNote
	if reason == "" {
		reason = e.denyWhy
	}
	if reason == "" && e.skipped != "" {
		reason = e.toolResult
	}
	first, _, _ := strings.Cut(strings.TrimSpace(reason), "\n")
	return first
}

// evidenceLine is a footer line as it is drawn: one run of the tool's text,
// a hunk head's indentation folded to the one space after its marker, since
// the code's own indent is a column the footer cannot show.
func evidenceLine(line string) string {
	line = strings.ReplaceAll(line, "\t", " ")
	for _, mark := range []string{"+ ", "- "} {
		if rest, ok := strings.CutPrefix(line, mark); ok {
			return mark + strings.TrimLeft(rest, " ")
		}
		if path, rest, ok := strings.Cut(line, " · "+mark); ok {
			return path + " · " + mark + strings.TrimLeft(rest, " ")
		}
	}
	return line
}

// headerSays reports whether the header already names line as a subject:
// the lead call's, or a single call's in the rollup.
func headerSays(h receipt.Header, line string) bool {
	if line == h.Subject {
		return true
	}
	for _, clause := range strings.Split(h.Rollup, " · ") {
		if _, subject, ok := strings.Cut(clause, " "); ok && subject == line {
			return true
		}
	}
	return false
}

// cardVerdict is a reading's verdict as a card states it: the summary's own
// mark and today's four words, the two that ask the reader to look in the
// accent (docs/interface/surfaces.md#the-session-summary).
func cardVerdict(s agent.SummaryState) (string, bool) {
	tone := summaryTone(s)
	alert := tone == components.SummaryOffTarget || tone == components.SummaryUnclear
	return "≡ " + components.SummaryWord(tone), alert
}

// markState is a strip cell's standing as a row's state.
func markState(s receipt.State) components.ActivityState {
	switch s {
	case receipt.StateFailed:
		return components.ActivityFailed
	case receipt.StateRefused:
		return components.ActivityDenied
	case receipt.StateRunning:
		return components.ActivityRunning
	}
	return components.ActivityDone
}

// lineChange is a write's change as an outcome: `+41`, `+88 −5`, `−3`.
func lineChange(added, removed int) string {
	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("+%d", added))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("−%d", removed))
	}
	return strings.Join(parts, " ")
}

// joinNonEmpty joins the parts that say something with the separator every
// outcome field uses.
func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// cardTakesKey reports whether the entry at idx is a card the reader is
// standing on, rather than a call inside one. A run nothing titled is kept
// on its first call, so once it is open the stop there is that call's row:
// the card has no line of its own to stand on until it is folded again.
func (m Model) cardTakesKey(es []entry, idx int) (transcriptBlock, bool) {
	blk, ok := m.cardBlockAt(es, idx)
	if !ok || (blk.step == nil && m.cardOpen(blk, es)) {
		return transcriptBlock{}, false
	}
	return blk, true
}

// toggleCardFold folds a card to its header or unfolds it, recording the
// choice on the entry the card is kept on. It is a click on the card and
// [-] on it, and with cycleCard the only thing that folds a finished card.
func (m *Model) toggleCardFold(idx int) bool {
	es := *m.entries()
	blk, ok := m.cardBlockAt(es, idx)
	if !ok {
		return false
	}
	if d, folded := m.cardShape(blk, es); folded || d == components.CardLow {
		m.unfoldCard(es, idx)
	} else {
		es[idx].stepFold = foldClosed
	}
	return true
}

// cycleCard is enter on a card: the card opens onto its calls, folds to its
// header, and comes back to the card, the three depths a row's own enter
// walks (docs/interface/surfaces.md#the-step). The artboards draw an open
// card and a folded one and leave the key between them to the binary.
// See docs/interface/departures.md#enter-walks-a-card-through-three-depths.
func (m *Model) cycleCard(idx int) bool {
	es := *m.entries()
	blk, ok := m.cardTakesKey(es, idx)
	if !ok {
		return false
	}
	switch d, folded := m.cardShape(blk, es); {
	case folded || d == components.CardLow:
		m.unfoldCard(es, idx)
	case d == components.CardHigh:
		es[idx].stepFold = foldClosed
	default:
		es[idx].stepFold = foldOpen
	}
	return true
}

// unfoldCard takes a card back from its header alone. Where the rung draws
// the card anyway, that is taking the reader's fold back rather than an
// answer of its own, and it leaves nothing on record for esc's fold to put
// back (readinghint.go); at low it is the reader's answer, and outranks the
// rung.
func (m *Model) unfoldCard(es []entry, idx int) {
	es[idx].stepFold = foldAuto
	if !m.density(verbosityNormal) {
		es[idx].stepFold = foldCard
	}
}
