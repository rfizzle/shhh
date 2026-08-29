package chat

// Stream resume and cheaper-model fallback (
// docs/interface/surfaces.md#the-recovery-row).
//
// Classification made a broken request legible; it still cost the whole turn.
// Two failures deserve better than starting over, and they are not the same
// failure:
//
//   - a stream that dropped *mid-reply* already has an answer on the wire.
//     The words are written and the tool calls the model finished are whole
//     (internal/provider/partial.go). Asking again from the top is asking a
//     different question, so the session offers to hand the partial turn back
//     as context and let the model carry on from its own last sentence.
//   - a request that was *never answered* — rate limited, overloaded, a
//     connection that died before a token — has nothing to keep. Waiting is
//     the whole remedy, so the session waits, on a meter, a bounded number of
//     times, and offers to finish the turn on a cheaper model from the same
//     provider rather than sit out a limit that belongs to the expensive one.
//
// The split is what keeps each path honest: the drop path never re-requests
// behind your back, and the wait path never claims to have kept something it
// does not have.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The keys a stream-drop row offers are keys.Row.Continue and keys.Row.Retry.
// They live in focus mode on the row, like every other recovery key (
// a failure row), so the input keeps both letters for typing — which is the
// whole reason a failure row's `[enter] continue from here` becomes `[c]`
// here: enter is how
// you send the message you just typed, and a row cannot have it while there
// is an input under it.
//
// keys.Wait.Fallback finishes the turn on the cheaper model while a retry
// waits. It is a bare letter, which every other recovery key refused to be —
// but the wait owns the keyboard outright (nothing is streaming and the input
// is not live), so there is no draft for it to steal a character from.

// The retry bound and its cadence. Three attempts is the bound the countdown
// states out loud: a limit you cannot see is indistinguishable from a hang.
const (
	maxRetryAttempts = 3
	// minRetryWait floors a provider that asks for an implausibly short wait;
	// maxRetryWait caps one that asks for an hour, because a wait longer than
	// this is a decision for the user, not a countdown.
	minRetryWait = time.Second
	maxRetryWait = 60 * time.Second
	// retryTickInterval is how often the meter redraws. The countdown is read
	// in seconds, so twice a second is enough to look continuous and cheap
	// enough to cost nothing.
	retryTickInterval = 500 * time.Millisecond
)

// maxDropDetail bounds the tail of the partial reply shown on the drop row.
const maxDropDetail = 2

// streamResume is what a dropped stream kept: the text the model had written
// and the tool calls it had finished. It is stored on the transcript entry,
// so the offer survives a resize, a scroll, and everything else that
// re-renders the row — and so that `[c]` acts on the partial that row is
// describing rather than on whatever the session streamed most recently.
type streamResume struct {
	text  string
	calls []provider.ToolCall
	// tokens is the estimated size of what was kept, which is the row's
	// qualifier. It is agent.EstimateTokens, the same arithmetic the context
	// accounting uses, so the row says `~` and means it.
	tokens int64
	// spent marks an offer that has been taken or overtaken. The row stays on
	// screen — it is a record of what happened — but it stops claiming keys,
	// because continuing from a partial the conversation has already moved
	// past would send the model its own reply twice.
	spent bool
}

// retryWait is one bounded wait between a failed request and the next one.
type retryWait struct {
	// fail is the failure being waited out. It is compared by pointer against
	// the transcript entry, so the row that stalled is the row whose keys are
	// suspended while the wait runs.
	fail    *provider.Failure
	attempt int
	max     int
	// wait is the whole span, deadline the moment it ends; the meter is the
	// ratio between what is left and the whole.
	wait     time.Duration
	deadline time.Time
	// fallback is the cheaper model `[m]` would finish on, empty when the
	// session has none to offer. A key that cannot be honoured is not offered.
	fallback string
	// seq fences stale ticks: a wait that was cancelled and one that was
	// restarted must not be advanced by the timer of the one before it.
	seq int
}

// retryTickMsg advances the countdown.
type retryTickMsg struct{ seq int }

