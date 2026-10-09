package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

// renderObserveSession prints one session: its provenance, then its events
// in order under a section per turn, so a reader can see where the rounds
// went and where the loop's safeguards spoke.
func renderObserveSession(cmd *cobra.Command, db *storage.DB, id int64, transcript bool) error {
	s, ok, err := db.AgentSession(id)
	if err != nil {
		return fmt.Errorf("query session: %w", err)
	}
	if !ok {
		return fmt.Errorf("no recorded session %d", id)
	}
	events, err := db.AgentSessionEvents(id)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	firstWrite, _, err := db.AgentSessionFirstWrite(id)
	if err != nil {
		return fmt.Errorf("query first write: %w", err)
	}
	// What each of those calls was pointed at, read out of the conversation
	// the session saved on this machine. It is the half the record does not
	// hold and must not: see observeCalls.
	calls, err := db.AgentSessionCalls(id)
	if err != nil {
		return fmt.Errorf("query calls: %w", err)
	}
	timings, err := db.AgentTimings(id)
	if err != nil {
		return fmt.Errorf("query timings: %w", err)
	}
	return report.Fprint(cmd.OutOrStdout(),
		observeWithTurnSplit(observeSessionReport(s, events, firstWrite, calls, transcript), timings))
}

// observeSessionReport builds that page. It is separate from the query so
// the whole render can be held against a fixture: every event kind this
// draws is a code some surface reports, and a surface that starts reporting
// through a different path must still land on the same page.
func observeSessionReport(s storage.AgentSessionSummary, events []storage.AgentExportEvent,
	firstWrite storage.AgentFirstWrite, calls []storage.AgentSessionCall, transcript bool) report.Report {
	pairs := []report.Pair{{Key: "started", Value: s.StartedAt.Local().Format("Jan 2 15:04")}}
	// A row that asked no model — a backlog run's driver, whose stages each
	// record their own — prints no model line, the way a setting that was not
	// in force prints none: the provider alone under "model" would read as
	// the model's name, and a blank as a write that went missing.
	if s.Model != "" {
		pairs = append(pairs, report.Pair{Key: "model", Value: joinDetail(s.Provider, s.Model)})
	}
	pairs = append(pairs, report.Pair{Key: "turns", Value: strconv.FormatInt(s.Turns, 10)})
	for _, p := range []report.Pair{
		{Key: "outcome", Value: s.Outcome},
		// The rating sits next to the outcome because that is what it is for:
		// the outcome is inferred from how the session ended, and the two
		// side by side is how anyone finds out whether the inference is any
		// good (docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference).
		// A session nobody has answered for prints no row at all, which is a
		// different fact from one somebody disliked.
		{Key: "rated", Value: observeRating(s.Rating)},
		// How much of the session went on finding a place to start. A
		// session that never wrote gets no row: the count is the looking
		// that a write ended, and there is none to report where nothing did
		// (docs/capabilities/coding-agent.md#where-a-map-would-sit).
		{Key: "first write", Value: observeFirstWriteOf(firstWrite)},
		{Key: "tokens", Value: observeTokens(s.TokensIn, s.TokensOut)},
		{Key: "cost", Value: observeCost(s.Cost)},
		{Key: "version", Value: s.Version},
		{Key: "prompt", Value: s.PromptHash},
		{Key: "project", Value: s.Project},
		{Key: "conversation", Value: s.ChatSession},
		// Where the targets on the rows below come from, said before the
		// rows are read rather than after (observeTargetsOf). A session that
		// recorded nothing gets no such row: the page has no rows for it to
		// be about.
		{Key: "targets", Value: observeTargetsOf(s, calls, transcript, len(events))},
	} {
		if p.Value != "" {
			pairs = append(pairs, p)
		}
	}
	if s.Skills > 0 {
		pairs = append(pairs, report.Pair{Key: "skills", Value: strconv.Itoa(s.Skills)})
	}
	if s.ParentID != nil {
		pairs = append(pairs, report.Pair{Key: "child of", Value: strconv.FormatInt(*s.ParentID, 10)})
	}
	pairs = append(pairs, observeChildPairs(s.Child)...)
	pairs = append(pairs, observeSettingsPairs(s.Settings)...)

	r := report.Report{
		Title:    "shhh observe session " + strconv.FormatInt(s.ID, 10),
		Subject:  joinDetail(observeKindOf(s), observeElapsed(s)),
		Sections: []report.Section{{Pairs: pairs}},
	}

	if len(events) == 0 {
		return emptyInto(r, "no events recorded for this session", "shhh observe")
	}
	found := newObserveCalls(calls)
	turn := int64(-1)
	for _, e := range events {
		// An event with no position joins the section it landed in, which
		// is the turn it arrived during. Only the ones recorded before the
		// first turn opened get a heading of their own: a gate verdict
		// takes no position at all, and a section of its own for each would
		// split the turn it arrived in two and claim the second half
		// started over. The cost is that a background run landing after the
		// last turn closed is drawn under that turn, which is where it
		// arrived rather than where it belongs.
		unplaced := e.Turn == 0 && turn > 0
		if e.Turn != turn && !unplaced {
			turn = e.Turn
			header := fmt.Sprintf("TURN %d", turn)
			if turn == 0 {
				header = "BEFORE THE FIRST TURN"
			}
			r.Sections = append(r.Sections, report.Section{Header: header})
		}
		last := &r.Sections[len(r.Sections)-1]
		last.Rows = append(last.Rows, observeEventRow(e, found.next(e), transcript))
	}
	return r
}

