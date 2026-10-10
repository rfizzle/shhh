package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// suggestProvider answers every suggestion request with a scripted line, and
// keeps what it was asked.
type suggestProvider struct {
	line  string
	calls int
	msgs  []provider.Message
	opts  provider.CompletionOpts
}

func (p *suggestProvider) StreamCompletion(_ context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.calls++
	p.msgs, p.opts = msgs, opts
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Token: p.line, Usage: &provider.Usage{PromptTokens: 120, CompletionTokens: 8}, Done: true}
	close(ch)
	return ch, nil
}

func (p *suggestProvider) Name() string { return "suggestions" }

const offered = "Run the full test suite now."

var keyRight = tea.KeyPressMsg{Code: tea.KeyRight}

// suggestingModel is a session with a suggester on p, recording every signal
// it files.
func suggestingModel(t *testing.T, p provider.Provider, on bool) (Model, *[]string) {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	var signals []string
	m := New(msgs, multiTokenStream("hi there"), Wiring{
		Suggester:   agent.NewSuggester(p, agent.SuggestConfig{Model: "fast"}),
		Suggestions: on,
		Observer: observe.Observer{Signal: func(_ observe.Pos, code, reason string) {
			if code == observe.SignalSuggestion {
				signals = append(signals, reason)
			}
		}},
	})
	// Wide enough for the frame's key bar, which is where the arrow is
	// offered.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 30})
	return updated.(Model), &signals
}

// driveSuggestion runs a close's commands and returns the suggestion reading
// among them, or false when none was asked for.
func driveSuggestion(t *testing.T, cmd tea.Cmd) (suggestDoneMsg, bool) {
	t.Helper()
	if cmd == nil {
		return suggestDoneMsg{}, false
	}
	for _, c := range unwrapBatch(cmd) {
		if c == nil {
			continue
		}
		if msg, ok := c().(suggestDoneMsg); ok {
			return msg, true
		}
	}
	return suggestDoneMsg{}, false
}

// offeredModel is a session that has closed one turn and landed the offer.
func offeredModel(t *testing.T) (Model, *suggestProvider, *[]string) {
	t.Helper()
	p := &suggestProvider{line: offered}
	m, signals := suggestingModel(t, p, true)
	m, cmd := closeTurn(t, m, "add the retry backoff")
	msg, ok := driveSuggestion(t, cmd)
	if !ok {
		t.Fatal("a turn that closed at the input should ask for a next step")
	}
	updated, _ := m.Update(msg)
	return updated.(Model), p, signals
}

// A closed turn asks once, on the evidence the session holds, and the answer
// is drawn in the empty draft with the key bar offering the arrow.
func TestSuggestion_AClosedTurnOffersANextStep(t *testing.T) {
	m, p, _ := offeredModel(t)
	if p.calls != 1 {
		t.Fatalf("calls = %d, want one per closed turn", p.calls)
	}
	if p.opts.Model != "fast" || p.opts.ToolChoice != provider.ToolChoiceNone {
		t.Errorf("opts = %+v", p.opts)
	}
	if !strings.Contains(p.msgs[1].Content, "add the retry backoff") || !strings.Contains(p.msgs[1].Content, "ended done") {
		t.Errorf("the evidence should carry the instruction and the close: %s", p.msgs[1].Content)
	}
	if !m.suggestionShown() {
		t.Fatal("the offer should be drawn on the idle, empty draft")
	}
	frame := m.renderPromptFrame()
	if !strings.Contains(frame, offered) {
		t.Fatalf("the frame should draw the offer:\n%s", frame)
	}
	if !strings.Contains(frame, "take the suggestion") {
		t.Fatalf("the key bar should offer the arrow while one is up:\n%s", frame)
	}
	if m.input.Value() != "" {
		t.Fatalf("an offer is never the draft's until taken, got %q", m.input.Value())
	}
	// Nothing the model can read has the offer in it.
	for _, msg := range m.agent.Messages() {
		if strings.Contains(msg.Content, offered) {
			t.Fatalf("the offer reached the conversation: %+v", msg)
		}
	}
}