// handleStreamFailure routes a failed stream to the path its failure earns.
// Every path still appends the classified failure row — this decides only
// what happens after it.
func (m Model) handleStreamFailure(msg streamErrMsg) (tea.Model, tea.Cmd) {
	f := classifyFailure(msg.err, m.providerName)
	partial := m.streaming
	calls := provider.CompletedToolCalls(msg.calls)
	// The thinking behind the calls that survived the drop survives with
	// them: continuing the partial is what re-uses them, and the request it
	// makes needs the reasoning that produced them.
	m.agent.CarryReasoning(msg.reasoning)
	// And the readable half of it is the round's think row, which stops here
	// whether it streamed or arrived whole with the failure (think.go).
	//
	// A retry leaves that row standing and opens a new one under the failure
	// row between them, because the model did think and then the wire broke.
	// The partial answer below is discarded instead, and the difference is
	// what the two are: half a sentence reads as the reply, a count of lines
	// thought is a record of what happened.
	m.recordReasoning(msg.reasoning)
	switch {
	case m.compacting || f.Class == provider.ClassCancelled:
		// A compaction that broke discards its partial summary rather than
		// offering to continue one (the conversation it was summarising is
		// untouched either way), and a stop you asked for is not a stall.
	case strings.TrimSpace(partial) != "" || len(calls) > 0:
		return m.dropStream(f, partial, calls)
	case f.Recoverable():
		return m.startRetryWait(f)
	}
	m.appendFailureRecord(f)
	return m.endBrokenTurn()
}

// dropStream records a reply that stopped halfway. Nothing is re-requested:
// the row states what was kept and offers the two ways on, because which of
// them is right depends on whether the sentence the model was in the middle
// of is worth having.
func (m Model) dropStream(f *provider.Failure, partial string, calls []provider.ToolCall) (tea.Model, tea.Cmd) {
	res := &streamResume{
		text:   partial,
		calls:  calls,
		tokens: agent.EstimateTokens(partial),
	}
	for _, tc := range calls {
		res.tokens += agent.EstimateTokens(tc.Arguments)
	}
	// The failure itself is still a row — this is a second row under it, not
	// a replacement for it, so `unclassified` and friends keep their detail
	// body and the drop keeps its offer.
	m.appendFailureRecord(f)
	m.appendEntry(entry{kind: entryStreamDrop, resume: res, duration: m.turnElapsed()})
	return m.endBrokenTurn()
}

// endBrokenTurn is the tail every failure path shares: the stream is let go,
// the turn closes as failed, and anything typed while it ran comes back to
// the input rather than disappearing with it.
func (m Model) endBrokenTurn() (tea.Model, tea.Cmd) {
	m.compacting = false
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.retry = nil
	// The close rows say the turn broke, and still report what it changed
	// before it stopped.
	m.turnOutcome = components.TurnFailed
	m.setTurnState(stateInput)
	m.restoreSteering()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.autosaveCmd()
}

// dropRow renders a stream-drop entry on the column grid, under the `stream`
// verb it shares with nothing else.
func (m Model) dropRow(e entry) components.RecoveryRow {
	res := e.resume
	if res == nil {
		return components.RecoveryRow{}
	}
	return components.RecoveryRow{
		State:     components.RecoveryStalled,
		Verb:      components.VerbStream,
		Subject:   "dropped mid-reply",
		Qualifier: res.qualifier(),
		Outcome:   "partial",
		Duration:  turnDuration(e.duration),
		Detail:    res.tail(),
		MaxDetail: maxDropDetail,
		Keys:      m.dropKeys(res),
		Note:      "the partial reply stays",
	}
}

// qualifier is what the row says was kept. It is an estimate and says so: the
// count comes from the same len/4 arithmetic as the context accounting, not
// from a provider that never got to report usage for a request it dropped.
func (r streamResume) qualifier() string {
	q := "~" + formatTokenCount(r.tokens) + " tokens kept"
	switch n := len(r.calls); {
	case n == 1:
		q += " · 1 tool call"
	case n > 1:
		q += fmt.Sprintf(" · %d tool calls", n)
	}
	return q
}

// tail is the detail body: the end of what was written, which is the part
// that decides whether continuing is worth it. The leading ellipsis is the
// row admitting it is showing you the end of something.
func (r streamResume) tail() []string {
	lines := strings.Split(strings.TrimRight(r.text, "\n"), "\n")
	var out []string
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		// A drop with no text at all still has its calls to describe, and the
		// qualifier already counted them.
		return nil
	}
	if len(out) > maxDropDetail {
		out = out[len(out)-maxDropDetail:]
	}
	out[0] = "…" + out[0]
	return out
}

