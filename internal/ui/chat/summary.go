package chat

// The session summary (
// docs/interface/surfaces.md#the-session-summary).
//
// The inspector rail answers every standing question about a session in
// numbers. It cannot answer the one a reader asks after looking away for five
// minutes — what is this actually doing, and is it still doing what I asked —
// and reconstructing that means scrolling the transcript, which is the work
// the rail exists to remove. So every few tool rounds a cheap model reads a
// small digest of the session and writes the two sentences the rail was
// missing, plus its judgement of whether the run is still on the instruction
// that started it.
//
// Three rules shape the mechanism, and they are the same three the scheduling
// constants below encode:
//
//   - A summary is never the reason a turn is slower. The reading is a
//     background command like the classifier's, nothing waits on it, and a
//     reading still in flight when the next one falls due is simply not
//     asked twice.
//   - A failed reading changes nothing. The last summary stands and the
//     block says it is stale. Blanking a status block because one request
//     timed out is how a reader learns not to trust it.
//   - The block is honest about its age. Every reading carries the round it
//     was taken at, and the heading states it, because a sentence about
//     "now" that is forty rounds old is worse than no sentence.
//
// The verdict is also acted on, in intervene.go: an off-target reading
// interrupts the turn at the next round boundary. Three properties of this
// file are what make that safe, and none of them may be traded away for
// convenience. The target is anchored at turn start rather than re-derived,
// so a run that drifts cannot drag its own yardstick along. The verdict is a
// closed enum rather than prose, so the policy branches on a value instead of
// on a sentence written by whatever it is judging. And the digest carries no
// tool output — which used to be a cost and privacy nicety and is now a
// prompt-injection boundary: a fetched web page that could reach the digest
// could write the instruction the agent is steered with.
// See docs/capabilities/coding-agent.md#the-verdict-is-a-steering-signal-so-the-digest-is-a-boundary.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The summary is turn-scoped, which is the one scope decision the rest of
// this file follows from. The round counter resets with every turn, so a
// reading stamped "round 24" only means anything inside the turn it was taken
// in; and a new instruction is a new target, so last turn's narrative held on
// screen while the agent works on something else would be the exact stale
// status this block exists to prevent. A finished turn's last reading does
// stand while the session is idle — that is the one you come back to the
// terminal for.
const (
	// summaryCloseMinRounds is how many rounds a turn has to have taken
	// before its close is worth a reading. A one-round answer is already on
	// screen in full; summarizing it would be the same sentence twice.
	summaryCloseMinRounds = 2
	// summaryActivityRows bounds the recent work in the digest.
	summaryActivityRows = 24
	// summaryAssistantChars bounds the last assistant message in the digest.
	summaryAssistantChars = 600
	// summaryBackoff is how many consecutive failures put the summarizer on
	// a doubled interval. A provider that is refusing should be asked less
	// often, not at the same rate for the rest of the session.
	summaryBackoff = 2
)

// summaryState is what the session knows about its own summary: the reading
// on screen, when it was taken, and whether another is in flight.
type summaryState struct {
	// last is the reading being drawn. It survives a failed refresh, which is
	// the whole reason it is kept here rather than rebuilt per frame.
	last *agent.SummaryVerdict
	// lastRound is the round last was read at, and lastAt the wall clock —
	// the two halves of the schedule, because rounds are not evenly spaced
	// in time and neither bound alone is enough.
	lastRound int
	lastAt    time.Time
	// inFlight marks a reading already asked for; a second is never sent.
	inFlight bool
	// runID is the run the in-flight reading belongs to, so a verdict that
	// arrives after the turn was cancelled is discarded rather than drawn.
	runID int
	// failures counts consecutive failed readings, for the backoff.
	failures int
	// tokensIn/tokensOut are what summaries have cost this session. /status
	// names them: a mechanism that spends the user's money in the background
	// should be able to say how much.
	tokensIn, tokensOut int64
}

// startTurn scopes the summary to the turn that is beginning: the reading on
// screen described the last instruction, and the round it was stamped with is
// about to mean something else. What survives is the accounting — spend is
// the session's, and a provider that was failing a moment ago still is.
func (s *summaryState) startTurn() {
	s.last = nil
	s.lastRound = 0
	s.lastAt = time.Time{}
}

