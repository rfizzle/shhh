package chat

// The fifth invariant across every keyed surface (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The mono precedent: a rule stated in the design system becomes a test that
// enforces it everywhere rather than a paragraph each surface is trusted to
// have read. The register below is the audit — every surface in the product
// that offers a bare single-character key, and which of the two positions it
// is in — and the tests walk it in both states.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// draftLead is what the reader is half way through typing when the surface
// under test appears. Every key the register names is pressed into it, and
// the letter has to land at the end of it and nowhere else.
const draftLead = "also add a --max-rounds "

// keyedSurface is one row of the register: a surface that offers bare
// single-character keys, the keys it offers, and a session in which the
// surface does not hold the keyboard.
//
// `open` returns that session. For a decision that arrives unbidden it is the
// surface on screen and ungated, because that is the state a reader meets it
// in. For a transcript row it is the row in the transcript with the
// draft below live. For a takeover it is the session with the surface not
// opened, because opening one is what gives it the keyboard — there is no
// state in which a takeover is on screen without it.
type keyedSurface struct {
	name string
	keys []string
	open func(t *testing.T) Model
	// hold gives the surface the keyboard, or nil where the surface is a
	// takeover and holds it by construction.
	hold func(t *testing.T, m Model) Model
	// answer says the handover chord gives the surface the keyboard once the
	// pointer has selected it: a recovery or round-limit row, reached the way
	// a waiting decision is. A takeover holds the keyboard by construction,
	// and a turn's close answers the chord with its commit card instead.
	answer bool
	// row is the kind of transcript entry the surface is, for reading back
	// what it drew. Zero on the surfaces that are not rows.
	row entryKind
}

