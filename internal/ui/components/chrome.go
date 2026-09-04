package components

// The chrome the take-over screens share
// (docs/interface/surfaces.md#the-supporting-screens).
//
// Seven surfaces open the same way: the surface's name on the left, what it
// offers on the right, a rule under both, a body that spends whatever rows
// are left, and a key row at the foot. They were seven copies of that, and
// the copies had drifted. Three of them clipped the left half and let the
// keys go, which on a narrow terminal is a take-over surface with nothing on
// screen saying how to leave it; three clipped the right; one of the seven
// dropped its subject whole and the rest cut it mid-word. Drift like that is
// invisible in review — each screen reads correctly on its own, and only the
// four widths side by side show that they disagree.
//
// So the skeleton is here and a screen supplies its parts. What a screen
// still owns is everything that is a fact about that screen: what its title
// says, what it is counting, which keys it offers, and what its body draws in
// the rows the chrome leaves it.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Sized is a surface the host hands the terminal's rectangle to.
//
// Height went by three names before this: `MaxLines` on most of the
// components, `Height` on the viewers, and the private `budget` each screen
// worked out from whichever of the two it had. A host that set the wrong one
// got a surface that drew past the bottom of the terminal, with nothing
// failing to say so. One method closes that.
//
// The width is passed with the height because a surface is given a rectangle
// rather than a number. A surface that lays itself out from the width it is
// rendered at may ignore it here; the seven screens do.
type Sized interface {
	SetSize(width, height int)
}

// Keyed is a surface that answers one key press with a result of its own
// type: done reports the surface is finished, and the result is what it
// decided. It is a type parameter rather than `any` because the alternative
// was twenty type assertions at the call sites, every one of which is a
// question the compiler could have answered — and a result renamed on one
// side of one of those compiles and then never matches.
type Keyed[R any] interface {
	Update(msg tea.KeyPressMsg) (done bool, result R)
}

// textureMark is what an empty run of chrome is filled with: a diagonal
// rather than a flat rule, so a screen's title rule and a card's top edge are
// the same material and read as one product rather than as two borrowed
// widgets (docs/interface/surfaces.md#the-supporting-screens).
//
// It is a texture and not a gradient for the reason the working label's sweep
// is two rungs and not a blend: a gradient is a run of colours the palette
// does not name, and a token the table does not hold is a token the mono swap
// cannot answer for and the mono goldens cannot check. A diagonal costs one
// glyph and no colour at all.
const textureMark = "╱"

// plainMark is the flat rule the texture collapses to. The diagonal is
// decoration — it says nothing the rule does not — so a palette with two greys
// to spend declines it exactly the way it declines the sweep's crest, and the
// row it leaves behind is the rule that was always there
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
const plainMark = "─"

// textureFill is width columns of whichever of the two the palette is
// carrying. A caller paints it; this decides only the material.
func textureFill(width int) string {
	if width <= 0 {
		return ""
	}
	if Mono() {
		return strings.Repeat(plainMark, width)
	}
	return strings.Repeat(textureMark, width)
}

// screenRule is the horizontal rule a screen's panes and sections are divided
// by. It stays flat: the texture marks where a surface *ends*, and a rule
// between two halves of one surface would be saying the opposite.
func screenRule(width int) string { return sty.Dim.Render(strings.Repeat(plainMark, max(width, 0))) }

// titleRule is the rule under a screen's header — the edge of the surface
// rather than a division inside it, which is why it is the one drawn in the
// texture. A card's top edge is the same row on a smaller frame, and drawing
// the two in the same material is the whole of what makes them one product
// (docs/interface/surfaces.md#the-supporting-screens).
func titleRule(width int) string { return sty.Dim.Render(textureFill(max(width, 0))) }

// screenTitle is a header's first field: the surface's own name, in the one
// treatment that is never dropped. It is clipped instead, and only once
// everything beside it has gone.
func screenTitle(name string) RailSegment {
	return RailSegment{Text: brightStyle().Render(name), Drop: RailKeep}
}

// screenField is a field that continues the title in dim, carrying the
// separator that joins it to whatever is in front of it. The separator
// travels with the field rather than being painted between fields for two
// reasons: a dropped field takes its ` · ` with it, so a row can never end in
// a dangling separator; and the fields do not share a colour — a spinner, a
// percentage and a warning about unwritten changes are all fields here, and a
// separator painted by the rail would have to pick one of them.
func screenField(text string) RailSegment {
	return RailSegment{Text: sty.Dim.Render(" · " + text), Drop: RailDetail}
}

