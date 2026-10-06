package chat

// /stats: the spend screen (docs/interface/surfaces.md#the-supporting-screens).
// `/stats`, and the rail's SPEND heading and its fold marker, open the
// session's whole bill: the total, each model's share with the kinds of
// request that spent on it, each child's share and each turn's cost. The
// rail's SPEND block draws the same ledger in shares, and the screen reads
// the block's own division of it (spendShares, inspector.go) rather than
// dividing it a second time, so a share here is the row there. What the
// window is occupied by is the other half /stats used to print, and it is
// /context's.

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// WithToolTokenEstimate sets the estimated token cost of the registered tool
// definitions, shown in /context's occupancy breakdown.
func (m Model) WithToolTokenEstimate(n int64) Model {
	m.toolDefTokens = n
	return m
}

// formatCost is the shared dollar format: four decimals below a cent, two
// above, so a cheap session is not reported as $0.00.
func formatCost(v float64) string {
	if v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// sessionSpend is what the whole session has spent — the agent's turns, the
// permission classifier, the session summary and every sub-agent. It comes
// from the provider gate's ledger, so a feature added later is included
// without this function changing. A session with no ledger has only the
// agent's own accounting to report.
func (m Model) sessionSpend() meter.Totals {
	if m.ledger != nil {
		return m.ledger.Total()
	}
	return m.mainSpend()
}

// mainSpend is the main agent's own spend — its turns and nothing else,
// which is the figure the rail's MAIN row and the agent map's orchestrator
// row report beside the children they are being read against.
//
// It carries the cost the session priced as each request came back rather
// than a bare pair of token counts, so a caller renders what was billed
// instead of re-pricing the whole input at the fresh rate (vitals.go,
// attach.go).
func (m Model) mainSpend() meter.Totals {
	return meter.Totals{
		In:     m.vitals.totalIn,
		Out:    m.vitals.totalOut,
		Cached: m.vitals.totalCached,
		Cost:   m.vitals.totalCost,
		Priced: m.vitals.priced,
	}
}

// turnSpend is the open turn's own accounting in the same shape: what the
// turn under way has cost, priced request by request as it went.
func (m Model) turnSpend() meter.Totals {
	return meter.Totals{
		In:     m.vitals.current.In,
		Out:    m.vitals.current.Out,
		Cached: m.vitals.current.Cached,
		Cost:   m.vitals.current.Cost,
		Priced: m.vitals.current.Priced,
	}
}

// openStats puts the spend screen up. It is built once per opening, like the
// turns screen: what it draws is the bill as it stood when the reader asked.
// It opens over a session that has spent nothing, on one sentence, because
// the rail has no SPEND block then and the command is the only door.
func (m Model) openStats() (tea.Model, tea.Cmd) {
	screen := m.spendScreenData()
	m.screens = m.screens.with(stateSpend, &screen)
	m.enterSurface(stateSpend)
	return m, nil
}

// updateStats routes keys while the screen is up. `[enter]` on a turn opens
// it on the turns screen with that turn under the pointer, in place of this
// one: the turns screen is where a turn is read whole.
func (m Model) updateStats(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.spend()
	if screen == nil {
		return m.closeStatsScreen()
	}
	done, result := screen.Update(msg)
	if !done {
		return m, nil
	}
	next, cmd := m.closeStatsScreen()
	if result.Turn == 0 {
		return next, cmd
	}
	return next.(Model).openTurnsOn(result.Turn)
}

// closeStatsScreen hands the screen back to the turn, the way its own esc
// does and the way the rail cell that opened it does.
func (m Model) closeStatsScreen() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateSpend)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

func statsShowing(m Model) any {
	if m.state != stateSpend || m.screens.spend() == nil {
		return nil
	}
	return m.screens.spend()
}

// renderStatsHint is the one line the screen leaves where the draft box was:
// the way out and nothing else, the way the turns screen's does.
func (m Model) renderStatsHint() string {
	return sty.SystemMsg.Render("spend · ") + segAs(keys.Screen.Quit, "back to the prompt").render()
}

