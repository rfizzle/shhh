package components

// The spend screen (docs/interface/surfaces.md#the-supporting-screens): the
// session's whole bill, the ledger the rail's SPEND block draws in shares.
//
// The list is the bill cut three ways under the session total that heads it:
// by the model that answered, each with the kinds of request that spent on
// it and what the children that ran on it cost, the block's own row; by
// child, each sub-agent's share by name; and by turn, what each turn cost as
// its close row states it. The three cuts are three readings of one total,
// not three parts of it: a child's share is on its model's row and on its
// own. The preview is the row under the pointer laid out, and `[enter]` on a
// turn opens it on the turns screen. This is a renderer; the figures are the
// host's.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// spendStackWidth is the width below which the panes stack. The preview
	// is a short account and one line per kind of request, so the panes
	// stand side by side as soon as the alerts screen's do.
	spendStackWidth = 88
	// spendListMin / spendListMax bound the list column. A row is a name,
	// the kinds of request or the model it ran on, and a cost; past the
	// ceiling the columns are worth more to the account beside it.
	spendListMin = 30
	spendListMax = 60
	// spendMinPreview is the smallest preview the stacked layout leaves
	// standing: the title, and the first line of the account.
	spendMinPreview = 3
	// spendLabelWidth is the account's label column: the longest kind of
	// request the ledger names, and the gap after it.
	spendLabelWidth = 13
)

// SpendKind is which cut of the bill a row belongs to.
type SpendKind int

const (
	// SpendTotal is the session total, the row the list opens on.
	SpendTotal SpendKind = iota
	// SpendModel is one model's share, the SPEND block's own row.
	SpendModel
	// SpendChild is one sub-agent's share, by name.
	SpendChild
	// SpendTurn is one turn's cost, as its close row states it.
	SpendTurn
)

// SpendPart is one kind of request on a row's account: the word the rail
// names it by, what it cost, and the tokens and requests behind that.
type SpendPart struct {
	Word     string
	Cost     string
	Tokens   string
	Requests int
}

// SpendRow is one row of the bill, already resolved to what the screen
// draws.
type SpendRow struct {
	Kind SpendKind
	// Name is the model's or the child's name; the total and a turn carry
	// none.
	Name string
	// Cost is the row's figure: the session total, a model's own requests
	// (its children's share is Children, beside it, as on the rail), a
	// child's share, or a turn's cost.
	Cost string
	// Parts are the kinds of request behind the figure — the session's
	// kinds on the total, a model's own kinds on a model — in the order they
	// first billed.
	Parts []SpendPart
	// Children is what the sub-agents that ran on a model cost.
	Children string
	// Models are the models a child ran on.
	Models []string
	// Tokens and Requests are what the figure was billed for.
	Tokens   string
	Requests int
	// Turn is a turn row's turn, as the turns screen holds it — its number,
	// how it ended and how long it took are that screen's words for it.
	Turn *TurnsItem
}

// SpendScreen is `/stats`: a takeover in the chat, full width, owning the
// keyboard for as long as it is up.
type SpendScreen struct {
	// The pointer is an index into Rows.
	listScreen[SpendRow]
	// Rows are the total, then the models, the children and the turns
	// (newest first) — the order the list draws them in.
	Rows []SpendRow
	// MaxLines bounds the screen height. 0 is unbounded.
	MaxLines int

	optAt map[int]int
}

// spendResult is what the screen leaves with: nothing, or the turn the
// reader asked to open on the turns screen.
type spendResult struct {
	Turn int64
}

// Update is the screen's whole keyboard: it moves, it opens a turn, it shows
// its keys, and it leaves. It reports whether the screen is done and, where
// it is done by opening a turn, which one.
func (s *SpendScreen) Update(msg tea.KeyPressMsg) (done bool, result spendResult) {
	pressed := msg.String()
	switch {
	case s.moved(s.Rows, pressed, keys.Screen.Move):
	case keys.Is(pressed, keys.Screen.Take):
		// Only on a turn: a key that cannot act is not an offer, and the
		// footer draws it grey (invariant 5).
		if r := s.current(s.Rows); r != nil && r.Kind == SpendTurn && r.Turn != nil {
			return true, spendResult{Turn: r.Turn.N}
		}
	case keys.Is(pressed, keys.Screen.List):
		s.keys = !s.keys
	case keys.Is(pressed, keys.Screen.Quit):
		return true, spendResult{}
	}
	return false, spendResult{}
}

// SetSize gives the screen the terminal's rectangle. It lays itself out from
// the width it is rendered at, so only the height is kept.
func (s *SpendScreen) SetSize(_, height int) { s.MaxLines = height }