// register is the enumeration
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard
// keeps in words. A surface added to the product with bare letters on it
// belongs here, and the tests below are what stop it shipping ungated.
func register(t *testing.T) []keyedSurface {
	t.Helper()
	return []keyedSurface{
		{
			name: "approval card",
			// The shifted pair is on this list for the reason the rest of it
			// is: a capital is a bare key too, and "Y" is how a sentence
			// about YAML starts.
			keys: []string{"y", "n", "Y", "N", "a", "d", "A"},
			open: func(t *testing.T) Model { return interruptedModel(t, draftLead) },
			hold: handover,
		},
		{
			// The command card's own key, which the edit card above has no
			// state for: a command that can be asked what it would do.
			name: "the command card's dry run",
			keys: []string{"t"},
			open: func(t *testing.T) Model {
				var bare, contained []string
				m := containedModel(t, &bare, &contained, "contained: bwrap")
				return pendingExec(t, typeChars(t, m, draftLead), "rsync --delete src/ dst/")
			},
			hold: handover,
		},
		{
			// The command card's other question about the command, in a
			// session with a model configured to answer it.
			name: "the command card's explanation",
			keys: []string{"x"},
			open: func(t *testing.T) Model {
				m := explainerModel(t, &paragraphProvider{text: "it copies src/ into dst/."},
					agent.ExplainConfig{Model: "small", Prompt: explainWording})
				return pendingExec(t, typeChars(t, m, draftLead), "rsync -a --delete src/ dst/")
			},
			hold: handover,
		},
		{
			name: "plan card",
			// [j] leads because the answering test presses the first key,
			// and moving the choice is the one plan-card key that neither
			// writes a file nor ends the card.
			keys: []string{"j", "k", "s", "S", "i"},
			open: func(t *testing.T) Model {
				m := planModel(t, mockStream)
				updated, _ := m.Update(doneMsg{})
				m = updated.(Model)
				if m.state != statePlanApprove {
					t.Fatalf("the fixture should be sitting on the plan card, state %v", m.state)
				}
				return typeChars(t, m, draftLead)
			},
			hold: handover,
		},
		{
			name: "a child agent's routed approval",
			keys: []string{"y", "n", "a", "g"},
			open: func(t *testing.T) Model {
				sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
				t.Cleanup(sup.Close)
				m := newSubagentModel(t, sup)
				m.childAsks = []*subagent.Ask{subagent.NewAsk("writer-1", subagent.AskCommand, "run make")}
				return typeChars(t, m, draftLead)
			},
			hold: handover,
		},
		{
			name: "the changeset row a turn closes with",
			// Every one of these is on the list as the letter it now only
			// is: review is the row's own open, and undo, commit and
			// running the checks again are commands, not keys it offers.
			keys: []string{"u", "g", "t", "v"},
			open: func(t *testing.T) Model {
				m, _ := undoModel(t)
				return typeChars(t, m, draftLead)
			},
			hold: readingCursorOn(entryTurnClose),
			row:  entryTurnClose,
		},
		{
			name: "a provider failure's row",
			keys: []string{"e", "p", "r", "c"},
			open: func(t *testing.T) Model {
				m := failureModel(t)
				updated, _ := m.Update(streamErrMsg{err: authFailure()})
				return typeChars(t, updated.(Model), draftLead)
			},
			hold:   readingCursorOn(entryFailure),
			answer: true,
			row:    entryFailure,
		},
		{
			name: "a dropped stream's row",
			keys: []string{"c", "r"},
			open: func(t *testing.T) Model {
				m := streamed(resumeModel(t), "so I'll thread the sentinel through runRound and then")
				updated, _ := m.Update(streamErrMsg{err: networkFailure()})
				return typeChars(t, updated.(Model), draftLead)
			},
			hold:   readingCursorOn(entryStreamDrop),
			answer: true,
			row:    entryStreamDrop,
		},
		{
			name: "a round-limit pause's row",
			keys: []string{keys.Shown(keys.Row.Rounds), keys.Shown(keys.Row.Uncap), "u", "v"},
			open: func(t *testing.T) Model {
				m, _ := pausedModel(t)
				return typeChars(t, m, draftLead)
			},
			hold:   readingCursorOn(entryRoundPause),
			answer: true,
			row:    entryRoundPause,
		},
		{
			name: "reading mode's own keys and its per-row offers",
			keys: []string{"j", "k", "q", "-", "/", "n", "N"},
			open: func(t *testing.T) Model { return typeChars(t, focusModel(t), draftLead) },
		},
		{
			name: "review mode",
			keys: []string{"s", "A", "n", "p"},
			open: func(t *testing.T) Model {
				m, _ := reviewModel(t)
				return typeChars(t, m, draftLead)
			},
		},
		{
			name: "the undo confirm",
			keys: []string{"y", "n", "f"},
			open: func(t *testing.T) Model {
				m, _ := undoModel(t)
				return typeChars(t, m, draftLead)
			},
		},
		{
			name: "the context pressure card",
			keys: []string{"n"},
			open: func(t *testing.T) Model { return typeChars(t, pressureModel(t, 110), draftLead) },
		},
		{
			name: "the selector family",
			keys: []string{"1", "2", "j", "k"},
			open: func(t *testing.T) Model { return typeChars(t, readyModel(t), draftLead) },
		},
		{
			name: "the agent list",
			keys: []string{"x", "X", "j", "k"},
			open: func(t *testing.T) Model {
				sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
				t.Cleanup(sup.Close)
				return typeChars(t, newSubagentModel(t, sup), draftLead)
			},
		},
	}
}

// authFailure is the classified 401 the failure row is built from.
func authFailure() *provider.Failure {
	return &provider.Failure{
		Class: provider.ClassAuth, Status: 401, Provider: "openai",
		Message: "Incorrect API key provided", KeyTail: "4f9c",
	}
}

// readingCursorOn hands a transcript row the keyboard the way a reader does:
// ctrl+o opens reading mode, then the cursor is put on the row of that kind.
// This is the only way a transcript row's keys ever go live.
func readingCursorOn(kind entryKind) func(*testing.T, Model) Model {
	return func(t *testing.T, m Model) Model {
		t.Helper()
		next, _ := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
		rm, ok := next.(Model)
		if !ok || rm.state != stateFocus {
			t.Fatalf("ctrl+o should open reading mode, state %v", rm.state)
		}
		for _, idx := range rm.expandableIndices() {
			if rm.transcript[idx].kind == kind {
				rm.focusIdx = idx
				rm.refreshFocusView()
				return rm
			}
		}
		t.Fatalf("no selectable row of kind %v to put the cursor on", kind)
		return rm
	}
}

