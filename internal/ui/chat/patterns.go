package chat

// What repeats, proposed
// (docs/capabilities/sessions-and-memory.md#memory-is-what-shhh-knows-about-your-project).
//
// The record is read for what this checkout's sessions kept doing — the same
// file read, the same command asked about and allowed, the same commands run
// in the same order, the same suite failing first — and each is offered as
// the one thing that would stop it: a memory, a line in the checkout's
// allowlist, or a skill. Which one is the code's decision, made by the host
// before anything is drawn; a cheap model is asked only for a memory's or a
// skill's words, and a reading that fails leaves the code's own.
//
// Three rules hold it:
//
//   - Nothing is written without the person's yes on a card. `/patterns` is a
//     list; enter opens the proposal's card; the memory card is the one the
//     model's own proposals are answered on, and the allowlist and skill
//     cards show the exact line or file they would write.
//   - A no comes in two. Not now leaves the proposal for next time; never
//     writes the person's no down, keyed on the proposal's kind and the code's
//     statement of the pattern, so no surface raises it again whatever words
//     a later reading would have chosen.
//   - The words never reach the conversation. A worded proposal is drawn on a
//     card, and a memory the person saved is recalled the way every memory
//     is, from the next session.

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// patternsCommandName is the command the start screen's offer stands for,
// named once so the row and the registry cannot drift apart.
const patternsCommandName = "/patterns"

// Proposal is one thing the record proposes, decided and worded by the host.
type Proposal struct {
	// Kind is what a yes writes: storage.ProposalMemory, ProposalAllowlist
	// or ProposalSkill. It is also the kind a never is recorded under.
	Kind string
	// Pattern is what repeated, as the code names it; What is what happened
	// to it; Sessions is how many sessions it happened in.
	Pattern  string
	What     string
	Sessions int
	// Key is what the proposal is recognised by when it is declined: the
	// code's statement of the pattern, the same in every session whatever
	// words a reading chose, so a never is a never and not a no to one
	// wording.
	Key string
	// MemoryKind and Text are a memory proposal's kind and sentence.
	MemoryKind string
	Text       string
	// Entry is an allowlist proposal's entry, as the list holds it.
	Entry string
	// Skill is a skill proposal's draft.
	Skill skill.Draft
	// Facts and Lines are what a reading is given to word it, and nothing
	// else is: the pattern as the code states it, and the transcript lines
	// that made it.
	Facts string
	Lines []string
}

// Patterns wires the proposals into the chat. The zero value hides
// /patterns.
type Patterns struct {
	// Read reads the record for this checkout's proposals, the ones already
	// declined, written or held back by the checkout's trust left out.
	Read func() ([]Proposal, error)
	// Word asks the cheap model for a memory's or a skill's words and
	// reports whether it did; a failed reading hands the proposal back as it
	// was. Nil words nothing.
	Word func(ctx context.Context, p Proposal) (Proposal, bool)
	// Preview is the file a yes would change, as it stands and as it would
	// be, for an allowlist line or a skill.
	Preview func(p Proposal) (path, before, after string, err error)
	// Write writes an allowlist line or a skill and returns the path.
	Write func(p Proposal) (string, error)
	// Decline records the person's never.
	Decline func(p Proposal) error
}

// patternsState is the list, the reading out for one row, and the card.
type patternsState struct {
	cfg Patterns
	// wording is the row a reading is out for, -1 for none; cancel stops it.
	wording int
	cancel  context.CancelFunc
	// asked numbers the readings, so one cancelled with the list it was
	// asked from cannot land on the row of a list opened since.
	asked int
	// card is the proposal on the card while one is up.
	card *proposalCard
}

// proposalCard is one proposal on its card: the file it would change, or the
// memory card's rows.
type proposalCard struct {
	p                   Proposal
	path, before, after string
	ask                 *components.NoteSelect
}

// patternWordedMsg carries a reading back for the row it was asked for.
type patternWordedMsg struct {
	asked  int
	row    int
	p      Proposal
	worded bool
}