// resetSummary starts the whole mechanism over at a session boundary, where
// startTurn is not enough: the spend is this session's, the backoff describes
// a provider that was failing this session, and a reading still out was asked
// for about a conversation that no longer exists. Its cancel goes with it, so
// the answer is never paid for twice over.
func (m *Model) resetSummary() {
	if m.summaryCancel != nil {
		m.summaryCancel()
		m.summaryCancel = nil
	}
	m.summary = summaryState{}
}

// summaryDoneMsg carries a finished reading back to the model.
type summaryDoneMsg struct {
	runID   int
	verdict agent.SummaryVerdict
}

// WithSummarizer enables the session summary. A nil summarizer, or a
// disabled one, leaves the block undrawn and no requests made.
func (m Model) WithSummarizer(s *agent.Summarizer) Model {
	m.summarizer = s
	return m
}

// summaryEnabled reports whether readings are taken at all.
func (m Model) summaryEnabled() bool { return m.summarizer.Enabled() }

// summaryInterval is the round interval in force, doubled while the
// summarizer is failing.
func (m Model) summaryInterval() int {
	n := m.summarizer.Config().Interval()
	if m.summary.failures >= summaryBackoff {
		n *= 2
	}
	return n
}

// summaryDue reports whether a reading should be taken now. It is the one
// predicate: three bounds, in the order that makes the cheapest check first.
//
// The first reading of a turn comes at agent.FirstSummaryRound rather than at
// the interval, so a long turn has a block within its first half-minute
// instead of after ten rounds of an empty rail.
func (m Model) summaryDue() bool {
	if !m.summaryEnabled() || m.summary.inFlight {
		return false
	}
	rounds := m.agent.Rounds()
	switch {
	case m.summary.last == nil:
		if rounds < agent.FirstSummaryRound {
			return false
		}
	case rounds-m.summary.lastRound < m.summaryInterval():
		return false
	}
	// The wall-clock floor is last because it is the one that catches a burst
	// of fast read-only rounds, which is the case the round interval cannot
	// see.
	return time.Since(m.summary.lastAt) >= m.summarizer.Config().Gap()
}

// summaryCmd takes a reading if one is due, and is a no-op otherwise — so
// every call site is one line and none of them has to know the schedule.
func (m *Model) summaryCmd() tea.Cmd {
	if !m.summaryDue() {
		return nil
	}
	return m.forceSummaryCmd()
}

// summaryCloseCmd is the reading a turn ends on, derived from the model
// before against the model after — the same shape the desktop notification is
// derived with, and for the same reason: a turn ending is a
// transition, not a message any one of the dozen handlers that reach it could
// be trusted to send.
//
// It ignores the interval, because the close is the reading that will sit on
// screen while nothing else moves, and a turn that finished at round 7 with a
// summary from round 3 would be describing its own middle. It does not ignore
// summaryCloseMinRounds: a turn that took one round is already legible in
// full.
func (m *Model) summaryCloseCmd(prev Model) tea.Cmd {
	if !prev.working() || m.working() {
		return nil
	}
	rounds := m.agent.Rounds()
	if rounds < summaryCloseMinRounds || rounds <= m.summary.lastRound {
		return nil
	}
	return m.forceSummaryCmd()
}

// forceSummaryCmd takes a reading now, ignoring the interval. The turn's
// close and /status are the callers: both are moments where the reading is
// about to be read rather than glanced at.
func (m *Model) forceSummaryCmd() tea.Cmd {
	if !m.summaryEnabled() || m.summary.inFlight {
		return nil
	}
	m.summary.inFlight = true
	m.summary.runID = m.agent.RunID()
	summarizer := m.summarizer
	runID := m.summary.runID
	req := m.summaryRequest()
	// Background, like the classifier's judge: nothing on screen
	// waits for it, and the turn under it is untouched either way.
	ctx, cancel := context.WithCancel(context.Background())
	m.summaryCancel = cancel
	return func() tea.Msg {
		defer cancel()
		return summaryDoneMsg{runID: runID, verdict: summarizer.Summarize(ctx, req)}
	}
}