// → takes it: the draft holds the words, the cursor stands at their end, the
// record files a take, and nothing is sent.
func TestSuggestion_TheArrowTakesItIntoTheDraft(t *testing.T) {
	m, _, signals := offeredModel(t)
	sent := len(m.agent.Messages())
	m = pressKeys(t, m, keyRight)
	if m.input.Value() != offered {
		t.Fatalf("draft = %q, want the offer", m.input.Value())
	}
	if col := m.input.Column(); col != len([]rune(offered)) {
		t.Fatalf("cursor column = %d, want the end (%d)", col, len([]rune(offered)))
	}
	if m.suggest.text != "" || m.suggestionShown() {
		t.Fatal("a taken offer is no longer drawn")
	}
	if strings.Join(*signals, ",") != observe.SuggestionTaken {
		t.Fatalf("signals = %v, want one take", *signals)
	}
	if len(m.agent.Messages()) != sent || m.working() {
		t.Fatal("taking an offer sends nothing")
	}
	if strings.Contains(m.renderPromptFrame(), "take the suggestion") {
		t.Fatal("the key bar offers the arrow only while an offer is up")
	}
}

// The arrow leads the foot row at every width and goes with the offer, in
// the short words below the wide layout so the floor keeps its room; the
// notice rail does not say it a second time.
func TestSuggestion_TheArrowLeadsTheFootRowAtEveryWidth(t *testing.T) {
	for _, tc := range []struct {
		width int
		lead  string
	}{{60, "[→] take it · [enter] send"}, {80, "[→] take it · [enter] send"}, {130, "[→] take the suggestion · [enter] send"}} {
		m, _, _ := offeredModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 30})
		m = updated.(Model)
		if strings.Contains(m.noticeLine(), "take ") {
			t.Fatalf("at %d columns the notice rail says the arrow again:\n%s", tc.width, m.noticeLine())
		}
		if got := stripANSI(m.renderPromptFrame()); !strings.Contains(got, tc.lead) {
			t.Fatalf("at %d columns the foot row should lead with %q:\n%s", tc.width, tc.lead, got)
		}
		m = press(t, m, "n")
		if got := stripANSI(m.renderPromptFrame()); strings.Contains(got, "[→]") {
			t.Fatalf("at %d columns the arrow should go with the offer:\n%s", tc.width, got)
		}
	}
}

// A key does not end the offer: it waits under the typed letter, and comes
// back when the draft is erased.
func TestSuggest_ErasingTheDraftBringsTheOfferBack(t *testing.T) {
	m, _, signals := offeredModel(t)
	m = press(t, m, "n")
	if m.input.Value() != "n" {
		t.Fatalf("the letter should reach the draft, got %q", m.input.Value())
	}
	if m.suggestionShown() || m.suggest.text == "" {
		t.Fatal("the offer stands under the draft and is not drawn over it")
	}
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if !m.suggestionShown() || !strings.Contains(m.renderPromptFrame(), offered) {
		t.Fatal("an erased draft should bring the offer back")
	}
	if len(*signals) != 0 {
		t.Fatalf("signals = %v, nothing has ended the offer", *signals)
	}
	m = pressKeys(t, m, keyRight)
	if m.input.Value() != offered || strings.Join(*signals, ",") != observe.SuggestionTaken {
		t.Fatalf("the arrow should take it: %q %v", m.input.Value(), *signals)
	}
}

// A sent message ends the offer, filed as ignored.
func TestSuggest_SendingEndsTheOffer(t *testing.T) {
	m, _, signals := offeredModel(t)
	m = press(t, m, "n")
	m = sendText(t, m, "next thing")
	if m.suggest.text != "" || strings.Join(*signals, ",") != observe.SuggestionIgnored {
		t.Fatalf("a send should end the offer as ignored: %q %v", m.suggest.text, *signals)
	}
}