// dropKeys are the two ways on. A spent offer keeps its words and loses its
// keys: the row is a record, and a record that rewrites itself is worse than
// one that stops being actionable.
func (m Model) dropKeys(res *streamResume) []components.KeyOffer {
	if res.spent {
		return nil
	}
	return []components.KeyOffer{
		{Key: keys.Bracket(keys.Row.Continue), Label: keys.Words(keys.Row.Continue)},
		{Key: keys.Bracket(keys.Row.Retry), Label: "ask again from scratch"},
	}
}

// focusedDrop returns the stream-drop row the focus cursor is on, if it is on
// one. Like every recovery row, drops live in the session's own transcript,
// so an attached child's feed never offers them.
func (m Model) focusedDrop() (entry, bool) {
	if m.attachedTo != "" || m.focusIdx < 0 || m.focusIdx >= len(m.transcript) {
		return entry{}, false
	}
	e := m.transcript[m.focusIdx]
	if e.kind != entryStreamDrop || e.resume == nil {
		return entry{}, false
	}
	return e, true
}

// dropKey routes a keystroke to the focused stream-drop row, reporting false
// when the row is not claiming it.
func (m Model) dropKey(key string) (tea.Model, tea.Cmd, bool) {
	e, ok := m.focusedDrop()
	if !ok || e.resume.spent {
		return m, nil, false
	}
	switch key {
	case keys.Shown(keys.Row.Continue):
		next, cmd := m.continueStream(e.resume)
		return next, cmd, true
	case keys.Shown(keys.Row.Retry):
		e.resume.spent = true
		m.invalidateRenderCache()
		next, cmd := m.retryTurn()
		return next, cmd, true
	}
	return m, nil, false
}

// continueStream hands the partial turn back to the model as its own. The
// conversation gains the assistant message that was interrupted — text, tool
// calls and all — so the next request continues a reply already in progress
// instead of re-asking a question that was already half answered.
func (m Model) continueStream(res *streamResume) (tea.Model, tea.Cmd) {
	if m.working() {
		return m.systemNotice("The turn is already running again.")
	}
	m.clearRetryChain()
	res.spent = true
	m.invalidateRenderCache()
	m.turnStarted, m.turnEnded = time.Now(), time.Time{}
	m.turnOpen, m.turnOutcome = true, components.TurnDone
	m.turnTokensIn, m.turnTokensOut = 0, 0
	m.vitals.startTurn()
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true

	if len(res.calls) > 0 {
		// The calls were finished before the wire broke, so continuing is
		// running them — the same round the stream was in, resumed at the
		// point it reached. BeginToolRound appends the assistant message
		// itself, which is why it is not appended above.
		auto, gated := m.agent.BeginToolRound(res.text, res.calls, m.requiresApproval)
		m.approvalTotal = len(gated)
		m.beginSpawnBatch()
		if strings.TrimSpace(res.text) != "" {
			m.appendEntry(m.stampStep(entry{kind: entryAssistant, text: res.text}))
		}
		m.trimForRequest()
		m.syncViewport()
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		if len(auto) > 0 {
			return m, m.execToolsCmd(auto)
		}
		return m.advanceApprovalQueue()
	}

	m.agent.Append(provider.Message{Role: provider.RoleAssistant, Content: res.text})
	m.appendEntry(m.stampStep(entry{kind: entryAssistant, text: res.text}))
	m.agent.Append(provider.Message{Role: provider.RoleUser, Content: continuePrompt})
	m.appendEntry(entry{kind: entrySystem, text: "Continuing from the partial reply."})
	m.trimForRequest()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, tea.Batch(m.requestStream(), m.autosaveCmd())
}

// continuePrompt is the one sentence that turns a partial assistant message
// into a resumable one. Without it the model is handed its own unfinished
// sentence and asked nothing; with it the instruction is explicit, and the
// message list stays a well-formed alternation of roles for every dialect.
const continuePrompt = "Your previous reply was cut off by a connection failure. Continue it from exactly where it stopped — do not repeat what you already wrote."

