package chat

// /agents new: a profile drafted in conversation
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
// The flow is three exchanges at most — a brief, perhaps a few questions,
// then a draft on a card — and it runs on a surface of its own
// (docs/interface/surfaces.md#the-profile-drafter) rather than through the
// transcript. What that buys is the thing the flow was missing: a step you
// can see yourself standing on, one question at a time with the answers you
// have already given still on screen, and a way back through them.
//
// The drafting is a background command like the backlog reading: nothing on
// screen waits for it, and a result arriving after /clear is dropped by its
// run number. The wait is on the surface rather than in the transcript, which
// is what gives the person somewhere to press the key that stops it — a
// cancel function with nothing bound to it was a cancel nobody had.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Personas is what the session hands the flow: the drafter, the roles that
// exist, and the writer that puts a file where the person chose and makes
// the role spawnable now.
type Personas struct {
	Kind persona.Kind
	// Enabled reports a drafter with a model behind it.
	Enabled bool
	// Draft runs one drafting turn.
	Draft func(ctx context.Context, req persona.Request) persona.Outcome
	// Existing lists the role names the session has now.
	Existing func() []string
	// Models the draft may name.
	Models []string
	// Save writes the draft under scope and registers the role with the
	// running session; it returns the path written.
	Save func(scope persona.Scope, d persona.Draft, overwrite bool) (string, error)
	// ProjectDir and GlobalDir are the two places a file can go, for the
	// card to name.
	ProjectDir, GlobalDir string
}

// WithPersonas wires the drafting flow.
func (m Model) WithPersonas(p Personas) Model {
	m.personas = p
	return m
}

// personaFlow is one drafting in progress. The surface holds what is on
// screen; this holds what the drafter is being told.
type personaFlow struct {
	brief string
	// questions is the batch the drafter last asked and at which of them the
	// flow is standing. They are asked one at a time — three questions and
	// one line to answer them all in was a form with the boxes removed.
	questions []string
	at        int
	exchange  []persona.QA
	draft     *persona.Draft
	drafting  bool
	runID     int
	cancel    context.CancelFunc
	// started stamps the drafting turn, for the wait's elapsed.
	started time.Time
	// overwrite is set once the person has been told a file exists and
	// chosen to replace it.
	overwrite bool
}

// personaCommandName is the command the flow is opened by, named once so the
// surface's header and the manager's own row cannot drift apart.
const personaCommandName = "/agents new"

// personaDraftMsg carries a finished drafting turn back to the model.
type personaDraftMsg struct {
	runID   int
	outcome persona.Outcome
}

// personaSave is one row of the draft card that writes the file, and where
// it writes it. The rows and the scopes are declared together so the card
// cannot offer a place the save does not know how to write to.
type personaSave struct {
	option components.SelectOption
	scope  persona.Scope
}

// personaDrafting reports a drafting turn in flight, which is what keeps the
// tick chain running while the wait is on screen (spin.go).
func (m Model) personaDrafting() bool {
	return m.persona != nil && m.persona.drafting
}

// startPersona opens the surface. With a brief already typed it goes
// straight to the drafter; without one it asks, and offers a few starting
// points worded for this session, for a person who has the wish but not the
// sentence yet.
func (m Model) startPersona(brief string) (tea.Model, tea.Cmd) {
	if !m.personas.Enabled {
		return m.systemNotice("No model is configured to draft a profile. The reference in docs/agents/README.md says how to write one by hand.")
	}
	if m.persona != nil && m.persona.drafting {
		return m.systemNotice("Still drafting — the card opens when it is done.")
	}
	m.persona = &personaFlow{}
	m.personaScreen = components.NewProfileScreen(personaCommandName)
	m.personaScreen.Subject = m.personaSubject()
	m.enterSurface(statePersona)
	if brief = strings.TrimSpace(brief); brief != "" {
		return m.draftPersona(brief)
	}
	m.askPersonaBrief("")
	m.syncViewport()
	return m, nil
}

