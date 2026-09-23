package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
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
	return runPersonaCmd(t, m, cmd)
}

// runPersonaCmd runs a drafting command and feeds its result back, which is
// what the drafting turn is: a background command whose answer arrives as a
// message.
func runPersonaCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	// Update batches the session's own tick onto whatever a handler
	// returned (spin.go), so the drafting command arrives inside a batch.
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			m = runPersonaCmd(t, m, sub)
		}
		return m
	}
	if msg == nil {
		return m
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// pressOn sends one key to the surface and runs whatever it started.
func pressOn(t *testing.T, m Model, key tea.KeyPressMsg) Model {
	t.Helper()
	updated, cmd := m.Update(key)
	return runPersonaCmd(t, updated.(Model), cmd)
}

// typeInto types a line into whichever field the surface has focused.
func typeInto(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(Model)
	}
	return m
}

// personaView is the surface as the pane draws it.
func personaView(m Model) string { return m.personaPane(130, 30) }

func lastNote(m Model) string { return m.transcript[len(m.transcript)-1].text }

func TestPersona_BriefStepOffersStartingPointsAndTakesOne(t *testing.T) {
	draft := &persona.Draft{Name: "skeptic", Description: "checks claims", Permissions: []string{"web"}, Prompt: "Doubt everything."}
	m, reqs, saves := personaModel(t, persona.KindChat, persona.Outcome{Draft: draft})
	m = submitLine(t, m, "/agents new")
	if m.state != statePersona || m.personaScreen == nil {
		t.Fatalf("bare /agents new should open the surface, state=%d", m.state)
	}
	brief := personaView(m)
	for _, want := range []string{"/agents new", "a chat colleague", "researcher", "● brief", "a skeptic who checks each claim"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief step lacks %q:\n%s", want, brief)
		}
	}
	// Down moves off the field onto the first starting point; enter takes it.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 1 || (*reqs)[0].Brief != persona.Suggestions(persona.KindChat)[0] || (*reqs)[0].Kind != persona.KindChat {
		t.Fatalf("request = %+v", *reqs)
	}
	if (*reqs)[0].Existing[0] != "researcher" {
		t.Fatal("existing roles not passed to the drafter")
	}
	if m.personaScreen.Step != components.ProfileDraft {
		t.Fatalf("the draft should be showing, step=%d", m.personaScreen.Step)
	}
	card := personaView(m)
	for _, want := range []string{"skeptic — checks claims", "permissions", "read + web", "Doubt everything.", "Save", "Refine", "Discard"} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "Save to this project") {
		t.Fatal("a chat persona must not be offered a project scope")
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*saves) != 1 || (*saves)[0] != persona.ScopeGlobal {
		t.Fatalf("saves = %v", *saves)
	}
	if m.state != stateInput || m.persona != nil || !strings.Contains(lastNote(m), "spawnable now as role \"skeptic\"") {
		t.Fatalf("after save: state=%d note=%q", m.state, lastNote(m))
	}
}

// A typed brief is what enter takes while the field has the pointer, which is
// where the pointer starts: someone who already has the sentence types it.
func TestPersona_TypedBriefIsWhatEnterTakes(t *testing.T) {
	m, reqs, _ := personaModel(t, persona.KindChat, persona.Outcome{Draft: &persona.Draft{
		Name: "poet", Description: "writes verse", Prompt: "Rhyme."}})
	m = submitLine(t, m, "/agents new")
	m = typeInto(t, m, "someone who argues back")
	pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 1 || (*reqs)[0].Brief != "someone who argues back" {
		t.Fatalf("request = %+v", *reqs)
	}
}

