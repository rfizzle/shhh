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
}

// personaBlocks is the draft step's blocks in order: the five prose sections
// by their loader names, then the three field blocks the file keeps beside
// them. The index is what the surface hands back with a key.
var personaBlocks = append(config.PromptSectionNames(), personaToolsBlock, "Commands", "Model")

// personaToolsBlock is the block that is a set of tiers and tools rather
// than prose. Enter on it is the door the tools-and-permissions selector
// comes in by (pickPersonaSection); until that selector exists it opens
// nothing.
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
	name := personaProse(index)
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

// personaProse is the prose section a block index names, or "" for a field
// block or an index past the end.
func personaProse(index int) string {
	names := config.PromptSectionNames()
	if index < 0 || index >= len(names) {
		return ""
	}
	return names[index]
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
	if f.redrafting != "" {
		return m.finishSectionRedraft(o)
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
	if o.Draft != nil {
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

// resetSections reads a new draft's sections as the flow's starting point:
// a whole draft is a new draft, so nothing revised before it can be taken
// back into it.
func (f *personaFlow) resetSections() {
	f.sections = map[string]personaSection{}
	f.revisions = map[string][]personaSection{}
	for _, sec := range f.draft.SectionList() {
		f.sections[sec.Name] = personaSection{body: strings.TrimSpace(sec.Body)}
	}
}

// applySection is every revision of a section: the standing it replaces is
// kept, and the draft's own section is written from the new one. It is the
// one writer of the draft's prompt after the drafter's answer, and it writes
// through SetSection — never the prompt itself, which Normalise would build
// again from the sections over a hand edit.
func (f *personaFlow) applySection(name string, next personaSection) {
	f.revisions[name] = append(f.revisions[name], f.sections[name])
	f.sections[name] = next
	f.draft.SetSection(name, next.body)
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
	f.draft.SetSection(name, prev.body)
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
	blocks := make([]components.ProfileSection, 0, len(personaBlocks))
	for _, name := range config.PromptSectionNames() {
		sec := f.sections[name]
		mark, tone := personaMark(name, sec)
		blocks = append(blocks, components.ProfileSection{
			Name:     name,
			Body:     sec.body,
			Mark:     mark,
			MarkTone: tone,
			Prose:    true,
			Revised:  len(f.revisions[name]) > 0,
		})
	}
	blocks = append(blocks, personaToolsSection(*d), personaCommandsSection(), personaModelSection(*d))
	saves := make([]components.SelectOption, 0, 2)
	for _, s := range m.personaSaves() {
		saves = append(saves, s.option)
	}
	m.personaScreen.Show(components.ProfileDraftView{
		Name:        d.Name,
		Description: d.Description,
		Sections:    blocks,
	}, saves)
}

// personaMark is what the mark after a section's heading says about it. An
// empty section is marked whatever else happened to it, because a required
// section with nothing in it is the thing to find before saving; a section
// the person wrote is theirs, which is the mark no refine may take away; and
// the drafter's own rewrites are counted, dim.
func personaMark(name string, s personaSection) (string, components.ProfileMarkTone) {
	switch {
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
	}
}

// personaCommandsSection is the Commands block. A drafted profile states no
// command fields yet, so the block says what applies instead: the session's
// own lists.
func personaCommandsSection() components.ProfileSection {
	return components.ProfileSection{
		Name:   "Commands",
		Value:  "none stated",
		Tone:   components.ToneQuiet,
		Detail: "the session's own command lists apply",
	}
}

// personaModelSection is the Model & budget block: each value, or the
// session's own where the draft names none.
func personaModelSection(d persona.Draft) components.ProfileSection {
	model := d.Model
	if model == "" {
		model = "inherited from this session"
	}
	var detail []string
	if d.Reasoning != "" {
		detail = append(detail, "reasoning "+d.Reasoning)
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
	case components.ProfileEdit:
		return m.editPersonaSection(res.Index)
	case components.ProfileClear:
		return m.clearPersonaSection(res.Index)
	case components.ProfileUndo:
		return m.undoPersonaSection(res.Index)
	case components.ProfilePick:
		return m.pickPersonaSection(res.Index)
	}
	return m.closePersona("Profile discarded.")
}

// pickPersonaSection is enter on a block that is a set of fields rather than
// prose. It is the seam the tools-and-permissions selector opens from — the
// Tools block — and until that selector exists it opens nothing: the key row
// does not offer enter there, so nothing was promised.
func (m Model) pickPersonaSection(int) (tea.Model, tea.Cmd) {
	m.syncViewport()
	return m, nil
}

// clearPersonaSection empties a section. It is a revision like any other,
// so esc gives the text back.
func (m Model) clearPersonaSection(index int) (tea.Model, tea.Cmd) {
	f := m.persona
	name := personaProse(index)
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
	name := personaProse(index)
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
	name := personaProse(index)
	if f.draft == nil || name == "" {
		return m, nil
	}
	if reason, refused := m.terminalRefusal(); refused {
		m.personaScreen.Warn(reason)
		m.syncViewport()
		return m, nil
	}
	path, err := writeDraftFile(f.sections[name].body + "\n")
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
	} else if body := strings.TrimSpace(string(content)); body != f.sections[msg.section].body {
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
	if f.redrafting != "" && f.draft != nil {
		f.redrafting = ""
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