// finishSummary applies a reading. A failed one keeps what was on screen and
// marks it stale; nothing is ever blanked.
//
// It reports whether the reading landed a transcript row, because a reading
// arrives with no stream behind it owing a repaint and the caller is the only
// thing that will draw one.
func (m *Model) finishSummary(msg summaryDoneMsg) bool {
	if !m.summary.inFlight || msg.runID != m.summary.runID {
		// A reading from a run the session has moved past. Its cost still
		// counts — it was spent — but its words are about a turn that is over.
		m.countSummarySpend(msg.verdict)
		return false
	}
	m.summary.inFlight = false
	m.countSummarySpend(msg.verdict)
	if msg.verdict.Failed {
		m.summary.failures++
		// The clock still moves on a failure, so a provider that is down is
		// retried on the interval rather than on every round.
		m.summary.lastAt = time.Now()
		return false
	}
	m.summary.failures = 0
	v := msg.verdict
	m.signal(observe.SignalSummary, observe.SummaryCode(v.State))
	m.summary.last = &v
	m.summary.lastRound = v.Round
	m.summary.lastAt = time.Now()
	// The row goes in before the verdict is considered: an off-target reading
	// queues an interruption, and the row that explains the interruption has
	// to be above it in the transcript rather than after it.
	rowed := m.appendSummaryRow(v)
	// A reading that says the run has drifted, or that it has what it needs,
	// is the one thing a summary does besides being read. It only ever queues
	// here; the round boundary delivers it (intervene.go).
	m.considerVerdict(v)
	return rowed
}

// countSummarySpend records what a reading cost against the summary's own
// running figure, which is what the /summary line quotes.
//
// It does not add to the session totals: the reading was billed at the
// provider gate as it streamed, and adding it here as well would count it
// twice. What the session spent on summaries is the ledger's answer.
func (m *Model) countSummarySpend(v agent.SummaryVerdict) {
	if v.Usage.PromptTokens == 0 && v.Usage.CompletionTokens == 0 {
		return
	}
	m.summary.tokensIn += int64(v.Usage.PromptTokens)
	m.summary.tokensOut += int64(v.Usage.CompletionTokens)
	m.notifyUsage()
}

// summaryRequest assembles the digest. Everything in it is something the
// session already knows and already shows; the only thing being paid for is
// the reading of it.
func (m Model) summaryRequest() agent.SummaryRequest {
	req := agent.SummaryRequest{
		Target:   m.summaryTarget,
		Plan:     m.summaryPlan(),
		Activity: m.summaryActivity(),
		Changes:  m.summaryChanges(),
		Alerts:   m.summaryAlerts(),
		Round:    m.agent.Rounds(),
		Elapsed:  m.turnElapsed(),
	}
	if m.summary.last != nil {
		req.Previous = m.summary.last.Text
	}
	req.Assistant = m.summaryAssistant()
	return req
}

// summaryPlan is the approved plan's steps with their states, so a reading
// judging "on target" has the declared list in front of it rather than only
// the work.
func (m Model) summaryPlan() []string {
	steps := m.planChecklist()
	if len(steps) == 0 {
		return nil
	}
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, fmt.Sprintf("[%s] %s", summaryStepState(s.State), s.Title))
	}
	return out
}

func summaryStepState(s components.PlanStepState) string {
	switch s {
	case components.PlanStepRunning:
		return "running"
	case components.PlanStepDone:
		return "done"
	case components.PlanStepFailed:
		return "failed"
	}
	return "queued"
}