// startRetryWait puts the turn on a bounded, visible wait. Attempts count
// across the whole stall, so three rate limits in a row are three attempts
// and not three fresh chances; a request that is actually answered clears the
// count (clearRetryChain).
func (m Model) startRetryWait(f *provider.Failure) (tea.Model, tea.Cmd) {
	// The count lives on the model rather than on the wait: the wait is over
	// the moment the retry is sent, and a counter that died with it would
	// grant every attempt a fresh bound and retry forever.
	m.retryAttempt++
	attempt := m.retryAttempt
	m.appendFailureRecord(f)
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	if attempt > maxRetryAttempts {
		// The bound was reached, and the row that reports it keeps its own
		// Recovery keys: from here retrying is a decision, not a policy.
		return m.endBrokenTurn()
	}
	m.retrySeq++
	wait := retryDelay(f, attempt)
	m.retry = &retryWait{
		fail:     f,
		attempt:  attempt,
		max:      maxRetryAttempts,
		wait:     wait,
		deadline: time.Now().Add(wait),
		fallback: m.cheaperModel(),
		seq:      m.retrySeq,
	}
	m.setTurnState(stateRetryWait)
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, tea.Batch(m.retryTickCmd(), m.autosaveCmd())
}

// retryDelay is how long to wait before attempt n. A provider that named its
// own wait is believed — it knows when its window rolls over — and one that
// did not gets doubling backoff.
func retryDelay(f *provider.Failure, attempt int) time.Duration {
	if d := f.RetryAfter; d > 0 {
		return min(max(d, minRetryWait), maxRetryWait)
	}
	return min(time.Duration(1<<attempt)*time.Second, maxRetryWait)
}

// retryTickCmd schedules the next redraw of the countdown, fenced by seq.
func (m Model) retryTickCmd() tea.Cmd {
	seq := m.retrySeq
	return tea.Tick(retryTickInterval, func(time.Time) tea.Msg { return retryTickMsg{seq: seq} })
}

// retryTick advances the countdown, and fires the retry when it runs out.
func (m Model) retryTick(msg retryTickMsg) (tea.Model, tea.Cmd) {
	if m.retry == nil || msg.seq != m.retry.seq || m.turnState() != stateRetryWait {
		return m, nil
	}
	if time.Now().Before(m.retry.deadline) {
		return m, m.retryTickCmd()
	}
	return m.resumeAfterWait()
}

// clearRetryChain forgets the attempts so far. A request the provider
// actually answered ends the stall, whatever happens next — so does starting,
// retrying or continuing a turn, each of which is a decision the user made
// and not the automatic policy the bound exists to limit.
func (m *Model) clearRetryChain() { m.retryAttempt = 0 }

// resumeAfterWait asks again with the conversation exactly as the failed
// request left it. The request that broke never reached the conversation, so
// this is the same question rather than a second one — which is also why the
// turn's own accounting is not restarted here the way `[r]` restarts it.
func (m Model) resumeAfterWait() (tea.Model, tea.Cmd) {
	m.retry = nil
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.trimForRequest()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.requestStream()
}

// cancelRetryWait stops the wait. Esc is honoured at any point in it, and
// everything the turn already did — its edits, its rows, its steering — is
// exactly where it was (invariant 3).
func (m Model) cancelRetryWait() (tea.Model, tea.Cmd) {
	m.retry = nil
	m.turnOutcome = components.TurnCancelled
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.setTurnState(stateInput)
	m.restoreSteering()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.autosaveCmd()
}

// updateRetryWait owns the keyboard while the countdown drains. Two keys, and
// both of them end the wait: everything else would be a keystroke typed into
// an input that is not listening.
func (m Model) updateRetryWait(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Draft.Quit):
		m.quitting = true
		return m, m.quitCmd()
	case keys.Is(pressed, keys.Select.Cancel):
		return m.cancelRetryWait()
	case keys.Is(pressed, keys.Wait.Fallback):
		if m.retry != nil && m.retry.fallback != "" {
			return m.finishOnFallback(m.retry.fallback)
		}
	}
	return m, nil
}

