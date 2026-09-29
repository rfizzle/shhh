package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// CockpitMode classifies the permission-mode segment's rendering
// (docs/interface/surfaces.md#the-input-frame).
type CockpitMode int

const (
	CockpitPermissive CockpitMode = iota // ⏵⏵ green
	CockpitGated                         // ⏸ amber
	CockpitChecking                      // ✦ classifier deciding
)

// Cockpit is the status-bar rail of session vitals. It is a passive
// renderer: the host feeds it current values and renders View every frame.
// When the bar overflows, right-side segments drop first.
type Cockpit struct {
	Mode     string
	ModeKind CockpitMode
	// Round is the tool-round counter segment ("round 7 of 25"); empty hides it.
	Round string
	// CtxPct drives the 8-cell context meter; negative hides it.
	CtxPct int
	// CtxWord is the meter's label: `context` where the terminal has the
	// columns for the word, `ctx` where it does not. The host decides,
	// because the rung is a terminal width and this rail is handed a content
	// width. Empty is `ctx`.
	CtxWord string
	// WarnPct/AlertPct override the meter's warning-color thresholds (0 keeps
	// the defaults), so the host can match its own trim warnings.
	WarnPct  int
	AlertPct int
	// Tokens is the usage segment ("↑41.2k ↓9.8k", and every digit of it
	// while a turn is still spending them — odometer.go); Spend the cost
	// ("$0.14"). Both arrive already formatted, because which resolution the
	// moment calls for is a fact about the session rather than about this
	// rail. The host fills Tokens only where it cannot fill Spend: the pair
	// stands in for a price nobody knows, and beside one it is a second
	// reading of the same bill.
	Tokens string
	Spend  string
	// Agents is the live sub-agent count, a child waiting on the user among
	// them.
	Agents int
	// Extra segments (queued steering, policy label, …) render after the
	// built-ins.
	Extra []string
}

// modeSegment renders the always-present permission-mode segment.
func (c Cockpit) modeSegment() string {
	switch c.ModeKind {
	case CockpitChecking:
		return sty.SpinText.Render("✦ " + c.Mode)
	case CockpitPermissive:
		return sty.Add.Render("⏵⏵ " + c.Mode)
	default:
		return sty.Accent.Render("⏸ " + c.Mode)
	}
}

// CtxMeter is a vitals rail's context segment: the shared eight-cell Meter
// with its number ahead of the bar — `context 62% ▰▰▰▰▰▱▱▱`, or `ctx` where
// the host says the terminal is too narrow for the word — which is how every
// rail that carries one draws it. The percentage leads because it is the
// figure the reader is after and the bar is the shape it is read against; a
// rail that put the bar first made the eye cross it to reach the number
// (docs/interface/surfaces.md#the-input-frame).
//
// It is exported so a rail scoped to something other than this session — an
// attached child's, whose vitals the host assembles itself — draws the same
// pressure the same way rather than building a meter of its own.
func CtxMeter(word string, pct, warn, alert int) string {
	if word == "" {
		word = "ctx"
	}
	return Meter{
		Pct:        pct,
		Cells:      MeterCellsVitals,
		Tone:       MeterPressure,
		Label:      word,
		ValueFirst: true,
		Warn:       warn,
		Alert:      alert,
	}.View()
}

// ctxMeter renders the context occupancy bar with its warning colors — the
// shared Meter, so the vitals rail and the inspector rail cannot
// report the same pressure two ways.
func (c Cockpit) ctxMeter() string {
	return CtxMeter(c.CtxWord, c.CtxPct, c.WarnPct, c.AlertPct)
}

// agentsSegment renders the sub-agent count, `◇3`. It is a count and carries
// no badge for a child waiting on the user: that child says so on its own
// lane, and the frame's title counts its ask among the decisions waiting
// (docs/interface/departures.md#the-childrens-tally-says-who-needs-you-first).
func (c Cockpit) agentsSegment() string {
	return sty.Info.Render(fmt.Sprintf("◇%d", c.Agents))
}