// observeCalls hands each tool event the call in the conversation that
// produced it.
//
// The pairing is the turn and the round on both sides and the tool's name,
// taken in order: a round that called `search` three times recorded three
// events in the order the calls were made and holds three calls in that same
// order, so the nth event of a name in a round is the nth call of that name
// in it. Matching on the name as well as the position is what keeps a round
// that read a file and searched for a word from handing either row the
// other's target.
//
// A session whose conversation is not reachable — a child, a slot pruned
// since, a build that recorded events before messages carried a position —
// pairs nothing, and every row draws exactly as it did before.
type observeCalls struct {
	at map[observeCallKey][]storage.AgentSessionCall
}

type observeCallKey struct {
	turn, round int64
	tool        string
}

func newObserveCalls(calls []storage.AgentSessionCall) *observeCalls {
	c := &observeCalls{at: make(map[observeCallKey][]storage.AgentSessionCall, len(calls))}
	for _, call := range calls {
		k := observeCallKey{turn: call.Turn, round: call.Round, tool: call.Tool}
		c.at[k] = append(c.at[k], call)
	}
	return c
}

// next is the call this event came from, and the zero call when there is
// none — which draws the row the record alone can draw. A call is handed out
// once: two events of one name in one round are two calls, and answering both
// with the first would report the second as asking a question it did not ask.
func (c *observeCalls) next(e storage.AgentExportEvent) storage.AgentSessionCall {
	if e.Kind != storage.AgentEventTool {
		return storage.AgentSessionCall{}
	}
	k := observeCallKey{turn: e.Turn, round: e.Round, tool: e.Tool}
	queue := c.at[k]
	if len(queue) == 0 {
		return storage.AgentSessionCall{}
	}
	c.at[k] = queue[1:]
	return queue[0]
}

// observeTargetsOf is the header's word on where the targets below came from,
// and it is in the header rather than in a footnote because that is where a
// reader decides how to read the page.
//
// Two boundaries meet on this page and both have to be visible at the moment
// somebody reads across them. The record is content-free and exportable; a
// target is neither, and is read here out of a conversation that lives on
// this machine. And a session whose conversation cannot be reached says so
// rather than drawing a timeline of nameless calls, which reads as a session
// that made none.
// See docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back.
func observeTargetsOf(s storage.AgentSessionSummary, calls []storage.AgentSessionCall,
	transcript bool, events int) string {
	if events == 0 {
		return ""
	}
	if s.ChatSessionID == nil || len(calls) == 0 {
		if s.ParentID != nil {
			return "none · a sub-agent's conversation is not kept"
		}
		return "none · no conversation to read them from"
	}
	if transcript {
		return "from the conversation, with results · not exported"
	}
	return "from the conversation · not exported"
}

