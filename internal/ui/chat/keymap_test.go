package chat

// The realigned keymap: the readline chords reach the textarea, the palette
// answers the chord Crush and OpenCode taught, esc esc opens rewind, ctrl+r
// searches the ring, and `?` on an empty draft prints the keys.
//
// And the liveness table at the foot: every key a decision card advertises,
// pressed through the surface's real route.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

var (
	ctrlA = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	ctrlE = tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}
	ctrlK = tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	ctrlU = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	ctrlP = tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	// The palette's chord, in the two spellings a terminal delivers it in:
	// the enhanced keyboard protocol reports the slash with a ctrl modifier,
	// and everything else sends the single byte the decoder resolves to
	// ctrl+_.
	ctrlSlash = tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}
	ctrlUnder = tea.KeyPressMsg{Code: '_', Mod: tea.ModCtrl}
	ctrlR     = tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	enter     = tea.KeyPressMsg{Code: tea.KeyEnter}
)

func TestDraft_ReadlineChordsReachTheTextarea(t *testing.T) {
	m := typeChars(t, readyModel(t), "hello world")

	m, _ = pressKey(t, m, ctrlA)
	m = typeChars(t, m, "x")
	if got := m.input.Value(); got != "xhello world" {
		t.Errorf("ctrl+a did not move to line start: %q", got)
	}

	m, _ = pressKey(t, m, ctrlA)
	m, _ = pressKey(t, m, ctrlK)
	if got := m.input.Value(); got != "" {
		t.Errorf("ctrl+a then ctrl+k did not kill to end of line: %q", got)
	}

	m = typeChars(t, m, "back again")
	m, _ = pressKey(t, m, ctrlA)
	m, _ = pressKey(t, m, ctrlE)
	m = typeChars(t, m, "!")
	if got := m.input.Value(); got != "back again!" {
		t.Errorf("ctrl+e did not move to line end: %q", got)
	}

	m, _ = pressKey(t, m, ctrlU)
	if got := m.input.Value(); got != "" {
		t.Errorf("ctrl+u did not kill to start of line: %q", got)
	}

	m = typeChars(t, m, "two words")
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if got := m.input.Value(); got != "two " {
		t.Errorf("ctrl+w did not delete the previous word: %q", got)
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt})
	m = typeChars(t, m, "x")
	if got := m.input.Value(); got != "xtwo " {
		t.Errorf("alt+b did not move back a word: %q", got)
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt})
	m = typeChars(t, m, "!")
	if got := m.input.Value(); got != "xtwo! " {
		t.Errorf("alt+f did not move forward a word: %q", got)
	}
}

// The palette answers the slash chord in both of the spellings a terminal
// can deliver it in, and the chord it used to answer belongs to the hold now.
// A person whose terminal sends neither is not stranded: the `?` list names
// the other door, and that is asserted beside the text (help_test.go).
func TestPalette_OpensOnBothSpellingsOfItsChord(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"the enhanced keyboard's", ctrlSlash},
		{"the legacy byte's", ctrlUnder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := pressKey(t, readyModel(t), tc.key)
			if m.palette == nil {
				t.Fatalf("%s spelling did not open the palette", tc.name)
			}
		})
	}

	m, _ := pressKey(t, readyModel(t), ctrlP)
	if m.palette != nil {
		t.Fatal("the old chord still opens the palette; it holds the turn now")
	}

	m2, _ := pressKey(t, readyModel(t), ctrlK)
	if m2.palette != nil {
		t.Fatal("ctrl+k still opens the palette; it belongs to the textarea now")
	}
}

func TestRewind_DoubleEscOnAnEmptyIdleDraftOpensThePicker(t *testing.T) {
	m := readyModel(t)
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}

	m, _ = pressKey(t, m, escK)
	if m.state != stateInput {
		t.Fatal("a single esc opened a surface")
	}
	m, _ = pressKey(t, m, escK)
	if m.state != statePick {
		t.Fatal("esc esc on an empty idle draft did not open the rewind picker")
	}

	before := len(m.transcript)
	m, _ = pressKey(t, m, escK)
	if m.state != stateInput {
		t.Fatal("esc did not close the picker")
	}
	if len(m.transcript) != before {
		t.Fatal("closing the picker changed the history")
	}
}

