package components

// The rail's SPEND block: what this turn cost, what the session has cost and
// what the ledger will allow. It is a file of its own so the block that says
// money says it in one place.

// InspectorSpend is the SPEND block: this turn's cost and the session total.
// The per-model shares the total adds up to are the /stats screen's
// (docs/interface/surfaces.md#the-inspector-rail).
type InspectorSpend struct {
	Turn    string
	Session string
}

func (r InspectorRail) spendBlock(width int) (railBlock, bool) {
	s := r.Spend
	if s == nil || (s.Turn == "" && s.Session == "") {
		return railBlock{}, false
	}
	// The bill reads the same from send to send. Collapsed, the heading
	// carries the session total beside the turn's cost, and the shares, which
	// add up to it, are /stats's.
	b := railBlock{heading: railHeading("SPEND", sty.body.Render(s.Turn), sty.body, width), class: railStatic}
	if s.Session != "" {
		collapsed := sty.dim.Render("session " + s.Session)
		if s.Turn != "" {
			collapsed = sty.body.Render(s.Turn) + sty.dim.Render(" · ") + collapsed
		}
		b.collapsedHeading = railHeading("SPEND", collapsed, sty.body, width)
	}
	if s.Session != "" {
		// The total is the one figure the block is read for, and the one a
		// short rail keeps.
		b.rows = append(b.rows, railLine{text: railRow(sty.dim.Render("session total · "+s.Session), "", width, inspectorIndent),
			pinned: true, stated: true})
	}
	return b, true
}
