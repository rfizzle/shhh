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
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/config"
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
	// Roles is the same set read for the manager: what each role is for and
	// where the file that says so lives, so a profile drafted in conversation
	// can be found again (docs/interface/surfaces.md#the-agent-manager).
	Roles func() []SpawnableRole
	// Models the draft may name.
	Models []string
	// Save writes the draft under scope and registers the role with the
	// running session; it returns the path written.
	Save func(scope persona.Scope, d persona.Draft, overwrite bool) (string, error)
	// Reload reads a role's file again and registers what it now says with
	// the running session, the way Save registers a file it has just
	// written. It is what the manager's editor ends on, and an error is the
	// loader's refusal, with the running role left as it was.
	Reload func(path string) error
	// Open reads a role's file for the drafter's surface, and SaveOpened
	// writes a profile opened that way back over its own file — only what
	// changed, refusing a file changed on disk since it was opened — and
	// registers what it now says the way Save does
	// (docs/capabilities/subagents.md#an-older-profile-is-moved-into-sections-not-rewritten).
	Open       func(path string) (*persona.Source, error)
	SaveOpened func(src *persona.Source, d persona.Draft) (string, error)
	// ProjectDir and GlobalDir are the two places a file can go, for the
	// card to name.
	ProjectDir, GlobalDir string
}

// SpawnableRole is one role this session can spawn, as the manager lists it:
// what it is called, what it is for, and where it lives. Scope is the word
// the drafter's own card uses for the two places a file can go; a role shhh
// ships lives in neither and has no Path, which is what leaves its row
// nothing for enter to open.
type SpawnableRole struct {
	Name        string
	Description string
	Scope       string
	Path        string
	// Older says the file was written before the five sections, which the
	// manager marks and offers to move
	// (docs/capabilities/subagents.md#an-older-profile-is-moved-into-sections-not-rewritten).
	Older bool
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
	// sections is where each prose section of the draft stands, and
	// revisions every earlier standing of it, newest last, so esc can take
	// a revision back; both are keyed by the loader's section name and kept
	// for the life of the flow. The draft's own Sections are written from
	// sections at every revision (applySection), never the other way.
	sections  map[string]personaSection
	revisions map[string][]personaSection
	// redrafting is the section a drafting turn in flight is rewriting, or
	// "" when the turn drafts the whole profile.
	redrafting string
	// whole is a note on the whole draft in flight, and passes every one
	// that landed, newest last: what each changed, so esc on the row it
	// leaves takes back exactly those revisions, and what it kept.
	whole  *personaPass
	passes []personaPass
	passID int
	// pick is the Tools block's selector while it is open (personatools.go).
	pick *personaPick
	// source is the file a profile opened from the manager was read from;
	// nil for a profile drafted from a brief. migrating is the turn in
	// flight that moves it into the sections, and migrated says that turn
	// landed: its sections are marked as moved and the original prompt is
	// shown beside them. showDiff says the card's diff is up.
	source    *persona.Source
	scope     string
	migrating bool
	migrated  bool
	showDiff  bool
}

// personaSection is one prose section as it stands: its text, and what has
// happened to it — how many times the drafter rewrote it on a note, whether
// the person wrote it themselves, whether they cleared it. The mark after
// its heading is read off this.
type personaSection struct {
	body    string
	refined int
	mine    bool
	cleared bool
	// pass is the whole-draft note this standing came from, or 0. A pass is
	// taken back only from the sections still standing where it put them,
	// so a revision made on top of one is never popped in its name.
	pass int
	// migrated says the section is where a migration moved the file's own
	// text, which the mark says until it is revised.
	migrated bool
}

// personaPass is one note on the whole draft: the note, which sections it
// changed and which it sent as fixed context because the person wrote them.
type personaPass struct {
	id      int
	note    string
	changed []string
	kept    []string
}

// personaBlocks is the draft step's blocks in order: the five prose sections
// by their loader names, then the three field blocks the file keeps beside
// them. The index is what the surface hands back with a key.
var personaBlocks = append(config.PromptSectionNames(), personaToolsBlock, persona.SectionCommands, "Model")