// spendScreenData builds the screen from the ledger the SPEND block reads:
// the block's total, the block's own shares by model, the children by name
// and the turns as the turns screen holds them.
func (m Model) spendScreenData() components.SpendScreen {
	total := m.billTotal()
	if !spent(total) {
		return components.SpendScreen{}
	}
	rows := []components.SpendRow{{
		Kind: components.SpendTotal, Cost: m.totalsLabel(total),
		Parts: m.billParts(), Tokens: tokensLabel(total), Requests: total.Requests,
	}}
	for _, share := range m.spendShares() {
		row := components.SpendRow{
			Kind: components.SpendModel, Name: share.model,
			Cost: shareLabel(share.own), Children: shareLabel(share.children),
			Tokens: tokensLabel(share.own), Requests: share.own.Requests,
		}
		if row.Name == "" {
			row.Name = "(unnamed)"
		}
		for i, src := range share.sources {
			row.Parts = append(row.Parts, spendPart(spendWord(src), share.parts[i]))
		}
		rows = append(rows, row)
	}
	for _, c := range m.childShares() {
		rows = append(rows, components.SpendRow{
			Kind: components.SpendChild, Name: c.name, Models: c.models,
			Cost: shareLabel(c.spend), Tokens: tokensLabel(c.spend), Requests: c.spend.Requests,
		})
	}
	turns := m.turnsScreenData().Turns
	for i := range turns {
		t := &turns[i]
		cost := ""
		switch {
		case t.Close != nil:
			cost = t.Close.Spend
		case t.Running != nil:
			// The rail's own reading of the turn under way: its heading.
			cost = m.totalsLabel(m.turnSpend())
		case t.Paused != nil:
			// A turn stopped at its round limit has no close; its pause row
			// kept what it had cost, and its requests are already in the
			// total above, so the row is that figure and nothing is added.
			cost = t.Paused.Spend
		}
		if cost == "" {
			// A turn whose cost was not kept, or that has spent nothing, has
			// no figure to put on the bill.
			continue
		}
		rows = append(rows, components.SpendRow{Kind: components.SpendTurn, Cost: cost, Turn: t})
	}
	return components.SpendScreen{Rows: rows}
}

// billParts is the whole bill by kind of request, in the order each first
// billed: the ledger's own roll-up by source, or — with no ledger — the
// session's turns and the children's roll-up, the two things the block adds
// up without one.
func (m Model) billParts() []components.SpendPart {
	var parts []components.SpendPart
	if m.ledger == nil {
		for _, p := range []struct {
			src meter.Source
			t   meter.Totals
		}{{meter.SourceAgent, m.mainSpend()}, {meter.SourceSubagent, m.childSpend()}} {
			if spent(p.t) {
				parts = append(parts, spendPart(spendWord(p.src), p.t))
			}
		}
		return parts
	}
	for _, e := range m.ledger.BySource() {
		if t := entryTotals(e); spent(t) {
			parts = append(parts, spendPart(spendWord(e.Origin.Source), t))
		}
	}
	return parts
}

// childShare is one sub-agent's part of the bill: its name, the models it
// ran on and what it cost on all of them.
type childShare struct {
	name   string
	models []string
	spend  meter.Totals
}

// childShares divides the children's part of the bill by child, in the
// order each first billed. The ledger is the answer where there is one — the
// same sub-agent entries the block's `◇` shares are summed from, so the
// children here add up to those. A session with no ledger has each child's
// own roll-up, the ones childSpend sums.
func (m Model) childShares() []childShare {
	var out []childShare
	if m.ledger == nil {
		if m.subagents == nil {
			return nil
		}
		for _, st := range m.subagents.Snapshot() {
			if !spent(st.Spend) {
				continue
			}
			c := childShare{name: st.Name, spend: st.Spend}
			if st.Model != "" {
				c.models = []string{st.Model}
			}
			out = append(out, c)
		}
		return out
	}
	at := map[string]int{}
	for _, e := range m.ledger.Entries() {
		t := entryTotals(e)
		if e.Origin.Source != meter.SourceSubagent || !spent(t) {
			continue
		}
		name := e.Origin.Label
		if name == "" {
			name = "(unnamed)"
		}
		i, ok := at[name]
		if !ok {
			i = len(out)
			at[name] = i
			out = append(out, childShare{name: name})
		}
		out[i].spend = out[i].spend.Plus(t)
		if e.Model != "" && !slices.Contains(out[i].models, e.Model) {
			out[i].models = append(out[i].models, e.Model)
		}
	}
	return out
}

// entryTotals is one ledger entry as a roll-up.
func entryTotals(e meter.Entry) meter.Totals {
	return meter.Totals{In: e.In, Out: e.Out, Cached: e.Cached, Cost: e.Cost, Priced: e.Priced, Requests: e.Requests}
}

// spendPart is one kind of request as the screen's account lines it.
func spendPart(word string, t meter.Totals) components.SpendPart {
	return components.SpendPart{Word: word, Cost: shareLabel(t), Tokens: tokensLabel(t), Requests: t.Requests}
}

// tokensLabel is what a figure was billed for: the tokens each way, and how
// many of the input's the provider served from its cache.
func tokensLabel(t meter.Totals) string {
	if !spent(t) {
		return ""
	}
	s := fmt.Sprintf("↑%s ↓%s", formatTokenCount(t.In), formatTokenCount(t.Out))
	if t.Cached > 0 {
		s += fmt.Sprintf(" · %s cached", formatTokenCount(t.Cached))
	}
	return s
}