// TestInertKeys_ABareLetterReachesNoSurfaceWithoutTheKeyboard is the audit's
// own assertion. Every key in the register is pressed into a
// half-typed sentence while the surface offering it does not hold the
// keyboard, and every one of them has to be a letter: it lands at the end of
// the draft, and the session is otherwise exactly where it was.
//
// The alternative — routing `y` to whichever surface happens to be on screen
// — is what made a sentence containing the word "yes" able to approve a shell
// command, and there is no reason that hazard is special to
// approvals.
func TestInertKeys_ABareLetterReachesNoSurfaceWithoutTheKeyboard(t *testing.T) {
	for _, s := range register(t) {
		t.Run(s.name, func(t *testing.T) {
			for _, key := range s.keys {
				m := s.open(t)
				if got := m.input.Value(); got != draftLead {
					t.Fatalf("the fixture should start with the draft intact, got %q", got)
				}
				before := snapshot(m)
				next := press(t, m, key)
				if got, want := next.input.Value(), draftLead+key; got != want {
					t.Fatalf("%q should have gone into the sentence: draft is %q, want %q", key, got, want)
				}
				if after := snapshot(next); after != before {
					t.Fatalf("%q changed the session behind the draft:\n before %s\n after  %s", key, before, after)
				}
			}
		})
	}
}

// TestInertKeys_TheSurfaceAnswersOnceItHoldsTheKeyboard is the other half:
// the same key, on the same surface, once the keyboard has been handed over.
// A rule that only ever refuses would be indistinguishable from a broken key.
func TestInertKeys_TheSurfaceAnswersOnceItHoldsTheKeyboard(t *testing.T) {
	for _, s := range register(t) {
		if s.hold == nil {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			m := s.hold(t, s.open(t))
			next := press(t, m, s.keys[0])
			if next.input.Value() != draftLead {
				t.Fatalf("%q reached the draft after the handover: %q", s.keys[0], next.input.Value())
			}
		})
	}
}

// answerMsg is the handover chord as the decoder reports one: ctrl+y, the
// spelling every terminal delivers.
func answerMsg(t *testing.T) tea.KeyPressMsg {
	t.Helper()
	msg := tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}
	if !keys.Match(msg, keys.Draft.Answer) {
		t.Fatalf("%q is not the handover", msg.String())
	}
	return msg
}

// pressAnswer presses the handover chord.
func pressAnswer(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(answerMsg(t))
	return updated.(Model)
}

// pointAt lights the pointer on the first row of a kind, the way shift+up
// does from the prompt: the draft keeps the keyboard and the row is selected.
func pointAt(t *testing.T, m Model, kind entryKind) Model {
	t.Helper()
	m.pointer.lit = true
	m.focusIdx = indexOfKind(t, m, kind)
	m.refreshCursorView()
	if !m.pointerLit() {
		t.Fatalf("the pointer should be lit on the %v row", kind)
	}
	return m
}

// TestRowHandover_TheSelectedRowTakesTheKeyboardAndItsLettersAct is the
// story the row offers tell together with the test above. That one presses
// the letter and watches it land in the sentence, which is what a letter
// beside a live draft has to do. This one selects the row with the pointer,
// presses the handover — the chord a waiting decision is answered through —
// and watches the row take the keyboard with the sentence still in the box;
// then the same letter acts, and esc gives the keyboard back
// (docs/interface/surfaces.md#the-recovery-row).
//
// While the draft holds the keyboard, what the row draws beside its grey
// letters is the handover itself, because that is the key that reaches it.
func TestRowHandover_TheSelectedRowTakesTheKeyboardAndItsLettersAct(t *testing.T) {
	for _, s := range register(t) {
		if !s.answer {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			m := pointAt(t, s.open(t), s.row)
			at := m.focusIdx
			drawn := ansi.Strip(m.renderEntryKeys(m.transcript[at], 110, rowPointed))
			if !strings.Contains(drawn, keys.Bracket(keys.Draft.Answer)) {
				t.Fatalf("the selected row does not name the handover that reaches it:\n%s", drawn)
			}

			held := pressAnswer(t, m)
			if held.state != stateFocus || held.focusIdx != at {
				t.Fatalf("the handover did not give the row the keyboard: state %v, cursor %d want %d",
					held.state, held.focusIdx, at)
			}
			if got := held.input.Value(); got != draftLead {
				t.Fatalf("the handover touched the sentence: %q", got)
			}

			before := snapshot(held)
			next := press(t, held, s.keys[0])
			if got := next.input.Value(); got != draftLead {
				t.Fatalf("%q reached the draft once the row held the keyboard: %q", s.keys[0], got)
			}
			if after := snapshot(next); after == before {
				t.Fatalf("%q did nothing on the row that holds the keyboard\n %s", s.keys[0], before)
			}

			back, _ := pressKey(t, held, tea.KeyPressMsg{Code: tea.KeyEscape})
			if back.state == stateFocus || !back.inputLive() {
				t.Fatalf("esc did not give the keyboard back: state %v", back.state)
			}
			if got := back.input.Value(); got != draftLead {
				t.Fatalf("giving the keyboard back lost the sentence: %q", got)
			}
		})
	}
}

