package chat

// The next step, offered (docs/capabilities/chat.md#the-next-step-is-offered-not-typed).
//
// When a turn has closed, a cheap model reads what the session's other close
// readings already hold — the close row, the working steps, the last
// reading's verdict, the standing account, the last instruction — and writes
// the message the person is most likely to send next. The draft box draws it
// dim while it is empty and the session idle, and the right arrow takes it
// into the draft. The rules:
//
//   - It is never a message. Nothing is sent from it, saved with the
//     conversation or shown to the model: it reaches the model only once the
//     person has taken it and sent it, through every gate a typed line goes
//     through.
//   - A keystroke does not end it: it waits under whatever is typed, and
//     comes back when the draft is empty again. It ends when a message is
//     sent, a turn opens, a later offer replaces it or the session ends. Each
//     offer ends taken or ignored, and the record says which.
//   - It is asked at a turn's close as a background command like the
//     title's: nothing waits for it and a failed reading offers nothing. A
//     turn that broke, was cancelled or stopped at a card is not asked for
//     one, nor is a session looking at a child.
//   - behavior.suggestions turns it off and /ui suggest flips it for the
//     session; off asks for nothing after a close. The same switch holds the
//     start screen's reading (startoffers.go): one switch for the two offered
//     things. /suggest asks for one on request, switch or no switch, from the
//     same writer.

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// suggestState is the offer and its writer. The text is the one thing a
// frame reads; the rest is what keeps a reading from landing on a turn it
// was not read for.
type suggestState struct {
	writer *agent.Suggester
	// on is the session's switch (/ui suggest, behavior.suggestions).
	on bool
	// text is the offer drawn in the empty draft, or empty for none.
	text string
	// asked is the turn the last reading was asked for, so a turn is asked
	// about once however many transitions its close passes through.
	asked int64
	// gen counts readings asked for; a reading carries the one it was asked
	// under, and only the newest may land.
	gen      int
	inFlight bool
	// asking is set while a reading the person asked for with /suggest is
	// out, so the empty draft can say so.
	asking bool
	cancel context.CancelFunc
}

// suggestDoneMsg carries a finished reading back, with the turn and the
// generation it was asked under: either may have moved on by the time it
// lands.
type suggestDoneMsg struct {
	gen     int
	turn    int64
	verdict agent.SuggestVerdict
}

// suggestEnabled reports whether readings are taken at all.
func (m Model) suggestEnabled() bool {
	return m.suggest.on && m.suggest.writer.Enabled()
}

// suggestionShown reports whether the offer is drawn: there is one, the draft
// is empty, the session is idle at its own input, and nothing else is being
// typed into. It is the one question the frame, the key bar and the take key
// all ask, so the key is live exactly where the words are on the screen.
func (m Model) suggestionShown() bool {
	return m.suggest.text != "" && m.emptyIdleDraft()
}

// emptyIdleDraft is the place an offer, or the word that one is being asked
// for, is drawn.
func (m Model) emptyIdleDraft() bool {
	return m.input.Value() == "" &&
		m.state == stateInput &&
		!m.turnInFlight() &&
		m.attachedTo == "" &&
		!m.historySearching()
}

// suggestAsking reports whether the empty draft says an offer asked for with
// /suggest is on its way.
func (m Model) suggestAsking() bool {
	return m.suggest.asking && m.suggest.inFlight && m.emptyIdleDraft()
}

// suggestAskingWord is what the empty draft says while /suggest waits.
const suggestAskingWord = "asking for a next step…"

