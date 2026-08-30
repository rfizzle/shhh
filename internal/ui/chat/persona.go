package chat

// /agents new: a profile drafted in conversation
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
// The flow is three exchanges at most — a brief, perhaps a few questions,
// then a draft on a card — and the person's words go straight to the
// drafter each time. The card is the decision: where the file lives, a
// note that sends the draft back for revision, or nothing.
//
// The drafting is a background command like the backlog reading: nothing
// on screen waits for it, and a result arriving after /clear is dropped by
// its run number.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

type personaWait int

const (
	personaIdle personaWait = iota
	// personaWaitBrief: the question was asked; the next line is the brief.
	personaWaitBrief
	// personaWaitAnswers: questions were asked; the next line answers them.
	personaWaitAnswers
)

// personaFlow is one drafting in progress.
type personaFlow struct {
	waiting   personaWait
	brief     string
	questions []string
	exchange  []persona.QA
	draft     *persona.Draft
	drafting  bool
	runID     int
	cancel    context.CancelFunc
	// overwrite is set once the person has been told a file exists and
	// chosen to replace it.
	overwrite bool
}

// personaDraftMsg carries a finished drafting turn back to the model.
type personaDraftMsg struct {
	runID   int
	outcome persona.Outcome
}

// personaHoldsInput reports that the next typed line is the flow's, not
// the model's.
func (m *Model) personaHoldsInput() bool {
	return m.persona != nil && m.persona.waiting != personaIdle
}

// startPersona begins a drafting: with a brief, straight to the drafter;
// without one, the question and a few starting points worded for this
// session, for a person who has the wish but not the sentence yet.
func (m Model) startPersona(brief string) (tea.Model, tea.Cmd) {
	if !m.personas.Enabled {
		return m.systemNotice("No model is configured to draft a profile. The reference in docs/agents/README.md says how to write one by hand.")
	}
	if m.persona != nil && m.persona.drafting {
		return m.systemNotice("Still drafting — the card opens when it is done.")
	}
	m.persona = &personaFlow{}
	if brief = strings.TrimSpace(brief); brief != "" {
		return m.draftPersona(brief)
	}
	m.persona.waiting = personaWaitBrief
	var b strings.Builder
	if m.personas.Kind == persona.KindChat {
		b.WriteString("What should this colleague be for? Say it however you like — a standpoint, a job, a voice. Pick a number to start from one of these, or type your own:\n")
	} else {
		b.WriteString("What should this agent do? Say the job however you like — what it changes, what it checks, what it must leave alone. Pick a number to start from one of these, or type your own:\n")
	}
	for i, s := range persona.Suggestions(m.personas.Kind) {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, s)
	}
	b.WriteString("(an empty line or \"cancel\" stops)")
	return m.systemNotice(strings.TrimRight(b.String(), "\n"))
}

// answerPersona takes the line the flow was waiting for.
func (m Model) answerPersona(text string) (tea.Model, tea.Cmd) {
	f := m.persona
	text = strings.TrimSpace(text)
	if text == "" || strings.EqualFold(text, "cancel") {
		m.persona = nil
		return m.systemNotice("No profile drafted.")
	}
	switch f.waiting {
	case personaWaitBrief:
		if n, err := strconv.Atoi(text); err == nil {
			if s := persona.Suggestions(m.personas.Kind); n >= 1 && n <= len(s) {
				text = s[n-1]
			}
		}
		return m.draftPersona(text)
	case personaWaitAnswers:
		f.exchange = append(f.exchange, persona.QA{Question: strings.Join(f.questions, " / "), Answer: text})
		f.questions = nil
		return m.draftPersona(f.brief)
	}
	return m, nil
}

// draftPersona sends the brief — and the exchange and draft so far — to
// the drafter in the background.
func (m Model) draftPersona(brief string) (tea.Model, tea.Cmd) {
	f := m.persona
	f.brief = brief
	f.waiting = personaIdle
	f.drafting = true
	f.runID++
	runID := f.runID
	req := persona.Request{Kind: m.personas.Kind, Brief: brief, Exchange: f.exchange, Models: m.personas.Models}
	if m.personas.Existing != nil {
		req.Existing = m.personas.Existing()
	}
	draft := m.personas.Draft
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	model, _ := m.systemNotice("Drafting…")
	return model, func() tea.Msg {
		defer cancel()
		return personaDraftMsg{runID: runID, outcome: draft(ctx, req)}
	}
}

// refinePersona sends the draft back with the person's note.
func (m Model) refinePersona(feedback string) (tea.Model, tea.Cmd) {
	f := m.persona
	f.drafting = true
	f.runID++
	runID := f.runID
	req := persona.Request{Kind: m.personas.Kind, Brief: f.brief, Exchange: f.exchange, Current: f.draft, Feedback: feedback, Models: m.personas.Models}
	if m.personas.Existing != nil {
		req.Existing = m.personas.Existing()
	}
	draft := m.personas.Draft
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	model, _ := m.systemNotice("Revising…")
	return model, func() tea.Msg {
		defer cancel()
		return personaDraftMsg{runID: runID, outcome: draft(ctx, req)}
	}
}

// dropPersona retires a flow: /clear calls it.
func (m *Model) dropPersona() {
	if m.persona == nil {
		return
	}
	if m.persona.cancel != nil {
		m.persona.cancel()
	}
	m.persona = nil
}