// A profile that narrows its toolset is still agreed to on the card, so the
// card names the tools: "read" that is three tools is not the offer "read"
// that is all of them, and the person saving it should see which one it is.
func TestPersona_CardNamesANarrowedToolset(t *testing.T) {
	draft := &persona.Draft{Name: "reviewer", Description: "reads diffs",
		Tools: []string{"read_file", "search", "quality_gate"}, Prompt: "Review the diff."}
	m, _, _ := personaModel(t, persona.KindCode, persona.Outcome{Draft: draft})
	m = submitLine(t, m, "/agents new a reviewer")
	card := personaView(m)
	for _, want := range []string{"tools", "read_file search quality_gate"} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
}

// A chat draft whose tools the tidy cut is still a card, and the card says
// which tools came off.
func TestPersona_CardNamesDroppedTools(t *testing.T) {
	draft := &persona.Draft{Name: "skeptic", Description: "checks claims",
		Tools: []string{"read_file", "write_file"}, Prompt: "Doubt."}
	if err := draft.Normalise(persona.KindChat); err != nil {
		t.Fatal(err)
	}
	m, _, _ := personaModel(t, persona.KindChat, persona.Outcome{Draft: draft})
	m = submitLine(t, m, "/agents new a skeptic")
	card := personaView(m)
	for _, want := range []string{"dropped", "write_file", "a chat persona only reads"} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "read_file write_file") {
		t.Errorf("card still lists the dropped tool among the tools:\n%s", card)
	}
}

func TestPersona_QuestionsAreAskedOneAtATime(t *testing.T) {
	first := &persona.Draft{Name: "test-writer", Description: "adds tests", Permissions: []string{"write", "execute"}, Prompt: "Write tests."}
	revised := &persona.Draft{Name: "test-writer", Description: "adds table tests", Permissions: []string{"write", "execute"}, Prompt: "Write table-driven tests."}
	m, reqs, saves := personaModel(t, persona.KindCode,
		persona.Outcome{Questions: []string{"Which package?", "Run them too?"}},
		persona.Outcome{Draft: first},
		persona.Outcome{Draft: revised},
	)
	m = submitLine(t, m, "/agents new something for tests")
	view := personaView(m)
	if !strings.Contains(view, "question 1 of 2") || !strings.Contains(view, "Which package?") {
		t.Fatalf("the first question should be asked alone:\n%s", view)
	}
	if strings.Contains(view, "Run them too?") {
		t.Fatalf("the second question should not be on screen yet:\n%s", view)
	}
	m = typeInto(t, m, "internal/foo")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	view = personaView(m)
	if !strings.Contains(view, "question 2 of 2") || !strings.Contains(view, "✓ Which package?") || !strings.Contains(view, "internal/foo") {
		t.Fatalf("the answered question should stay on screen:\n%s", view)
	}
	m = typeInto(t, m, "yes")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 2 || len((*reqs)[1].Exchange) != 2 {
		t.Fatalf("both answers should reach the drafter: %+v", (*reqs)[1].Exchange)
	}
	if got := (*reqs)[1].Exchange[0]; got.Question != "Which package?" || got.Answer != "internal/foo" {
		t.Fatalf("first exchange = %+v", got)
	}
	if m.personaScreen.Step != components.ProfileDraft {
		t.Fatalf("the draft should be showing, step=%d", m.personaScreen.Step)
	}
	card := personaView(m)
	if !strings.Contains(card, "Save to this project") || !strings.Contains(card, "/repo/.shhh/agents") || !strings.Contains(card, "read + write + execute") {
		t.Fatalf("code card:\n%s", card)
	}
	// Down twice to Refine, tab to the note, type, enter.
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyDown}, {Code: tea.KeyTab}} {
		updated, _ := m.Update(k)
		m = updated.(Model)
	}
	m = typeInto(t, m, "table driven")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 3 || (*reqs)[2].Current == nil || (*reqs)[2].Current.Name != "test-writer" || (*reqs)[2].Feedback != "table driven" {
		t.Fatalf("revision request = %+v", (*reqs)[2])
	}
	if !strings.Contains(personaView(m), "adds table tests") {
		t.Fatal("the revised draft should be on the card")
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*saves) != 1 || (*saves)[0] != persona.ScopeProject {
		t.Fatalf("saves = %v", *saves)
	}
}