// personaSubject is the header's dim clause: which kind of profile this is
// and what the session already has. Someone about to describe a colleague is
// exactly the person who wants to know which ones already exist.
func (m Model) personaSubject() string {
	kind := "a coding agent"
	if m.personas.Kind == persona.KindChat {
		kind = "a chat colleague"
	}
	if m.personas.Existing == nil {
		return kind
	}
	existing := m.personas.Existing()
	if len(existing) == 0 {
		return kind + " · none yet"
	}
	return kind + " · " + strings.Join(existing, " ")
}

// askPersonaBrief puts the flow on its first step, with text already in the
// field when the person is coming back to it.
func (m Model) askPersonaBrief(text string) {
	ask := "What should this agent do? Say the job however you like — what it changes, what it checks, what it must leave alone."
	lead := "or start from one of these"
	if m.personas.Kind == persona.KindChat {
		ask = "What should this colleague be for? Say it however you like — a standpoint, a job, a voice."
	}
	m.personaScreen.AskBrief(ask, lead, persona.Suggestions(m.personas.Kind))
	m.personaScreen.SetText(text)
}

// draftPersona sends the brief — and the exchange and draft so far — to the
// drafter in the background, and puts the wait on the surface.
func (m Model) draftPersona(brief string) (tea.Model, tea.Cmd) {
	f := m.persona
	f.brief = brief
	return m.runDrafter(persona.Request{
		Kind:     m.personas.Kind,
		Brief:    brief,
		Exchange: drafterExchange(f.exchange),
		Models:   m.personas.Models,
	}, "drafting")
}

// refinePersona sends the draft back with the person's note.
func (m Model) refinePersona(feedback string) (tea.Model, tea.Cmd) {
	f := m.persona
	return m.runDrafter(persona.Request{
		Kind:     m.personas.Kind,
		Brief:    f.brief,
		Exchange: drafterExchange(f.exchange),
		Current:  f.draft,
		Feedback: feedback,
		Models:   m.personas.Models,
	}, "revising")
}

// drafterExchange is the exchange as the drafter is told it. An empty answer
// is an answer — the person has no preference — and it has to be said, because
// a blank beside a question reads as a question nobody asked.
func drafterExchange(qas []persona.QA) []persona.QA {
	if len(qas) == 0 {
		return nil
	}
	out := make([]persona.QA, len(qas))
	for i, qa := range qas {
		if strings.TrimSpace(qa.Answer) == "" {
			qa.Answer = "no preference"
		}
		out[i] = qa
	}
	return out
}

// runDrafter starts one drafting turn behind the wait.
func (m Model) runDrafter(req persona.Request, doing string) (tea.Model, tea.Cmd) {
	f := m.persona
	f.drafting = true
	f.started = time.Now()
	f.runID++
	runID := f.runID
	if m.personas.Existing != nil {
		req.Existing = m.personas.Existing()
	}
	draft := m.personas.Draft
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	m.personaScreen.Work(doing)
	m.syncViewport()
	// No tick is batched with it: Update applies the one-tick rule after
	// every message, so entering the wait resumes the chain on its own
	// (spin.go).
	return m, func() tea.Msg {
		defer cancel()
		return personaDraftMsg{runID: runID, outcome: draft(ctx, req)}
	}
}

// dropPersona retires a flow and the surface it was running on: /clear calls
// it, and so does every way out of the flow. A flow dropped with its surface
// left up would be a takeover holding the keyboard for a drafting that no
// longer exists.
func (m *Model) dropPersona() {
	if m.persona == nil && m.personaScreen == nil {
		return
	}
	if m.persona != nil && m.persona.cancel != nil {
		m.persona.cancel()
	}
	m.persona = nil
	m.personaScreen = nil
	if m.state == statePersona {
		m.leaveSurface()
	}
}

// closePersona takes the surface down and says what became of the draft.
func (m Model) closePersona(note string) (tea.Model, tea.Cmd) {
	m.dropPersona()
	m.syncViewport()
	return m.systemNotice(note)
}