// personaToolsBlock is the block that is a set of tiers and tools rather
// than prose. Enter on it is the door the tools-and-permissions selector
// comes in by (pickPersonaSection).
const personaToolsBlock = "Tools"

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
	// diff marks the row of a profile opened from its file that shows what
	// the save would change rather than saving it.
	diff bool
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
		return m.systemNotice("no model is configured to draft a profile. The reference in docs/agents/README.md says how to write one by hand")
	}
	if m.persona != nil && m.persona.drafting {
		return m.systemNotice("still drafting — the card opens when it is done")
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

// refinePersonaSection sends the draft back with the person's note about one
// section. The whole draft goes as fixed context and only that section is
// taken from the answer (finishSectionRedraft), which is what keeps a
// section the person wrote themselves — or was simply happy with — from
// being rewritten by a note about another one
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
func (m Model) refinePersonaSection(index int, note string) (tea.Model, tea.Cmd) {
	f := m.persona
	name := personaRevisable(index)
	if f.draft == nil || name == "" {
		return m, nil
	}
	f.redrafting = name
	// A copy, because the request is read on the drafting turn's own
	// goroutine and the flow's draft may be revised meanwhile once a stopped
	// turn hands the keyboard back.
	current := *f.draft
	if current.Sections != nil {
		sections := *current.Sections
		current.Sections = &sections
	}
	return m.runDrafter(persona.Request{
		Kind:     m.personas.Kind,
		Brief:    f.brief,
		Exchange: drafterExchange(f.exchange),
		Current:  &current,
		Section:  name,
		Feedback: note,
		Models:   m.personas.Models,
	}, "redrafting "+name)
}

// refinePersonaAll sends the draft back with one note for every section
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation). It
// is the section refine's request with no section named: the tiers, tools
// and fields go as fixed context, and so do the sections the person wrote,
// unless the note's second press said they go too.
func (m Model) refinePersonaAll(note string, include bool) (tea.Model, tea.Cmd) {
	f := m.persona
	if f.draft == nil {
		return m, nil
	}
	var keep []string
	if !include {
		for _, name := range config.PromptSectionNames() {
			if f.sections[name].mine {
				keep = append(keep, name)
			}
		}
	}
	f.whole = &personaPass{note: note, kept: keep}
	current := *f.draft
	if current.Sections != nil {
		sections := *current.Sections
		current.Sections = &sections
	}
	return m.runDrafter(persona.Request{
		Kind:     m.personas.Kind,
		Brief:    f.brief,
		Exchange: drafterExchange(f.exchange),
		Current:  &current,
		Keep:     keep,
		Feedback: note,
		Models:   m.personas.Models,
	}, "redrafting every section")
}

// personaProse is the prose section a block index names, or "" for a field
// block or an index past the end.
func personaProse(index int) string {
	names := config.PromptSectionNames()
	if index < 0 || index >= len(names) {
		return ""
	}
	return names[index]
}