// ScreenHeader is the row every take-over screen opens with: what the surface
// is on the left, what it offers on the right.
//
// The left half is a rail, so the two ways these headers used to differ are
// one thing now — a drop rank. A field that must survive its neighbours is
// ranked below them; the title is `RailKeep` and is the only field that is
// clipped rather than dropped, because a header that has given up its own
// name has stopped being one.
//
// The right half is fitted first and the left into what is left of the row.
// That is the rule the three screens which clipped the other way had wrong:
// the tally goes before the keys do, on every screen, because a reading the
// header is counting is something a narrow terminal can do without and a
// stated way out of a take-over is not
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard,
// invariant 5).
type ScreenHeader struct {
	// Left is the surface's name and the fields that continue it, in reading
	// order. screenTitle and screenField build the two ordinary shapes.
	Left []RailSegment
	// Keys is the run of keys the header ends with, unpainted — the header
	// paints it dim, along with the separator that joins the tally to it, so
	// the run reads as one field however much of it is showing.
	Keys string
	// Tally is the reading that rides in front of the keys, already painted:
	// the elapsed clock, the spend, the occupancy. It is where the eye
	// already goes for the state of a surface, and it is what the row gives
	// up first.
	Tally string
}

// Row renders the header at the given width.
func (h ScreenHeader) Row(width int) string {
	right := sty.Dim.Render(h.Keys)
	// The tally is measured rather than compared to "": it arrives painted,
	// and a style renders a pair of escapes around an empty string, so a
	// screen with nothing to count would otherwise draw the separator anyway.
	if lipgloss.Width(h.Tally) > 0 {
		if tallied := h.Tally + sty.Dim.Render(" · "+h.Keys); lipgloss.Width(tallied) <= width {
			right = tallied
		}
	}
	// Two columns of gap, so the two halves never read as one run.
	room := width - lipgloss.Width(right) - 2
	if room < 1 {
		return Clip(right, width)
	}
	left := FitRail(h.Left, "", room)
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 {
		return left + strings.Repeat(" ", pad) + right
	}
	return Clip(right, width)
}

// ScreenChrome is the frame a take-over screen is drawn in: the header and
// its rule, whatever else is pinned under them, the body's row budget, and
// the footer.
//
// A screen with no footer ends at its body — the blank row above the keys is
// the footer's, and a screen that draws none would otherwise end in one.
type ScreenChrome struct {
	Header ScreenHeader
	// Head are rows pinned between the rule and the body: a filter row the
	// screen keeps above its list, the profile drafter's step rail.
	Head []string
	// Foot is the key row and whatever annotates it, already rendered.
	Foot []string
	// Notice is the one dim line under the footer, an answer the last key
	// left behind. It clears on the next key, which is the host's business.
	Notice string
	// MaxLines is the screen's whole row budget. Zero is a screen nothing
	// bounds, which drops nothing.
	MaxLines int
	// Reserve is rows the body pins for itself out of its own budget — the
	// sub-surface the settings screen splices in under the row being changed.
	Reserve int
}

// View assembles the screen. body is called once with the rows left for it,
// which is the screen's height less everything pinned around it.
func (c ScreenChrome) View(width int, body func(budget int) []string) string {
	head := append([]string{c.Header.Row(width), titleRule(width), ""}, c.Head...)

	pinned := len(head) + c.Reserve
	if len(c.Foot) > 0 {
		// The blank row that separates the body from the keys is the footer's.
		pinned += 1 + len(c.Foot)
	}
	if c.Notice != "" {
		pinned++
	}

	rows := append(head, body(c.budget(pinned))...)
	if len(c.Foot) > 0 {
		rows = append(append(rows, ""), c.Foot...)
	}
	if c.Notice != "" {
		rows = append(rows, sty.Dim.Render(Clip(c.Notice, width)))
	}
	return strings.Join(rows, "\n")
}

// budget is how many rows the body may spend. An unbounded screen drops
// nothing, which is what a zero budget means to every body here.
func (c ScreenChrome) budget(pinned int) int {
	if c.MaxLines <= 0 {
		return 0
	}
	return max(c.MaxLines-pinned, 1)
}