// finishPersonaDraft applies a drafting turn: questions are asked one at a
// time, a draft opens the card.
func (m Model) finishPersonaDraft(msg personaDraftMsg) (tea.Model, tea.Cmd) {
	f := m.persona
	if f == nil || !f.drafting || msg.runID != f.runID {
		return m, nil
	}
	f.drafting = false
	f.cancel = nil
	o := msg.outcome
	if o.Failed {
		return m.closePersona("The profile could not be drafted — " + o.Err + ".")
	}
	if len(o.Questions) > 0 {
		f.questions, f.at = o.Questions, 0
		m.askPersonaQuestion("")
		m.syncViewport()
		return m, nil
	}
	f.draft = o.Draft
	m.openPersonaCard()
	m.syncViewport()
	return m, nil
}

// askPersonaQuestion puts the question the flow is standing on onto the
// surface, with text already in the field when the person stepped back to it.
func (m Model) askPersonaQuestion(text string) {
	f := m.persona
	m.personaScreen.AskQuestion(f.questions[f.at], f.at+1, len(f.questions))
	m.personaScreen.SetText(text)
}

// openPersonaCard puts the draft on the surface above the decision: where it
// lives, a revision with a note, or nothing.
func (m *Model) openPersonaCard() {
	d := m.persona.draft
	facts := []components.ProfileFact{{
		Label: "permissions",
		Value: d.Tier(),
		Tone:  personaTierTone(*d),
		Detail: map[bool]string{
			true:  "it can change things",
			false: "it reads and reports",
		}[d.Writes()],
	}}
	model := d.Model
	if model == "" {
		model = "inherited from this session"
	}
	facts = append(facts, components.ProfileFact{Label: "model", Value: model})
	if d.Reasoning != "" {
		facts = append(facts, components.ProfileFact{Label: "reasoning", Value: d.Reasoning})
	}
	if d.MaxTokens > 0 {
		facts = append(facts, components.ProfileFact{
			Label: "budget", Value: formatTokenCount(d.MaxTokens) + " tokens"})
	}
	saves := make([]components.SelectOption, 0, 2)
	for _, s := range m.personaSaves() {
		saves = append(saves, s.option)
	}
	m.personaScreen.Show(components.ProfileDraftView{
		Name:        d.Name,
		Description: d.Description,
		Facts:       facts,
		Why:         d.Why,
		Prompt:      d.Prompt,
	}, saves)
}

// personaSaves is the card's writing rows. A chat persona is the person's,
// not the project's, so chat offers one place to keep it; a coding agent's
// profile can belong to the work
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
func (m Model) personaSaves() []personaSave {
	project := m.personas.ProjectDir
	if project == "" {
		project = ".shhh/agents"
	}
	global := m.personas.GlobalDir
	if global == "" {
		global = "the config directory's agents/"
	}
	if m.personas.Kind == persona.KindChat {
		return []personaSave{{
			option: components.SelectOption{Label: "Save", Desc: global},
			scope:  persona.ScopeGlobal,
		}}
	}
	return []personaSave{
		{
			option: components.SelectOption{Label: "Save to this project", Desc: project},
			scope:  persona.ScopeProject,
		},
		{
			option: components.SelectOption{Label: "Save globally", Desc: global},
			scope:  persona.ScopeGlobal,
		},
	}
}

// personaTierTone reads the permission line the way a card field is read: a
// profile that can change something is the one the eye should find.
func personaTierTone(d persona.Draft) components.FieldTone {
	if d.Writes() {
		return components.ToneRisk
	}
	return components.ToneSafe
}

// updatePersona routes the surface's keys.
func (m Model) updatePersona(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.personaScreen == nil || m.persona == nil {
		return m.closePersona("No profile drafted.")
	}
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	done, result := m.personaScreen.Update(msg)
	if !done {
		m.syncViewport()
		return m, nil
	}
	res, ok := result.(components.ProfileResult)
	if !ok {
		return m, nil
	}
	switch res.Action {
	case components.ProfileTake:
		return m.takePersonaAnswer(res.Text)
	case components.ProfileBack:
		return m.stepPersonaBack()
	case components.ProfileAbort:
		return m.abortPersonaDraft()
	case components.ProfileSave:
		return m.savePersona(res.Index)
	case components.ProfileRefine:
		return m.refinePersona(res.Text)
	}
	return m.closePersona("Profile discarded.")
}