// TestRowHandover_ASelectedRowThatOffersNothingKeepsIt is the other half of
// acting on the selected row: the handover is not passed on to a newer row
// that does make offers. Here the pointer stands on a tool row, which offers
// nothing, with a turn's close under it.
func TestRowHandover_ASelectedRowThatOffersNothingKeepsIt(t *testing.T) {
	m, _ := undoModel(t)
	m = typeChars(t, m, draftLead)
	idxs := m.expandableIndices()
	at := -1
	for _, i := range idxs {
		if m.transcript[i].kind != entryTurnClose {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("the fixture should have a row other than the close to point at")
	}
	m.pointer.lit, m.focusIdx = true, at
	m.refreshCursorView()
	before := snapshot(m)
	if after := snapshot(pressAnswer(t, m)); after != before {
		t.Fatalf("the handover fell through to a row the pointer was not on:\n before %s\n after  %s",
			before, after)
	}
}

// TestRowHandover_AClickOnAnOfferIsItsLetter is clickKey's rule on a row
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard):
// while the draft holds the keyboard the only live key on the row is the
// handover, and a click there is the chord; once the row holds the keyboard
// a click on an offer is that offer's letter, through the row's own dispatch.
// A click on a grey offer is nothing.
func TestRowHandover_AClickOnAnOfferIsItsLetter(t *testing.T) {
	m := failureModel(t)
	updated, _ := m.Update(streamErrMsg{err: authFailure()})
	m = typeChars(t, updated.(Model), draftLead)
	m = pointAt(t, m, entryFailure)
	at := m.focusIdx

	key := keys.Bracket(keys.Row.Key)
	if next, _, ok := m.clickRowOffer(pointOn(t, m, key)); ok && snapshot(next.(Model)) != snapshot(m) {
		t.Fatalf("a click on a grey offer acted: %s", snapshot(next.(Model)))
	}

	next, _, ok := m.clickRowOffer(pointOn(t, m, keys.Bracket(keys.Draft.Answer)))
	held := next.(Model)
	if !ok || held.state != stateFocus || held.focusIdx != at {
		t.Fatalf("a click on the handover did not give the row the keyboard: state %v", held.state)
	}

	next, _, ok = held.clickRowOffer(pointOn(t, held, key))
	if !ok || next.(Model).state != stateKeyEntry {
		t.Fatalf("a click on %s did not do what the letter does: state %v", key, next.(Model).state)
	}
	if got := next.(Model).input.Value(); got != draftLead {
		t.Fatalf("the click touched the sentence: %q", got)
	}
}

// pointOn is the pane cell of the first drawn line carrying mark, at the
// mark's first column.
func pointOn(t *testing.T, m Model, mark string) selPoint {
	t.Helper()
	for i, line := range m.viewport.lines {
		plain := ansi.Strip(line)
		if j := strings.Index(plain, mark); j >= 0 {
			return selPoint{line: i, col: ansi.StringWidth(plain[:j]) + 1}
		}
	}
	t.Fatalf("no drawn line carries %q", mark)
	return selPoint{}
}

// TestInertKeys_EveryTakeoverHoldsTheKeyboardExclusively is why most of the
// register passes by construction. A takeover surface is a state, the state
// is routed before the input sees a key, and the input is not live while one
// is up — so its letters are live because nothing else is listening, which is
// the first of the register's two positions.
func TestInertKeys_EveryTakeoverHoldsTheKeyboardExclusively(t *testing.T) {
	takeovers := []struct {
		name string
		s    state
	}{
		{"reading mode", stateFocus},
		{"the full-screen diff", stateDiffFull},
		{"review mode", stateReview},
		{"the selector family", statePick},
		{"the model picker", stateModelList},
		{"the undo confirm", stateUndoConfirm},
		{"the key entry", stateKeyEntry},
		{"the context pressure card", statePressure},
	}
	for _, tc := range takeovers {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.s.isSurface() {
				t.Fatal("a surface offering bare letters has to be a surface, or the input keeps them")
			}
			m := readyModel(t)
			m.state = tc.s
			if m.inputLive() {
				t.Fatal("the draft cannot be live while a takeover is up: both would answer the same letter")
			}
		})
	}
	// The agent manager is a surface without being a state — it borrows the
	// bottom panel — so it makes the same claim its own way.
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	opened, _ := m.openAgentList()
	if opened.(Model).agentList == nil {
		t.Fatal("the agent list should be up")
	}
	if press(t, opened.(Model), "x").input.Value() != "" {
		t.Fatal("the open agent list has the keyboard, so x is its own")
	}
}

