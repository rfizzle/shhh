package components

// The rail's SPEND block: what this turn cost, what the session has cost and
// what the ledger will allow. It is a file of its own so the block that says
// money says it in one place.

import (
	"strings"
)

// InspectorSpend is the SPEND block: this turn's cost, how it split between
// the orchestrator and its children, and the session total.
type InspectorSpend struct {
	Turn     string
	Main     string
	Children string
	Session  string
	Model    string
}

func (r InspectorRail) spendBlock(width int) (railBlock, bool) {
	s := r.Spend
	if s == nil || (s.Turn == "" && s.Main == "" && s.Session == "") {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("SPEND", sty.Body.Render(s.Turn), sty.Body, width)}
	var split []string
	if s.Model != "" {
		split = append(split, s.Model)
	}
	if s.Main != "" {
		split = append(split, s.Main+" main")
	}
	if s.Children != "" {
		split = append(split, s.Children+" ◇")
	}
	if len(split) > 0 {
		b.add(railRow(sty.Dim.Render(strings.Join(split, " · ")), "", width, inspectorIndent))
	}
	if s.Session != "" {
		b.add(railRow(sty.Dim.Render("session total "+s.Session), "", width, inspectorIndent))
	}
	return b, true
}
