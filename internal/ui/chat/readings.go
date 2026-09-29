package chat

// The readings screen (docs/interface/surfaces.md#the-supporting-screens):
// `/readings`, and the rail's SUMMARY heading, open every reading the session
// has taken of its own run. The rail holds the latest and a transcript row
// holds each one with something to say; a quiet reading is drawn nowhere once
// the next replaces it, so the history is kept on summaryState (summary.go)
// and read here.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// openReadings puts the screen up. It is built once per opening, like the
// steps screen: what it draws is the history as it stood when the reader
// asked, and a list that grew under them mid-read would move the pointer off
// the reading they were on.
func (m Model) openReadings() (tea.Model, tea.Cmd) {
	switch {
	case !m.summaryEnabled():
		text, _ := m.summaryStatus()
		return m.systemNotice(text)
	case len(m.summary.readings) == 0:
		return m.systemNotice("the session has taken no readings yet")
	}
	screen := m.readingsScreenData()
	m.screens = m.screens.with(stateReadings, &screen)
	m.enterSurface(stateReadings)
	return m, nil
}

// updateReadings routes keys while the screen is up.
func (m Model) updateReadings(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.readings()
	if screen == nil || screen.Update(msg) {
		return m.closeReadingsScreen()
	}
	return m, nil
}

// closeReadingsScreen hands the screen back to the turn, the way its own esc
// does and the way the rail cell that opened it does.
func (m Model) closeReadingsScreen() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateReadings)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// renderReadingsHint is the one line the screen leaves where the draft box
// was: the way out and nothing else, the way the steps screen's does.
func (m Model) renderReadingsHint() string {
	return sty.SystemMsg.Render("readings · ") + segAs(keys.Screen.Quit, "back to the prompt").render()
}

// readingsScreenData builds the screen from the session's history, newest
// first. The preview is the transcript's opened summary row, built by the
// row's own renderer at the pane's width, so a reading reads the same here
// as it does in the feed.
func (m Model) readingsScreenData() components.ReadingsScreen {
	held := m.summary.readings
	readings := make([]summaryReading, len(held))
	items := make([]components.ReadingsItem, len(held))
	for i := range held {
		r := held[len(held)-1-i]
		readings[i] = r
		items[i] = components.ReadingsItem{
			Round: r.verdict.Round, Turn: int(r.turn), Tone: summaryTone(r.verdict.State),
			Steered: r.steer != "", Withdrawn: r.withdrawn,
		}
	}
	screen := components.ReadingsScreen{
		Readings: items,
		Row: func(i, width int) components.ActivityRow {
			return m.summaryRowFor(entry{kind: entrySummary, reading: &readings[i], expanded: true}, width)
		},
		Subject: plural(len(held)+m.summary.dropped, "reading"),
		Dropped: m.summary.dropped,
		Kept:    summaryHistoryMax,
	}
	if cost := m.freshRateLabel(m.summary.tokensIn, m.summary.tokensOut); cost != "" {
		screen.Cost = fmt.Sprintf("%s spent", cost)
	}
	return screen
}