// Keys, a turn and the session's end file one ignored between them.
func TestSuggest_IgnoredIsFiledOnceAtTheEnd(t *testing.T) {
	m, _, signals := offeredModel(t)
	m = press(t, m, "n")
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if len(*signals) != 0 {
		t.Fatalf("keys filed %v", *signals)
	}
	ended := m
	m = sendText(t, m, "x")
	_ = sendText(t, m, "y")
	if strings.Join(*signals, ",") != observe.SuggestionIgnored {
		t.Fatalf("signals = %v, want one ignored", *signals)
	}
	// The session's end files it too, for an offer still standing.
	ended.quitCmd()
	if strings.Join(*signals, ",") != observe.SuggestionIgnored+","+observe.SuggestionIgnored {
		t.Fatalf("the end should file the standing offer: %v", *signals)
	}
}

// A palette opened and closed, or a half-typed command erased, brings the
// offer back; a sent command keeps it.
func TestSuggest_AScreenKeepsTheOffer(t *testing.T) {
	m, _, signals := offeredModel(t)
	m = press(t, m, "/")
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.input.Value() != "" || !m.suggestionShown() {
		t.Fatalf("a half-typed command erased should bring the offer back (draft %q)", m.input.Value())
	}
	m, _ = submit(t, m, "/help")
	if m.suggest.text == "" || len(*signals) != 0 {
		t.Fatalf("a sent command keeps the offer: %q %v", m.suggest.text, *signals)
	}
}