// observeFirstWriteOf is one session's looking as the page states it, and
// nothing at all for a session that never wrote.
func observeFirstWriteOf(f storage.AgentFirstWrite) string {
	if !f.Wrote {
		return ""
	}
	return "after " + countOf(f.Searches, "search call", "search calls")
}

// classifierLine is the classifier's model with the backend it was asked
// through beside it, so two sessions on one model and different backends do
// not read as the same setting. A session recorded before the backend was
// has the model alone.
func classifierLine(c *storage.AgentSettings) string {
	if c.ClassifierModel == "" {
		return ""
	}
	return joinDetail(c.ClassifierModel, c.ClassifierBackend)
}

// observeSettingsPairs is what the session ran under, beside the provenance
// it already prints: one line per setting that was in force, and nothing at
// all for a session recorded before settings were — the page must not fill
// the gap with today's defaults, because a reader comparing two sessions
// would take the fill for a fact.
func observeSettingsPairs(c *storage.AgentSettings) []report.Pair {
	if c == nil {
		return nil
	}
	rounds := "uncapped"
	if c.MaxRounds > 0 {
		rounds = strconv.Itoa(c.MaxRounds)
	}
	summary := "off"
	if c.SummaryEnabled {
		summary = joinDetail(c.SummaryModel, fmt.Sprintf("every %d rounds", c.SummaryInterval))
	}
	var pairs []report.Pair
	for _, p := range []report.Pair{
		{Key: "mode", Value: c.Mode},
		{Key: "reasoning", Value: c.Reasoning},
		{Key: "rounds", Value: rounds},
		{Key: "check-in", Value: checkInEvery(c.CheckInInterval)},
		{Key: "summary", Value: summary},
		{Key: "classifier", Value: classifierLine(c)},
		{Key: "sandbox", Value: c.SandboxProfile},
		{Key: "item", Value: c.Item},
		{Key: "stage", Value: c.Stage},
		{Key: "config", Value: c.ConfigHash},
	} {
		if p.Value != "" {
			pairs = append(pairs, p)
		}
	}
	return pairs
}

// checkInEvery is the check-in interval as the page words it, and empty on a
// surface that never asks — a one-shot has no rounds to count, and a row
// saying so would read as a mechanism that was switched off rather than one
// that does not apply.
func checkInEvery(rounds int) string {
	if rounds <= 0 {
		return ""
	}
	return fmt.Sprintf("every %d rounds", rounds)
}

// observeChildPairs is how a sub-agent's attempt ended, for the page of a
// child's own row: the reason it stopped, the last reading of its work, the
// steers it took and which attempt it was. Nothing at all for a session that
// is not a child's, and nothing for a child still running — the row is
// closed with these.
func observeChildPairs(e *observe.ChildEnd) []report.Pair {
	if e == nil {
		return nil
	}
	pairs := []report.Pair{{Key: "ended", Value: e.Reason}}
	if e.Budget > 0 {
		pairs = append(pairs, report.Pair{Key: "budget", Value: fmt.Sprintf("%d of %d (floor %d)", e.Tokens.Fresh, e.Budget, e.AdmissionFloor)})
		pairs = append(pairs, report.Pair{Key: "tokens", Value: fmt.Sprintf("inherited %d · setup %d · tools %d · analysis %d · handoff %d", e.Tokens.Inherited, e.Tokens.Setup, e.Tokens.Tools, e.Tokens.Analysis, e.Tokens.Handoff)})
	}
	if e.Attempt > 1 {
		pairs = append(pairs, report.Pair{Key: "attempt", Value: strconv.Itoa(e.Attempt)})
	}
	if e.Verdict != "" {
		pairs = append(pairs, report.Pair{Key: "read as", Value: e.Verdict})
	}
	if e.Steers > 0 {
		pairs = append(pairs, report.Pair{Key: "steers", Value: strconv.Itoa(e.Steers)})
	}
	return pairs
}