// View renders the screen: the shared chrome, with the two panes in the rows
// it leaves.
func (s *SpendScreen) View(width int) string { return s.view(width, s) }

// chrome is the header over the panes and the keys under them.
func (s *SpendScreen) chrome(width int) screenChrome {
	field := s.footField()
	return screenChrome{
		header:   s.header(),
		foot:     s.footer(s.offers(width, field), s.keyList(), field).rows(width),
		maxLines: s.MaxLines,
	}
}

// panes is the body, split the way every screen with a list and a preview
// splits it (screenpanes.go): on the left the total, and the bill under it
// three ways.
func (s *SpendScreen) panes() screenPanes {
	return screenPanes{
		stackAt: spendStackWidth, listMin: spendListMin,
		listMax: spendListMax, minPreview: spendMinPreview,
		list: func(width, budget int) []string {
			return s.listRows(s.Rows, "the session has not been billed for anything yet", width, budget)
		},
		preview: s.previewRows,
	}
}

// previewRows is the right pane: the row under the pointer's account.
func (s *SpendScreen) previewRows(width int) []string {
	r := s.current(s.Rows)
	if r == nil {
		return []string{sty.dim.Render(Clip("nothing selected", width))}
	}
	title, word := spendTitle(*r)
	rows := []string{paneTitle(brightStyle().Render(title), sty.dim.Render(word), width), ""}
	var fields []field
	if r.Kind == SpendTurn && r.Turn != nil {
		fields = append(fields, field{"spent", r.Cost})
		if r.Turn.Close != nil && r.Turn.Close.Elapsed != "" {
			fields = append(fields, field{"took", r.Turn.Close.Elapsed})
		}
	} else {
		fields = append(fields, field{"spent", r.Cost})
		switch len(r.Models) {
		case 0:
		case 1:
			fields = append(fields, field{"model", r.Models[0]})
		default:
			fields = append(fields, field{"models", strings.Join(r.Models, " · ")})
		}
		if r.Tokens != "" {
			fields = append(fields, field{"tokens", r.Tokens})
		}
		if r.Requests > 0 {
			fields = append(fields, field{"requests", fmt.Sprint(r.Requests)})
		}
	}
	for _, f := range fields {
		if f.value != "" {
			rows = append(rows, spendField(f.label, sty.body.Render(f.value), width))
		}
	}
	if len(r.Parts) > 0 || r.Children != "" {
		rows = append(rows, "", "  "+sty.status.Render("by kind of request"))
		for _, p := range r.Parts {
			rows = append(rows, spendField(p.Word, sty.body.Render(spendPart(p)), width))
		}
		if r.Children != "" {
			rows = append(rows, spendField("children", sty.body.Render(r.Children+" ◇"), width))
		}
	}
	return rows
}

// spendField is one line of the account: a dim label in a fixed column and
// the value beside it, clipped to the pane.
func spendField(label, value string, width int) string {
	lead := "  " + sty.status.Render(fmt.Sprintf("%-*s", spendLabelWidth, label))
	return Clip(lead+value, width)
}

// spendPart is one kind of request's line: its cost, then what it was billed
// for.
func spendPart(p SpendPart) string {
	parts := []string{p.Cost}
	if p.Tokens != "" {
		parts = append(parts, p.Tokens)
	}
	if p.Requests > 0 {
		parts = append(parts, plural(p.Requests, "request"))
	}
	return strings.Join(parts, " · ")
}

// spendTitle is what the preview names a row, and the dim word beside it
// saying which cut of the bill it is from.
func spendTitle(r SpendRow) (string, string) {
	switch r.Kind {
	case SpendModel:
		return r.Name, "model"
	case SpendChild:
		return "◇ " + r.Name, "child"
	case SpendTurn:
		if r.Turn != nil {
			word, _ := turnWord(*r.Turn)
			return fmt.Sprintf("turn %d", r.Turn.N), word
		}
	}
	return "session total", "the whole bill"
}

// spendGroup is the heading a kind of row is listed under; the total heads
// the list on its own and has none.
func spendGroup(k SpendKind) string {
	switch k {
	case SpendModel:
		return "by model"
	case SpendChild:
		return "by child"
	case SpendTurn:
		return "by turn"
	}
	return ""
}

