package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// personaView is the surface as the pane draws it, on a pane tall enough to
// hold every section of a draft over its card.
func personaView(m Model) string { return m.personaPane(130, 48) }

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
	for _, want := range []string{"skeptic — checks claims", "Tools", "read + web", "Purpose", "Doubt everything.", "Save", "Discard"} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "Save to this project") {
		t.Fatal("a chat persona must not be offered a project scope")
	}
	// The sections hold the keyboard when the draft arrives; tab hands it to
	// the card, and the card's row is the one that saves.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
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
	for _, want := range []string{"Tools", "read_file, search, quality_gate"} {
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
	if strings.Contains(card, "read_file, write_file") {
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
	// enter on Purpose opens its note; the note goes to the drafter about
	// Purpose alone.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeInto(t, m, "table driven")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 3 || (*reqs)[2].Current == nil || (*reqs)[2].Current.Name != "test-writer" ||
		(*reqs)[2].Feedback != "table driven" || (*reqs)[2].Section != "Purpose" {
		t.Fatalf("revision request = %+v", (*reqs)[2])
	}
	view = personaView(m)
	if !strings.Contains(view, "Write table-driven tests.") || !strings.Contains(view, "Purpose · refined once") {
		t.Fatalf("the revised section should be on the draft:\n%s", view)
	}
	// Only the section is taken from the answer: the description it also
	// changed stays as the draft had it.
	if strings.Contains(view, "adds table tests") {
		t.Fatalf("a section's refine rewrote the description:\n%s", view)
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
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
	if !strings.Contains(lastNote(off), "no model is configured") {
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
// with the loader's own sentence underneath, and its sections can still be
// revised, because a refusal a revision could answer should not cost the
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
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.persona == nil || m.personaScreen.Step != components.ProfileDraft {
		t.Fatalf("a refused save closed the card: note=%q", lastNote(m))
	}
	card := personaView(m)
	for _, want := range []string{"tiny", "Could not save the profile", "max_tokens: must be at least 300000", "Revise a section", "Discard"} {
		if !strings.Contains(card, want) {
			t.Fatalf("the card should hold %q:\n%s", want, card)
		}
	}
	// The sections are live: tab back to them and a note sends one section
	// to the drafter.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeInto(t, m, "read two files")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*reqs) != 2 || (*reqs)[1].Feedback != "read two files" || (*reqs)[1].Section != "Purpose" || (*reqs)[1].Current == nil {
		t.Fatalf("refine after a refusal: %+v", *reqs)
	}
}

// sectionedModel opens the drafter on a draft whose five sections are all
// filled, with the outcomes after the first served to the refines that
// follow, and hands back the requests and saves the fake records.
func sectionedModel(t *testing.T, kind persona.Kind, refines ...persona.Outcome) (Model, *[]persona.Request, *[]persona.Scope) {
	t.Helper()
	draft := &persona.Draft{Name: "test-writer", Description: "adds tests", Permissions: []string{"write", "execute"},
		Sections: &persona.Sections{Purpose: "Add tests.", Scope: "One package.", Restrictions: "Never delete a test.",
			Method: "Read, then write.", Report: "The cases added."}}
	if kind == persona.KindChat {
		draft.Permissions = []string{"web"}
	}
	if err := draft.Normalise(kind); err != nil {
		t.Fatal(err)
	}
	m, reqs, saves := personaModel(t, kind, append([]persona.Outcome{{Draft: draft}}, refines...)...)
	m = submitLine(t, m, "/agents new a test writer")
	if m.personaScreen.Step != components.ProfileDraft {
		t.Fatalf("the draft should be showing, step=%d", m.personaScreen.Step)
	}
	return m, reqs, saves
}

// refineSection moves the pointer to a section, opens its note and sends it.
func refineSection(t *testing.T, m Model, downs int, note string) Model {
	t.Helper()
	for range downs {
		m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeInto(t, m, note)
	return pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

// answerRewritingEverything is a drafter answer that rewrote every section
// and granted every tier, which is what a careless redraft looks like: only
// the section the note was about may be taken from it.
func answerRewritingEverything() persona.Outcome {
	d := &persona.Draft{Name: "renamed", Description: "something else", Permissions: []string{"web", "write", "execute"},
		Tools: []string{"write_file"},
		Sections: &persona.Sections{Purpose: "NEW purpose.", Scope: "NEW scope.", Restrictions: "NEW restrictions.",
			Method: "NEW method.", Report: "NEW report."}}
	return persona.Outcome{Draft: d}
}

// handEdit is the editor's return over a section: the file it was handed,
// rewritten, arriving as the message the editor's exit sends.
func handEdit(t *testing.T, m Model, section, text string) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "section.md")
	if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(personaEditorDoneMsg{flow: m.persona, section: section, path: path})
	return updated.(Model)
}

// A note about one section rewrites that section and nothing else: the other
// four, the name, the description and the tiers stay as the draft had them
// whatever the drafter's answer says.
func TestPersona_ARefineRewritesItsSectionOnly(t *testing.T) {
	m, reqs, saves := sectionedModel(t, persona.KindCode, answerRewritingEverything())
	m = refineSection(t, m, 3, "run go vet as well")
	req := (*reqs)[len(*reqs)-1]
	if req.Section != "Method" || req.Feedback != "run go vet as well" || req.Current == nil {
		t.Fatalf("request = %+v", req)
	}
	d := m.persona.draft
	want := []string{"Add tests.", "One package.", "Never delete a test.", "NEW method.", "The cases added."}
	for i, sec := range d.SectionList() {
		if sec.Body != want[i] {
			t.Fatalf("section %s = %q, want %q", sec.Name, sec.Body, want[i])
		}
	}
	if d.Name != "test-writer" || d.Description != "adds tests" || strings.Join(d.Permissions, ",") != "write,execute" {
		t.Fatalf("a section's refine moved the rest of the draft: %+v", d)
	}
	if !strings.Contains(d.Prompt, "NEW method.") || strings.Contains(d.Prompt, "Read, then write.") {
		t.Fatalf("the prompt should be written from the sections:\n%s", d.Prompt)
	}
	if view := personaView(m); !strings.Contains(view, "Method · refined once") {
		t.Fatalf("the refined section should be marked:\n%s", view)
	}
	if len(*saves) != 0 {
		t.Fatalf("a revision wrote a file: %v", *saves)
	}
}

// A section the person wrote themselves is marked as theirs, and a later
// refine of another section leaves it exactly as they wrote it — in the
// draft and in the prompt the save will write.
func TestPersona_AHandEditIsNeverRewrittenByAnotherSectionsRefine(t *testing.T) {
	m, _, _ := sectionedModel(t, persona.KindCode, answerRewritingEverything())
	m = handEdit(t, m, "Restrictions", "Never touch the goldens.")
	if view := personaView(m); !strings.Contains(view, "Restrictions · edited by you") {
		t.Fatalf("a hand edit should be marked:\n%s", view)
	}
	// Emptied in the editor is the person's act, not a gap the drafter left.
	if view := personaView(handEdit(t, m, "Report", "")); !strings.Contains(view, "Report ⚠ empty · you cleared it") {
		t.Fatalf("a section emptied in the editor should say who emptied it:\n%s", view)
	}
	m = refineSection(t, m, 0, "say when it is done")
	d := *m.persona.draft
	if got := d.Sections.Restrictions; got != "Never touch the goldens." {
		t.Fatalf("the hand edit was rewritten: %q", got)
	}
	// Normalise is what the save runs, and it rebuilds the prompt from the
	// sections: the hand edit has to survive it.
	if err := d.Normalise(persona.KindCode); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Prompt, "Never touch the goldens.") || !strings.Contains(d.Prompt, "NEW purpose.") {
		t.Fatalf("the prompt the save writes lost a revision:\n%s", d.Prompt)
	}
	if view := personaView(m); !strings.Contains(view, "Restrictions · edited by you") || !strings.Contains(view, "Purpose · refined once") {
		t.Fatalf("both marks should stand:\n%s", view)
	}
}

// Every revision is kept for the life of the flow, and esc on a section takes
// back its last one — a clear, a hand edit, a refine — before it is the
// step's own esc and discards the draft.
func TestPersona_EscTakesBackASectionsRevisionsOneAtATime(t *testing.T) {
	m, _, saves := sectionedModel(t, persona.KindCode, answerRewritingEverything())
	m = refineSection(t, m, 1, "name the goldens")
	m = handEdit(t, m, "Scope", "Only internal/agent.")
	m = pressOn(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := m.persona.draft.Sections.Scope; got != "" {
		t.Fatalf("x should clear the section, got %q", got)
	}
	if view := personaView(m); !strings.Contains(view, "Scope ⚠ empty · you cleared it") {
		t.Fatalf("a cleared section should say so:\n%s", view)
	}
	for _, want := range []string{"Only internal/agent.", "NEW scope.", "One package."} {
		m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.persona == nil || m.state != statePersona {
			t.Fatal("esc on a revised section should not leave the step")
		}
		if got := m.persona.draft.Sections.Scope; got != want {
			t.Fatalf("esc took Scope back to %q, want %q", got, want)
		}
		if !strings.Contains(m.persona.draft.Prompt, want) {
			t.Fatalf("the prompt should follow the section back:\n%s", m.persona.draft.Prompt)
		}
	}
	// Nothing left to take back: esc is the step's own and drops the draft,
	// and nothing was ever written.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.persona != nil || !strings.Contains(lastNote(m), "Profile discarded.") {
		t.Fatalf("esc on an unrevised section should discard: %q", lastNote(m))
	}
	if len(*saves) != 0 {
		t.Fatalf("a revision wrote a file: %v", *saves)
	}
}

// A redraft that fails or is stopped leaves the section — and every other
// revision — as it was, on the draft; it does not cost the draft.
func TestPersona_AFailedOrStoppedRedraftKeepsTheDraft(t *testing.T) {
	m, _, _ := sectionedModel(t, persona.KindCode,
		persona.Outcome{Failed: true, Err: "the model timed out"})
	m = handEdit(t, m, "Purpose", "Add table tests.")
	m = refineSection(t, m, 3, "shorter")
	if m.persona == nil || m.personaScreen.Step != components.ProfileDraft {
		t.Fatalf("a failed redraft closed the draft: %q", lastNote(m))
	}
	view := personaView(m)
	if !strings.Contains(view, "Method could not be redrafted — the model timed out") ||
		m.persona.draft.Sections.Method != "Read, then write." || m.persona.draft.Sections.Purpose != "Add table tests." {
		t.Fatalf("the draft should stand with the failure under it:\n%s", view)
	}

	// Stopped: the wait's esc hands the draft back, and the answer that
	// arrives after it is dropped.
	m, _, _ = sectionedModel(t, persona.KindCode, answerRewritingEverything())
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeInto(t, m, "shorter")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(personaView(m), "redrafting Purpose") {
		t.Fatalf("the wait should name the section:\n%s", personaView(m))
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = runPersonaCmd(t, m, cmd)
	if m.personaScreen.Step != components.ProfileDraft || m.persona.draft.Sections.Purpose != "Add tests." {
		t.Fatalf("a stopped redraft should leave the section: step=%d purpose=%q",
			m.personaScreen.Step, m.persona.draft.Sections.Purpose)
	}
}

// refineWhole opens the note for the whole draft — pressing its key again
// first when include is set — and sends it.
func refineWhole(t *testing.T, m Model, note string, include bool) Model {
	t.Helper()
	m = pressOn(t, m, tea.KeyPressMsg{Code: 'R', Text: "R"})
	if include {
		m = pressOn(t, m, tea.KeyPressMsg{Code: 'R', Text: "R"})
	}
	m = typeInto(t, m, note)
	return pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

// One note revises every section at once through the section's own request
// with no section named: the fields are fixed context, a section the person
// wrote is kept and says so, and each section the drafter changed carries
// its own revision and its own mark.
func TestPersona_AWholeDraftNoteRevisesEverySectionButTheOnesYouWrote(t *testing.T) {
	m, reqs, saves := sectionedModel(t, persona.KindCode, answerRewritingEverything())
	m = handEdit(t, m, "Restrictions", "Never touch the goldens.")
	m = pressOn(t, m, tea.KeyPressMsg{Code: 'R', Text: "R"})
	view := personaView(m)
	for _, want := range []string{"┄ a note on the whole draft", "Restrictions, which you edited, is kept",
		"[R] again to include the sections you edited", "[enter] redraft every section"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the whole-draft note lacks %q:\n%s", want, view)
		}
	}
	m = typeInto(t, m, "terser throughout")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	req := (*reqs)[len(*reqs)-1]
	if req.Section != "" || req.Feedback != "terser throughout" || req.Current == nil ||
		strings.Join(req.Keep, ",") != "Restrictions" {
		t.Fatalf("request = %+v", req)
	}
	d := m.persona.draft
	want := []string{"NEW purpose.", "NEW scope.", "Never touch the goldens.", "NEW method.", "NEW report."}
	for i, sec := range d.SectionList() {
		if sec.Body != want[i] {
			t.Fatalf("section %s = %q, want %q", sec.Name, sec.Body, want[i])
		}
	}
	if d.Name != "test-writer" || d.Description != "adds tests" || strings.Join(d.Permissions, ",") != "write,execute" || len(d.Tools) != 0 {
		t.Fatalf("a whole-draft note moved the fields: %+v", d)
	}
	for _, name := range []string{"Purpose", "Scope", "Method", "Report"} {
		if n := len(m.persona.revisions[name]); n != 1 {
			t.Fatalf("%s carries %d revisions, want its own one", name, n)
		}
	}
	view = personaView(m)
	for _, want := range []string{"Purpose · refined once", "Report · refined once", "Restrictions · edited by you · kept",
		"Whole draft · redrafted on your note", "Purpose, Scope, Method and Report changed", "[esc] take it back from every section it changed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the result lacks %q:\n%s", want, view)
		}
	}
	if len(*saves) != 0 {
		t.Fatalf("a whole-draft note wrote a file: %v", *saves)
	}
}