// summaryActivity is the recent work as rows the observability layer would
// recognise: what was called, what it was pointed at, how it came back.
//
// It carries no tool output and no file contents, which is the digest's whole
// security posture. The summary is a steering signal, so material an outside
// party can write — a fetched page, a dependency's README, a test's stdout —
// must not be able to reach the thing that steers. The row is a tool name, an
// argument, and an outcome word from a closed set; the result text is read
// only to choose between those words and never travels.
func (m Model) summaryActivity() []string {
	var rows []string
	for _, e := range m.transcript {
		if !isActivityEntry(e) {
			continue
		}
		switch e.kind {
		case entryCommand:
			rows = append(rows, agent.SummaryActivity(
				"command", firstLine(e.text), components.OutcomeExit(e.exitCode)))
		default:
			rows = append(rows, agent.SummaryActivity(
				e.toolName, digest.Arg(e.toolName, e.toolArgs), digest.Outcome(e.toolResult)))
		}
	}
	if len(rows) > summaryActivityRows {
		rows = rows[len(rows)-summaryActivityRows:]
	}
	return rows
}

// summaryAssistant is the last thing the agent said in its own words. It is
// the one piece of free text in the digest, and the most useful part of it:
// what the agent believes it is doing is most of what a status line is.
func (m Model) summaryAssistant() string {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind != entryAssistant {
			continue
		}
		if text := strings.TrimSpace(m.transcript[i].text); text != "" {
			return truncateRunes(text, summaryAssistantChars)
		}
	}
	// Mid-round the current reply has not landed in the transcript yet, so
	// the words on screen are the freshest thing there is.
	return truncateRunes(strings.TrimSpace(m.streaming), summaryAssistantChars)
}

// summaryChanges is the session's changeset in words, so the reading can talk
// about what has been done without being told to count it.
func (m Model) summaryChanges() string {
	files, added, removed := m.changes.Totals()
	if files == 0 {
		return ""
	}
	if added == 0 && removed == 0 {
		// Nothing was counted, because the whole of what the session did to
		// these files was their permissions. Saying `+0 −0` would tell the
		// reading the session changed nothing.
		return plural(files, "file")
	}
	return fmt.Sprintf("%s · +%d −%d", plural(files, "file"), added, removed)
}

// summaryAlerts is the standing bad news the rail already keeps on screen.
func (m Model) summaryAlerts() []string {
	alerts := m.inspectorAlerts()
	if len(alerts) == 0 {
		return nil
	}
	out := make([]string, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, strings.TrimSpace(a.Label+" — "+a.Note))
	}
	return out
}

// inspectorSummary is the rail's SUMMARY block, or nil when no reading has
// landed yet — a block with nothing to say is omitted, not drawn empty.
func (m Model) inspectorSummary() *components.InspectorSummary {
	v := m.summary.last
	if v == nil || !m.summaryEnabled() {
		return nil
	}
	return &components.InspectorSummary{
		Text:   v.Text,
		State:  summaryTone(v.State),
		Reason: v.Reason,
		Round:  v.Round,
		Stale:  m.summaryStale(),
	}
}

// summaryStale reports whether the session has outrun its own summary: a
// whole extra interval of rounds has passed and no reading has replaced it.
// A refresh merely in flight is not stale — that is the ordinary state of the
// block for a second or two, and a heading that flickered every interval
// would be noise, not news.
func (m Model) summaryStale() bool {
	if m.summary.last == nil {
		return false
	}
	return m.agent.Rounds()-m.summary.lastRound > 2*m.summaryInterval()
}

// summaryTone maps the session's verdict onto the rail's own vocabulary.
func summaryTone(s agent.SummaryState) components.SummaryTone {
	switch s {
	case agent.SummaryOnTarget:
		return components.SummaryOnTarget
	case agent.SummaryOffTarget:
		return components.SummaryOffTarget
	case agent.SummarySufficient:
		return components.SummarySufficient
	}
	return components.SummaryUnclear
}

// statusCommand is `/status`: the SUMMARY block in words, for the terminals
// that have no rail to draw it in.
//
// Below 130 columns the rail is dropped, and what stands in for it is one
// row of the reading's verdict (statusrow.go) — the sentence behind that
// verdict, what it was read against and what the readings have cost are all
// still the rail's, and on a narrow terminal they have to be asked for. This
// is the same answer the rail's rules gives for PLAN — nothing is lost, it
// just has to be asked for — and asking is itself a reason to have a current
// reading, so the command forces one where the row can only display the last.
// It says which tool sources the session has for the same reason: the block
// naming them is the rail's too.
func (m *Model) statusCommand() (string, tea.Cmd) {
	text, cmd := m.summaryStatus()
	if sources := m.toolSourceStatus(); sources != "" {
		text += "\n\n" + sources
	}
	return text, cmd
}