// KeyFooter is what a take-over screen ends with: the keys it offers, and the
// field that annotates them.
//
// No offer is ever truncated to make room
// (docs/interface/principles.md#fold-never-hide): they are packed greedily
// into as few rows as the width allows, and an offer that will not fit beside
// the one before it starts a row of its own. That is the narrow terminal's
// stack, arrived at rather than branched to. What does give ground is the
// field beside them, which is an annotation.
type KeyFooter struct {
	// Offers are the keys the surface is offering now.
	Offers []KeyOffer
	// Field is the annotation, dim and right-aligned on the first row, where
	// there is room for it. It is what drops: the offers wrap instead.
	Field string
	// Lead is the row the keys annotate rather than the other way round — a
	// diagnostic's counts, where the thing to read is what the run found and
	// the key beside it is the annotation. Where both fit they share a row.
	Lead string
	// Register is every key the surface has, one per row, behind `[?]`.
	Register []KeyOffer
	// Showing reports that the register is open, and the footer is it.
	Showing bool
	// Taken is the footer a sub-surface has taken over — an armed confirm,
	// which is the only thing the keyboard is answering while it is up.
	Taken string
}

// Rows renders the footer at the given width.
func (f KeyFooter) Rows(width int) []string {
	if f.Taken != "" {
		return []string{Clip(f.Taken, width)}
	}
	if f.Showing {
		rows := make([]string, 0, len(f.Register)+1)
		for _, offer := range f.Register {
			rows = append(rows, Clip(keyOffers([]KeyOffer{offer}), width))
		}
		return append(rows, Clip(keyOffers([]KeyOffer{hideKeysOffer()}), width))
	}
	if f.Lead != "" {
		if len(f.Offers) == 0 {
			return []string{f.Lead}
		}
		// The keys annotate the lead where both fit, and take a row of their
		// own where they do not.
		run := keyOffers(f.Offers)
		if pad := width - lipgloss.Width(f.Lead) - lipgloss.Width(run); pad >= 2 {
			return []string{f.Lead + strings.Repeat(" ", pad) + run}
		}
		return append([]string{f.Lead}, packOffers(f.Offers, width)...)
	}
	rows := packOffers(f.Offers, width)
	if len(rows) == 0 || f.Field == "" {
		return rows
	}
	painted := sty.Dim.Render(f.Field)
	if pad := width - lipgloss.Width(rows[0]) - lipgloss.Width(painted); pad >= 2 {
		rows[0] += strings.Repeat(" ", pad) + painted
	}
	return rows
}

// packOffers lays the key offers out in as few rows as the width allows.
// They are what the surface can do, so none of them is truncated away; a
// narrow terminal gets more rows instead of fewer offers.
func packOffers(offers []KeyOffer, width int) []string {
	return packOffersIn(offers, width, true)
}

// packOffersIn is the same, in whichever treatment the keyboard puts the run
// in. A takeover surface holds the keyboard by definition and always
// passes true; only a transcript row has the other state.
func packOffersIn(offers []KeyOffer, width int, live bool) []string {
	paint := keyOffers
	if !live {
		paint = inertOffers
	}
	var rows []string
	line := []KeyOffer{}
	flush := func() {
		if len(line) > 0 {
			rows = append(rows, paint(line))
			line = nil
		}
	}
	for _, o := range offers {
		next := append(append([]KeyOffer{}, line...), o)
		if len(line) > 0 && lipgloss.Width(paint(next)) > width {
			flush()
		}
		line = append(line, o)
	}
	flush()
	return rows
}

// hintRows renders a run of hint segments — a selector's `↑↓ move`, a card's
// `[y] to allow`. They pack the other way round from the offers above, and
// the difference is not drift: an offer run is measured against the width it
// is drawn at, and a hint run is drawn inside a frame the caller has already
// budgeted for, so it is joined into one line while the terminal is wide
// enough to read one and stacked one per segment when it is not. Packing it
// greedily against a width that is four columns wider than the frame is how
// a hint row ends up clipped by the border it was measured past.
func hintRows(segments []string, width int) []string {
	joined := strings.Join(segments, " · ")
	if width >= narrowWidth || lipgloss.Width(joined) <= width-cardFrameWidth {
		return []string{sty.Hint.Render(joined)}
	}
	rows := make([]string, 0, len(segments))
	for _, seg := range segments {
		rows = append(rows, sty.Hint.Render(seg))
	}
	return rows
}
