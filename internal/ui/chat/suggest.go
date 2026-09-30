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
//   - A keystroke of any kind drops it for that turn, and the next turn's
//     close replaces it. Each offer ends taken or ignored, and the record
//     says which.
//   - It is asked at a turn's close as a background command like the
//     title's: nothing waits for it and a failed reading offers nothing. A
//     turn that broke, was cancelled or stopped at a card is not asked for
//     one, nor is a session looking at a child.
//   - behavior.suggestions turns it off and /ui suggest flips it for the
//     session; off asks for nothing.

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	gen int
	// keysAt is when the keyboard had last been touched as the reading was
	// asked for; a reading that lands after another key is dropped.
	keysAt   time.Time
	inFlight bool
	cancel   context.CancelFunc
}

// suggestDoneMsg carries a finished reading back, with the turn and the
// generation it was asked under: either may have moved on by the time it
// lands.
type suggestDoneMsg struct {
	gen     int
	turn    int64
	verdict agent.SuggestVerdict
}

// WithSuggester wires the writer of the offered next step and whether the
// session starts with offers on. A nil writer offers nothing and asks
// nothing, which is every surface but the interactive session.
func (m Model) WithSuggester(s *agent.Suggester, on bool) Model {
	m.suggest.writer = s
	m.suggest.on = on
	return m
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
	return m.suggest.text != "" &&
		m.input.Value() == "" &&
		m.state == stateInput &&
		!m.turnInFlight() &&
		m.attachedTo == "" &&
		!m.historySearching()
}

// suggestCloseCmd asks for an offer when a turn has ended at the input,
// derived from the model before against the model after — the transition
// the title's and the summary's close readings are asked at.
func (m *Model) suggestCloseCmd(prev Model) tea.Cmd {
	// A turn that opens without a keystroke (a line another session sent)
	// ends the offer the last close left; otherwise it would come back on
	// the draft when that turn is cancelled or breaks, stale and never filed.
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
	// The offer the last close left, if the person never touched the keys
	// since, is replaced rather than kept beside this one.
	m.dropSuggestion()
	if m.suggest.cancel != nil {
		m.suggest.cancel()
	}
	m.suggest.asked = m.turnCount
	m.suggest.keysAt = m.lastKeypress
	m.suggest.gen++
	m.suggest.inFlight = true
	writer, gen, turn := m.suggest.writer, m.suggest.gen, m.turnCount
	ctx, cancel := context.WithCancel(context.Background())
	m.suggest.cancel = cancel
	return func() tea.Msg {
		defer cancel()
		return suggestDoneMsg{gen: gen, turn: turn, verdict: writer.Suggest(ctx, req)}
	}
}

// finishSuggest applies a reading. A failed one offers nothing; one that
// lands after the session moved on — another reading asked, another turn
// begun, the switch turned off — is dropped.
func (m *Model) finishSuggest(msg suggestDoneMsg) {
	if msg.gen != m.suggest.gen {
		return
	}
	m.suggest.inFlight = false
	m.suggest.cancel = nil
	if msg.verdict.Failed || msg.verdict.Suggestion == "" || !m.suggestEnabled() {
		return
	}
	if msg.turn != m.turnCount || m.turnInFlight() {
		return
	}
	// Words typed while the reading was out are the person's draft: an offer
	// landing now would reappear behind them once they were cleared.
	// And a key pressed while it was out has already done what any key does
	// to an offer, even where the draft was cleared again since.
	if m.input.Value() != "" || !m.lastKeypress.Equal(m.suggest.keysAt) {
		return
	}
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
// what every keystroke but the take does, and what a later close and the
// session boundary do to an offer nobody touched.
func (m *Model) dropSuggestion() {
	if m.suggest.text == "" {
		return
	}
	m.suggest.text = ""
	m.signal(observe.SignalSuggestion, observe.SuggestionIgnored)
}

// suggestionKey is the take key and the drop: it runs ahead of every other
// route, because any keystroke ends the offer and only one of them takes it.
// handled is true only for the take.
func (m Model) suggestionKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.suggest.text == "" {
		return m, nil, false
	}
	if m.suggestionShown() && keys.Match(msg, keys.Draft.TakeSuggestion) {
		next, cmd := m.takeSuggestion()
		return next, cmd, true
	}
	m.dropSuggestion()
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
	msgs := stripResumeContext(m.agent.Messages())
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
	if t, ok := m.changes.Turn(m.turnCount); ok && len(t.Records) > 0 {
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
			"\nusage: /ui suggest <on|off> — on, a cheap model offers a next step in the empty draft after each turn, and → takes it"
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