// TestInertKeys_ARowDrawsTheKeyThatIsLiveWhereItStands is invariant 1 applied
// to the state of a key. A turn's close offers its keys only once it is
// selected, and it leads with what enter does on it, which is its turn's
// review, then the handover that opens its commit card. Both are chords —
// neither is a letter of a sentence — so the pointer and the cursor draw the
// same two. Unselected, no close offers anything, the newest included: the
// same keys on every turn a session has closed told the reader nothing about
// which turn they would act on.
func TestInertKeys_ARowDrawsTheKeyThatIsLiveWhereItStands(t *testing.T) {
	m, _ := undoModel(t)
	e := m.transcript[indexOfKind(t, m, entryTurnClose)]
	commit := keys.Bracket(keys.Draft.Answer) + " " + commitWords

	plain := ansi.Strip(m.renderEntryKeys(e, 110, rowUnselected))
	for _, never := range []string{"review turn", commit, "alt+", "to use them"} {
		if strings.Contains(plain, never) {
			t.Fatalf("an unselected close offers nothing, found %q in:\n%s", never, plain)
		}
	}

	pointed := ansi.Strip(m.renderEntryKeys(e, 110, rowPointed))
	held := readingCursorOn(entryTurnClose)(t, m)
	live := ansi.Strip(held.renderEntryKeys(e, 110, rowUnderCursor))
	for _, drawn := range []string{pointed, live} {
		for _, want := range []string{"[enter] " + reviewTurnWords, commit} {
			if !strings.Contains(drawn, want) {
				t.Fatalf("the selected row offers %q, got:\n%s", want, drawn)
			}
		}
		if strings.Contains(drawn, "to use them") || strings.Contains(drawn, "[u]") {
			t.Fatalf("the selected row waits on nothing and offers no undo key:\n%s", drawn)
		}
	}
}

// TestInertKeys_AnUnselectedRecoveryRowDrawsItsLettersWaiting is the recovery
// rows' half. A failure's offers are how the reader gets out of it, so the
// row keeps saying what they are when nothing selects it — but grey, beside
// the key that hands the keyboard over: the handover itself on the row it
// reaches, and reading mode's key on a row it does not.
func TestInertKeys_AnUnselectedRecoveryRowDrawsItsLettersWaiting(t *testing.T) {
	m := failureModel(t)
	updated, _ := m.Update(streamErrMsg{err: authFailure()})
	m = updated.(Model)
	e := m.transcript[indexOfKind(t, m, entryFailure)]

	plain := ansi.Strip(m.renderEntryKeys(e, 110, rowUnselected))
	pointed := ansi.Strip(m.renderEntryKeys(e, 110, rowPointed))
	want := keys.Bracket(keys.Row.Key) + " enter a new key"
	if !strings.Contains(plain, want) || !strings.Contains(pointed, want) {
		t.Fatalf("both states name the offer, want %q in:\n%s\n%s", want, plain, pointed)
	}
	unselected := keys.Draft.Reading
	if m.isLatestRecovery(e) {
		unselected = keys.Draft.Answer
	}
	if !strings.Contains(plain, keys.Shown(unselected)+"] to use them") {
		t.Fatalf("an unselected row names the key that makes its offers live:\n%s", plain)
	}
	if !strings.Contains(pointed, keys.Shown(keys.Draft.Answer)+"] to use them") {
		t.Fatalf("the selected row names the handover that reaches it:\n%s", pointed)
	}
}

