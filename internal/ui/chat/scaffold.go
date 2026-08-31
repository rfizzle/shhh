package chat

// The scaffolding offer. A checkout with no `.shhh` of its own has told the
// model nothing about itself, and the file that would fix that is one the
// session could write in a keystroke — which is exactly why it does not.
// A write to the machine gets a card, and the card lists what it would write
// before it asks (docs/interface/principles.md#weight-tracks-risk).
//
// The offer is made in two places and answered in one: the start screen's
// third row, and `/init` typed. The row's action is that command, so
// choosing the offer and typing it are the same act — the rule every other
// suggestion on that screen keeps (start.go).
//
// A refusal is remembered for the checkout, so the offer is made once
// (docs/interface/surfaces.md#the-start-screen). `/init` still works
// afterwards: what was refused was being asked, not the file.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// scaffoldCommandName is the command the offer stands for, named once so
// the start screen's row and the registry cannot drift apart.
const scaffoldCommandName = "/init"

// Scaffold wires the project-scaffolding offer into the chat TUI.
type Scaffold struct {
	// Offer reports a checkout that could take the scaffold and has not
	// already refused one. It is answered once, by the host, at session
	// start — nothing on the start screen is computed per frame.
	Offer bool
	// Paths are what the write creates, relative to the checkout, in the
	// order it creates them. The card lists them.
	Paths []string
	// Write creates them and returns the context file's path. Nil in a
	// session with no checkout to scaffold, which is what hides /init.
	Write func() (string, error)
	// Decline remembers the refusal for this checkout.
	Decline func() error
}

// WithScaffold enables the scaffolding offer and the /init command.
func (m Model) WithScaffold(s Scaffold) Model {
	m.scaffold = s
	return m
}

// scaffoldOffered reports whether the start screen should spend its third
// row on the offer: a wired session, a checkout that could take it, and no
// refusal already on record.
func (m Model) scaffoldOffered() bool {
	return m.scaffold.Write != nil && m.scaffold.Offer
}

// scaffoldCommand is /init: the card, or the reason there is none.
func (m Model) scaffoldCommand() (tea.Model, tea.Cmd) {
	if m.scaffold.Write == nil {
		return m.systemNotice("This session has no checkout to scaffold.")
	}
	return m.openScaffold()
}

// openScaffold puts the card up. It is a takeover: the reader asked for it,
// so it holds the keyboard the way every summoned surface does.
func (m Model) openScaffold() (tea.Model, tea.Cmd) {
	m.enterSurface(stateScaffold)
	m.syncViewport()
	return m, nil
}

// scaffoldCard is the approval card for the write: what it touches, whether
// it can be taken back, and the files themselves.
func (m Model) scaffoldCard() *components.ApprovalCard {
	card := &components.ApprovalCard{
		Variant:  components.ApprovalGeneric,
		Title:    "Approve scaffold",
		Headline: "shhh wants to write this project's context file",
		Summary:  "it is read into the system prompt of every session opened here",
		Question: "Write these files?",
		MaxLines: m.planPanelBound(),
	}
	for _, path := range m.scaffold.Paths {
		// A directory is created and a file is written, and the row says
		// which: a reader scanning the block should not have to work out
		// what a trailing slash means.
		label := "writes"
		if strings.HasSuffix(path, "/") {
			label = "creates"
		}
		card.Fields = append(card.Fields, components.CardField{Label: label, Value: path})
	}
	card.Fields = append(card.Fields,
		components.CardField{Label: "undo", Value: "yes", Detail: "delete them; nothing else is touched",
			Tone: components.ToneSafe},
		components.CardField{Label: "network", Value: "closed", Tone: components.ToneSafe},
	)
	// Two ways of not writing, and the card says which is which, because
	// they differ in what outlives this screen: [n] is an answer and settles
	// the offer, esc is a way out and settles nothing
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	card.SafeDefault = "[n] no — nothing written; not offered again"
	card.Return = "[esc] leave — nothing written, and the offer stays"
	return card
}

// updateScaffold routes the card's three keys. It does not go through the
// card's own Update: that maps esc and ctrl+c onto the decline, which is
// right for a card nobody asked for and wrong here, where esc is the way
// back out of a screen the reader opened (keys.Decision.Refuse).
func (m Model) updateScaffold(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case keys.Match(msg, keys.Draft.Quit):
		m.quitting = true
		return m, m.quitCmd()
	case keys.Match(msg, keys.Select.Cancel):
		m.leaveSurface()
		m.syncViewport()
		return m, nil
	case keys.Match(msg, keys.Decision.Refuse):
		m.leaveSurface()
		m.syncViewport()
		return m.declineScaffold()
	case keys.Match(msg, keys.Decision.Allow):
		m.leaveSurface()
		m.syncViewport()
		return m.writeScaffold()
	}
	return m, nil
}

// declineScaffold answers the offer for good. The refusal is recorded before
// it is reported, and a store that would not take it says so: the reader has
// been told the offer will not come back.
func (m Model) declineScaffold() (tea.Model, tea.Cmd) {
	m.scaffold.Offer = false
	if m.scaffold.Decline != nil {
		if err := m.scaffold.Decline(); err != nil {
			return m.systemNotice("Nothing written. The refusal could not be remembered: " + err.Error())
		}
	}
	return m.systemNotice("Nothing written. This checkout will not be offered again; " +
		scaffoldCommandName + " asks any time.")
}

// writeScaffold takes the offer.
func (m Model) writeScaffold() (tea.Model, tea.Cmd) {
	path, err := m.scaffold.Write()
	if err != nil {
		return m.systemNotice("Could not scaffold this project: " + err.Error())
	}
	m.scaffold.Offer = false
	return m.systemNotice("Wrote " + path + ". Describe this project's tooling and conventions in it; " +
		"it is read into the system prompt from the next session on.")
}

// scaffoldLines renders the card, one row per line.
func (m Model) scaffoldLines() []string {
	return strings.Split(m.scaffoldCard().View(m.contentWidth()), "\n")
}

// renderScaffold pads the card to the bottom panel height, like every other
// surface that borrows it.
func (m Model) renderScaffold() string {
	lines := m.scaffoldLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}