// finishOnFallback switches the session's model and resumes the turn on it
// straight away — the wait was for a limit that belongs to the model being
// left behind, so waiting it out on the new one would be waiting for nothing.
func (m Model) finishOnFallback(name string) (tea.Model, tea.Cmd) {
	from := m.modelName
	if m.switchFn == nil {
		return m.systemNotice("This session cannot switch models.")
	}
	m.switchFn(name)
	m.modelName = name
	// The switch is on the record in both places a cost is read from: the
	// transcript, and the per-model spend /stats reports, so a turn
	// that finished on two models is never priced as though it finished on
	// one.
	if from != "" {
		m.vitals.noteModel(from)
	}
	m.vitals.noteModel(name)
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
		"Finishing this turn on %s — %s was %s. /stats reports what each of them cost.",
		name, from, m.retry.fail.Class)})
	return m.resumeAfterWait()
}

// cheaperModel is the model `[m]` would offer: the closest one below the
// session's own in price, from the provider's own catalog — it
// is never invented, and never a model from somewhere else, because switching
// provider mid-turn is a different decision with a different key.
//
// Closest rather than cheapest: the point is to finish the turn, and the
// least capable model in the catalog is the one least likely to.
func (m Model) cheaperModel() string {
	if m.switchFn == nil || m.prices == nil || m.modelName == "" {
		return ""
	}
	current, ok := m.modelRate(m.modelName)
	if !ok {
		// Without a price for the model in hand there is no "cheaper" to
		// point at, and a fallback the session cannot justify is one it does
		// not offer.
		return ""
	}
	best, bestRate := "", 0.0
	for _, name := range m.modelPickChoices() {
		if name == m.modelName {
			continue
		}
		rate, ok := m.modelRate(name)
		if !ok || rate >= current {
			continue
		}
		if best == "" || rate > bestRate {
			best, bestRate = name, rate
		}
	}
	return best
}

// modelRate prices a model per million tokens in and out together, which is
// the only comparison that ranks two models by what they cost to finish a
// turn on rather than by half of it.
func (m Model) modelRate(name string) (float64, bool) {
	in, out, ok := m.prices.Cost(name, 1_000_000, 1_000_000)
	if !ok {
		return 0, false
	}
	return in + out, true
}

// retryWaitBlock is the live block under the row that stalled: the draining
// meter, the attempt and the bound, and the two offers.
func (m Model) retryWaitBlock(width int) string {
	if m.retry == nil {
		return ""
	}
	r := m.retry
	left := time.Until(r.deadline)
	if left < 0 {
		left = 0
	}
	pct := 0
	if r.wait > 0 {
		pct = int(left * 100 / r.wait)
	}
	w := components.RetryWait{
		Pct:  min(max(pct, 0), 100),
		Text: "retry in " + countdownText(left),
		Note: fmt.Sprintf("attempt %d of %d", r.attempt, r.max),
	}
	if r.fallback != "" {
		w.Keys = append(w.Keys, components.KeyOffer{
			Key: keys.Bracket(keys.Wait.Fallback), Label: "finish this turn on " + r.fallback,
		})
	}
	w.Keys = append(w.Keys, components.KeyOffer{Key: "[esc]", Label: m.retryStopLabel()})
	return w.View(width)
}

// countdownText is the wait in whole seconds. FormatElapsed's tenth of a
// second is right for an elapsed time that has already happened and wrong for
// one draining twice a second — a digit that flickers is noise, not
// precision — and the seconds are rounded up so the meter never reads `0s`
// while it is still waiting.
func countdownText(d time.Duration) string {
	secs := int((d + time.Second - 1) / time.Second)
	if secs >= 60 {
		return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%ds", secs)
}

// retryStopLabel says what stopping keeps. A turn that has already changed
// files says how many, because that is the fact that makes esc safe to press.
func (m Model) retryStopLabel() string {
	if t, ok := m.changes.Turn(m.turnCount); ok && t.Files() > 0 {
		return fmt.Sprintf("stop and keep the %s", pluralFiles(t.Files()))
	}
	return "stop the turn"
}

// pluralFiles is `1 edit` / `3 edits`.
func pluralFiles(n int) string {
	if n == 1 {
		return "1 edit"
	}
	return fmt.Sprintf("%d edits", n)
}