// finishPersonaDraft applies a drafting turn: questions go to the
// transcript and wait for one line of answers; a draft opens the card.
func (m Model) finishPersonaDraft(msg personaDraftMsg) (tea.Model, tea.Cmd) {
	f := m.persona
	if f == nil || !f.drafting || msg.runID != f.runID {
		return m, nil
	}
	f.drafting = false
	f.cancel = nil
	o := msg.outcome
	if o.Failed {
		m.persona = nil
		return m.systemNotice("The profile could not be drafted — " + o.Err + ".")
	}
	if len(o.Questions) > 0 {
		f.questions = o.Questions
		f.waiting = personaWaitAnswers
		var b strings.Builder
		b.WriteString("Before drafting, a few questions — answer them in one line, in order (or \"cancel\"):\n")
		for i, q := range o.Questions {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, q)
		}
		return m.systemNotice(strings.TrimRight(b.String(), "\n"))
	}
	f.draft = o.Draft
	return m.openPersonaCard()
}

// openPersonaCard shows the draft above the decision: where it lives, a
// revision with a note, or nothing.
func (m Model) openPersonaCard() (tea.Model, tea.Cmd) {
	project := m.personas.ProjectDir
	global := m.personas.GlobalDir
	if project == "" {
		project = ".shhh/agents"
	}
	if global == "" {
		global = "the config directory's agents/"
	}
	// A chat persona is the person's, not the project's: chat offers one
	// place to keep it. A coding agent's profile can belong to the work
	// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
	opts := []components.SelectOption{{Label: "Save", Desc: global}}
	if m.personas.Kind == persona.KindCode {
		opts = []components.SelectOption{
			{Label: "Save to this project", Desc: project},
			{Label: "Save globally", Desc: global},
		}
	}
	opts = append(opts,
		components.SelectOption{Label: "Refine", Desc: "say what to change in the note", RequireNote: true},
		components.SelectOption{Label: "Discard"},
	)
	ns := components.NewNoteSelect("Keep this profile?", opts)
	ns.Note.Placeholder = "what to change (for Refine)"
	ns.Select.MaxLines = m.maxConfirmPanelHeight() - 1
	m.personaAsk = ns
	m.enterSurface(statePersona)
	m.syncViewport()
	return m, nil
}

// updatePersona routes the card's keys.
func (m Model) updatePersona(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	done, result := m.personaAsk.Update(msg)
	if !done {
		return m, nil
	}
	res := result.(components.NoteSelectResult)
	m.personaAsk = nil
	m.leaveSurface()
	m.syncViewport()
	f := m.persona
	// The card's rows: the save rows first, then Refine, then Discard.
	saves := 1
	if m.personas.Kind == persona.KindCode {
		saves = 2
	}
	if res.Canceled || res.Index > saves || f == nil || f.draft == nil {
		m.persona = nil
		return m.systemNotice("Profile discarded.")
	}
	if res.Index == saves {
		return m.refinePersona(res.Note)
	}
	scope := persona.ScopeGlobal
	if m.personas.Kind == persona.KindCode && res.Index == 0 {
		scope = persona.ScopeProject
	}
	path, err := m.personas.Save(scope, *f.draft, f.overwrite)
	if err != nil {
		if path != "" && !f.overwrite {
			// The file exists. The card comes back with the choice made
			// explicit: saving again replaces it, or a note renames it.
			f.overwrite = true
			model, _ := m.systemNotice(err.Error() + " Save again to replace it, or Refine with a new name.")
			return model.(Model).openPersonaCard()
		}
		m.persona = nil
		return m.systemNotice("Could not save the profile: " + err.Error())
	}
	name := f.draft.Name
	m.persona = nil
	return m.systemNotice(fmt.Sprintf("Saved %s to %s. It is spawnable now as role %q; edit the file any time.", name, path, name))
}

// personaLines renders the draft above the card: the facts a decision
// needs, and the prompt as far as the panel allows.
func (m Model) personaLines() []string {
	if m.personaAsk == nil || m.persona == nil || m.persona.draft == nil {
		return nil
	}
	d := m.persona.draft
	width := m.contentWidth()
	var lines []string
	add := func(text string, style func(...string) string) {
		for _, l := range strings.Split(m.wordWrap(text, width), "\n") {
			lines = append(lines, style(l))
		}
	}
	head := fmt.Sprintf("%s — %s", d.Name, d.Description)
	add(head, sty.User.Render)
	facts := "tier: " + d.Tier()
	if d.Model != "" {
		facts += " · model: " + d.Model
	}
	if d.Reasoning != "" {
		facts += " · reasoning: " + d.Reasoning
	}
	if d.MaxTokens > 0 {
		facts += fmt.Sprintf(" · budget: %d tokens", d.MaxTokens)
	}
	add(facts, sty.Welcome.Render)
	if d.Why != "" {
		add("why: "+d.Why, sty.Welcome.Render)
	}
	// The prompt is the profile; show what fits and say what did not.
	budget := m.maxConfirmPanelHeight() - len(lines) - 7
	if budget < 3 {
		budget = 3
	}
	prompt := strings.Split(m.wordWrap(d.Prompt, width), "\n")
	if len(prompt) > budget {
		prompt = append(prompt[:budget-1], fmt.Sprintf("… %d more lines in the file", len(prompt)-budget+1))
	}
	for _, l := range prompt {
		lines = append(lines, sty.Assistant.Render(l))
	}
	return append(lines, strings.Split(m.personaAsk.View(width), "\n")...)
}

// renderPersona renders the card padded to the bottom panel height.
func (m Model) renderPersona() string {
	lines := m.personaLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}