// esc unwinds the flow one exchange at a time: an answer, then the brief,
// then out. An esc that always cancelled made a mistyped answer cost the
// whole drafting.
func TestPersona_EscStepsBackThroughTheFlow(t *testing.T) {
	m, reqs, _ := personaModel(t, persona.KindCode,
		persona.Outcome{Questions: []string{"Which package?", "Run them too?"}})
	m = submitLine(t, m, "/agents new something for tests")
	m = typeInto(t, m, "internal/foo")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	// Back onto the first question, with the answer still in the field.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	view := personaView(m)
	if !strings.Contains(view, "question 1 of 2") || !strings.Contains(view, "internal/foo") {
		t.Fatalf("esc should reopen the answered question:\n%s", view)
	}
	if len(m.persona.exchange) != 0 {
		t.Fatalf("the answer should have been taken back: %+v", m.persona.exchange)
	}
	// Back again lands on the brief, with the brief still in the field.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	view = personaView(m)
	if !strings.Contains(view, "● brief") || !strings.Contains(view, "something for tests") {
		t.Fatalf("esc should reopen the brief:\n%s", view)
	}
	// And once more leaves, having drafted nothing beyond the first turn.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.state != stateInput || m.persona != nil || lastNote(m) != "No profile drafted." {
		t.Fatalf("esc from the brief should leave: state=%d note=%q", m.state, lastNote(m))
	}
	if len(*reqs) != 1 {
		t.Fatalf("stepping back should draft nothing new: %d requests", len(*reqs))
	}
}

// The wait has a key, which is the point of putting it on the surface: a
// cancel function nothing was bound to was a cancel nobody had.
func TestPersona_TheWaitCanBeStopped(t *testing.T) {
	m, _, _ := personaModel(t, persona.KindChat, persona.Outcome{Draft: &persona.Draft{Name: "x"}})
	m = submitLine(t, m, "/agents new")
	m = typeInto(t, m, "a poet")
	// Take the brief without running the drafting command, so the wait is
	// what is on screen.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.personaScreen.Step != components.ProfileWorking || !m.personaDrafting() {
		t.Fatalf("the wait should be up, step=%d", m.personaScreen.Step)
	}
	if !strings.Contains(personaView(m), "drafting") {
		t.Fatalf("the wait should say what it is waiting for:\n%s", personaView(m))
	}
	if !m.spinnerWanted() {
		t.Fatal("the wait should keep the tick chain running")
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.personaScreen.Step != components.ProfileBrief || m.personaDrafting() {
		t.Fatalf("esc should stop the drafting and hand back the brief, step=%d", m.personaScreen.Step)
	}
	// The stopped turn's own result arrives late and is dropped by its run
	// number.
	m = runPersonaCmd(t, m, cmd)
	if m.personaScreen.Step != components.ProfileBrief {
		t.Fatalf("a stopped turn's result opened a card, step=%d", m.personaScreen.Step)
	}
}