// suggestCloseCmd asks for an offer when a turn has ended at the input,
// derived from the model before against the model after — the transition
// the title's and the summary's close readings are asked at.
func (m *Model) suggestCloseCmd(prev Model) tea.Cmd {
	// A turn that opens ends the offer the last close left (a sent line ends
	// it at openTurn; this is the line another session sent, or any path that
	// opened one without it); otherwise it would come back on the draft when
	// that turn is cancelled or breaks, stale and never filed.
	if !prev.working() && m.working() {
		m.dropSuggestion()
	}
	if !prev.working() || m.working() {
		return nil
	}
	if !m.suggestEnabled() || m.suggest.asked == m.turnCount {
		return nil
	}
	// Only a turn that ran to its end and handed the screen back to the
	// input: a broken or cancelled turn has no obvious next step but the one
	// the person is already reaching for, and a turn stopped at a card or at
	// its round limit is not over. Attached, the draft is a child's.
	if m.turnOutcome != components.TurnDone || m.pausedAtRoundLimit() ||
		m.state != stateInput || m.turnInFlight() || m.attachedTo != "" {
		return nil
	}
	// A backlog run owns the input between its stages, and a plain line
	// typed there is refused, so an offer would be one the draft refuses.
	if _, held := m.todoRunHoldsInput(); held {
		return nil
	}
	req := m.suggestRequest()
	if req.Empty() {
		return nil
	}
	// The offer the last close left is replaced rather than kept beside this
	// one.
	m.dropSuggestion()
	m.suggest.asked = m.turnCount
	return m.askSuggestion(req, false)
}

// askSuggestion sends one reading, replacing any still out: only the newest
// may land. byCommand marks a reading the person asked for with /suggest.
func (m *Model) askSuggestion(req agent.SuggestRequest, byCommand bool) tea.Cmd {
	if m.suggest.cancel != nil {
		m.suggest.cancel()
	}
	m.suggest.gen++
	m.suggest.inFlight = true
	m.suggest.asking = byCommand
	writer, gen, turn := m.suggest.writer, m.suggest.gen, m.turnCount
	ctx, cancel := context.WithCancel(context.Background())
	m.suggest.cancel = cancel
	return func() tea.Msg {
		defer cancel()
		return suggestDoneMsg{gen: gen, turn: turn, verdict: writer.Suggest(ctx, req)}
	}
}

// suggestOnRequest is /suggest: it asks the writer for a next step from the
// evidence a close uses, whether or not the automatic offer is on, and the
// answer lands as the offer. The note is what the transcript says when
// nothing was asked.
func (m *Model) suggestOnRequest() (string, tea.Cmd) {
	if !m.suggest.writer.Enabled() {
		return "no model to ask — set behavior.suggestion_model", nil
	}
	req := m.suggestRequest()
	if req.Empty() {
		return "nothing to go on yet — send a message first", nil
	}
	return "", m.askSuggestion(req, true)
}

// finishSuggest applies a reading. A failed one offers nothing; one that
// lands after the session moved on — another reading asked, another turn
// begun, the switch turned off — is dropped.
func (m *Model) finishSuggest(msg suggestDoneMsg) {
	if msg.gen != m.suggest.gen {
		return
	}
	byCommand := m.suggest.asking
	m.suggest.inFlight = false
	m.suggest.asking = false
	m.suggest.cancel = nil
	if msg.verdict.Failed || msg.verdict.Suggestion == "" {
		return
	}
	// A close's reading obeys the switch as it stands now; one the person
	// asked for does not.
	if !byCommand && !m.suggestEnabled() {
		return
	}
	if msg.turn != m.turnCount || m.turnInFlight() {
		return
	}
	// Words typed while the reading was out are the person's draft, and the
	// offer waits under them until the draft is empty again.
	m.dropSuggestion()
	m.suggest.text = msg.verdict.Suggestion
}

// takeSuggestion puts the offer in the draft, cursor at its end, where it is
// the person's draft like any other: edited, sent or thrown away.
func (m Model) takeSuggestion() (tea.Model, tea.Cmd) {
	text := m.suggest.text
	m.suggest.text = ""
	m.signal(observe.SignalSuggestion, observe.SuggestionTaken)
	m.input.SetValue(text)
	m.input.MoveToEnd()
	m.syncCompletions()
	m.syncViewport()
	return m, nil
}

// dropSuggestion takes the offer off the draft, filed as ignored. It is
// what a sent message, a turn opening, a later offer and the session's end do
// to an offer nobody took.
func (m *Model) dropSuggestion() {
	if m.suggest.text == "" {
		return
	}
	m.suggest.text = ""
	m.signal(observe.SignalSuggestion, observe.SuggestionIgnored)
}

// suggestionKey is the take key: it runs ahead of every other route, because
// the take is live only where the offer is drawn and nothing else may answer
// it there. Any other key goes on to its surface and leaves the offer
// standing. handled is true only for the take.
func (m Model) suggestionKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.suggestionShown() && keys.Match(msg, keys.Draft.TakeSuggestion) {
		next, cmd := m.takeSuggestion()
		return next, cmd, true
	}
	return m, nil, false
}