// observeEventRow is one recorded event on the grid: what kind of thing
// happened, to what, and how it came out.
//
// A tool row carries what the call was pointed at, when the conversation it
// came from is on this machine to be read: `search · r8 · steeringItem
// ./internal/ui/chat · 57ms` rather than `search · r8 · 57ms`. That is the
// difference between twenty-seven read-only rounds and twenty-seven
// read-only rounds asking one question, and the record cannot hold it — so
// it is drawn from the transcript and never stored beside the event
// (docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back).
//
// The target is `digest.Arg`, which is the wording the activity feed and the
// summariser's reading already use. One call reads one way everywhere, or a
// row here and a row there describe the same call differently and nobody can
// tell whether the difference is the call or the renderer.
func observeEventRow(e storage.AgentExportEvent, call storage.AgentSessionCall, transcript bool) report.Row {
	at := e.CreatedAt
	if t, err := time.Parse(observeTimeLayout, e.CreatedAt); err == nil {
		at = t.Local().Format("15:04:05")
	}
	row := report.Row{State: report.Pass, Name: at, Outcome: e.Outcome}
	switch e.Kind {
	case storage.AgentEventTool:
		row.Subject = e.Tool
		row.Detail = joinDetail(digest.Arg(call.Tool, call.Args), joinDetail(e.Reason, fmtEventMs(e.DurationMs)))
		if transcript {
			row.Body = observeResult(call.Result)
		}
		if e.Outcome == "error" {
			row.State = report.Fail
		}
	case storage.AgentEventDecision:
		row.State = observeDecisionState(e.Outcome)
		row.Subject, row.Outcome = "decision", components.OutcomeBy(
			observeDecisionWord(e.Outcome), observeDecider(e.Reason))
	case storage.AgentEventSignal:
		// The gate is the one signal with a subject as well as a qualifier,
		// and the one whose qualifier is a judgement rather than a
		// description — so its row carries the suite and takes the verdict's
		// own weight instead of the neutral one every other signal draws at.
		row.State, row.Subject, row.Detail = report.Queue, e.Outcome, joinDetail(e.Tool, e.Reason)
		row.Outcome = "signal"
		if e.Outcome == "gate" {
			row.State = observeGateState(e.Reason)
		}
	case storage.AgentEventTurn:
		row.State = observeTurnState(e.Outcome)
		row.Subject = "turn"
		row.Detail = joinDetail(fmt.Sprintf("%d rounds", e.Round), fmtEventMs(e.DurationMs))
	default:
		row.Subject, row.Detail = e.Kind, e.Reason
	}
	if e.Round > 0 && e.Kind != storage.AgentEventTurn {
		row.Detail = joinDetail(fmt.Sprintf("r%d", e.Round), row.Detail)
	}
	return row
}

// observeTimeLayout is how storage stamps event times.
const observeTimeLayout = "2006-01-02T15:04:05.000Z"

// observeResultLines is how much of a call's answer a row prints. Eight is
// the activity feed's own depth for a row nobody has opened
// (docs/interface/surfaces.md#the-activity-row), and the reason is the same
// here: a timeline whose every row can print a whole file is not a timeline.
const observeResultLines = 8

// observeResult is what a call came back with, bounded the way the feed
// bounds it — the head of the output, and a line saying how much was left,
// because a result that trails off says nothing about how much was missed.
// The call itself is the row above; this is the answer beside it.
func observeResult(result string) []string {
	result = strings.TrimRight(result, "\n")
	if result == "" {
		return nil
	}
	lines := strings.Split(result, "\n")
	if len(lines) <= observeResultLines {
		return lines
	}
	return append(lines[:observeResultLines:observeResultLines],
		fmt.Sprintf("… %d more lines", len(lines)-observeResultLines))
}

func fmtEventMs(ms *int64) string {
	if ms == nil {
		return ""
	}
	return (time.Duration(*ms) * time.Millisecond).String()
}