// TestInertKeys_WaitingAndLiveNeverPaintAlike holds the same rule in both
// palettes. The two states of a row's keys are never the same run of
// characters, and the difference is in the words rather than in a shade a
// monochrome terminal loses: the handover beside the letters where the draft
// has the keyboard, the letters alone under the cursor.
func TestInertKeys_WaitingAndLiveNeverPaintAlike(t *testing.T) {
	for _, mono := range []bool{false, true} {
		label := "color"
		if mono {
			label = "mono"
		}
		t.Run(label, func(t *testing.T) {
			was := components.Mono()
			components.SetMono(mono)
			t.Cleanup(func() { components.SetMono(was) })

			m := failureModel(t)
			updated, _ := m.Update(streamErrMsg{err: authFailure()})
			m = updated.(Model)
			e := m.transcript[indexOfKind(t, m, entryFailure)]
			waiting := m.renderEntryKeys(e, 110, rowPointed)
			live := m.renderEntryKeys(e, 110, rowUnderCursor)
			if waiting == live {
				t.Fatal("the two states of a row's keys have to be told apart")
			}
			// The spelling carries it whatever the palette does, which is
			// the point of not leaving it to the colour (invariant 1).
			if ansi.Strip(waiting) == ansi.Strip(live) {
				t.Fatalf("the two states differ in the key each offers:\n%s", ansi.Strip(waiting))
			}
		})
	}
}

// snapshot is everything about a session that a bare letter must not move:
// which surface is up, how much transcript there is, what is waiting for an
// answer, and where the reading cursor is. The draft itself is checked
// separately, because that is the one thing the letter is allowed to change.
func snapshot(m Model) string {
	return fmt.Sprintf("state=%v entries=%d waiting=%d gated=%v focus=%d agents=%v attached=%q",
		m.state, len(m.transcript), m.waitingCount(), m.decisionGated(),
		m.focusIdx, m.agentList != nil, m.attachedTo)
}

// TestRowHandover_WithNothingSelectedOnlyTheLastFailureAnswers holds the one
// exception to the handover reaching only the selected row to its target
// (docs/interface/surfaces.md#the-recovery-row): the newest recovery row, and
// only while the session's last turn ended on it. An older failure above it
// is not reached, and once the session has moved past the failure — a turn
// that ended any other way — the handover reaches nothing.
func TestRowHandover_WithNothingSelectedOnlyTheLastFailureAnswers(t *testing.T) {
	failure := func() *provider.Failure {
		return &provider.Failure{Class: provider.ClassUnclassified, Status: 400, Message: "no"}
	}
	build := func(outcome components.TurnState) (Model, *provider.Failure, *provider.Failure) {
		m := frameModel(t, 110, 40)
		m.turnOutcome = outcome
		older, newer := failure(), failure()
		m.appendEntry(entry{kind: entryUser, text: "rename the sentinel"})
		m.appendEntry(entry{kind: entryFailure, fail: older})
		m.appendEntry(entry{kind: entryFailure, fail: newer})
		m.invalidateRenderCache()
		return m, older, newer
	}

	m, older, newer := build(components.TurnFailed)
	idx, target := m.latestRecovery()
	if target.fail != newer || idx != 2 {
		t.Fatalf("the target is %+v at %d, want the newest failure at 2", target, idx)
	}
	for _, e := range m.transcript {
		drawn := ansi.Strip(m.renderEntry(e, 110))
		labelled := strings.Contains(drawn, latestRetryWords)
		if want := e.fail == newer; labelled != want {
			t.Fatalf("failure %p labelled=%v, want %v (older %p):\n%s", e.fail, labelled, want, older, drawn)
		}
	}
	held := pressAnswer(t, m)
	if held.state != stateFocus || held.focusIdx != idx {
		t.Fatalf("the handover did not reach the last failure: state %v, cursor %d", held.state, held.focusIdx)
	}
	if next := press(t, held, keys.Shown(keys.Row.Retry)); next.turnState() != stateStreaming {
		t.Fatalf("the letter did not retry the last failure: state %v", next.turnState())
	}

	for _, outcome := range []components.TurnState{components.TurnDone, components.TurnCancelled} {
		m, _, _ := build(outcome)
		if _, target := m.latestRecovery(); target != (recoveryTarget{}) {
			t.Fatalf("a session whose last turn ended %v still names a last failure", outcome)
		}
		if strings.Contains(ansi.Strip(m.renderHistory()), latestRetryWords) {
			t.Fatalf("a session whose last turn ended %v still draws the labelled retry", outcome)
		}
		before := snapshot(m)
		if after := snapshot(pressAnswer(t, m)); after != before {
			t.Fatalf("the handover acted after the session moved past the failure:\n before %s\n after  %s", before, after)
		}
	}
}