func TestPersona_CancelAndFailure(t *testing.T) {
	m, reqs, _ := personaModel(t, persona.KindChat, persona.Outcome{Failed: true, Err: "no answer"})
	m = submitLine(t, m, "/agents new")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
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

// An empty answer reaches the drafter as an answer rather than as a blank: a
// blank beside a question reads as a question nobody asked. What the flow
// keeps is the person's own words, so stepping back puts those in the field.
func TestPersona_AnEmptyAnswerIsWordedForTheDrafter(t *testing.T) {
	m, reqs, _ := personaModel(t, persona.KindCode,
		persona.Outcome{Questions: []string{"Which package?"}},
		persona.Outcome{Draft: &persona.Draft{Name: "x", Description: "d", Prompt: "p"}})
	m = submitLine(t, m, "/agents new something for tests")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 2 || len((*reqs)[1].Exchange) != 1 {
		t.Fatalf("the empty answer should still be an exchange: %+v", *reqs)
	}
	if got := (*reqs)[1].Exchange[0].Answer; got != "no preference" {
		t.Fatalf("empty answer sent as %q", got)
	}
	if got := m.persona.exchange[0].Answer; got != "" {
		t.Fatalf("the flow should keep the person's own words, got %q", got)
	}
}

// A session that has spawned nothing still has a manager to open, and what it
// says is "nothing yet, and here is how to make one". A drafter with no
// supervisor behind it is enough to open it: the list is where the offer to
// draft lives.
func TestPersona_TheManagerOpensOnASessionWithNoAgents(t *testing.T) {
	m, _, _ := personaModel(t, persona.KindCode, persona.Outcome{Draft: &persona.Draft{Name: "x"}})
	m = submitLine(t, m, "/agents")
	if m.agentList == nil {
		t.Fatal("/agents should open the manager with no supervisor wired")
	}
	rows := m.agentList.Rows
	if len(rows) != 2 || rows[0].Name != "orchestrator" || rows[1].State != components.AgentOffer {
		t.Fatalf("an empty session should list itself and the offer: %+v", rows)
	}
	view := m.panelView()
	for _, want := range []string{"orchestrator", "draft a new profile", "/agents new"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the manager lacks %q:\n%s", want, view)
		}
	}
}

// The manager is where the session's roles are read. Each is a row under the
// agents — what it is for, and where the file that says so lives — with the
// drafter's own row under them, and enter on a role hands its file to the
// editor and leaves the list.
func TestPersona_TheManagerListsTheRolesThisSessionCanSpawn(t *testing.T) {
	m, _, _ := personaModel(t, persona.KindCode, persona.Outcome{Draft: &persona.Draft{Name: "x"}})
	m.personas.Roles = func() []SpawnableRole {
		return []SpawnableRole{
			{Name: "critic", Description: "reads a diff", Scope: "project", Path: "/repo/.shhh/agents/critic.toml"},
			{Name: "researcher", Description: "read-only tools", Scope: "built-in"},
		}
	}
	m = submitLine(t, m, "/agents")
	rows := m.agentList.Rows
	if len(rows) != 4 || rows[1].Name != "critic" || rows[2].Name != "researcher" ||
		rows[1].State != components.AgentRole || rows[3].State != components.AgentOffer {
		t.Fatalf("the roles belong under the agents and above the drafter row: %+v", rows)
	}
	if !rows[1].Editable || rows[2].Editable {
		t.Fatalf("only a role with a file behind it can be opened: %+v", rows[1:3])
	}
	view := m.panelView()
	for _, want := range []string{"critic · reads a diff", "project", "researcher", "built-in"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the manager lacks %q:\n%s", want, view)
		}
	}
	m.agentList.Focus = 1
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.agentList != nil {
		t.Fatal("enter on a role row should hand the terminal to the editor and leave the list")
	}
}