// personaRevisable is the section a block index names where it is revised
// by a note, the editor or clearing: a prose section, or Commands.
func personaRevisable(index int) string {
	if name := personaProse(index); name != "" {
		return name
	}
	if index >= 0 && index < len(personaBlocks) && personaBlocks[index] == persona.SectionCommands {
		return persona.SectionCommands
	}
	return ""
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
	// The package's clock rather than time.Now, so a golden of the wait
	// reads the same elapsed on every run.
	f.started = clock()
	f.runID++
	runID := f.runID
	if m.personas.Existing != nil {
		req.Existing = m.personas.Existing()
	}
	draft := m.personas.Draft
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	if f.migrating {
		m.personaScreen.Migrating(doing)
	} else {
		m.personaScreen.Work(doing)
	}
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
// time, a draft opens the card, and a section's redraft lands on that
// section alone.
func (m Model) finishPersonaDraft(msg personaDraftMsg) (tea.Model, tea.Cmd) {
	f := m.persona
	if f == nil || !f.drafting || msg.runID != f.runID {
		return m, nil
	}
	f.drafting = false
	f.cancel = nil
	o := msg.outcome
	if f.migrating {
		return m.finishMigration(o)
	}
	if f.redrafting != "" {
		return m.finishSectionRedraft(o)
	}
	if f.whole != nil {
		return m.finishWholeRedraft(o)
	}
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
	f.resetSections()
	m.openPersonaCard()
	m.syncViewport()
	return m, nil
}

// finishSectionRedraft takes the one section the note was about out of the
// drafter's answer and nothing else: every other section, the tiers and the
// name stay as the draft had them, whatever the answer says. A turn that
// failed, asked instead of answering, or came back with the section empty
// leaves the section as it was and says so, because the draft on screen is
// every revision the person has made and none of them is worth a failed turn.
func (m Model) finishSectionRedraft(o persona.Outcome) (tea.Model, tea.Cmd) {
	f := m.persona
	name := f.redrafting
	f.redrafting = ""
	body := ""
	if o.Draft != nil && name == persona.SectionCommands {
		// The answer was normalised and validated as a whole draft, so
		// its two fields are already what the file may hold.
		body = o.Draft.CommandsText()
	} else if o.Draft != nil {
		for _, sec := range o.Draft.SectionList() {
			if sec.Name == name {
				body = strings.TrimSpace(sec.Body)
			}
		}
	}
	var warning string
	switch {
	case o.Failed:
		warning = name + " could not be redrafted — " + o.Err + ". It keeps its last text."
	case len(o.Questions) > 0:
		warning = name + " was not redrafted: the drafter asked " + strconv.Quote(o.Questions[0]) + " instead. Refine it again with the answer in the note."
	case body == "":
		warning = "The drafter answered with nothing for " + name + ". It keeps its last text."
	}
	if warning == "" {
		cur := f.sections[name]
		f.applySection(name, personaSection{body: body, refined: cur.refined + 1})
	}
	m.openPersonaCard()
	m.personaScreen.Warn(warning)
	m.syncViewport()
	return m, nil
}

// finishWholeRedraft takes every section the note could change out of the
// drafter's answer, each as a revision of its own, and nothing else: the
// tiers, tools and name stay as the draft had them whatever the answer says,
// which is what keeps a chat profile unable to write. A section the person
// wrote and the note kept is not read at all, and a section the answer left
// empty keeps its text, since an empty answer is a drafter that said
// nothing rather than one that cleared it. A failed or question-only turn
// keeps the draft, as a section's does.
func (m Model) finishWholeRedraft(o persona.Outcome) (tea.Model, tea.Cmd) {
	f := m.persona
	pass := *f.whole
	f.whole = nil
	var warning string
	switch {
	case o.Failed:
		warning = "The draft could not be redrafted — " + o.Err + ". Every section keeps its last text."
	case len(o.Questions) > 0:
		warning = "The draft was not redrafted: the drafter asked " + strconv.Quote(o.Questions[0]) + " instead. Refine it again with the answer in the note."
	case o.Draft == nil:
		warning = "The drafter answered with no draft. Every section keeps its last text."
	}
	if warning == "" {
		// Numbered for the life of the flow and never reused: a standing
		// an older note left can come back off a stack after that note's
		// row is gone, and must not be read as a newer note's.
		f.passID++
		pass.id = f.passID
		answer := map[string]string{}
		for _, sec := range o.Draft.SectionList() {
			answer[sec.Name] = strings.TrimSpace(sec.Body)
		}
		for _, name := range config.PromptSectionNames() {
			cur := f.sections[name]
			body := answer[name]
			if slices.Contains(pass.kept, name) || body == "" || body == cur.body {
				continue
			}
			f.applySection(name, personaSection{body: body, refined: cur.refined + 1, pass: pass.id})
			pass.changed = append(pass.changed, name)
		}
		if len(pass.changed) == 0 {
			warning = "The drafter changed no section on that note. The draft is as it was."
		} else {
			f.passes = append(f.passes, pass)
		}
	}
	m.openPersonaCard()
	if warning == "" {
		// The pointer lands on the note's own row, whose esc takes the whole
		// revision back: the act a person who disliked it reaches for first.
		m.personaScreen.Select(len(personaBlocks))
	}
	m.personaScreen.Warn(warning)
	m.syncViewport()
	return m, nil
}

// livePass is the newest whole-draft note that still has a section standing
// where it put it — the one the row under the sections offers to take back.
// A pass every section of which has since been taken back one at a time is
// dropped on the way, so the row falls to the note before it.
func (f *personaFlow) livePass() *personaPass {
	for len(f.passes) > 0 {
		p := &f.passes[len(f.passes)-1]
		for _, name := range p.changed {
			if f.sections[name].pass == p.id {
				return p
			}
		}
		f.passes = f.passes[:len(f.passes)-1]
	}
	return nil
}

// undoPass takes a whole-draft note back: one revision off each section it
// changed, and only off a section still standing where the note put it — a
// section revised again since, or already taken back on its own, is not
// popped in the note's name.
func (f *personaFlow) undoPass() bool {
	p := f.livePass()
	if p == nil {
		return false
	}
	for _, name := range p.changed {
		if f.sections[name].pass == p.id {
			f.undoSection(name)
		}
	}
	f.passes = f.passes[:len(f.passes)-1]
	return true
}

// resetSections reads a new draft's sections as the flow's starting point:
// a whole draft is a new draft, so nothing revised before it can be taken
// back into it.
func (f *personaFlow) resetSections() {
	f.sections = map[string]personaSection{}
	f.revisions = map[string][]personaSection{}
	f.passes = nil
	for _, sec := range f.draft.SectionList() {
		body := strings.TrimSpace(sec.Body)
		f.sections[sec.Name] = personaSection{body: body, migrated: f.migrated && body != ""}
	}
	f.sections[persona.SectionCommands] = personaSection{body: f.draft.CommandsText()}
}

// setDraftSection writes one section's standing into the draft: a prose one
// through SetSection, Commands through SetCommands. Every text that reaches
// it was checked where it was taken (personaCommandsFrom), so the Commands
// half cannot refuse here.
func (f *personaFlow) setDraftSection(name, body string) {
	if name == persona.SectionCommands {
		_ = f.draft.SetCommands(body)
		return
	}
	f.draft.SetSection(name, body)
}

// personaCommandsFrom is a Commands text as the draft would hold it, or the
// refusal it would meet — the loader's own words for an allow line — so a
// revision the file could not load is refused while it is still a card.
func personaCommandsFrom(text string) (string, error) {
	var d persona.Draft
	if err := d.SetCommands(text); err != nil {
		return "", err
	}
	return d.CommandsText(), nil
}

// applySection is every revision of a section: the standing it replaces is
// kept, and the draft's own section is written from the new one. It is the
// one writer of the draft's prompt after the drafter's answer, and it writes
// through SetSection — never the prompt itself, which Normalise would build
// again from the sections over a hand edit.
func (f *personaFlow) applySection(name string, next personaSection) {
	f.revisions[name] = append(f.revisions[name], f.sections[name])
	f.sections[name] = next
	f.setDraftSection(name, next.body)
}

// undoSection takes back a section's last revision, and reports whether it
// had one.
func (f *personaFlow) undoSection(name string) bool {
	history := f.revisions[name]
	if len(history) == 0 {
		return false
	}
	prev := history[len(history)-1]
	f.revisions[name] = history[:len(history)-1]
	f.sections[name] = prev
	f.setDraftSection(name, prev.body)
	return true
}

// askPersonaQuestion puts the question the flow is standing on onto the
// surface, with text already in the field when the person stepped back to it.
func (m Model) askPersonaQuestion(text string) {
	f := m.persona
	m.personaScreen.AskQuestion(f.questions[f.at], f.at+1, len(f.questions))
	m.personaScreen.SetText(text)
}

// openPersonaCard puts the draft on the surface above the decision: its
// sections one block each, in the order the file keeps them, over the card
// that writes it.
func (m *Model) openPersonaCard() {
	f := m.persona
	d := f.draft
	if f.sections == nil {
		f.resetSections()
	}
	blocks := make([]components.ProfileSection, 0, len(personaBlocks)+1)
	pass := f.livePass()
	for _, name := range config.PromptSectionNames() {
		sec := f.sections[name]
		mark, tone := f.mark(name, sec)
		if pass != nil && sec.mine && slices.Contains(pass.kept, name) {
			// Sent as fixed context with the note, and the result says so.
			mark += " · kept"
		}
		blocks = append(blocks, components.ProfileSection{
			Name:     name,
			Body:     sec.body,
			Mark:     mark,
			MarkTone: tone,
			Prose:    true,
			Revised:  len(f.revisions[name]) > 0,
			Mine:     sec.mine,
		})
	}
	var file *config.AgentDefinition
	if f.source != nil {
		file = &f.source.Def
	}
	blocks = append(blocks, personaToolsSection(*d),
		personaCommandsSection(f.sections[persona.SectionCommands], len(f.revisions[persona.SectionCommands]) > 0),
		personaModelSection(*d, file))
	if pass != nil {
		blocks = append(blocks, personaPassSection(*pass, f))
	}
	saves := make([]components.SelectOption, 0, 2)
	for _, s := range m.personaSaves() {
		saves = append(saves, s.option)
	}
	view := components.ProfileDraftView{
		Name:        d.Name,
		Description: d.Description,
		Sections:    blocks,
	}
	m.personaScreen.Migratable, m.personaScreen.OlderNote, m.personaScreen.Original = false, "", ""
	m.personaScreen.Diff = nil
	if src := f.source; src != nil {
		m.personaScreen.Migratable = !f.migrated && src.Older()
		m.personaScreen.OlderNote = personaOlderNote(src.Def)
		if f.migrated {
			view.Note = "moved into sections, nothing written yet"
			m.personaScreen.Original = src.Def.Prompt
		}
		if f.showDiff {
			lines, err := src.Diff(*d)
			switch {
			case err != nil:
				lines = []string{"the change could not be worked out — " + err.Error()}
			case len(lines) == 0:
				lines = []string{"nothing has changed yet · the file is as it stands"}
			}
			m.personaScreen.Diff = lines
		}
	}
	m.personaScreen.Show(view, saves)
}

// personaOlderNote is what the older-shape offer says about the file's
// prompt: the shape it is in, in the words the sections are read in.
func personaOlderNote(def config.AgentDefinition) string {
	filled := 0
	for _, sec := range def.Sections() {
		if strings.TrimSpace(sec.Body) != "" {
			filled++
		}
	}
	switch {
	case filled == 0:
		return "the file has no prompt yet"
	case filled == 1 && def.Sections()[0].Body != "":
		return "the file's prompt is one block, read here as Purpose"
	}
	return "the file's prompt leaves a section empty"
}

// mark is what the mark after a section's heading says, for a profile
// opened from its file as well as a drafted one: a file in the older shape
// is read as it stands, a migration's sections are marked as moved until
// they are revised, and a gap the file left is the file's rather than the
// drafter's (docs/capabilities/subagents.md#an-older-profile-is-moved-into-sections-not-rewritten).
func (f *personaFlow) mark(name string, s personaSection) (string, components.ProfileMarkTone) {
	if f.source == nil || s.mine || s.cleared || s.refined > 0 {
		return personaMark(name, s)
	}
	switch {
	case f.migrated && s.body == "":
		return "⚠ empty · the file says nothing about it", components.ProfileMarkEmpty
	case s.migrated:
		return "· migrated", components.ProfileMarkQuiet
	case f.source.Older() && s.body == "":
		return "· not in the file", components.ProfileMarkQuiet
	case f.source.Older():
		return "· as the file wrote it", components.ProfileMarkQuiet
	}
	return personaMark(name, s)
}

// personaPassSection is the row a note on the whole draft leaves under the
// sections: the note, and which sections it changed and still stand as it
// left them — the ones esc on the row takes back.
func personaPassSection(p personaPass, f *personaFlow) components.ProfileSection {
	var standing []string
	for _, name := range p.changed {
		if f.sections[name].pass == p.id {
			standing = append(standing, name)
		}
	}
	detail := personaList(standing) + " changed"
	if len(p.kept) > 0 {
		detail += " · " + personaList(p.kept) + " kept as you wrote " + personaItThem(p.kept)
	}
	return components.ProfileSection{
		Name:   "Whole draft",
		Mark:   "· redrafted on your note",
		Value:  strconv.Quote(p.note),
		Tone:   components.ToneQuiet,
		Detail: detail,
		Whole:  true,
	}
}

// personaList is section names as a sentence lists them.
func personaList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// personaItThem agrees a sentence with how many sections it names.
func personaItThem(names []string) string {
	if len(names) == 1 {
		return "it"
	}
	return "them"
}

// personaMark is what the mark after a section's heading says about it. An
// empty section is marked whatever else happened to it, because a required
// section with nothing in it is the thing to find before saving; a section
// the person wrote is theirs, which is the mark no refine may take away; and
// the drafter's own rewrites are counted, dim.
func personaMark(name string, s personaSection) (string, components.ProfileMarkTone) {
	switch {
	case name == persona.SectionCommands && s.body == "" && (s.cleared || s.mine):
		// Optional: an empty Commands section is the session's own lists,
		// which is no gap to find before saving.
		return "· you cleared it", components.ProfileMarkQuiet
	case name == persona.SectionCommands && s.body == "":
		return "", components.ProfileMarkQuiet
	case s.body == "" && (s.cleared || s.mine):
		// Emptied by the person, with x or in the editor: the drafter did
		// not leave it, and the mark must not say it did.
		return "⚠ empty · you cleared it", components.ProfileMarkEmpty
	case s.body == "":
		return "⚠ empty · the drafter left it; " + personaRequired[name], components.ProfileMarkEmpty
	case s.mine:
		return "· edited by you", components.ProfileMarkMine
	case s.refined > 0:
		return "· refined " + personaTimes(s.refined), components.ProfileMarkQuiet
	}
	return "", components.ProfileMarkQuiet
}

// personaRequired says each section is required in a sentence that reads.
var personaRequired = map[string]string{
	config.SectionPurpose:      "a purpose is required",
	config.SectionScope:        "a scope is required",
	config.SectionRestrictions: "restrictions are required",
	config.SectionMethod:       "a method is required",
	config.SectionReport:       "a report is required",
}

// personaTimes is a count of rewrites as a sentence says it.
func personaTimes(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return strconv.Itoa(n) + " times"
}

// personaToolsSection is the Tools & permissions block: the tiers in words
// and what they let the agent do, then the tools it narrows to and any the
// session could not grant.
func personaToolsSection(d persona.Draft) components.ProfileSection {
	detail := []string{"it reads and reports"}
	if d.Writes() {
		detail = []string{"it can change things, in its own copy of the tree"}
	}
	if len(d.Tools) > 0 {
		// A narrowed profile gets fewer tools than its tier grants, and the
		// draft is where the file is agreed to: a person confirming "read"
		// should see that it is three tools rather than all of them.
		detail = append(detail, strings.Join(d.Tools, ", "))
	}
	if len(d.Dropped) > 0 {
		// The drafter named tools this session cannot grant and they were
		// taken off; the person agreeing to the file should see that the
		// file differs from what was proposed.
		detail = append(detail, "dropped "+strings.Join(d.Dropped, ", ")+" — a chat persona only reads")
	}
	return components.ProfileSection{
		Name:   personaToolsBlock,
		Value:  d.Tier(),
		Tone:   personaTierTone(d),
		Detail: strings.Join(detail, " · "),
		Pick:   "open the selector",
	}
}

// personaCommandsSection is the Commands block: what the agent's commands
// are for and what it must never run, on one line the way the artboard
// draws it, or what applies instead where the draft states neither. It is a
// field block revised the way a prose section is — a note to the drafter,
// the editor over its `intent:`/`deny:` lines, x to clear — so its standing
// is kept beside the prose sections' and esc takes a revision back the
// same way (docs/capabilities/subagents.md#a-profile-is-a-file).
func personaCommandsSection(s personaSection, revised bool) components.ProfileSection {
	var d persona.Draft
	// The standing's text was checked when it was applied, so reading it
	// back cannot fail; a cleared one reads as a draft stating neither.
	_ = d.SetCommands(s.body)
	mark, tone := personaMark(persona.SectionCommands, s)
	sec := components.ProfileSection{
		Name:      persona.SectionCommands,
		Body:      s.body,
		Mark:      mark,
		MarkTone:  tone,
		Revised:   revised,
		Revisable: true,
	}
	var parts []string
	if d.Intent != "" {
		parts = append(parts, "for: "+d.Intent)
	}
	if len(d.Deny) > 0 {
		parts = append(parts, "never: "+strings.Join(d.Deny, ", "))
	}
	if len(parts) == 0 {
		sec.Value, sec.Tone, sec.Detail = "none stated", components.ToneQuiet, "the session's own command lists apply"
		return sec
	}
	sec.Value, sec.Detail = parts[0], strings.Join(parts[1:], " · ")
	return sec
}

// personaModelSection is the Model & budget block: each value, or the
// session's own where the draft names none. A profile opened from its file
// shows every field the file sets, the ones a draft could not propose
// included, since the save keeps them and the person is agreeing to the file.
func personaModelSection(d persona.Draft, file *config.AgentDefinition) components.ProfileSection {
	model := d.Model
	if model == "" {
		model = "inherited from this session"
	}
	var detail []string
	if d.Reasoning != "" {
		detail = append(detail, "reasoning "+d.Reasoning)
	}
	if file != nil {
		if mode := strings.TrimSpace(file.Mode); mode != "" {
			detail = append(detail, mode)
		}
		if file.MaxRounds > 0 {
			detail = append(detail, strconv.Itoa(file.MaxRounds)+" rounds")
		}
		if file.Reviews {
			detail = append(detail, "reviews")
		}
		if file.Inherit > 0 {
			detail = append(detail, "inherits "+strconv.Itoa(file.Inherit)+" turns")
		}
		if strings.EqualFold(strings.TrimSpace(file.PromptMode), config.PromptReplace) {
			detail = append(detail, "replaces the base prompt")
		}
	}
	if d.MaxTokens > 0 {
		detail = append(detail, formatTokenCount(d.MaxTokens)+" tokens")
	}
	return components.ProfileSection{Name: "Model", Value: model, Detail: strings.Join(detail, " · ")}
}

// personaSaves is the card's writing rows. A chat persona is the person's,
// not the project's, so chat offers one place to keep it; a coding agent's
// profile can belong to the work
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
func (m Model) personaSaves() []personaSave {
	if f := m.persona; f != nil && f.source != nil {
		// The file is the profile's own: the save replaces it, and the row
		// beside it shows what that would change before anything is written.
		return []personaSave{
			{option: components.SelectOption{Label: "Replace its file", Desc: f.source.Path}},
			{option: components.SelectOption{Label: "See what changes", Desc: "the file as it stands against the file as it would be written"}, diff: true},
		}
	}
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
	done, res := m.personaScreen.Update(msg)
	if ms := m.personaScreen.Picker; ms != nil && m.persona.pick != nil {
		// The rules are asked again after every keystroke the selector
		// takes, so what it refuses is on the card before enter is pressed.
		m.persona.refreshPick(ms, m.persona.draft.Name)
	}
	if !done {
		m.syncViewport()
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
		return m.refinePersonaSection(res.Index, res.Text)
	case components.ProfileRefineAll:
		return m.refinePersonaAll(res.Text, res.Include)
	case components.ProfileEdit:
		return m.editPersonaSection(res.Index)
	case components.ProfileClear:
		return m.clearPersonaSection(res.Index)
	case components.ProfileUndo:
		return m.undoPersonaSection(res.Index)
	case components.ProfilePick:
		return m.pickPersonaSection(res.Index)
	case components.ProfilePicked:
		return m.takePersonaPick()
	case components.ProfileUnpicked:
		return m.dropPersonaPick()
	case components.ProfileMigrate:
		return m.migratePersona()
	}
	if f := m.persona; f.source != nil {
		return m.closePersona(f.source.Def.Name + " is as its file has it; nothing was written.")
	}
	return m.closePersona("Profile discarded.")
}

// clearPersonaSection empties a section. It is a revision like any other,
// so esc gives the text back.
func (m Model) clearPersonaSection(index int) (tea.Model, tea.Cmd) {
	f := m.persona
	name := personaRevisable(index)
	if f.draft == nil || name == "" || f.sections[name].body == "" {
		return m, nil
	}
	cur := f.sections[name]
	f.applySection(name, personaSection{refined: cur.refined, cleared: true})
	m.openPersonaCard()
	m.syncViewport()
	return m, nil
}

// undoPersonaSection takes back the section's last revision — a refine, an
// edit or a clear — and nothing else on the draft moves.
func (m Model) undoPersonaSection(index int) (tea.Model, tea.Cmd) {
	f := m.persona
	if f.draft != nil && index == len(personaBlocks) {
		// The whole-draft note's own row: one revision off every section
		// it changed.
		if f.undoPass() {
			m.openPersonaCard()
			m.syncViewport()
		}
		return m, nil
	}
	name := personaRevisable(index)
	if f.draft == nil || name == "" || !f.undoSection(name) {
		return m, nil
	}
	m.openPersonaCard()
	m.syncViewport()
	return m, nil
}

// personaEditorDoneMsg is the editor's exit over one section's text. It
// carries the flow it was opened for, so an edit that returns to a flow that
// has since been dropped is dropped with it.
type personaEditorDoneMsg struct {
	flow    *personaFlow
	section string
	path    string
	err     error
}

// editPersonaSection hands one section's text to the person's editor, the
// draft editor's way (editorArgv, over a temporary file the finish removes).
// It asks the terminal half of the draft editor's refusal and not the draft
// half: this surface holds the keyboard itself, which is exactly what the
// draft half refuses.
func (m Model) editPersonaSection(index int) (tea.Model, tea.Cmd) {
	f := m.persona
	name := personaRevisable(index)
	if f.draft == nil || name == "" {
		return m, nil
	}
	if reason, refused := m.terminalRefusal(); refused {
		m.personaScreen.Warn(reason)
		m.syncViewport()
		return m, nil
	}
	text := f.sections[name].body + "\n"
	if name == persona.SectionCommands {
		text = personaCommandsGuide + text
	}
	path, err := writeDraftFile(text)
	if err != nil {
		m.personaScreen.Warn("could not write " + name + " out for the editor — " + err.Error())
		m.syncViewport()
		return m, nil
	}
	argv := editorArgv(editorCommand(), path, 1, 1)
	proc := exec.Command(argv[0], argv[1:]...)
	return m, tea.ExecProcess(proc, func(err error) tea.Msg {
		return personaEditorDoneMsg{flow: f, section: name, path: path, err: err}
	})
}

// personaCommandsGuide heads the Commands text handed to the editor: the two
// line shapes it is read back in, as comments the reading skips.
const personaCommandsGuide = "# intent: what this agent's commands are for, in a sentence\n" +
	"# deny: a command prefix it must never run — one line each\n" +
	"# a profile has no allow list: only your own settings or a card let a command run unasked\n"

// personaEdited is a section's text as the editor left it, read the way the
// section is kept: a prose section trimmed, Commands through its two line
// shapes, which may refuse.
func personaEdited(section, content string) (string, error) {
	if section == persona.SectionCommands {
		return personaCommandsFrom(content)
	}
	return strings.TrimSpace(content), nil
}

// personaEditorFinished takes the edited text back into its section and
// marks it as the person's own, which no later refine of another section
// touches. Every exit the editor can make arrives here, which is what makes
// this the one place the temporary file is removed.
func (m Model) personaEditorFinished(msg personaEditorDoneMsg) (tea.Model, tea.Cmd) {
	defer func() { _ = os.Remove(msg.path) }()
	f := m.persona
	if f == nil || f != msg.flow || f.draft == nil || m.personaScreen == nil {
		return m, nil
	}
	var warning string
	if msg.err != nil {
		warning = "the editor exited with an error, so " + msg.section + " is as it was — " + msg.err.Error()
	} else if content, err := os.ReadFile(msg.path); err != nil {
		warning = "could not read " + msg.section + " back, so it is as it was — " + err.Error()
	} else if body, err := personaEdited(msg.section, string(content)); err != nil {
		warning = msg.section + " is as it was — " + err.Error()
	} else if body != f.sections[msg.section].body {
		cur := f.sections[msg.section]
		f.applySection(msg.section, personaSection{body: body, refined: cur.refined, mine: true})
	}
	m.openPersonaCard()
	m.personaScreen.Warn(warning)
	m.syncViewport()
	return m, nil
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
// brief, which is the step a person who stopped it is reconsidering — or,
// for a section's redraft, back to the draft with the section as it was.
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
	if f.migrating {
		// Stopped before it landed: the profile is as the file has it.
		f.migrating = false
		m.openPersonaCard()
		m.syncViewport()
		return m, nil
	}
	if (f.redrafting != "" || f.whole != nil) && f.draft != nil {
		f.redrafting, f.whole = "", nil
		m.openPersonaCard()
		m.syncViewport()
		return m, nil
	}
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
	if f.source != nil {
		return m.saveOpenedPersona(saves[index])
	}
	path, err := m.personas.Save(saves[index].scope, *f.draft, f.overwrite)
	if err != nil {
		if path != "" && !f.overwrite {
			// The file exists. The card comes back with the choice made
			// explicit: saving again replaces it.
			f.overwrite = true
			m.openPersonaCard()
			m.personaScreen.Warn(err.Error() + " Save again to replace it, or Discard it.")
			m.syncViewport()
			return m, nil
		}
		// The loader refused the profile. The draft stays on the card with
		// the refusal under it, because a refusal is usually something a
		// revision can fix, and closing the card would cost the person the
		// draft they came to keep.
		m.openPersonaCard()
		m.personaScreen.Warn("Could not save the profile — " + err.Error() + ". Revise a section, or Discard it.")
		m.syncViewport()
		return m, nil
	}
	name := f.draft.Name
	return m.closePersona(fmt.Sprintf(
		"Saved %s to %s. It is spawnable now as role %q; edit the file any time.", name, path, name))
}

// openPersonaProfile opens a role's file on the drafter's draft step, the
// way a draft is edited: its sections from the file, its tiers and tools on
// the selector, every field it sets on the Model block. With migrate set the
// profile is sent to be moved into the sections at once, the manager's m.
// See docs/interface/surfaces.md#the-profile-drafter.
func (m Model) openPersonaProfile(name string, migrate bool) (tea.Model, tea.Cmd) {
	var role SpawnableRole
	for _, r := range m.spawnableRoles() {
		if r.Name == name {
			role = r
		}
	}
	if role.Path == "" {
		return m, nil
	}
	if m.personas.Open == nil {
		return m.openRoleEditor(name)
	}
	if m.persona != nil && m.persona.drafting {
		return m.surfaceNotice("still drafting — the card opens when it is done")
	}
	src, err := m.personas.Open(role.Path)
	if err != nil {
		return m.surfaceNotice("could not open " + role.Path + " — " + err.Error())
	}
	d := persona.FromDefinition(src.Def)
	m.persona = &personaFlow{draft: &d, source: src, scope: role.Scope}
	screen := components.NewProfileScreen(src.Def.Name)
	screen.FromFile = true
	screen.Subject = role.Path
	if role.Scope != "" {
		screen.Subject += " · " + role.Scope
	}
	screen.DiscardDesc = "the file stays as it is"
	m.personaScreen = screen
	m.enterSurface(statePersona)
	m.openPersonaCard()
	if migrate && m.personaScreen.Migratable {
		return m.migratePersona()
	}
	m.syncViewport()
	return m, nil
}

// migratePersona sends a profile opened in the older shape to the drafter to
// be moved into the sections. Nothing is written by it: the answer lands on
// the draft step, to be reviewed and saved from the card like any draft. A
// profile with no prompt has nothing to move, so no request is spent on it.
// See docs/capabilities/subagents.md#an-older-profile-is-moved-into-sections-not-rewritten.
func (m Model) migratePersona() (tea.Model, tea.Cmd) {
	f := m.persona
	if f == nil || f.source == nil || f.migrated || f.drafting {
		return m, nil
	}
	def := f.source.Def
	if strings.TrimSpace(def.Prompt) == "" {
		m.personaScreen.Warn(def.Name + " has no prompt to move. Write each section with enter or e, then save.")
		m.syncViewport()
		return m, nil
	}
	f.migrating = true
	return m.runDrafter(persona.Request{
		Kind:   m.personas.Kind,
		Source: &def,
		Models: m.personas.Models,
	}, "moving "+def.Name+" into sections")
}

// finishMigration lands a migration's answer as the draft's starting point:
// each section the migration filled is marked as moved, a gap it left is
// marked empty, and the file's prompt is shown beside them. It is a new
// starting point rather than a revision, so esc on an unrevised section
// drops the migration with the file untouched. A failed turn leaves the
// profile as the file has it, and says so.
func (m Model) finishMigration(o persona.Outcome) (tea.Model, tea.Cmd) {
	f := m.persona
	f.migrating = false
	if o.Failed || o.Draft == nil {
		why := o.Err
		if why == "" {
			why = "the drafter answered with no sections"
		}
		m.openPersonaCard()
		m.personaScreen.Warn(f.source.Def.Name + " could not be moved into sections — " + why + ". The file is as it was.")
		m.syncViewport()
		return m, nil
	}
	f.draft = o.Draft
	f.migrated = true
	f.resetSections()
	m.personaScreen.Select(0)
	m.openPersonaCard()
	m.syncViewport()
	return m, nil
}

// saveOpenedPersona takes a row of an opened profile's card: the diff, or
// the save that replaces the file. A refusal — a file changed since it was
// opened, a profile the loader would not read, an untrusted checkout — leaves
// the draft on the card with the sentence under it and the file untouched.
func (m Model) saveOpenedPersona(row personaSave) (tea.Model, tea.Cmd) {
	f := m.persona
	if row.diff {
		f.showDiff = true
		m.openPersonaCard()
		m.syncViewport()
		return m, nil
	}
	if m.personas.SaveOpened == nil {
		return m.closePersona("Profile discarded.")
	}
	path, err := m.personas.SaveOpened(f.source, *f.draft)
	if err != nil {
		m.openPersonaCard()
		m.personaScreen.Warn("Not saved — " + err.Error() + ". The draft is still here.")
		m.syncViewport()
		return m, nil
	}
	name := f.draft.Name
	return m.closePersona(fmt.Sprintf(
		"Saved %s to %s. The next %s this session spawns is the file as it now reads.", name, path, name))
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
		m.personaScreen.Elapsed = components.FormatElapsed(clock().Sub(m.persona.started))
	}
	return m.personaScreen.View(width)
}

// renderPersonaHint is the surface's bottom panel: it holds the keyboard, so
// the panel states what it is and nothing else.
func (m Model) renderPersonaHint() string {
	return sty.SystemMsg.Render("drafting a profile · ") + seg(keys.Profile.Back).render()
}