// takePersonaAnswer applies the line the step was waiting for: the brief
// starts a drafting, an answer moves to the next question and the last one
// starts the drafting the questions were asked for.
func (m Model) takePersonaAnswer(text string) (tea.Model, tea.Cmd) {
	f := m.persona
	if m.personaScreen.Step == components.ProfileBrief {
		return m.draftPersona(text)
	}
	// The exchange holds what the person actually typed, empty included, so
	// stepping back onto a question puts their own words back in the field.
	// Wording an empty answer for the drafter is the request's job
	// (drafterExchange).
	f.exchange = append(f.exchange, persona.QA{Question: f.questions[f.at], Answer: text})
	m.personaScreen.Answered(f.questions[f.at], text)
	if f.at++; f.at < len(f.questions) {
		m.askPersonaQuestion("")
		m.syncViewport()
		return m, nil
	}
	return m.draftPersona(f.brief)
}

// stepPersonaBack unwinds one exchange. From the first question it goes back
// to the brief, and from the brief it leaves — an esc that always meant
// "cancel the whole thing" made a mistyped answer cost the flow.
func (m Model) stepPersonaBack() (tea.Model, tea.Cmd) {
	f := m.persona
	if m.personaScreen.Step != components.ProfileQuestions {
		return m.closePersona("No profile drafted.")
	}
	if f.at == 0 {
		// Back to the brief, with what was typed still in the field. The
		// answers go with it: they were answers to questions asked about a
		// brief that is now being reconsidered.
		f.exchange, f.questions, f.at = nil, nil, 0
		m.askPersonaBrief(f.brief)
		m.syncViewport()
		return m, nil
	}
	f.at--
	last := f.exchange[len(f.exchange)-1]
	f.exchange = f.exchange[:len(f.exchange)-1]
	m.personaScreen.Forget()
	m.askPersonaQuestion(last.Answer)
	m.syncViewport()
	return m, nil
}

// abortPersonaDraft stops a drafting turn and hands the flow back to the
// brief, which is the step a person who stopped it is reconsidering.
func (m Model) abortPersonaDraft() (tea.Model, tea.Cmd) {
	f := m.persona
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	// The run number moves, so the turn's own result is dropped when it
	// arrives (finishPersonaDraft).
	f.drafting = false
	f.runID++
	f.exchange, f.questions, f.at = nil, nil, 0
	m.askPersonaBrief(f.brief)
	m.syncViewport()
	return m, nil
}

// savePersona writes the file the chosen row names.
func (m Model) savePersona(index int) (tea.Model, tea.Cmd) {
	f := m.persona
	saves := m.personaSaves()
	if f.draft == nil || index < 0 || index >= len(saves) {
		return m.closePersona("Profile discarded.")
	}
	path, err := m.personas.Save(saves[index].scope, *f.draft, f.overwrite)
	if err != nil {
		if path != "" && !f.overwrite {
			// The file exists. The card comes back with the choice made
			// explicit: saving again replaces it, or a note renames it.
			f.overwrite = true
			m.openPersonaCard()
			m.personaScreen.Warn(err.Error() + " Save again to replace it, or Refine with a new name.")
			m.syncViewport()
			return m, nil
		}
		return m.closePersona("Could not save the profile: " + err.Error())
	}
	name := f.draft.Name
	return m.closePersona(fmt.Sprintf(
		"Saved %s to %s. It is spawnable now as role %q; edit the file any time.", name, path, name))
}

// personaPane renders the surface into the transcript pane it takes over.
func (m Model) personaPane(width, height int) string {
	if m.personaScreen == nil {
		return ""
	}
	m.personaScreen.MaxLines = height
	m.personaScreen.Frame = m.spinFrame
	m.personaScreen.Elapsed = ""
	if m.persona != nil && m.persona.drafting && !m.persona.started.IsZero() {
		m.personaScreen.Elapsed = components.FormatElapsed(time.Since(m.persona.started))
	}
	return m.personaScreen.View(width)
}

// renderPersonaHint is the surface's bottom panel: it holds the keyboard, so
// the panel states what it is and nothing else.
func (m Model) renderPersonaHint() string {
	return sty.SystemMsg.Render("drafting a profile · "+
		keys.Bracket(keys.Profile.Back)+" "+keys.Words(keys.Profile.Back)) +
		strings.Repeat("\n", inputHeight-1)
}