// The note's second press takes the person's own sections in with the rest,
// and says so before the note is sent; with text in the field the key is a
// letter of the note.
func TestPersona_AWholeDraftNotesSecondPressIncludesYourSections(t *testing.T) {
	m, reqs, _ := sectionedModel(t, persona.KindCode, answerRewritingEverything())
	m = handEdit(t, m, "Restrictions", "Never touch the goldens.")
	m = pressOn(t, pressOn(t, m, tea.KeyPressMsg{Code: 'R', Text: "R"}), tea.KeyPressMsg{Code: 'R', Text: "R"})
	if view := personaView(m); !strings.Contains(view, "Restrictions, which you edited, is redrafted too") ||
		!strings.Contains(view, "[R] again to keep the sections you edited") {
		t.Fatalf("the second press should say the edited section goes too:\n%s", view)
	}
	m = typeInto(t, m, "cut the Repetition")
	if view := personaView(m); !strings.Contains(view, "cut the Repetition") || strings.Contains(view, "[R] again") {
		t.Fatalf("an R in the note's text should be text:\n%s", view)
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	req := (*reqs)[len(*reqs)-1]
	if len(req.Keep) != 0 || req.Feedback != "cut the Repetition" {
		t.Fatalf("an included section should not be kept: %+v", req)
	}
	if got := m.persona.draft.Sections.Restrictions; got != "NEW restrictions." {
		t.Fatalf("the included section was not redrafted: %q", got)
	}
	if view := personaView(m); strings.Contains(view, "Restrictions · edited by you") {
		t.Fatalf("a redrafted section is no longer the person's own:\n%s", view)
	}
}

// esc on the note's own row takes one revision off every section the note
// changed — and only off those still standing where it put them — while esc
// on one section takes back that section alone.
func TestPersona_AWholeDraftNoteIsTakenBackExactly(t *testing.T) {
	m, _, saves := sectionedModel(t, persona.KindCode, answerRewritingEverything(),
		persona.Outcome{Draft: &persona.Draft{Name: "x", Description: "y",
			Sections: &persona.Sections{Purpose: "LATER purpose.", Scope: "s", Restrictions: "r", Method: "m", Report: "p"}}})
	m = refineWhole(t, m, "terser", false)
	// A section taken back on its own: Scope goes back, the rest stand.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	for m.personaScreen.Selected() != 1 {
		m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := m.persona.draft.Sections.Scope; got != "One package." {
		t.Fatalf("esc on Scope took it to %q", got)
	}
	// Purpose revised again on top of the note: the note's row must not pop
	// that revision in its name.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeInto(t, m, "later")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.persona.draft.Sections.Purpose; got != "LATER purpose." {
		t.Fatalf("Purpose = %q", got)
	}
	if view := personaView(m); !strings.Contains(view, "Method and Report changed") || strings.Contains(view, "Purpose, Scope") {
		t.Fatalf("the note's row should name only what still stands as it left it:\n%s", view)
	}
	for m.personaScreen.Selected() != len(personaBlocks) {
		m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	s := m.persona.draft.Sections
	if s.Purpose != "LATER purpose." || s.Scope != "One package." || s.Method != "Read, then write." ||
		s.Report != "The cases added." || s.Restrictions != "Never delete a test." {
		t.Fatalf("the note's row took back the wrong revisions: %+v", *s)
	}
	if !strings.Contains(m.persona.draft.Prompt, "Read, then write.") {
		t.Fatalf("the prompt should follow the sections back:\n%s", m.persona.draft.Prompt)
	}
	if n := len(m.persona.revisions["Purpose"]); n != 2 {
		t.Fatalf("Purpose's own stack should be untouched, has %d", n)
	}
	if m.persona == nil || strings.Contains(personaView(m), "Whole draft") {
		t.Fatalf("the note's row should be gone once it is taken back:\n%s", personaView(m))
	}
	if len(*saves) != 0 {
		t.Fatalf("a revision wrote a file: %v", *saves)
	}
}

// A whole-draft note that fails, asks instead, or is stopped keeps the
// draft as it was, with the wait drawn under the note while it lasts.
func TestPersona_AFailedOrStoppedWholeDraftNoteKeepsTheDraft(t *testing.T) {
	for _, o := range []persona.Outcome{
		{Failed: true, Err: "the model timed out"},
		{Questions: []string{"Terser how?"}},
	} {
		m, _, _ := sectionedModel(t, persona.KindCode, o)
		m = refineWhole(t, m, "terser", false)
		if m.persona == nil || m.personaScreen.Step != components.ProfileDraft ||
			m.persona.draft.Sections.Purpose != "Add tests." || len(m.persona.revisions["Purpose"]) != 0 {
			t.Fatalf("a %+v whole-draft note moved the draft", o)
		}
		view := personaView(m)
		if !strings.Contains(view, "the model timed out. Every section keeps its last text.") &&
			!strings.Contains(view, `the drafter asked "Terser how?" instead`) {
			t.Fatalf("the draft should stand with the reason under it:\n%s", view)
		}
		if strings.Contains(view, "Whole draft") {
			t.Fatalf("a note that changed nothing leaves no row:\n%s", view)
		}
	}

	m, _, _ := sectionedModel(t, persona.KindCode, answerRewritingEverything())
	m = pressOn(t, m, tea.KeyPressMsg{Code: 'R', Text: "R"})
	m = typeInto(t, m, "terser")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	view := personaView(m)
	if !strings.Contains(view, "redrafting every section") || !strings.Contains(view, "┄ a note on the whole draft") ||
		!strings.Contains(view, "every section keeps its last text") {
		t.Fatalf("the wait should draw under the note:\n%s", view)
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = runPersonaCmd(t, m, cmd)
	if m.personaScreen.Step != components.ProfileDraft || m.persona.draft.Sections.Purpose != "Add tests." {
		t.Fatalf("a stopped whole-draft note should leave the draft: step=%d purpose=%q",
			m.personaScreen.Step, m.persona.draft.Sections.Purpose)
	}
}

// A whole-draft note cannot grant a chat profile a writing tier: the tiers
// are never read from its answer.
func TestPersona_AChatWholeDraftNoteCannotGrantAWritingTier(t *testing.T) {
	m, _, _ := sectionedModel(t, persona.KindChat, answerRewritingEverything())
	m = refineWhole(t, m, "sharper", false)
	if m.persona.draft.Writes() || strings.Join(m.persona.draft.Permissions, ",") != "web" || len(m.persona.draft.Tools) != 0 {
		t.Fatalf("a chat whole-draft note granted a writing tier: %+v", *m.persona.draft)
	}
	if m.persona.draft.Sections.Purpose != "NEW purpose." {
		t.Fatalf("the sections should still be taken: %q", m.persona.draft.Sections.Purpose)
	}
}

// The chat drafter's rule holds through a revision: whatever tier a redraft
// answer grants, the chat profile still only reads, and the tools the first
// draft had taken off are still named.
func TestPersona_AChatRevisionCannotGrantAWritingTier(t *testing.T) {
	m, _, _ := sectionedModel(t, persona.KindChat, answerRewritingEverything())
	m.persona.draft.Dropped = []string{"write_file"}
	m = refineSection(t, m, 0, "sharper")
	if m.persona.draft.Writes() || strings.Join(m.persona.draft.Permissions, ",") != "web" {
		t.Fatalf("a chat revision granted a writing tier: %+v", m.persona.draft.Permissions)
	}
	if view := personaView(m); !strings.Contains(view, "dropped write_file — a chat persona only reads") {
		t.Fatalf("the dropped tool should still be named:\n%s", view)
	}
}

// commandsBlock is the Commands block's index among the draft's blocks.
func commandsBlock(t *testing.T) int {
	t.Helper()
	for i, name := range personaBlocks {
		if name == persona.SectionCommands {
			return i
		}
	}
	t.Fatal("the draft has no Commands block")
	return -1
}

// The Commands block is revised the way a prose section is: a note to the
// drafter takes only its two fields from the answer, the editor reads its
// lines back, x clears it and esc takes each revision back — and none of it
// moves the prompt, the tiers or the name.
func TestPersona_TheCommandsSectionIsRevisedLikeAProseOne(t *testing.T) {
	answer := answerRewritingEverything()
	answer.Draft.Intent = "running the package's tests"
	answer.Draft.Deny = []string{"git push"}
	m, reqs, saves := sectionedModel(t, persona.KindCode, answer)
	if view := personaView(m); !strings.Contains(view, "none stated · the session's own command lists apply") {
		t.Fatalf("a draft stating no commands should say what applies:\n%s", view)
	}
	m = refineSection(t, m, commandsBlock(t), "it must never push")
	req := (*reqs)[len(*reqs)-1]
	if req.Section != persona.SectionCommands || req.Feedback != "it must never push" {
		t.Fatalf("request = %+v", req)
	}
	d := m.persona.draft
	if d.Intent != "running the package's tests" || strings.Join(d.Deny, ",") != "git push" {
		t.Fatalf("the Commands fields should come from the answer: %q %v", d.Intent, d.Deny)
	}
	if d.Sections.Purpose != "Add tests." || d.Name != "test-writer" || strings.Join(d.Permissions, ",") != "write,execute" {
		t.Fatalf("a Commands refine moved the rest of the draft: %+v", *d)
	}
	if view := personaView(m); !strings.Contains(view, "Commands · refined once") ||
		!strings.Contains(view, "for: running the package's tests · never: git push") {
		t.Fatalf("the refined block should state its fields:\n%s", view)
	}

	m = handEdit(t, m, persona.SectionCommands, "# a comment\nintent: run the tests and the build\ndeny: git push\ndeny: go install")
	if d := m.persona.draft; d.Intent != "run the tests and the build" || strings.Join(d.Deny, ",") != "git push,go install" {
		t.Fatalf("the editor's lines should be read back: %q %v", d.Intent, d.Deny)
	}
	if view := personaView(m); !strings.Contains(view, "Commands · edited by you") {
		t.Fatalf("a hand edit should be marked:\n%s", view)
	}

	// An allow line is the loader's refusal, said while it is a card.
	m = handEdit(t, m, persona.SectionCommands, "allow: go test")
	if view := personaView(m); !strings.Contains(view, "a profile carries no allowlist") {
		t.Fatalf("an allow line should be refused in the loader's words:\n%s", view)
	}
	if strings.Join(m.persona.draft.Deny, ",") != "git push,go install" {
		t.Fatalf("a refused edit changed the draft: %v", m.persona.draft.Deny)
	}

	m = pressOn(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if d := m.persona.draft; d.Intent != "" || len(d.Deny) != 0 {
		t.Fatalf("x should clear the Commands fields: %q %v", d.Intent, d.Deny)
	}
	if view := personaView(m); !strings.Contains(view, "Commands · you cleared it") {
		t.Fatalf("a cleared Commands block should say so:\n%s", view)
	}
	for _, want := range []string{"git push,go install", "git push", ""} {
		m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
		if got := strings.Join(m.persona.draft.Deny, ","); got != want {
			t.Fatalf("esc took Commands back to %q, want %q", got, want)
		}
	}
	if len(*saves) != 0 {
		t.Fatalf("a revision wrote a file: %v", *saves)
	}
}