// Rail drop ranks (docs/interface/surfaces.md#the-input-frame): when a
// frame rail overflows, the highest rank present is dropped first.
// The session's rail sheds the round counter, the extras and the agent
// count; context pressure, spend, and error state are never removed, and the
// mode segment is never dropped. The token pair standing in for a spend
// nobody can price takes the spend's rank, because it is the only account the
// rail has; beside a spend it is a second reading of the same bill and sheds
// at the tokens rank. The model and
// the reasoning level are the header's, so nothing on the session's rail
// sits at the detail rank — an attached child's name is the one field that
// does.
const (
	RailKeep   = iota // mode — never dropped
	RailVital         // context meter, spend or the token pair standing in for it
	RailNormal        // round counter, extras, agent count
	RailTokens        // token counts beside a spend
	RailDetail        // an attached child's name — dropped first
)

// RailSegment is one cockpit segment prepared for embedding in a frame rail
// : the styled text plus its drop rank.
type RailSegment struct {
	Text string
	Drop int
}

// RailSegments returns the cockpit's segments in display order with their
// drop ranks, for hosts that embed the cockpit segments into frame rails
// instead of rendering the free-floating bar.
func (c Cockpit) RailSegments() []RailSegment {
	segs := []RailSegment{{Text: c.modeSegment(), Drop: RailKeep}}
	if c.Round != "" {
		segs = append(segs, RailSegment{Text: sty.Status.Render(c.Round), Drop: RailNormal})
	}
	if c.CtxPct >= 0 {
		segs = append(segs, RailSegment{Text: c.ctxMeter(), Drop: RailVital})
	}
	if c.Tokens != "" {
		drop := RailTokens
		if c.Spend == "" {
			drop = RailVital
		}
		segs = append(segs, RailSegment{Text: sty.Status.Render(c.Tokens), Drop: drop})
	}
	if c.Spend != "" {
		segs = append(segs, RailSegment{Text: sty.Status.Render(c.Spend), Drop: RailVital})
	}
	for _, e := range c.Extra {
		segs = append(segs, RailSegment{Text: sty.Status.Render(e), Drop: RailNormal})
	}
	if c.Agents > 0 {
		segs = append(segs, RailSegment{Text: c.agentsSegment(), Drop: RailNormal})
	}
	return segs
}

// FitRail joins segments with sep, dropping the rightmost segment of the
// highest drop rank until the rail fits the width. The last survivor is
// clipped when it alone overflows.
func FitRail(segs []RailSegment, sep string, width int) string {
	kept := append([]RailSegment(nil), segs...)
	for {
		parts := make([]string, len(kept))
		for i, s := range kept {
			parts[i] = s.Text
		}
		joined := strings.Join(parts, sep)
		if lipgloss.Width(joined) <= width || len(kept) <= 1 {
			return Clip(joined, width)
		}
		worstIdx, worst := 0, -1
		for i, s := range kept {
			if s.Drop >= worst {
				worst, worstIdx = s.Drop, i
			}
		}
		kept = append(kept[:worstIdx], kept[worstIdx+1:]...)
	}
}

// View assembles the rail, dropping trailing segments when the width runs
// out.
func (c Cockpit) View(width int) string {
	segments := []string{c.modeSegment()}
	if c.Round != "" {
		segments = append(segments, sty.Status.Render(c.Round))
	}
	if c.CtxPct >= 0 {
		segments = append(segments, c.ctxMeter())
	}
	if c.Tokens != "" {
		segments = append(segments, sty.Status.Render(c.Tokens))
	}
	if c.Spend != "" {
		segments = append(segments, sty.Status.Render(c.Spend))
	}
	for _, e := range c.Extra {
		segments = append(segments, sty.Status.Render(e))
	}
	if c.Agents > 0 {
		segments = append(segments, c.agentsSegment())
	}

	for {
		left := strings.Join(segments, sty.Status.Render(" · "))
		if pad := width - lipgloss.Width(left); pad >= 0 {
			return left + strings.Repeat(" ", pad)
		}
		if len(segments) > 1 {
			segments = segments[:len(segments)-1]
			continue
		}
		return Clip(left, width)
	}
}