// patternsEnabled reports a session that can read its record for proposals.
func (m Model) patternsEnabled() bool { return m.patterns.cfg.Read != nil }

// openPatterns is /patterns: the list, or the line saying there is none.
func (m Model) openPatterns() (tea.Model, tea.Cmd) {
	if !m.patternsEnabled() {
		return m.systemNotice("this session has no record to read for patterns")
	}
	rows, err := m.patterns.cfg.Read()
	switch {
	case err != nil:
		return m.surfaceNotice(failed("patterns", "the record could not be read: "+err.Error()))
	case len(rows) == 0:
		return m.surfaceNotice("nothing this checkout's sessions kept doing is left to propose")
	}
	screen := &patternsScreen{rows: rows}
	screen.view.Rows = patternsRows(rows)
	m.patterns.wording = -1
	m.screens = m.screens.with(stateProposals, screen)
	m.enterSurface(stateProposals)
	return m, nil
}

// patternsScreen is the list and the proposals behind its rows.
type patternsScreen struct {
	view components.PatternsScreen
	rows []Proposal
}

func (s *patternsScreen) SetSize(width, height int) { s.view.SetSize(width, height) }
func (s *patternsScreen) View(width int) string     { return s.view.View(width) }

// patternsRows words the proposals for the screen.
func patternsRows(ps []Proposal) []components.PatternsRow {
	rows := make([]components.PatternsRow, len(ps))
	for i, p := range ps {
		rows[i] = components.PatternsRow{Kind: p.Kind, Pattern: p.Pattern, Sessions: p.Sessions,
			What: p.What, Becomes: proposalBecomes(p)}
	}
	return rows
}

// proposalBecomes is what a yes would write, and where, in a line.
func proposalBecomes(p Proposal) string {
	switch p.Kind {
	case storage.ProposalAllowlist:
		return fmt.Sprintf("adds %q to behavior.command_allowlist in this checkout's settings, so it runs without asking", p.Entry)
	case storage.ProposalSkill:
		return "writes a skill under .shhh/skills/ that runs these in order"
	}
	return "keeps a " + p.MemoryKind + " memory every session here is told"
}

func (h heldScreens) patterns() *patternsScreen {
	return heldAs[patternsScreen](h, stateProposals)
}

// updatePatterns routes keys while the list is up. A row whose words are
// being read is not taken twice; every other key still answers.
func (m Model) updatePatterns(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.patterns()
	if screen == nil {
		return m.closePatterns()
	}
	done, take := screen.view.Update(msg)
	switch {
	case done:
		return m.closePatterns()
	case take && m.patterns.wording < 0:
		return m.takeProposal(screen.view.Current())
	}
	return m, nil
}

