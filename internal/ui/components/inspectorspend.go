package components

// The rail's SPEND block: what this turn cost, what the session has cost and
// what the ledger will allow. It is a file of its own so the block that says
// money says it in one place.

import (
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// InspectorSpend is the SPEND block: this turn's cost, the session's bill one
// row per model that answered, and the session total those rows add up to
// (docs/interface/surfaces.md#the-inspector-rail).
type InspectorSpend struct {
	Turn    string
	Models  []InspectorSpendModel
	Session string
}

// InspectorSpendModel is one model's share of the session's bill, as the host
// read it off the ledger: the model's name, what its own requests cost and
// which kinds of request those were, and what the children that ran on it
// cost. The fields stay apart rather than arriving as one line, because the
// block decides what to give up as the rail narrows and a joined string
// leaves it nothing to decide with.
type InspectorSpendModel struct {
	// Model is the name the ledger priced the requests against.
	Model string
	// Cost is what every request on this model except the children's cost,
	// and Sources the kinds of request those were, in the order they first
	// billed — "main" for the session's own turns, then the machinery
	// around them. A kind that spent nothing is not listed.
	Cost    string
	Sources []string
	// Children is what the sub-agents that ran on this model cost, drawn
	// after a ◇ the way every other surface marks a child.
	Children string
}

func (r InspectorRail) spendBlock(width int) (railBlock, bool) {
	s := r.Spend
	if s == nil || (s.Turn == "" && len(s.Models) == 0 && s.Session == "") {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("SPEND", sty.Body.Render(s.Turn), sty.Body, width)}
	room := railRoom(width, "", inspectorIndent)
	for _, m := range s.Models {
		if line := m.fit(room); line != "" {
			b.add(railRow(sty.Dim.Render(line), "", width, inspectorIndent))
		}
	}
	if s.Session != "" {
		// The total is what the rows above it add up to, and the one figure a
		// short rail keeps: its shares fold before it does.
		b.pin(railRow(sty.Dim.Render("session total · "+s.Session), "", width, inspectorIndent))
	}
	return b, true
}

// fit is the row in the widest of its spellings that fits room, giving
// things up in the block's drop order: the kinds of request fold to a count
// behind the first of them, then the model's name shortens to its family
// word, then the name goes, then the kinds go — so the narrowest rail still
// carries every figure, which is what the rows are there to add up. What is
// left over is clipped by the row like any other.
func (m InspectorSpendModel) fit(room int) string {
	name := m.Model
	if name == "" {
		name = "(unnamed)"
	}
	folded := m.Sources
	if len(m.Sources) > 1 {
		folded = []string{m.Sources[0], strconv.Itoa(len(m.Sources)-1) + " more"}
	}
	steps := []struct {
		name    string
		sources []string
	}{
		{name, m.Sources},
		{name, folded},
		{modelFamilyWord(name), folded},
		{"", folded},
		{"", nil},
	}
	var line string
	for _, st := range steps {
		line = m.spell(st.name, st.sources)
		if lipgloss.Width(line) <= room {
			return line
		}
	}
	return line
}

// spell joins one spelling of the row: the name, the model's own cost with
// the kinds after it, and the children's share.
func (m InspectorSpendModel) spell(name string, sources []string) string {
	var parts []string
	if name != "" {
		parts = append(parts, name)
	}
	if m.Cost != "" {
		own := m.Cost
		if len(sources) > 0 {
			own += " " + strings.Join(sources, " · ")
		}
		parts = append(parts, own)
	}
	if m.Children != "" {
		parts = append(parts, m.Children+" ◇")
	}
	return strings.Join(parts, " · ")
}

// modelFamilyWord is the one word of a model's name a narrow rail keeps: the
// second word of it made of letters alone, which is the family in the names
// vendors write (claude-opus-5-5 is opus, gpt-4o-mini is mini), or the first
// where there is no second (gpt-5.2 is gpt). A vendor prefix before a slash
// is not part of the name, and a word no shorter than the name is not a
// shortening.
func modelFamilyWord(name string) string {
	base := name
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	var words []string
	for _, w := range strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' || r == ':' }) {
		if strings.IndexFunc(w, func(r rune) bool { return !unicode.IsLetter(r) }) < 0 {
			words = append(words, w)
		}
	}
	word := base
	switch {
	case len(words) > 1:
		word = words[1]
	case len(words) == 1:
		word = words[0]
	}
	if len(word) >= len(name) {
		return name
	}
	return word
}
