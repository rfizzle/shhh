package chat

// `?` on a card that holds the keyboard, and in reading mode: the surface's
// whole register with the glyph legend under it, on the pane, until the same
// key or esc puts the surface back as it was
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Which keys a surface has is the register's answer (internal/ui/keys), read
// by the surface's name there, so the list a reader is shown is the list the
// handlers answer. A card says which register row it is standing in through
// its mode's keyList, and says nothing while a field on it holds the
// keyboard: there a question mark is a question mark.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// keyListView is the open key list and the surface it goes back to.
type keyListView struct {
	screen components.KeyListScreen
	ret    state
}

// registerOffers is a register row's keys as offers, in the row's order, less
// the `?` that opened the list: on the list itself that key is the way back,
// and the header says so.
func registerOffers(surface string) []components.KeyOffer {
	for _, s := range keys.Surfaces() {
		if s.Name != surface {
			continue
		}
		offers := make([]components.KeyOffer, 0, len(s.Bindings))
		for _, b := range s.Bindings {
			if keys.Shown(b) == keys.Shown(keys.Screen.List) {
				continue
			}
			offers = append(offers, components.Offer(b))
		}
		return offers
	}
	return nil
}

// readingListOffers is what reading mode's key list draws beside the mode's
// register for the row under the cursor: the row's own offers, and where the
// cursor stands on a step's card, a group line in an open one or its strip,
// what the mode's keys do there in the words the bar says them in — open it,
// close it, fold a group, along the strip, open that tool. The register names enter and the
// arrows once for every row; the card is where they mean something more
// particular, and a list that did not say so would leave the card's acts to
// the one line of bar at the foot (docs/interface/surfaces.md#the-step).
// See docs/interface/departures.md#the-key-list-names-a-cards-acts-beside-the-register.
func (m Model) readingListOffers() []components.KeyOffer {
	var segs []hintSeg
	if m.stripLive() {
		segs = m.stripKeys()
	} else if _, _, ok := m.groupAt(*m.entries(), m.focusIdx); ok {
		words := groupFoldWords
		if (*m.entries())[m.focusIdx].groupFolded {
			words = groupUnfoldWords
		}
		segs = append(segs, segAs(keys.Reading.Expand, words))
	} else if words, ok := m.focusedCardWords(); ok {
		segs = append(segs, segAs(keys.Reading.Expand, words))
		if m.focusedRowOpen() {
			segs = append(segs, segAs(keys.Reading.Collapse, cardCloseWords))
		}
	}
	offers := make([]components.KeyOffer, 0, len(segs))
	for _, s := range segs {
		offers = append(offers, components.KeyOffer{Key: "[" + s.key + "]", Label: s.label, Safe: s.safe})
	}
	return append(offers, m.readingRowOffers()...)
}

// openKeyList puts a surface's register on the pane. ret is the state the
// list goes back to, which is the surface it was opened over.
func (m Model) openKeyList(surface string, row []components.KeyOffer) (tea.Model, tea.Cmd) {
	m.keyList = &keyListView{
		screen: components.KeyListScreen{Surface: surface, Register: registerOffers(surface), Row: row},
		ret:    m.state,
	}
	m.enterSurface(stateKeyList)
	m.syncViewport()
	return m, nil
}

// updateKeyList answers a key on the list.
func (m Model) updateKeyList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.keyList == nil {
		return m.closeKeyList()
	}
	if done, _ := m.keyList.screen.Update(msg); done {
		return m.closeKeyList()
	}
	return m, nil
}

// closeKeyList goes back to the surface the list was opened over, the way
// the full-screen output goes back to the card that opened it
// (outputview.go): a surface that borrows the screen is put back directly,
// and a decision — which borrows nothing, being a stage of the turn — comes
// back through the turn the list borrowed the screen from.
func (m Model) closeKeyList() (tea.Model, tea.Cmd) {
	ret := stateInput
	if m.keyList != nil {
		ret = m.keyList.ret
	}
	m.keyList = nil
	if ret.isSurface() {
		m.state = ret
	} else {
		m.leaveSurface()
	}
	m.invalidateRenderCache()
	m.syncViewport()
	if m.state == stateFocus {
		m.refreshFocusView()
	} else {
		m.viewport.SetLines(m.renderHistoryLines())
	}
	return m, nil
}

// keyListLines is the list drawn into the pane.
func (m Model) keyListLines(width, height int) []string {
	if m.keyList == nil {
		return nil
	}
	m.keyList.screen.SetSize(width, height)
	return strings.Split(m.keyList.screen.View(width), "\n")
}

// renderKeyListHint is the one line the list leaves where the draft was: the
// way back, in the spelling that means it on every surface, to the surface
// the list was opened over.
func (m Model) renderKeyListHint() string {
	back := "back"
	if m.keyList != nil && m.keyList.screen.Surface != "" {
		back = "back to " + m.keyList.screen.Surface
	}
	return sty.SystemMsg.Render("keys · ") + segAs(keys.Select.Cancel, back).render()
}

// The register rows a card's `?` lists. Each answers "" while a field on the
// card holds the keyboard, which is where `?` is text.

// confirmKeyList is the approval card's, or the list or field it has open
// under it.
func (m Model) confirmKeyList() string {
	switch {
	case m.askOverlay() != nil, m.approval.note != nil, m.approval.edit != nil:
		return ""
	case m.approval.list != nil:
		return "the approval card's queue list"
	case m.approval.grant != nil:
		return "the approval card's grant list"
	}
	return "the approval card and the /run confirm"
}

// questionKeyList is the question card's, in whichever dressing it is.
func (m Model) questionKeyList() string {
	c := m.question
	if c == nil || c.submit || c.typing() {
		return ""
	}
	if c.q.Shape == ask.ShapeConfirm {
		return "a yes-or-no question"
	}
	return "the question card"
}

// agentListKeyList is the agent manager's, while nothing on it is answering
// a child or being typed into.
func (m Model) agentListKeyList() string {
	if m.agentList == nil || m.listAnswerAsk() != nil || m.killConfirm != nil || m.agentList.Typing() {
		return ""
	}
	return "the agent manager"
}

// staticKeyList is a card whose `?` always lists the same register row.
func staticKeyList(surface string) func(Model) string {
	return func(Model) string { return surface }
}
