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

// Below the wide breakpoint the frame draws no key bar, so the notice rail
// names the arrow while an offer is up and lets it go with the offer; wide,
// the key bar already says it and the rail does not say it twice.
func TestSuggestion_ANarrowFrameNamesTheArrowOnTheNoticeRail(t *testing.T) {
	for _, tc := range []struct {
		width  int
		onRail bool
	}{{60, true}, {80, true}, {130, false}} {
		m, _, _ := offeredModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 30})
		m = updated.(Model)
		if got := strings.Contains(m.noticeLine(), "take it"); got != tc.onRail {
			t.Fatalf("at %d columns the rail names the arrow = %v, want %v:\n%s", tc.width, got, tc.onRail, m.noticeLine())
		}
		if !strings.Contains(m.renderPromptFrame(), "take ") {
			t.Fatalf("at %d columns something should name the arrow:\n%s", tc.width, m.renderPromptFrame())
		}
		m = press(t, m, "n")
		if strings.Contains(m.noticeLine(), "take it") {
			t.Fatalf("at %d columns the note should go with the offer:\n%s", tc.width, m.noticeLine())
		}
	}
}

// Any other key drops it, filed as ignored — and a letter typed is typed.
func TestSuggestion_AnyOtherKeyDropsIt(t *testing.T) {
	m, _, signals := offeredModel(t)
	m = press(t, m, "n")
	if m.suggest.text != "" {
		t.Fatal("a keystroke should drop the offer")
	}
	if m.input.Value() != "n" {
		t.Fatalf("the letter should reach the draft, got %q", m.input.Value())
	}
	if strings.Join(*signals, ",") != observe.SuggestionIgnored {
		t.Fatalf("signals = %v, want one ignored", *signals)
	}
	// Emptied again, the draft does not bring it back.
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.input.Value() != "" || m.suggestionShown() {
		t.Fatal("a dropped offer stays dropped for that turn")
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

// A reading landing over words the person typed meanwhile is not kept, or it
// would come back behind the draft once they were cleared.
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
	if m.suggest.text != "" {
		t.Fatal("a reading landed over a non-empty draft")
	}
}

// A key pressed while the reading was out has dropped it already, even when
// the draft was emptied again before it landed.
func TestSuggestion_AKeyPressedWhileItWasOutDropsIt(t *testing.T) {
	p := &suggestProvider{line: offered}
	m, signals := suggestingModel(t, p, true)
	m, cmd := closeTurn(t, m, "hello")
	msg, ok := driveSuggestion(t, cmd)
	if !ok {
		t.Fatal("expected a reading")
	}
	m = press(t, m, "x")
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.input.Value() != "" {
		t.Fatalf("draft = %q", m.input.Value())
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.suggest.text != "" || len(*signals) != 0 {
		t.Fatalf("a reading landed after a keystroke: %q %v", m.suggest.text, *signals)
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
		taken := pressKeys(t, up, keyRight)
		none := frameModel(t, width, 40)
		return []golden.Panel{
			{Label: "an offer up · dim in the empty draft, the arrow on the key bar", View: promptSurface(up)},
			{Label: "taken · the draft's own text, cursor at its end", View: promptSurface(taken)},
			{Label: "none · the empty draft as it always was", View: promptSurface(none)},
		}
	})
}
