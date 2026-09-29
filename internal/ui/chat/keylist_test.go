package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

var questionMark = tea.KeyPressMsg{Code: '?', Text: "?"}

// `?` on a card holding the keyboard puts the card's own register on the
// pane, and the same key takes the reader back to the card with the decision
// still waiting on it
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func TestKeyList_ACardAnswersQuestionMarkWithItsRegister(t *testing.T) {
	m := notedCardModel(t)
	if !m.decisionGated() && !m.decisionHeld {
		t.Fatal("the fixture's card does not hold the keyboard")
	}
	m = pressOn(t, m, questionMark)
	if m.state != stateKeyList || m.keyList == nil {
		t.Fatalf("? on the card did not open the key list (state %d)", m.state)
	}
	if got := m.keyList.screen.Surface; got != "the approval card and the /run confirm" {
		t.Errorf("the list is about %q, want the approval card's register", got)
	}
	view := ansi.Strip(strings.Join(m.keyListLines(100, 60), "\n"))
	for _, want := range []string{"[" + keys.Shown(keys.Decision.Allow) + "]", "[" + keys.Shown(keys.Decision.Deny) + "]", "glyphs", "⚙", "⊘"} {
		if !strings.Contains(view, want) {
			t.Errorf("the card's key list is missing %q:\n%s", want, view)
		}
	}
	m = pressOn(t, m, questionMark)
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("? again did not go back to the card with its decision waiting (state %d)", m.state)
	}
}

// While a field on the card has the keyboard, `?` is a character in it.
func TestKeyList_AFieldOnTheCardKeepsQuestionMarkAsText(t *testing.T) {
	m := pressOn(t, notedCardModel(t), tea.KeyPressMsg{Code: 'N', Text: "N"})
	if m.decisionNote == nil {
		t.Fatal("the shifted deny should open the note field")
	}
	m = pressOn(t, m, questionMark)
	if m.keyList != nil {
		t.Fatal("? opened the key list from inside the note field")
	}
	if !strings.Contains(m.decisionNote.field.Value(), "?") {
		t.Errorf("? did not land in the note: %q", m.decisionNote.field.Value())
	}
}

// Every register row a card names for `?` is a row of the register: a name
// that matched none would open a list with no keys on it.
func TestKeyList_EveryCardNamesARegisterRow(t *testing.T) {
	names := []string{
		"reading mode", "a yes-or-no question", "the question card", "the agent manager",
		"the approval card's queue list", "the approval card's grant list",
	}
	overlays()
	for _, o := range append([]*mode{agentListMode(), childAskMode(nil)}, values(overlayTable)...) {
		if o.keyList == nil {
			continue
		}
		if name := o.keyList(Model{}); name != "" {
			names = append(names, name)
		}
	}
	for _, name := range names {
		if len(registerOffers(name)) == 0 {
			t.Errorf("%q names no row of the register", name)
		}
	}
}

func values(table map[state]*mode) []*mode {
	out := make([]*mode, 0, len(table))
	for _, o := range table {
		out = append(out, o)
	}
	return out
}

// Every card that answers `?` says so on its own run, and the offer it draws
// is the key that opens its list — a `[?] keys` that did nothing would be the
// one offer on the card a reader could not trust
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func TestKeyList_EveryCardThatAnswersQuestionMarkOffersIt(t *testing.T) {
	const offer = "[?] keys"
	for _, tc := range []struct {
		name string
		open func(t *testing.T) Model
	}{
		{"the plan card", func(t *testing.T) Model { return plannedModel(t, structuredPlan) }},
		{"the question card", func(t *testing.T) Model {
			return openedQuestion(t, agent.ModeManual, `{"question":"Which one?","shape":"choose","options":[{"label":"a"},{"label":"b"}]}`)
		}},
		{"the checkbox question", func(t *testing.T) Model {
			return openedQuestion(t, agent.ModeManual, `{"question":"Which?","shape":"choose_many","options":[{"label":"a"},{"label":"b"}]}`)
		}},
		{"a yes-or-no question", func(t *testing.T) Model {
			return openedQuestion(t, agent.ModeManual, `{"question":"Should the migration be reversible?","shape":"confirm"}`)
		}},
		{"the scaffold card", func(t *testing.T) Model {
			m := frameModel(t, 120, 40).WithScaffold(Scaffold{
				Offer: true, Paths: scaffoldFixturePaths(),
				Write: func() (string, error) { return project.ContextFile, nil },
			})
			next, _ := m.scaffoldCommand()
			return next.(Model)
		}},
		{"the rewind scope card", func(t *testing.T) Model {
			m, _, _ := rewindOfferModel(t)
			return sendText(t, m, "/rewind 1")
		}},
		{"the undo confirm", func(t *testing.T) Model {
			m, _ := undoModel(t)
			next, _ := m.undoTurn(1, nil)
			return next.(Model)
		}},
		{"the quit confirm", func(t *testing.T) Model {
			m := frameModel(t, 120, 40)
			m.state = stateStreaming
			next, _ := m.openQuitConfirm()
			return next.(Model)
		}},
		{"the held-line card", func(t *testing.T) Model {
			m := frameModel(t, 120, 40).WithInbound(Inbound{Policy: InboundHold})
			next, _ := m.Update(inboundMsg{line: InboundLine{From: "2026-09-23 10:41:07", Text: "rebase onto master"}})
			return next.(Model)
		}},
		{"the context-pressure card", func(t *testing.T) Model {
			m := pressureModel(t, 120)
			m.armPressureCard()
			return m
		}},
		{"the agent manager", func(t *testing.T) Model {
			sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
			t.Cleanup(sup.Close)
			next, _ := newSubagentModel(t, sup).openAgentList()
			return next.(Model)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(t)
			before, list := m.state, m.agentList != nil
			if view := ansi.Strip(m.View().Content); !strings.Contains(view, offer) {
				t.Fatalf("the card does not offer %q:\n%s", offer, view)
			}
			m = pressOn(t, m, questionMark)
			if m.state != stateKeyList || m.keyList == nil {
				t.Fatalf("%q is offered and ? did not open the key list (state %d)", offer, m.state)
			}
			m = pressOn(t, m, questionMark)
			if m.state != before || (m.agentList != nil) != list {
				t.Fatalf("? again did not go back to the card (state %d, want %d)", m.state, before)
			}
		})
	}
}