// option is a row as the list draws it: a model in the SPEND block's own
// words — its own cost, the kinds of request, and its children's share after
// their ◇ — a child by name with the model it ran on, a turn in the turns
// screen's mark and word.
func (r SpendRow) option() SelectOption {
	opt := SelectOption{Meta: r.Cost, metaTone: ToneQuiet}
	switch r.Kind {
	case SpendTotal:
		opt.Label = "session total"
	case SpendModel:
		opt.Label = r.Name
		var words []string
		for _, p := range r.Parts {
			words = append(words, p.Word)
		}
		if len(words) > 0 {
			opt.Detail = append(opt.Detail, DetailSpan{Text: strings.Join(words, " · "), Tone: ToneQuiet})
		}
		if r.Children != "" {
			if len(opt.Detail) > 0 {
				opt.Detail = append(opt.Detail, DetailSpan{Text: " · ", Tone: ToneQuiet})
			}
			opt.Detail = append(opt.Detail, DetailSpan{Text: r.Children + " ◇", Tone: ToneQuiet})
		}
	case SpendChild:
		opt.Label = "◇ " + r.Name
		if len(r.Models) > 0 {
			opt.Detail = []DetailSpan{{Text: strings.Join(r.Models, " · "), Tone: ToneQuiet}}
		}
	case SpendTurn:
		if r.Turn != nil {
			opt.Label = fmt.Sprintf("%s turn %d", turnGlyph(*r.Turn), r.Turn.N)
			opt.Value, opt.valueTone = turnWord(*r.Turn)
		}
	}
	return opt
}

// header names the surface, what the bill is cut into, and the total.
func (s *SpendScreen) header() screenHeader {
	h := screenHeader{left: []RailSegment{screenTitle("/stats")}, keys: s.headerKeys(keys.Screen.List, keys.Screen.Quit)}
	counts := map[SpendKind]int{}
	total := ""
	for _, r := range s.Rows {
		counts[r.Kind]++
		if r.Kind == SpendTotal {
			total = r.Cost
		}
	}
	for _, c := range []struct {
		kind SpendKind
		noun string
	}{{SpendModel, "model"}, {SpendChild, "child"}, {SpendTurn, "turn"}} {
		if n := counts[c.kind]; n > 0 {
			noun := plural(n, c.noun)
			if c.kind == SpendChild && n > 1 {
				noun = fmt.Sprintf("%d children", n)
			}
			h.left = append(h.left, screenField(noun))
		}
	}
	if total != "" {
		h.tally = sty.dim.Render(total + " spent")
	}
	return h
}

// offers is the key row: the pointer's keys, the turn, and the way out, and
// the last two alone where the field leaves no room for all three. Opening a
// turn is drawn grey off a turn rather than dropped, so the row does not
// change shape under a pointer walking the list.
func (s *SpendScreen) offers(width int, field string) []KeyOffer {
	var acts []KeyOffer
	if r := s.current(s.Rows); r != nil {
		open := keyOfferAs(keys.Screen.Take, "open the turn")
		open.inert = r.Kind != SpendTurn
		acts = append(acts, open)
	}
	acts = append(acts, wayOut(backToPrompt))
	return offersBeside(keyOffer(keys.Screen.Move), acts, field, width)
}

// keyList is every key the screen has, for `[?]`.
func (s *SpendScreen) keyList() []KeyOffer {
	return []KeyOffer{
		keyOfferAs(keys.Screen.Move, "move through the bill"),
		keyOfferAs(keys.Screen.Take, "open the turn under the pointer on the turns screen"),
		wayOut(backToPrompt),
		keyOfferAs(keys.Screen.Quit, backToPrompt),
	}
}

// footField annotates the key row with where the figures come from: the
// ledger the rail's SPEND block reads, so a share here and its row there are
// one figure.
func (s *SpendScreen) footField() string {
	if len(s.Rows) == 0 {
		return ""
	}
	return "the session's bill, as the rail's SPEND block reads it"
}

// sync rebuilds the list from Rows, with a heading over each cut. It runs
// before every View because the host may replace Rows, and the pointer has
// to survive that.
func (s *SpendScreen) sync() {
	s.clamp(len(s.Rows))
	s.optAt = make(map[int]int, len(s.Rows))
	opts := make([]SelectOption, 0, len(s.Rows)+3)
	group := ""
	for i, r := range s.Rows {
		if g := spendGroup(r.Kind); g != group {
			group = g
			if g != "" {
				opts = append(opts, SelectOption{Label: g, Header: true})
			}
		}
		s.optAt[i] = len(opts)
		opts = append(opts, r.option())
	}
	// The count the window's marker states is of rows, not of options: the
	// headings are rows on the screen and not parts of the bill.
	s.show(opts, len(s.Rows), s.optAt[s.Focus])
}