// closePatterns hands the screen back, and stops a reading still out: there
// is no list left for it to land on.
func (m Model) closePatterns() (tea.Model, tea.Cmd) {
	m.stopPatterns()
	m.screens = m.screens.without(stateProposals)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// stopPatterns cancels a reading still out.
func (m *Model) stopPatterns() {
	if m.patterns.cancel != nil {
		m.patterns.cancel()
		m.patterns.cancel = nil
	}
	m.patterns.wording = -1
	if s := m.screens.patterns(); s != nil {
		s.view.Busy = ""
	}
}

// takeProposal opens the card for row i, after its words are read where a
// memory or a skill needs them.
func (m Model) takeProposal(i int) (tea.Model, tea.Cmd) {
	screen := m.screens.patterns()
	if screen == nil || i < 0 || i >= len(screen.rows) {
		return m, nil
	}
	p := screen.rows[i]
	if p.Kind == storage.ProposalAllowlist || m.patterns.cfg.Word == nil {
		return m.openProposalCard(p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.patterns.asked++
	m.patterns.wording, m.patterns.cancel = i, cancel
	screen.view.Busy = "wording the " + p.Kind + "…"
	word, asked := m.patterns.cfg.Word, m.patterns.asked
	return m, func() tea.Msg {
		defer cancel()
		worded, ok := word(ctx, p)
		return patternWordedMsg{asked: asked, row: i, p: worded, worded: ok}
	}
}

// finishPatternWording opens the card a reading was out for. One that lands
// after the list closed, or for a row no longer waiting, is dropped: the
// person has moved on, and a card opening under them would be a card they
// did not ask for.
func (m Model) finishPatternWording(msg patternWordedMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.patterns()
	if m.state != stateProposals || screen == nil || m.patterns.wording != msg.row || m.patterns.asked != msg.asked {
		return m, nil
	}
	m.patterns.cancel = nil
	m.patterns.wording = -1
	screen.view.Busy = ""
	return m.openProposalCard(msg.p)
}

// openProposalCard puts the proposal's card up in place of the list. Nothing
// is written here: an allowlist line and a skill are previewed, so the card
// can show the exact change, and a memory is the memory card's three rows.
func (m Model) openProposalCard(p Proposal) (tea.Model, tea.Cmd) {
	card := &proposalCard{p: p}
	if p.Kind == storage.ProposalMemory {
		if m.wiring.Memory.Save == nil {
			return m.closeWithNotice("durable memory is off in this session, so a memory cannot be kept")
		}
		card.ask = m.memorySelect(false)
		// Esc here leaves the proposal for next time, which is the answer
		// that writes nothing at all; Don't save is the never, as it is on
		// the model's own proposals.
		card.ask.Select.CancelLabel = "not now"
	} else {
		if m.patterns.cfg.Preview == nil || m.patterns.cfg.Write == nil {
			return m.closeWithNotice("this session cannot write " + p.Kind + " proposals")
		}
		path, before, after, err := m.patterns.cfg.Preview(p)
		if err != nil {
			return m.closeWithNotice(failed("patterns", "nothing written: "+err.Error()))
		}
		card.path, card.before, card.after = path, before, after
	}
	m.screens = m.screens.without(stateProposals)
	m.patterns.card = card
	m.enterSurface(stateProposal)
	m.syncViewport()
	return m, nil
}

// closeWithNotice leaves the list with one line in the transcript.
func (m Model) closeWithNotice(text string) (tea.Model, tea.Cmd) {
	next, _ := m.closePatterns()
	return next.(Model).surfaceNotice(text)
}

// proposalApprovalCard is the allowlist or skill card: the file's change as
// a diff, so the one line an allowlist write adds is the line on screen, and
// the three answers with what each leaves behind.
func (m Model) proposalApprovalCard() *components.ApprovalCard {
	c := m.patterns.card
	if c == nil {
		return &components.ApprovalCard{Variant: components.ApprovalGeneric, Title: "Approve proposal"}
	}
	title, act := "Approve allowlist line", fmt.Sprintf("let %q run without asking in this checkout", c.p.Entry)
	if c.p.Kind == storage.ProposalSkill {
		title, act = "Approve skill", "write the skill "+c.p.Skill.Name
	}
	return &components.ApprovalCard{
		Variant:  components.ApprovalEdit,
		Title:    title,
		ActGlyph: "✎",
		Act:      act,
		Hunks:    diff.Compute(c.before, c.after),
		Syntax:   diffSyntax(c.path),
		Answer:   "write it",
		Decline:  "not now",
		Never:    "never",
		Return:   "leave — nothing written, and it is offered again",
		MaxLines: m.planPanelBound(),
		KeyList:  true,
		Footnote: "enter opened this card and writes nothing — only [y] does",
	}
}

// proposalLines draws the card.
func (m Model) proposalLines() []string {
	c := m.patterns.card
	if c != nil && c.ask != nil {
		var lines []string
		line := fmt.Sprintf("%s memory: %q", c.p.MemoryKind, firstLine(c.p.Text))
		for _, l := range strings.Split(m.wordWrap(line, m.contentWidth()), "\n") {
			lines = append(lines, sty.Header.Render(l))
		}
		return append(lines, strings.Split(c.ask.View(m.contentWidth()), "\n")...)
	}
	return strings.Split(m.proposalApprovalCard().View(m.contentWidth()), "\n")
}

// proposalKeyList is the card's register for `?`, on the cards that answer
// it: the memory card's rows are a selector whose keys are its own.
func proposalKeyList(m Model) (keys.SurfaceID, bool) {
	c := m.patterns.card
	return keys.OnProposal, c != nil && c.ask == nil
}

// answerProposal routes the card's keys. Only a yes writes, and only a never
// records the no; not now and esc leave the proposal to be made again. Enter
// is none of them: it is the key that opened the card (keys.ProposalKeys).
func (m *Model) answerProposal(msg tea.KeyPressMsg) (bool, overlayAction) {
	c := m.patterns.card
	if c == nil {
		return true, overlayAction{close: true}
	}
	if c.ask != nil {
		return m.answerProposalMemory(c, msg)
	}
	switch {
	case keys.Match(msg, keys.Select.Cancel), keys.Match(msg, keys.Proposal.Later):
		m.patterns.card = nil
		return true, overlayAction{close: true, note: "nothing written; " + patternsCommandName + " offers it again"}
	case keys.Match(msg, keys.Proposal.Never):
		m.patterns.card = nil
		return true, overlayAction{close: true, note: m.neverProposal(c.p)}
	case keys.Match(msg, keys.Proposal.Write):
		m.patterns.card = nil
		path, err := m.patterns.cfg.Write(c.p)
		if err != nil {
			return true, overlayAction{close: true, note: failed("patterns", "nothing written: "+err.Error())}
		}
		m.recordDecision(observe.DecisionAllow, observe.ReasonUser)
		note := "wrote " + path + "; sessions here read the skill from the next one"
		if c.p.Kind == storage.ProposalAllowlist {
			note = fmt.Sprintf("added %q to behavior.command_allowlist in %s; it runs without asking from the next session", c.p.Entry, path)
		}
		return true, overlayAction{close: true, note: note}
	}
	return false, overlayAction{}
}

// answerProposalMemory is the memory card's answer: a save writes the memory
// with the note the person added, Don't save is the never, and esc is not
// now.
func (m *Model) answerProposalMemory(c *proposalCard, msg tea.KeyPressMsg) (bool, overlayAction) {
	done, res := c.ask.Update(msg)
	if !done {
		return false, overlayAction{}
	}
	m.patterns.card = nil
	switch {
	case res.Canceled:
		return true, overlayAction{close: true, note: "nothing kept; " + patternsCommandName + " offers it again"}
	case res.Index != 0 && res.Index != 1:
		return true, overlayAction{close: true, note: m.neverProposal(c.p)}
	}
	scope := m.wiring.Memory.ProjectScope
	if res.Index == 1 {
		scope = memory.GlobalScope
	}
	text := c.p.Text
	if res.Note != "" {
		text += " (" + res.Note + ")"
	}
	saved, err := m.wiring.Memory.Save(scope, c.p.MemoryKind, text)
	if err != nil {
		return true, overlayAction{close: true, note: failed("patterns", "the memory was not kept: "+err.Error())}
	}
	m.recordDecision(observe.DecisionAllow, observe.ReasonUser)
	return true, overlayAction{close: true, note: firstLine(saved)}
}

// neverProposal writes the person's no down for this proposal's kind and
// pattern. A store that will not take it says so: the person has been told
// it will not come back.
func (m *Model) neverProposal(p Proposal) string {
	m.recordDecision(observe.DecisionDeny, observe.ReasonUser)
	if m.patterns.cfg.Decline == nil {
		return "nothing written. The no could not be remembered in this session"
	}
	if err := m.patterns.cfg.Decline(p); err != nil {
		return "nothing written. The no could not be remembered: " + err.Error()
	}
	return "nothing written, and it will not be proposed again; shhh memory declined lists it"
}

// renderPatternsHint is the one line the list leaves where the draft was.
func (m Model) renderPatternsHint() string {
	return sty.SystemMsg.Render("patterns · ") + segAs(keys.Screen.Quit, "back to the prompt").render()
}