func TestRewind_TheGestureWindowExpires(t *testing.T) {
	m := readyModel(t)
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}

	m, _ = pressKey(t, m, escK)
	m.armed.deadline = time.Now().Add(-time.Millisecond)
	m, _ = pressKey(t, m, escK)
	if m.state == statePick {
		t.Fatal("a second esc after the window shut still opened the picker")
	}
}

func TestRewind_EscWithADraftClearsItFirst(t *testing.T) {
	m := typeChars(t, readyModel(t), "half a thought")
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}

	m, _ = pressKey(t, m, escK)
	if m.input.Value() != "" || m.state != stateInput {
		t.Fatal("esc with a draft must clear it and nothing else")
	}
	m, _ = pressKey(t, m, escK)
	m, _ = pressKey(t, m, escK)
	if m.state != statePick {
		t.Fatal("the two presses after the clearing one did not open the picker")
	}
}

func TestRewind_AttachedEscDetachesAndNeverOpensThePicker(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")

	m, _ = pressKey(t, m, escK)
	if m.attachedTo != "" {
		t.Fatal("esc while attached must detach a level")
	}
	m, _ = pressKey(t, m, escK)
	if m.state == statePick {
		t.Fatal("the esc that detached counted toward the rewind gesture")
	}
}

func TestRewind_NeverWhileATurnStreams(t *testing.T) {
	m := streamingCancelModel(t)
	m, _ = pressKey(t, m, escK)
	m, _ = pressKey(t, m, escK)
	if m.state == statePick {
		t.Fatal("esc esc while streaming opened rewind; rewinding is idle-only")
	}
}

func searchModel(t *testing.T, history ...string) Model {
	t.Helper()
	m := readyModel(t)
	m.inputHistory = append(m.inputHistory, history...)
	m.historyIdx = len(m.inputHistory)
	return m
}

func TestHistorySearch_TypingFiltersAndEscRestores(t *testing.T) {
	m := typeChars(t, searchModel(t, "go test", "go build"), "draft words")

	m, _ = pressKey(t, m, ctrlR)
	if !m.historySearching() {
		t.Fatal("ctrl+r did not open the history search")
	}
	m = typeChars(t, m, "bu")
	if got := m.input.Value(); got != "go build" {
		t.Fatalf("the draft does not show the match: %q", got)
	}

	m, _ = pressKey(t, m, escK)
	if m.historySearching() {
		t.Fatal("esc did not close the search")
	}
	if got := m.input.Value(); got != "draft words" {
		t.Fatalf("esc did not put the draft back: %q", got)
	}
}

func TestHistorySearch_TheChordStepsOlderAndEnterKeeps(t *testing.T) {
	m := searchModel(t, "go build one", "go test", "go build two")

	m, _ = pressKey(t, m, ctrlR)
	m = typeChars(t, m, "build")
	if got := m.input.Value(); got != "go build two" {
		t.Fatalf("the newest match goes first: %q", got)
	}
	m, _ = pressKey(t, m, ctrlR)
	if got := m.input.Value(); got != "go build one" {
		t.Fatalf("ctrl+r again did not step to the older match: %q", got)
	}
	m, _ = pressKey(t, m, ctrlR)
	if got := m.input.Value(); got != "go build one" {
		t.Fatalf("stepping past the oldest match moved somewhere: %q", got)
	}

	m, _ = pressKey(t, m, enter)
	if m.historySearching() {
		t.Fatal("enter did not close the search")
	}
	if got := m.input.Value(); got != "go build one" {
		t.Fatalf("enter did not keep the match in the draft: %q", got)
	}
}

func TestHistorySearch_BackspaceEditsTheQuery(t *testing.T) {
	m := searchModel(t, "go test", "go build")
	m, _ = pressKey(t, m, ctrlR)
	m = typeChars(t, m, "bux")
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := m.input.Value(); got != "go build" {
		t.Fatalf("deleting the typo did not bring the match back: %q", got)
	}
}

func TestHistorySearch_AKeyItDoesNotAnswerHandsTheKeyboardBack(t *testing.T) {
	m := searchModel(t, "go build")
	m, _ = pressKey(t, m, ctrlR)
	m = typeChars(t, m, "bui")
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.historySearching() {
		t.Fatal("a key the search has no meaning for did not close it")
	}
	if got := m.input.Value(); got != "go build" {
		t.Fatalf("leaving the search dropped the match: %q", got)
	}
}