// suggestRequest is the evidence an offer is read from, all of it what the
// session already holds at a turn's close: nothing here reads the transcript
// a second time or a tool result at all.
func (m Model) suggestRequest() agent.SuggestRequest {
	req := agent.SuggestRequest{
		Close:     m.suggestCloseEvidence(),
		Steps:     m.suggestStepsEvidence(),
		Account:   m.compactSummary,
		Assistant: m.lastAssistantText(),
	}
	if last := m.summary.last; last != nil && !last.Failed {
		req.Verdict = last.State.String()
		if text := strings.TrimSpace(last.Text); text != "" {
			req.Verdict += ": " + text
		}
	}
	msgs := agent.StripResumeContext(m.agent.Messages())
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleUser && !msgs[i].Machine && strings.TrimSpace(msgs[i].Content) != "" {
			req.Instruction = msgs[i].Content
			break
		}
	}
	return req
}

// suggestCloseEvidence words the last close row: how the turn ended, the
// checks it left, and the files it changed.
func (m Model) suggestCloseEvidence() string {
	var c *components.TurnClose
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if e := m.transcript[i]; e.kind == entryTurnClose && e.close != nil {
			c = e.close
			break
		}
	}
	if c == nil {
		return ""
	}
	parts := []string{"ended " + strings.ToLower(c.State.Word())}
	if ch := c.Checks; ch != nil {
		verdict := "passed"
		if ch.Failed {
			verdict = "failed"
		}
		parts = append(parts, strings.TrimSpace(fmt.Sprintf("checks %s: %s %s", verdict, ch.Label, ch.Counts)))
	}
	if t, ok := m.runNow(); ok && len(t.Records) > 0 {
		paths := make([]string, 0, len(t.Records))
		for _, r := range t.Records {
			paths = append(paths, r.Path)
		}
		parts = append(parts, "changed "+strings.Join(paths, ", "))
	} else if c.WroteNothing {
		parts = append(parts, "changed no files")
	}
	if c.Commit != nil {
		parts = append(parts, "committed")
	}
	return strings.Join(parts, "; ")
}

// suggestStepsEvidence words the working steps, marked where done.
func (m Model) suggestStepsEvidence() string {
	var b strings.Builder
	for _, s := range m.workSteps.Steps {
		mark := "[ ]"
		if m.workSteps.Done[s.Number] {
			mark = "[x]"
		}
		fmt.Fprintf(&b, "%s %d. %s\n", mark, s.Number, s.Title)
	}
	return strings.TrimSpace(b.String())
}

// resetSuggestion forgets the offer and any reading still out: a new
// conversation has had no turns for one to follow.
func (m *Model) resetSuggestion() {
	m.dropSuggestion()
	if m.suggest.cancel != nil {
		m.suggest.cancel()
	}
	m.suggest = suggestState{writer: m.suggest.writer, on: m.suggest.on, gen: m.suggest.gen + 1}
}

// suggestStatus is the /ui readout's word for the offer.
func (m Model) suggestStatus() string {
	switch {
	case !m.suggest.on:
		return "off"
	case !m.suggest.writer.Enabled():
		return "on, but this session has no model to ask — nothing is asked"
	}
	return "on (" + m.suggest.writer.Model() + ")"
}

// suggestCommand handles /ui suggest [on|off].
func (m *Model) suggestCommand(parts []string) string {
	if len(parts) == 2 {
		return "next-step suggestions: " + m.suggestStatus() +
			"\nusage: /ui suggest <on|off> — on, a cheap model offers a next step in the empty draft after each turn, and → takes it; it also writes the start screen's read-only offers when a session opens"
	}
	if len(parts) != 3 {
		return "usage: /ui suggest <on|off>"
	}
	on, ok := parseToggle(parts[2])
	if !ok {
		return failed("ui", fmt.Sprintf("unknown suggest setting %q (on, off)", parts[2]))
	}
	m.suggest.on = on
	if !on {
		// Off is no offer on the screen and none on its way.
		m.resetSuggestion()
	}
	return "next-step suggestions: " + m.suggestStatus()
}