// toolSourceStatus is the rail's TOOLS block in words: one line per source
// with the same state word the block draws, and nothing at all when the
// session has no external source to have lost.
func (m Model) toolSourceStatus() string {
	if len(m.mcp.Sources) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Tools\n")
	if n := m.builtinToolCount(); n > 0 {
		fmt.Fprintf(&sb, "built-in — %s · %s\n",
			components.ToolSourceWord(components.ToolSourceUp), plural(n, "tool"))
	}
	for _, s := range m.mcp.Sources {
		line := s.Name + " — " + components.ToolSourceWord(s.State)
		if s.Note != "" {
			line += " · " + s.Note
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// summaryStatus is the reading half of `/status`, and the half that forces a
// fresh one.
func (m *Model) summaryStatus() (string, tea.Cmd) {
	if !m.summaryEnabled() {
		if m.summarizer.Config().Disabled {
			return "The session summary is off (summary.disabled). Turn it back on in ~/.config/shhh/config.toml.", nil
		}
		return "The session summary is not configured — no model resolved for it.", nil
	}
	cmd := m.forceSummaryCmd()
	v := m.summary.last
	if v == nil {
		if cmd != nil {
			return "Reading the session now — the summary lands in a moment.", cmd
		}
		return "A reading is already in flight; the summary lands in a moment.", nil
	}

	var sb strings.Builder
	sb.WriteString(v.Text + "\n")
	fmt.Fprintf(&sb, "%s · read at round %d", v.State, v.Round)
	if behind := m.agent.Rounds() - v.Round; behind > 0 {
		sb.WriteString(" (" + agent.SummaryElapsed(behind) + ")")
	}
	sb.WriteString("\n")
	if v.Reason != "" {
		sb.WriteString(v.Reason + "\n")
	}
	if m.summaryTarget != "" {
		sb.WriteString("Read against: " + truncateRunes(firstLine(m.summaryTarget), 120) + "\n")
	}
	model := v.Model
	if model == "" {
		model = m.modelName
	}
	fmt.Fprintf(&sb, "%s · every %d rounds · %s so far",
		model, m.summaryInterval(), m.spendLabel(m.summary.tokensIn, m.summary.tokensOut))
	if m.summarizer.Config().Model == "" {
		sb.WriteString("\nSet summary.model in config to read these on a faster model than the session's.")
	}
	return sb.String(), cmd
}

// truncateRunes bounds a string on a rune boundary, marking what it took.
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimRight(string(runes[:limit]), " ") + "…"
}

// The summary row (docs/interface/surfaces.md#the-session-summary).
//
// The rail draws the latest reading bounded to three lines, which is right
// for a rail — it is 46 columns of standing status and a block that grew
// would push the counts under it off the screen. But a reading that runs past
// three lines is then a sentence nobody can finish, and the reading before it
// is gone entirely: the rail holds one, and only the current one.
//
// So every landed reading also lands in the transcript as one folded activity
// row, the way a round's reasoning does (think.go). Folded it is a line: the
// round it was taken at, its verdict, and how many lines opening it costs.
// Opened it is the whole reading, the verdict in the rail's own marks, the
// reason behind a departure, and the instruction it was judged against — the
// last of which the rail never had room for at all and only `/status` could
// answer.
//
// It is a row rather than a block for the reason everything else is: one
// grid. And it is every reading rather than the last one because the readings
// in order are the run's own account of itself — what it thought it was doing
// at round 6 and again at round 24 is a thing the transcript can now be
// scrolled for, which is exactly the reconstruction the rail exists to
// remove and could only ever do for the present moment.

// summaryReading is what an entrySummary row holds: the verdict as it landed
// and the target it was judged against.
//
// The target is copied rather than read back off the model at render time
// because it is anchored per turn (intervene.go) — the next instruction moves
// it, and a row that then claimed to have been read against an instruction
// that did not exist yet would be the one lie this row can tell.
type summaryReading struct {
	verdict agent.SummaryVerdict
	target  string
}

// summaryVerb is the row's verb, closed like every other
// (docs/interface/principles.md#closed-vocabularies).
const summaryVerb = "summary"

// summaryReasonIndent is how far a departure's reason sits under the verdict
// it qualifies.
const summaryReasonIndent = 2

// summaryReadAgainstChars bounds the instruction quoted in an opened row. It
// is `/status`'s bound, for the same reason: the row states what the reading
// was judged against, and a whole pasted brief restated under every reading
// would bury the reading.
const summaryReadAgainstChars = 120

// appendSummaryRow puts a landed reading in the transcript. It reports
// whether it did, because the caller is the only thing that will repaint the
// transcript for it — a reading arrives out of band, with no stream behind it
// owing a frame.
func (m *Model) appendSummaryRow(v agent.SummaryVerdict) bool {
	if strings.TrimSpace(v.Text) == "" {
		// A reading with no words is not a reading. The rail declines to
		// draw one for the same reason, and a row counting zero lines would
		// be a fold over nothing.
		return false
	}
	m.appendEntry(entry{kind: entrySummary, reading: &summaryReading{verdict: v, target: m.summaryTarget}})
	return true
}

// summaryRowFor builds the row for a reading at the width it will be drawn
// at, so the body re-wraps on a resize like every other entry.
func (m Model) summaryRowFor(e entry, width int) components.ActivityRow {
	r := e.reading
	tone := summaryTone(r.verdict.State)
	lines := m.summaryBodyLines(r, width)
	row := components.ActivityRow{
		Kind:    components.ActivitySummary,
		Verb:    summaryVerb,
		Target:  fmt.Sprintf("round %d", r.verdict.Round),
		Outcome: components.SummaryGlyph(tone) + " " + components.SummaryWord(tone),
		Counts:  lineCounts(len(m.summaryTextLines(r.verdict.Text, width))),
	}
	if e.expanded {
		row.Expanded = true
		// Unbounded, unlike a tool result's body: a reading is a few
		// sentences by construction, and a fold over a fold would be this
		// row hiding the thing it exists to show.
		row.Detail = lines
	} else {
		row.Keys = components.GroupExpandKey
	}
	return row
}

// summaryTextLines is the reading wrapped to the detail body's width. It is
// what the closed row counts, so the number on it is the number of lines
// opening it costs — the verdict and the instruction under them are the row's
// own furniture and are not counted, the way a diff's header is not a hunk.
func (m Model) summaryTextLines(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return strings.Split(m.wordWrap(text, max(width-components.GridDetailIndent, 1)), "\n")
}

// summaryBodyLines is the opened row: the reading, its verdict, the reason
// behind a departure, and the instruction the verdict was reached against.
//
// The verdict is repeated here in the rail's own glyph and words even though
// the folded row already carries it, because the folded row's copy is in the
// outcome column at the far right and this one sits under the sentence it
// judges. Prose is wrapped rather than clipped: a detail body clips, which is
// right for a log line and wrong for a sentence (think.go).
func (m Model) summaryBodyLines(r *summaryReading, width int) []string {
	inner := max(width-components.GridDetailIndent, 1)
	lines := m.summaryTextLines(r.verdict.Text, width)
	tone := summaryTone(r.verdict.State)
	lines = append(lines, components.SummaryGlyph(tone)+" "+components.SummaryWord(tone))
	if reason := strings.TrimSpace(r.verdict.Reason); reason != "" {
		// One indent further in, under the verdict it qualifies — the same
		// place the rail puts it, and the same place an alert's note sits
		// under its alert. Without it the reason reads as a second paragraph
		// of the reading rather than as the argument for the verdict.
		for _, line := range strings.Split(m.wordWrap(reason, max(inner-summaryReasonIndent, 1)), "\n") {
			lines = append(lines, strings.Repeat(" ", summaryReasonIndent)+line)
		}
	}
	if target := strings.TrimSpace(r.target); target != "" {
		quoted := truncateRunes(firstLine(target), summaryReadAgainstChars)
		lines = append(lines, strings.Split(m.wordWrap("read against: "+quoted, inner), "\n")...)
	}
	return lines
}