// The editor's exit reads the role's file again through the session's own
// registration, so an edited role is the running session's; a file the
// loader refuses leaves the role as it was, and the note says which.
func TestPersona_AnEditedRoleIsTheRunningSessions(t *testing.T) {
	m, _, _ := personaModel(t, persona.KindCode, persona.Outcome{Draft: &persona.Draft{Name: "x"}})
	var reloaded []string
	m.personas.Reload = func(path string) error {
		reloaded = append(reloaded, path)
		return nil
	}
	next, _ := m.roleEditorFinished(roleEditorDoneMsg{name: "critic", path: "/repo/.shhh/agents/critic.toml"})
	if len(reloaded) != 1 || reloaded[0] != "/repo/.shhh/agents/critic.toml" {
		t.Fatalf("the editor's exit should reload the file it opened, reloaded %v", reloaded)
	}
	said := lastNote(next.(Model))
	for _, want := range []string{"/repo/.shhh/agents/critic.toml", "critic", "this session spawns"} {
		if !strings.Contains(said, want) {
			t.Fatalf("the note should name the file and the role, got %q", said)
		}
	}
	if strings.Contains(said, "session started from here") {
		t.Fatalf("an edit is no longer the next session's, got %q", said)
	}

	m.personas.Reload = func(string) error { return fmt.Errorf("agent profile critic.toml: unknown key colour") }
	next, _ = m.roleEditorFinished(roleEditorDoneMsg{name: "critic", path: "/repo/.shhh/agents/critic.toml"})
	if said := lastNote(next.(Model)); !strings.Contains(said, "as it was") || !strings.Contains(said, "unknown key colour") {
		t.Fatalf("a refused file should say the role is as it was and why, got %q", said)
	}

	reloaded = nil
	m.personas.Reload = func(path string) error { reloaded = append(reloaded, path); return nil }
	next, _ = m.roleEditorFinished(roleEditorDoneMsg{name: "critic", err: fmt.Errorf("exit status 1")})
	if said := lastNote(next.(Model)); !strings.Contains(said, "exit status 1") {
		t.Fatalf("an editor that failed should say so, got %q", said)
	}
	if len(reloaded) != 0 {
		t.Fatal("an editor that failed should leave the role unread")
	}
}

// The manager offers the flow, because it is where a person goes to see what
// the session has and so where "none of these" gets asked.
func TestPersona_AgentManagerOffersTheDrafter(t *testing.T) {
	m, reqs, _ := personaModel(t, persona.KindCode, persona.Outcome{Draft: &persona.Draft{
		Name: "reviewer", Description: "reads diffs", Prompt: "Review."}})
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m = m.WithSubagents(sup)
	m = submitLine(t, m, "/agents")
	if m.agentList == nil {
		t.Fatal("/agents should open the manager")
	}
	rows := m.agentList.Rows
	last := rows[len(rows)-1]
	if last.State != components.AgentOffer || last.Name != "draft a new profile" {
		t.Fatalf("the manager should offer the drafter: %+v", last)
	}
	m.agentList.Focus = len(rows) - 1
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.agentList != nil || m.state != statePersona {
		t.Fatalf("the offer row should open the drafter, state=%d", m.state)
	}
	m = typeInto(t, m, "a reviewer")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 1 || (*reqs)[0].Brief != "a reviewer" {
		t.Fatalf("request = %+v", *reqs)
	}
}

// A profile the loader refuses at save keeps the card: the draft stays on it
// with the loader's own sentence underneath, and Refine is still there to
// fix it with, because a refusal a note could answer should not cost the
// draft (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
func TestPersona_ARefusedSaveKeepsTheDraftOnTheCard(t *testing.T) {
	// Below the floor a child is admitted at, which only the loader knows.
	draft := &persona.Draft{Name: "tiny", Description: "reads one file", MaxTokens: 8000, Prompt: "Read."}
	m, reqs, _ := personaModel(t, persona.KindChat, persona.Outcome{Draft: draft})
	dir := t.TempDir()
	m.personas.Save = func(_ persona.Scope, d persona.Draft, overwrite bool) (string, error) {
		return persona.Write(dir, d, persona.KindChat, overwrite)
	}
	m = submitLine(t, m, "/agents new something small")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.persona == nil || m.personaScreen.Step != components.ProfileDraft {
		t.Fatalf("a refused save closed the card: note=%q", lastNote(m))
	}
	card := personaView(m)
	for _, want := range []string{"tiny", "Could not save the profile", "max_tokens: must be at least 300000", "Refine", "Discard"} {
		if !strings.Contains(card, want) {
			t.Fatalf("the card should hold %q:\n%s", want, card)
		}
	}
	// Refine is live: a note sends the draft back to the drafter.
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyTab}} {
		updated, _ := m.Update(k)
		m = updated.(Model)
	}
	m = typeInto(t, m, "leave the budget to the session")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 2 || (*reqs)[1].Feedback != "leave the budget to the session" || (*reqs)[1].Current == nil {
		t.Fatalf("refine after a refusal: %+v", *reqs)
	}
}
