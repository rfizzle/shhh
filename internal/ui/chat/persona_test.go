package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/persona"
)

// personaModel wires a scripted drafter: outcomes are served in order,
// and every request and save is recorded.
func personaModel(t *testing.T, kind persona.Kind, outcomes ...persona.Outcome) (Model, *[]persona.Request, *[]persona.Scope) {
	t.Helper()
	m := frameModel(t, 130, 40)
	var reqs []persona.Request
	var saves []persona.Scope
	i := 0
	p := Personas{
		Kind:       kind,
		Enabled:    true,
		ProjectDir: "/repo/.shhh/agents",
		GlobalDir:  "/home/x/.config/shhh/agents",
		Existing:   func() []string { return []string{"researcher"} },
		Draft: func(_ context.Context, req persona.Request) persona.Outcome {
			reqs = append(reqs, req)
			o := outcomes[i]
			if i < len(outcomes)-1 {
				i++
			}
			return o
		},
		Save: func(scope persona.Scope, d persona.Draft, _ bool) (string, error) {
			saves = append(saves, scope)
			return "/saved/" + d.Name + ".toml", nil
		},
	}
	return m.WithPersonas(p), &reqs, &saves
}

func submitLine(t *testing.T, m Model, line string) Model {
	t.Helper()
	m.input.SetValue(line)
	updated, cmd := m.submitInput()
	m = updated.(Model)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	return m
}

func lastNote(m Model) string { return m.transcript[len(m.transcript)-1].text }

func TestPersona_BareOffersSuggestionsAndNumberPicksOne(t *testing.T) {
	draft := &persona.Draft{Name: "skeptic", Description: "checks claims", Permissions: []string{"web"}, Prompt: "Doubt everything."}
	m, reqs, saves := personaModel(t, persona.KindChat, persona.Outcome{Draft: draft})
	m = submitLine(t, m, "/agents new")
	if !m.personaHoldsInput() || !strings.Contains(lastNote(m), "1. a skeptic") {
		t.Fatalf("bare /agents new should ask with suggestions: %q", lastNote(m))
	}
	m = submitLine(t, m, "1")
	if len(*reqs) != 1 || (*reqs)[0].Brief != persona.Suggestions(persona.KindChat)[0] || (*reqs)[0].Kind != persona.KindChat {
		t.Fatalf("request = %+v", *reqs)
	}
	if (*reqs)[0].Existing[0] != "researcher" {
		t.Fatal("existing roles not passed to the drafter")
	}
	if m.state != statePersona || m.personaAsk == nil {
		t.Fatalf("the card should be showing, state=%d", m.state)
	}
	card := strings.Join(m.personaLines(), "\n")
	for _, want := range []string{"skeptic — checks claims", "tier: read + web", "Doubt everything.", "Save", "Refine", "Discard"} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "Save to this project") {
		t.Fatal("a chat persona must not be offered a project scope")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if len(*saves) != 1 || (*saves)[0] != persona.ScopeGlobal {
		t.Fatalf("saves = %v", *saves)
	}
	if m.state != stateInput || m.persona != nil || !strings.Contains(lastNote(m), "spawnable now as role \"skeptic\"") {
		t.Fatalf("after save: state=%d note=%q", m.state, lastNote(m))
	}
}

func TestPersona_QuestionsThenDraftThenRefine(t *testing.T) {
	first := &persona.Draft{Name: "test-writer", Description: "adds tests", Permissions: []string{"write", "execute"}, Prompt: "Write tests."}
	revised := &persona.Draft{Name: "test-writer", Description: "adds table tests", Permissions: []string{"write", "execute"}, Prompt: "Write table-driven tests."}
	m, reqs, saves := personaModel(t, persona.KindCode,
		persona.Outcome{Questions: []string{"Which package?", "Run them too?"}},
		persona.Outcome{Draft: first},
		persona.Outcome{Draft: revised},
	)
	m = submitLine(t, m, "/agents new something for tests")
	if !strings.Contains(lastNote(m), "1. Which package?") || m.persona.waiting != personaWaitAnswers {
		t.Fatalf("questions should be asked: %q", lastNote(m))
	}
	m = submitLine(t, m, "internal/foo, yes")
	if len(*reqs) != 2 || len((*reqs)[1].Exchange) != 1 || (*reqs)[1].Exchange[0].Answer != "internal/foo, yes" {
		t.Fatalf("answers not carried: %+v", (*reqs)[1].Exchange)
	}
	if m.state != statePersona {
		t.Fatalf("the card should be showing, state=%d", m.state)
	}
	card := strings.Join(m.personaLines(), "\n")
	if !strings.Contains(card, "Save to this project") || !strings.Contains(card, "/repo/.shhh/agents") || !strings.Contains(card, "tier: read + write + execute") {
		t.Fatalf("code card:\n%s", card)
	}
	// Down twice to Refine, tab to the note, type, enter.
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyDown}, {Code: tea.KeyTab}} {
		updated, _ := m.Update(k)
		m = updated.(Model)
	}
	for _, r := range "table driven" {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(Model)
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("refine should start a revision")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if len(*reqs) != 3 || (*reqs)[2].Current == nil || (*reqs)[2].Current.Name != "test-writer" || (*reqs)[2].Feedback != "table driven" {
		t.Fatalf("revision request = %+v", (*reqs)[2])
	}
	if !strings.Contains(strings.Join(m.personaLines(), "\n"), "adds table tests") {
		t.Fatal("the revised draft should be on the card")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if len(*saves) != 1 || (*saves)[0] != persona.ScopeProject {
		t.Fatalf("saves = %v", *saves)
	}
}

func TestPersona_CancelAndFailure(t *testing.T) {
	m, reqs, _ := personaModel(t, persona.KindChat, persona.Outcome{Failed: true, Err: "no answer"})
	m = submitLine(t, m, "/agents new")
	m = submitLine(t, m, "cancel")
	if m.persona != nil || len(*reqs) != 0 || lastNote(m) != "No profile drafted." {
		t.Fatalf("cancel: persona=%v note=%q", m.persona, lastNote(m))
	}
	m = submitLine(t, m, "/agents new a poet")
	if m.persona != nil || !strings.Contains(lastNote(m), "could not be drafted — no answer") {
		t.Fatalf("failure: %q", lastNote(m))
	}
	// A late result for a retired run is dropped.
	m = submitLine(t, m, "/agents new")
	m.dropPersona()
	updated, _ := m.Update(personaDraftMsg{runID: 1, outcome: persona.Outcome{Draft: &persona.Draft{Name: "x"}}})
	if updated.(Model).state != stateInput {
		t.Fatal("a late draft opened a card")
	}
	off := frameModel(t, 130, 40)
	off = submitLine(t, off, "/agents new")
	if !strings.Contains(lastNote(off), "No model is configured") {
		t.Fatalf("disabled: %q", lastNote(off))
	}
}
