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
	// chords are the same offers as the chords that reach them without the
	// handover, on the surfaces that have them: a transcript row is drawn
	// beside a live draft nearly all the time, so its offers exist twice
	// (keys.RowChord). A takeover has none — it holds the keyboard, so its
	// letters are already live.
	chords []keys.Binding
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
			keys: []string{"j", "k", "s", "S"},
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
			keys: []string{"v", "u"},
			open: func(t *testing.T) Model {
				m, _ := undoModel(t)
				return typeChars(t, m, draftLead)
			},
			hold:   readingCursorOn(entryTurnClose),
			chords: []keys.Binding{keys.RowChord.Review, keys.RowChord.Undo},
			row:    entryTurnClose,
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
			chords: []keys.Binding{keys.RowChord.Key, keys.RowChord.Provider},
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
			chords: []keys.Binding{keys.RowChord.Continue, keys.RowChord.Retry},
			row:    entryStreamDrop,
		},
		{
			name: "a round-limit pause's row",
			keys: []string{"v", "u", keys.Shown(keys.Row.Rounds), keys.Shown(keys.Row.Uncap)},
			open: func(t *testing.T) Model {
				m, _ := pausedModel(t)
				return typeChars(t, m, draftLead)
			},
			hold:   readingCursorOn(entryRoundPause),
			chords: []keys.Binding{keys.RowChord.Review, keys.RowChord.Undo, keys.RowChord.Rounds},
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

// pressChord presses a chord the way the decoder reports one: the key with
// its modifier, never a rune the draft could produce.
func pressChord(t *testing.T, m Model, b keys.Binding) Model {
	t.Helper()
	spelling := keys.Shown(b)
	rest, ok := strings.CutPrefix(spelling, "alt+")
	if !ok || len([]rune(rest)) != 1 {
		t.Fatalf("%q is not an alt chord on a single key", spelling)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: []rune(rest)[0], Mod: tea.ModAlt})
	return updated.(Model)
}

// TestRowChords_ARowOffersOnlyWhatCanBePressedFromWhereItIsDrawn is the
// story the row offers tell together with the test above. That one presses
// the letter and watches it land in the sentence, which is what a letter
// beside a live draft has to do. This one presses the chord the row actually
// draws there, and watches it act — from the prompt, with the sentence still
// in the box.
//
// The third part is what the row has on screen while the draft holds the
// keyboard: every offer it prints is a chord. A bare letter drawn there would
// be the failure both halves are about, an offer that types a letter instead
// of doing what it says.
func TestRowChords_ARowOffersOnlyWhatCanBePressedFromWhereItIsDrawn(t *testing.T) {
	for _, s := range register(t) {
		if len(s.chords) == 0 {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			for _, chord := range s.chords {
				m := s.open(t)
				before := snapshot(m)
				next := pressChord(t, m, chord)
				if got := next.input.Value(); got != draftLead {
					t.Fatalf("%q is a chord and cannot reach the sentence: draft is %q",
						keys.Shown(chord), got)
				}
				// Beyond the cursor: the chord leaves the reading cursor on
				// the row it acted on, so the cursor having moved is not on
				// its own evidence that anything happened.
				if after := withoutFocus(snapshot(next)); after == withoutFocus(before) {
					t.Fatalf("%q did nothing from the draft; the row draws it, so it has to act\n %s",
						keys.Shown(chord), before)
				}
			}

			m := s.open(t)
			e := m.transcript[indexOfKind(t, m, s.row)]
			drawn := ansi.Strip(m.renderEntryKeys(e, 110, false))
			for _, offer := range bracketed.FindAllStringSubmatch(drawn, -1) {
				if len([]rune(offer[1])) == 1 {
					t.Fatalf("the row draws %q beside a live draft, which is a letter of the sentence:\n%s",
						offer[1], drawn)
				}
			}
		})
	}
}

// steerNotice is the row an automatic steer leaves, which is the one system
// row that offers a key of its own (intervene.go). The offer stands only
// while the turn it interrupted is live, which is what the caller sets up.
func steerNotice(turn int64) entry {
	return entry{kind: entrySystem, turn: turn,
		text: "Steered — the edits have moved outside the round loop.",
		intervened: &interveneRow{
			iv:  agent.Intervention{Kind: agent.InterveneSteer, Notice: "Steered", Reason: "edits outside the round loop"},
			row: "round 2 · steered · edits outside the round loop",
		}}
}

