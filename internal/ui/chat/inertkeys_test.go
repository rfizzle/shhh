package chat

// The fifth invariant across every keyed surface (S-125, DESIGN-TUI.md §7c).
//
// S-095's precedent: a rule stated in the design system becomes a test that
// enforces it everywhere rather than a paragraph each surface is trusted to
// have read. The register below is the audit — every surface in the product
// that offers a bare single-character key, and which of the two positions it
// is in — and the tests walk it in both states.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
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
// in (§7b). For a transcript row it is the row in the transcript with the
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
}

// register is the enumeration DESIGN-TUI.md §7c keeps in words. A surface
// added to the product with bare letters on it belongs here, and the tests
// below are what stop it shipping ungated.
func register(t *testing.T) []keyedSurface {
	t.Helper()
	return []keyedSurface{
		{
			name: "approval card (§2)",
			keys: []string{"y", "n", "a", "d", "A"},
			open: func(t *testing.T) Model { return interruptedModel(t, draftLead) },
			hold: handover,
		},
		{
			name: "plan card (§4d)",
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
			name: "a child agent's routed approval (§9c)",
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
			name: "the changeset row a turn closes with (§16)",
			keys: []string{"v", "u"},
			open: func(t *testing.T) Model {
				m, _ := undoModel(t)
				return typeChars(t, m, draftLead)
			},
			hold: readingCursorOn(entryTurnClose),
		},
		{
			name: "a provider failure's row (§17a)",
			keys: []string{"e", "p", "r", "c"},
			open: func(t *testing.T) Model {
				m := failureModel(t)
				updated, _ := m.Update(streamErrMsg{err: authFailure()})
				return typeChars(t, updated.(Model), draftLead)
			},
			hold: readingCursorOn(entryFailure),
		},
		{
			name: "a dropped stream's row (§17a)",
			keys: []string{"c", "r"},
			open: func(t *testing.T) Model {
				m := streamed(resumeModel(t), "so I'll thread the sentinel through runRound and then")
				updated, _ := m.Update(streamErrMsg{err: networkFailure()})
				return typeChars(t, updated.(Model), draftLead)
			},
			hold: readingCursorOn(entryStreamDrop),
		},
		{
			name: "a round-limit pause's row (§17a)",
			keys: []string{"v", "u", grantRoundsKey, uncapRoundsKey},
			open: func(t *testing.T) Model {
				m, _ := pausedModel(t)
				return typeChars(t, m, draftLead)
			},
			hold: readingCursorOn(entryRoundPause),
		},
		{
			name: "reading mode's own keys and its per-row offers (§7a)",
			keys: []string{"j", "k", "q", "-"},
			open: func(t *testing.T) Model { return typeChars(t, focusModel(t), draftLead) },
		},
		{
			name: "review mode (§16a)",
			keys: []string{"s", "A", "n", "p"},
			open: func(t *testing.T) Model {
				m, _ := reviewModel(t)
				return typeChars(t, m, draftLead)
			},
		},
		{
			name: "the undo confirm (§5)",
			keys: []string{"y", "n", "f"},
			open: func(t *testing.T) Model {
				m, _ := undoModel(t)
				return typeChars(t, m, draftLead)
			},
		},
		{
			name: "the context pressure card (§17b)",
			keys: []string{"n"},
			open: func(t *testing.T) Model { return typeChars(t, pressureModel(t, 110), draftLead) },
		},
		{
			name: "the selector family (§4)",
			keys: []string{"1", "2", "j", "k"},
			open: func(t *testing.T) Model { return typeChars(t, readyModel(t), draftLead) },
		},
		{
			name: "the agent list (§9a)",
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
// ctrl+e opens reading mode, then the cursor is put on the row of that kind.
// This is the only way a transcript row's keys ever go live (§7c).
func readingCursorOn(kind entryKind) func(*testing.T, Model) Model {
	return func(t *testing.T, m Model) Model {
		t.Helper()
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		rm, ok := next.(Model)
		if !ok || rm.state != stateFocus {
			t.Fatalf("ctrl+e should open reading mode, state %v", rm.state)
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
// own assertion (S-125). Every key in the register is pressed into a
// half-typed sentence while the surface offering it does not hold the
// keyboard, and every one of them has to be a letter: it lands at the end of
// the draft, and the session is otherwise exactly where it was.
//
// The alternative — routing `y` to whichever surface happens to be on screen
// — is what made a sentence containing the word "yes" able to approve a shell
// command (S-117), and there is no reason that hazard is special to
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

// TestInertKeys_EveryTakeoverHoldsTheKeyboardExclusively is why most of the
// register passes by construction. A takeover surface is a state, the state
// is routed before the input sees a key, and the input is not live while one
// is up — so its letters are live because nothing else is listening, which is
// the first of §7c's two positions.
func TestInertKeys_EveryTakeoverHoldsTheKeyboardExclusively(t *testing.T) {
	takeovers := []struct {
		name string
		s    state
	}{
		{"reading mode (§7a)", stateFocus},
		{"the full-screen diff (§3c)", stateDiffFull},
		{"review mode (§16a)", stateReview},
		{"the rewind picker (§4a)", stateRewindPick},
		{"the selector family (§4)", statePick},
		{"the model picker (§4a)", stateModelList},
		{"the undo confirm (§5)", stateUndoConfirm},
		{"the key entry (§17a)", stateKeyEntry},
		{"the context pressure card (§17b)", statePressure},
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
	// bottom panel — so it makes the same claim its own way (S-077).
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

// TestInertKeys_AWaitingRowSaysSoAndOffersTheKeyThatEndsIt is invariant 1
// applied to the state of a key (§7c). A transcript row whose keys are
// waiting says it in words — not in a shade a monochrome terminal loses —
// and offers the one key that makes them live.
func TestInertKeys_AWaitingRowSaysSoAndOffersTheKeyThatEndsIt(t *testing.T) {
	m, _ := undoModel(t)
	e := m.transcript[indexOfKind(t, m, entryTurnClose)]

	waiting := ansi.Strip(m.renderEntryKeys(e, 110, false))
	for _, want := range []string{"[v] review", "[u] undo turn", "[" + readingHandoverKey + "] to use them"} {
		if !strings.Contains(waiting, want) {
			t.Fatalf("a waiting row should still name its keys and the one that ends the wait, want %q in:\n%s", want, waiting)
		}
	}

	// Under the cursor the keys are ordinary keys again, and there is nothing
	// left to hand over.
	held := readingCursorOn(entryTurnClose)(t, m)
	live := ansi.Strip(held.renderEntryKeys(e, 110, true))
	if !strings.Contains(live, "[v] review") {
		t.Fatalf("the row under the cursor keeps its keys:\n%s", live)
	}
	if strings.Contains(live, readingHandoverKey) {
		t.Fatalf("a row that holds the keyboard has nothing to hand over:\n%s", live)
	}
}

// TestInertKeys_WaitingAndLiveNeverPaintAlike holds the colour half of the
// same rule, in both palettes. A key that is not live yet is not painted in
// info — the colour that means "you can press this" (§10a) — and the key that
// hands the keyboard over is.
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
			// The words carry it whatever the palette does, which is the
			// point of saying it in words at all (invariant 1).
			if !strings.Contains(ansi.Strip(waiting), "to use them") {
				t.Fatalf("the waiting state is said in words:\n%s", ansi.Strip(waiting))
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