// /suggest asks the writer, switch or no switch, and the answer is the offer.
func TestSuggest_TheCommandAsksForAnOffer(t *testing.T) {
	p := &suggestProvider{line: offered}
	m, signals := suggestingModel(t, p, false)
	m, _ = closeTurn(t, m, "add the retry backoff")
	if p.calls != 0 {
		t.Fatal("an off switch asks for nothing after a close")
	}
	m, cmd := submit(t, m, "/suggest")
	msg, ok := driveSuggestion(t, cmd)
	if !ok {
		t.Fatal("/suggest should ask the writer")
	}
	if got := stripANSI(m.renderPromptFrame()); !strings.Contains(got, suggestAskingWord) {
		t.Fatalf("the draft should say it is asking:\n%s", got)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if !m.suggestionShown() || !strings.Contains(m.renderPromptFrame(), offered) {
		t.Fatal("the answer should stand in the empty draft")
	}
	// A second ask replaces the first and files the old one once.
	m, cmd = submit(t, m, "/suggestion")
	msg, _ = driveSuggestion(t, cmd)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if !m.suggestionShown() {
		t.Fatal("the second answer should stand in the draft")
	}
	if strings.Join(*signals, ",") != observe.SuggestionIgnored {
		t.Fatalf("signals = %v", *signals)
	}
}

func TestSuggest_TheCommandNeedsAWriter(t *testing.T) {
	m, _ := suggestingModel(t, &suggestProvider{line: offered}, false)
	m.suggest.writer = nil
	m, _ = closeTurn(t, m, "hello")
	m, cmd := submit(t, m, "/suggest")
	if _, asked := driveSuggestion(t, cmd); asked {
		t.Fatal("nothing should be asked")
	}
	if got := lastSystemText(m); !strings.Contains(got, "no model to ask — set behavior.suggestion_model") {
		t.Fatalf("the note is missing: %q", got)
	}
}

// One offer from a command counts like one from a close.
func TestSuggest_TheRecordCountsOneOfferOnce(t *testing.T) {
	p := &suggestProvider{line: offered}
	m, signals := suggestingModel(t, p, false)
	m, _ = closeTurn(t, m, "hello")
	m, cmd := submit(t, m, "/suggest")
	msg, _ := driveSuggestion(t, cmd)
	updated, _ := m.Update(msg)
	m = pressKeys(t, updated.(Model), keyRight)
	if m.input.Value() != offered {
		t.Fatalf("draft = %q", m.input.Value())
	}
	if strings.Join(*signals, ",") != observe.SuggestionTaken {
		t.Fatalf("signals = %v, want one take", *signals)
	}
}

// With text in the draft the arrow is the line editor's: it moves the
// cursor and takes nothing.
func TestSuggestion_TheArrowOnADraftWithTextIsTheEditors(t *testing.T) {
	m, _, _ := offeredModel(t)
	m.input.SetValue("ab")
	m.input.SetCursorColumn(0)
	if m.suggestionShown() {
		t.Fatal("the offer is drawn only on an empty draft")
	}
	m = pressKeys(t, m, keyRight)
	if m.input.Value() != "ab" || m.input.Column() != 1 {
		t.Fatalf("the arrow should move the cursor: value %q column %d", m.input.Value(), m.input.Column())
	}
}

// The next turn's close replaces an offer nobody touched, filing the old one
// as ignored.
func TestSuggestion_ANewCloseReplacesIt(t *testing.T) {
	m, _, signals := offeredModel(t)
	// A turn that starts without a keystroke — a line another session sent.
	m.suggest.asked = 0
	prev := m
	prev.setTurnState(stateStreaming)
	if cmd := m.suggestCloseCmd(prev); cmd == nil {
		t.Fatal("a close should ask again")
	}
	if m.suggest.text != "" || strings.Join(*signals, ",") != observe.SuggestionIgnored {
		t.Fatalf("the old offer should be dropped as ignored: %q %v", m.suggest.text, *signals)
	}
}

// A turn opened without a keystroke ends the standing offer, so a cancel of
// that turn does not bring a stale offer back onto the draft.
func TestSuggestion_ATurnOpenedWithoutAKeyDropsIt(t *testing.T) {
	m, _, signals := offeredModel(t)
	prev := m
	m.setTurnState(stateStreaming)
	if cmd := m.suggestCloseCmd(prev); cmd != nil {
		t.Fatal("a turn opening asks for nothing")
	}
	if m.suggest.text != "" || strings.Join(*signals, ",") != observe.SuggestionIgnored {
		t.Fatalf("the offer should be dropped as ignored: %q %v", m.suggest.text, *signals)
	}
}

// A reading landing over words the person typed meanwhile waits under them
// and is drawn once they are cleared.
func TestSuggestion_ALateReadingDoesNotLandOverTypedWords(t *testing.T) {
	p := &suggestProvider{line: offered}
	m, _ := suggestingModel(t, p, true)
	m, cmd := closeTurn(t, m, "hello")
	msg, ok := driveSuggestion(t, cmd)
	if !ok {
		t.Fatal("expected a reading")
	}
	m.input.SetValue("fix")
	updated, _ := m.Update(msg)
	m = updated.(Model)
	m.input.SetValue("")
	if !m.suggestionShown() {
		t.Fatal("a reading that landed over words should be drawn once they are cleared")
	}
}

// Off asks for nothing at all, and /ui suggest flips it for the session.
func TestSuggestion_OffMakesNoRequest(t *testing.T) {
	p := &suggestProvider{line: offered}
	m, _ := suggestingModel(t, p, false)
	m, cmd := closeTurn(t, m, "hello")
	if _, ok := driveSuggestion(t, cmd); ok || p.calls != 0 {
		t.Fatalf("an off session asked: calls=%d", p.calls)
	}
	if out := m.uiCommand([]string{"/ui", "suggest", "on"}); !strings.Contains(out, "on (fast)") {
		t.Fatalf("/ui suggest on = %q", out)
	}
	_, cmd = closeTurn(t, m, "again")
	if _, ok := driveSuggestion(t, cmd); !ok {
		t.Fatal("turned on for the session, the next close should ask")
	}
	if out := m.uiCommand([]string{"/ui"}); !strings.Contains(out, "next-step suggestions: on") {
		t.Fatalf("the /ui readout should name the switch: %q", out)
	}
}

// Turning it off drops what is up and discards what is out.
func TestSuggestion_TurningItOffDropsTheOffer(t *testing.T) {
	m, _, signals := offeredModel(t)
	m.uiCommand([]string{"/ui", "suggest", "off"})
	if m.suggest.text != "" || m.suggestionShown() {
		t.Fatal("off leaves no offer drawn")
	}
	if strings.Join(*signals, ",") != observe.SuggestionIgnored {
		t.Fatalf("signals = %v", *signals)
	}
}

// A cancelled turn, a turn stopped at a card and a session looking at a
// child are not asked for a next step.
func TestSuggestion_NotAskedOnACancelOrACardOrAChild(t *testing.T) {
	p := &suggestProvider{line: offered}
	base, _ := suggestingModel(t, p, true)
	running := sendText(t, base, "do the work")

	cancelled := running
	cancelled.cancelStreaming()
	if cmd := cancelled.suggestCloseCmd(running); cmd != nil {
		t.Fatal("a cancelled turn asked for a next step")
	}

	carded := running
	carded.setTurnState(stateInput)
	carded.state = stateConfirmRun
	if cmd := carded.suggestCloseCmd(running); cmd != nil {
		t.Fatal("a turn stopped at a card asked for a next step")
	}

	attached := running
	attached.setTurnState(stateInput)
	attached.attachedTo = "researcher"
	if cmd := attached.suggestCloseCmd(running); cmd != nil {
		t.Fatal("a session looking at a child asked for a next step")
	}
	if p.calls != 0 {
		t.Fatalf("calls = %d", p.calls)
	}
}

// A reading that lands after another turn has begun is about a turn that is
// over, and is not drawn.
func TestSuggestion_AStaleReadingIsDropped(t *testing.T) {
	p := &suggestProvider{line: offered}
	m, _ := suggestingModel(t, p, true)
	m, cmd := closeTurn(t, m, "hello")
	msg, ok := driveSuggestion(t, cmd)
	if !ok {
		t.Fatal("expected a reading")
	}
	m = sendText(t, m, "next")
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.suggest.text != "" {
		t.Fatal("a reading for a turn that is over was kept")
	}
}

// The key the offer is taken with is the register's, so a keymap file that
// moves it moves the take.
func TestSuggestion_TheTakeIsTheRegistersKey(t *testing.T) {
	if got := keys.Draft.TakeSuggestion.Keys(); len(got) != 1 || got[0] != "right" {
		t.Fatalf("draft.take_suggestion = %v, want right", got)
	}
}

// TestGolden_Suggestion captures the draft box in the three states an offer
// gives it: drawn dim in the empty box with the arrow on the key bar, taken
// into the draft as text the reader owns, and none at all.
func TestGolden_Suggestion(t *testing.T) {
	captureBoundedGolden(t, "suggestion", "the offered next step", goldenWidths, func(width int) []golden.Panel {
		up := frameModel(t, width, 40)
		up.suggest.text = "Run the full test suite, then commit the retry backoff if it passes."
		// Each panel is drawn as soon as its state is reached: the draft's
		// buffer is shared between the copies of a model.
		offerUp := promptSurface(up)
		typed := press(t, up, "a")
		typedView := promptSurface(typed)
		erased := pressKeys(t, typed, tea.KeyPressMsg{Code: tea.KeyBackspace})
		erasedView := promptSurface(erased)
		taken := pressKeys(t, erased, keyRight)
		takenView := promptSurface(taken)
		none := frameModel(t, width, 40)
		return []golden.Panel{
			{Label: "an offer up · dim in the empty draft, the arrow on the key bar", View: offerUp},
			{Label: "typed over · the offer waits under the letter", View: typedView},
			{Label: "erased · the offer back in the empty draft", View: erasedView},
			{Label: "taken · the draft's own text, cursor at its end", View: takenView},
			{Label: "none · the empty draft as it always was", View: promptSurface(none)},
		}
	})
}
