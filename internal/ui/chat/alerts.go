package chat

// The alerts screen (docs/interface/surfaces.md#the-supporting-screens):
// `/alerts`, and the rail's ALERTS heading and its fold marker, open every
// alert the session has had. The block draws the two most recent standing
// ones and counts the rest; the screen lists them all, with what answered
// each and every run behind it.
//
// It reads the walk the block reads (alertEpisodes, inspector.go) — the same
// episodes, answered by the same resolution the turn's close reads
// (resolved.go) — and never a second scan of its own, so a standing alert
// here is a standing alert on the rail.

import (
	"sort"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// openAlerts puts the screen up. It is built once per opening, like the turns
// screen: what it draws is the session as it stood when the reader asked.
// Unlike the other screens it opens over a session that has broken nothing,
// because "nothing is broken" is itself the answer the reader asked for.
func (m Model) openAlerts() (tea.Model, tea.Cmd) {
	screen := m.alertsScreenData()
	m.screens = m.screens.with(stateAlerts, &screen)
	m.enterSurface(stateAlerts)
	return m, nil
}

// updateAlerts routes keys while the screen is up.
func (m Model) updateAlerts(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.alerts()
	if screen == nil || screen.Update(msg) {
		return m.closeAlertsScreen()
	}
	return m, nil
}

// closeAlertsScreen hands the screen back to the turn, the way its own esc
// does and the way the rail cell that opened it does.
func (m Model) closeAlertsScreen() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateAlerts)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// renderAlertsHint is the one line the screen leaves where the draft box was:
// the way out and nothing else, the way the turns screen's does.
func (m Model) renderAlertsHint() string {
	return sty.SystemMsg.Render("alerts · ") + segAs(keys.Screen.Quit, "back to the prompt").render()
}

// alertsScreenData builds the screen from the walk: standing episodes first,
// then the superseded, each newest first — the order the block would draw
// them in if it drew them all.
func (m Model) alertsScreenData() components.AlertsScreen {
	episodes := alertEpisodes(m.transcript)
	sort.SliceStable(episodes, func(a, b int) bool {
		sa, sb := episodes[a].alert.Superseded, episodes[b].alert.Superseded
		if sa != sb {
			return !sa
		}
		return episodes[a].lastFail > episodes[b].lastFail
	})
	items := make([]components.AlertsItem, 0, len(episodes))
	for _, ep := range episodes {
		item := components.AlertsItem{Alert: ep.alert, Answer: m.alertAnswer(ep)}
		for _, at := range ep.runs {
			e := m.transcript[at]
			item.Runs = append(item.Runs, components.AlertsRun{
				Turn: e.turn, Line: firstLine(e.text), Outcome: commandOutcome(e),
				Duration: activityDuration(e.duration), Evidence: runEvidence(e),
			})
		}
		items = append(items, item)
	}
	return components.AlertsScreen{Alerts: items}
}

// alertAnswer is what answered an episode, in words, and the turn it ran in:
// the suite's pass or a clean run of the command. Nil on a standing one.
func (m Model) alertAnswer(ep alertEpisode) *components.AlertsAnswer {
	if ep.answer == noAnswer || ep.answer >= len(m.transcript) {
		return nil
	}
	e := m.transcript[ep.answer]
	what := firstLine(e.text) + " came back clean"
	if s, ok := gateVerdict(e); ok {
		what = "the quality gate passed"
		if s.Suite != "" {
			what = "quality gate " + s.Suite + " passed"
		}
	}
	return &components.AlertsAnswer{What: what, Turn: e.turn}
}

// runEvidence is the id a run's output was kept under, where it was cut: the
// trim's record of it, or the id the reduction's notice named in the result
// the row holds. Empty where the output was never cut.
func runEvidence(e entry) string {
	if e.elided != nil && e.elided.evidence != "" {
		return e.elided.evidence
	}
	return evidenceHandle.FindString(e.toolResult)
}