func TestHistorySearch_ClosesWhenADecisionTakesTheKeyboard(t *testing.T) {
	m := gatedModel(t, func(string, json.RawMessage) (string, error) { return "", nil },
		map[string]GatedPreviewFunc{"write_file": writeFilePreview("old\n")})
	m.inputHistory = []string{"go build"}
	m.historyIdx = len(m.inputHistory)
	m, _ = pressKey(t, m, ctrlR)
	m = typeChars(t, m, "zzz") // no match, so the box stays empty
	if !m.historySearching() {
		t.Fatal("fixture: the search did not open")
	}

	// The keyboard has been quiet and the box is empty, so the arriving
	// card holds the keyboard (interrupt.go) — and the search must not sit
	// invisibly on top of it, filtering the card's answer keys.
	m.lastKeypress = time.Now().Add(-2 * graceQuiet)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "c1", Name: "write_file", Arguments: `{"path":"a.go","content":"new\n"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("fixture: the gated call did not open the card, state %d", m.state)
	}

	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.historySearching() {
		t.Fatal("the search stayed open under a card that holds the keyboard")
	}
	if m.state == stateConfirmRun && m.pendingApproval != nil {
		t.Fatal("the card's answer key filtered an invisible query instead of answering")
	}
}

func TestDraft_FreedChordsStayTextKeysUnderALiveSupervisor(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m = typeChars(t, m, "ab")
	m, _ = pressKey(t, m, ctrlA)
	m = typeChars(t, m, "x")
	if got := m.input.Value(); got != "xab" {
		t.Errorf("with a supervisor wired, ctrl+a stopped being line start: %q", got)
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	if m.agentList == nil {
		t.Error("the manager chord did not open the agent manager")
	}
}

func TestHistorySearch_AnEmptyRingSaysSo(t *testing.T) {
	m, _ := pressKey(t, readyModel(t), ctrlR)
	if m.historySearching() {
		t.Fatal("the search opened over nothing to search")
	}
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "No input history") {
		t.Errorf("the refusal did not say why:\n%s", view)
	}
}

func TestKeyList_QuestionMarkOnAnEmptyDraftPrintsTheKeys(t *testing.T) {
	m, _ := pressKey(t, readyModel(t), tea.KeyPressMsg{Code: '?', Text: "?"})
	if got := m.input.Value(); got != "" {
		t.Fatalf("? on an empty draft landed in the draft: %q", got)
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "Keys:") || !strings.Contains(view, "reading mode") {
		t.Errorf("? did not print the key section as a system row:\n%s", view)
	}
}

func TestKeysNotice_RidesTheNoticeRailWhenDue(t *testing.T) {
	m := frameModel(t, 130, 40).WithKeysNotice(KeysChangedNotice())
	line := stripANSI(m.noticeLine())
	if !strings.Contains(line, "keys changed:") {
		t.Errorf("the rebind notice is not on the rail: %q", line)
	}
	for _, want := range []string{"queue", "agents", "pointer", "palette", "reading"} {
		if !strings.Contains(line, want) {
			t.Errorf("the notice does not name the %s rebind: %q", want, line)
		}
	}

	if line := stripANSI(frameModel(t, 130, 40).noticeLine()); strings.Contains(line, "keys changed:") {
		t.Errorf("the notice shows without being due: %q", line)
	}
}

// --- every key a decision card offers is live ---

// decisionCard is one row of the liveness table: a card the session can be
// showing, and a fixture holding it the way a reader who answered the
// handover holds it. What the row does not carry is the keys — those are read
// back off the card the fixture built, so the walk cannot fall behind a card
// that started offering one more.
type decisionCard struct {
	name string
	open func(*testing.T) Model
	card func(Model) *components.ApprovalCard
	// scrolls says this fixture's body is longer than the panel, so the
	// counted tail's chord is one of the keys the card is offering.
	scrolls bool
}

// decisionCards is the table. A decision card is the one surface a bare
// letter can reach without reading mode or a takeover in front of it, which
// makes it the door the inert-keys register cannot see: that register asks
// whether a key is refused while the draft is live, and this one asks the
// other half — whether the key the card printed does anything at all once it
// is not.
func decisionCards(t *testing.T) []decisionCard {
	t.Helper()
	sessionCard := func(m Model) *components.ApprovalCard { return m.approvalCard() }
	routedCard := func(m Model) *components.ApprovalCard { return m.childAskCard(m.activeChildAsk()) }
	routed := func(build func(*testing.T) *subagent.Ask) func(*testing.T) Model {
		return func(t *testing.T) Model { return routedModel(t, build(t)) }
	}
	return []decisionCard{
		{
			name: "the session's edit card",
			open: func(t *testing.T) Model { return handover(t, interruptedModel(t, "")) },
			card: sessionCard,
		},
		{
			name: "the session's command card",
			open: func(t *testing.T) Model {
				var bare, contained []string
				return runExecApproval(t, containedModel(t, &bare, &contained, "contained: bwrap (workspace profile)"))
			},
			card: sessionCard,
		},
		{
			// The same card for a command that can be asked what it would do
			// rather than told to do it, which is the one state the dry-run
			// key is drawn in.
			name: "the session's command card with a dry run",
			open: func(t *testing.T) Model {
				var bare, contained []string
				m := containedModel(t, &bare, &contained, "contained: bwrap (workspace profile)")
				return execApproval(t, m, "rsync --delete src/ dst/")
			},
			card: sessionCard,
		},
		{
			// And the same card in a session that can be asked what the
			// command does, which is the one state the explain key is drawn
			// in: the offer is made only where a model is configured to
			// answer it.
			name: "the session's command card with an explanation",
			open: func(t *testing.T) Model {
				m := explainerModel(t, &paragraphProvider{text: "it copies src/ into dst/."},
					agent.ExplainConfig{Model: "small", Prompt: explainWording})
				return execApproval(t, m, "rsync -a --delete src/ dst/")
			},
			card: sessionCard,
		},
		{
			// The same card with a queue behind it, which is the one state
			// the queue key is drawn in: it opens the stack as the list that
			// answers it, and a key that is only offered where there is a
			// queue can only be walked where there is one.
			name: "the session's command card with a queue behind it",
			open: func(t *testing.T) Model {
				var ran []string
				m := execModel(t, &ran)
				updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
					execCall("c1", "echo one"),
					execCall("c2", "echo two"),
				}})
				return handover(t, updated.(Model))
			},
			card: sessionCard,
			// The strip above the card takes rows the card would have had,
			// so this fixture's body is one row longer than its panel.
			scrolls: true,
		},
		{
			name: "a child's routed command",
			open: routed(func(*testing.T) *subagent.Ask {
				ask := subagent.NewAsk("writer-1", subagent.AskCommand, "run make")
				ask.Command = "make"
				return ask
			}),
			card: routedCard,
		},
		{
			name: "a child's routed edit",
			open: routed(func(t *testing.T) *subagent.Ask {
				ask := subagent.NewAsk("writer-1", subagent.AskEdit, "edit internal/agent/loop.go")
				ask.Path, ask.Root, ask.Worktree = "internal/agent/loop.go", t.TempDir(), true
				ask.Hunks = diff.Compute("a\nb\nc\n", "a\nB\nc\n")
				return ask
			}),
			card: routedCard,
		},
		{
			name: "a child's routed patch",
			open: routed(func(t *testing.T) *subagent.Ask { return longPatchAsk(t.TempDir()) }),
			card: routedCard,
			// The body this table exists for: forty hunks behind a counted
			// tail, whose chord was printed and routed nowhere.
			scrolls: true,
		},
		{
			name: "a child's routed patch, answered from the manager",
			open: func(t *testing.T) Model {
				m := routedModel(t, longPatchAsk(t.TempDir()))
				m.answerAgent = m.activeChildAsk().Agent
				opened, _ := m.openAgentList()
				return opened.(Model)
			},
			card: func(m Model) *components.ApprovalCard {
				return m.listAnswerCard(m.listAnswerAsk())
			},
			scrolls: true,
		},
		{
			name: "a child's routed tool",
			open: routed(func(*testing.T) *subagent.Ask {
				ask := subagent.NewAsk("researcher-1", subagent.AskGeneric, "use web_fetch")
				ask.Summary = "https://example.com/docs"
				return ask
			}),
			card: routedCard,
		},
	}
}

// cardKeys is everything a card is advertising, in one list: the decision run
// it draws, the qualifier [d] rides beside it, the offers after it, and —
// where the body does not fit — the chord the counted tail names. It is read
// off the card rather than declared beside it, so a key added to a card joins
// this walk by construction.
//
// [d] is added by hand because it is the one answer the run leaves out: the
// card draws it as a qualifier on the key line rather than inside the
// brackets, so KeyRun, which is what a click resolves against, has never
// carried it.
func cardKeys(m Model, card *components.ApprovalCard) []string {
	var out []string
	for _, k := range card.KeyRun() {
		out = append(out, k.Key)
	}
	if card.FullDiff {
		out = append(out, keys.Shown(keys.Decision.Diff))
	}
	for _, o := range card.ExtraHints {
		out = append(out, strings.Trim(o.Key, "[]"))
	}
	if maxBody, _ := card.ScrollBounds(m.contentWidth()); maxBody > 0 {
		out = append(out, keys.Shown(keys.Decision.ScrollDown))
	}
	return out
}

// cardState is everything a key on a decision card could reasonably move: the
// session behind it, the card's own scroll, and the screen. A key that
// changes none of the three did nothing.
func cardState(m Model) string {
	return snapshot(m) + fmt.Sprintf(" scroll=%d/%d\n", m.cardScroll, m.cardPan) + m.View().Content
}

// TestDecisionCards_EveryOfferedKeyDoesSomething is the other half of the
// inert-keys register. That one presses every key a surface offers while the
// draft holds the keyboard and requires each to be a letter; this one presses
// the same keys once the card holds it and requires each to be a key.
//
// An offer nothing routes is the failure it catches, and it is invisible
// without it: [d] rendered on a card whose host never answered
// ApprovalFullDiff, or a counted tail naming a chord that reached no scroll,
// both look exactly like a working card until they are pressed.
func TestDecisionCards_EveryOfferedKeyDoesSomething(t *testing.T) {
	tails := 0
	for _, tc := range decisionCards(t) {
		if tc.scrolls {
			tails++
		}
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(t)
			card := tc.card(m)
			// A fixture that was built to overflow and does not would walk
			// the chord out of the list rather than fail on it, which is the
			// one key here nothing else would notice missing.
			if maxBody, _ := card.ScrollBounds(m.contentWidth()); (maxBody > 0) != tc.scrolls {
				t.Fatalf("the fixture's body outgrows its panel by %d rows, want scrolls=%v", maxBody, tc.scrolls)
			}
			offered := cardKeys(m, card)
			if len(offered) == 0 {
				t.Fatal("a decision card with no keys is not one")
			}
			for _, k := range offered {
				m := tc.open(t)
				before := cardState(m)
				next := pressSpelling(t, m, k)
				if after := cardState(next); after == before {
					t.Fatalf("%q is offered on the card and changed nothing:\n%s", k, before)
				}
			}
		})
	}
	if tails == 0 {
		t.Fatal("no fixture's body outgrew its panel, so the counted tail's chord went unpressed")
	}
}

// pressSpelling sends the keystroke a register spelling stands for. It is
// press() plus the chords, because the keys a card offers are no longer all
// bare letters.
func pressSpelling(t *testing.T, m Model, spelling string) Model {
	t.Helper()
	var msg tea.KeyPressMsg
	rest := spelling
	for {
		prefix, mod, ok := modPrefix(rest)
		if !ok {
			break
		}
		msg.Mod |= mod
		rest = strings.TrimPrefix(rest, prefix)
	}
	switch rest {
	case "up", "↑":
		msg.Code = tea.KeyUp
	case "down", "↓":
		msg.Code = tea.KeyDown
	case "left", "←":
		msg.Code = tea.KeyLeft
	case "right", "→":
		msg.Code = tea.KeyRight
	case "enter":
		msg.Code = tea.KeyEnter
	case "esc":
		msg.Code = tea.KeyEscape
	case "space":
		msg.Code = tea.KeySpace
	default:
		r := []rune(rest)
		if len(r) != 1 {
			t.Fatalf("no keystroke for the spelling %q", spelling)
		}
		msg.Code = r[0]
		if msg.Mod == 0 {
			msg.Text = rest
		}
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func modPrefix(s string) (prefix string, mod tea.KeyMod, ok bool) {
	for _, p := range []struct {
		text string
		mod  tea.KeyMod
	}{{"ctrl+", tea.ModCtrl}, {"alt+", tea.ModAlt}, {"shift+", tea.ModShift}} {
		if strings.HasPrefix(s, p.text) {
			return p.text, p.mod, true
		}
	}
	return "", 0, false
}