// TestRowChords_OneRowInASessionNamesTheOptionSetting is the sentence that
// makes the chords usable on the desktop shhh is most often run on. An alt
// chord composes a character on a stock Mac terminal until a profile setting
// is ticked, so the first row in a session to offer one names the doctor row
// that reads that setting — and only the first, because it is a fact about
// the terminal rather than about that row.
//
// Both kinds are checked because the rows do not agree on when they answer
// the question: a turn's close settles it as it is built, and every other row
// settles it as it is drawn. A kind the drawn answer cannot recognise says
// "not me" for itself and "somebody already said it" for every row after it,
// which is a session that never names the setting at all.
func TestRowChords_OneRowInASessionNamesTheOptionSetting(t *testing.T) {
	closeRow := func(m *Model) entry {
		return entry{kind: entryTurnClose, turn: 1, close: &components.TurnClose{
			State: components.TurnDone,
			Changes: &components.TurnChanges{Files: 1, Added: 1, Removed: 1,
				Keys: []components.TurnKey{rowOffer(keys.Row.Review, "review")}},
			Option: m.firstRowOffer(),
		}}
	}
	says := func(t *testing.T, m Model, at int) bool {
		t.Helper()
		return strings.Contains(ansi.Strip(m.renderEntryKeys(m.transcript[at], 110, false)), "Option")
	}

	t.Run("the steer notice is first", func(t *testing.T) {
		m := frameModel(t, 110, 40)
		m.turnCount, m.turnOpen = 1, true
		m.appendEntry(steerNotice(1))
		m.appendEntry(closeRow(&m))
		if !says(t, m, 0) {
			t.Error("the first row to offer a chord does not name the Option setting")
		}
		if says(t, m, 1) {
			t.Error("a second row names it again")
		}
	})

	t.Run("the turn's close is first", func(t *testing.T) {
		m := frameModel(t, 110, 40)
		m.turnCount, m.turnOpen = 1, true
		m.appendEntry(closeRow(&m))
		m.appendEntry(steerNotice(1))
		if !says(t, m, 0) {
			t.Error("the first row to offer a chord does not name the Option setting")
		}
		if says(t, m, 1) {
			t.Error("a second row names it again")
		}
	})
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
// to the state of a key. A transcript row is drawn beside a live draft nearly
// all the time, and the offer it prints there is the chord, because the
// letter would be a letter of the sentence being typed. Under reading mode's
// cursor the letters are live and the row prints those. Either way the row
// prints the key that works from where the reader is, and nothing waits.
func TestInertKeys_ARowDrawsTheKeyThatIsLiveWhereItStands(t *testing.T) {
	m, _ := undoModel(t)
	e := m.transcript[indexOfKind(t, m, entryTurnClose)]

	waiting := ansi.Strip(m.renderEntryKeys(e, 110, false))
	for _, want := range []string{
		keys.Bracket(keys.RowChord.Review) + " review",
		keys.Bracket(keys.RowChord.Undo) + " undo turn",
	} {
		if !strings.Contains(waiting, want) {
			t.Fatalf("a row beside a live draft offers its chords, want %q in:\n%s", want, waiting)
		}
	}
	if strings.Contains(waiting, "to use them") {
		t.Fatalf("nothing on the row is waiting for the keyboard any more:\n%s", waiting)
	}

	// Under the cursor the keys are the letters again, and there is nothing
	// left to hand over.
	held := readingCursorOn(entryTurnClose)(t, m)
	live := ansi.Strip(held.renderEntryKeys(e, 110, true))
	if !strings.Contains(live, "[v] review") {
		t.Fatalf("the row under the cursor keeps its letters:\n%s", live)
	}
	if strings.Contains(live, keys.Shown(keys.Draft.Reading)) {
		t.Fatalf("a row that holds the keyboard has nothing to hand over:\n%s", live)
	}
	if strings.Contains(live, "alt+") {
		t.Fatalf("the chord is the draft's spelling, not the cursor's:\n%s", live)
	}
}

// TestInertKeys_WaitingAndLiveNeverPaintAlike holds the same rule in both
// palettes. The two states of a row's keys are never the same run of
// characters, and the difference is in the spelling rather than in a shade a
// monochrome terminal loses: the chord where the draft has the keyboard, the
// letter under the cursor.
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

			m, _ := undoModel(t)
			e := m.transcript[indexOfKind(t, m, entryTurnClose)]
			waiting := m.renderEntryKeys(e, 110, false)
			live := m.renderEntryKeys(e, 110, true)
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

// withoutFocus is a snapshot with the reading cursor's position taken out of
// it, for the one check that has to ignore the cursor.
func withoutFocus(s string) string {
	i := strings.Index(s, " focus=")
	if i < 0 {
		return s
	}
	j := strings.Index(s[i+1:], " ")
	if j < 0 {
		return s[:i]
	}
	return s[:i] + s[i+1+j:]
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
