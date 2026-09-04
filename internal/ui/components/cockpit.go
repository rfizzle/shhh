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
	// Round is the tool-round counter segment ("round 7/25"); empty hides it.
	Round string
	// CtxPct drives the 8-cell context meter; negative hides it.
	CtxPct int
	// WarnPct/AlertPct override the meter's warning-color thresholds (0 keeps
	// the defaults), so the host can match its own trim warnings.
	WarnPct  int
	AlertPct int
	// Tokens is the usage segment ("↑41.2k ↓9.8k", and every digit of it
	// while a turn is still spending them — odometer.go); Spend the cost
	// ("$0.14"). Both arrive already formatted, because which resolution the
	// moment calls for is a fact about the session rather than about this
	// rail.
	Tokens string
	Spend  string
	// Agents is the running sub-agent count; AgentsBlocked adds the ⚠ badge
	// for children waiting on the user.
	Agents        int
	AgentsBlocked int
	// Extra segments (queued steering, policy label, …) render after the
	// built-ins.
	Extra []string
	// Reasoning is the thinking level the session is asking for ("think
	// high"); empty means it is asking for none, and nothing is drawn.
	// It renders right-aligned beside the model, in the same treatment: what
	// the session is answering with is one fact in two halves, and neither
	// half is chrome.
	Reasoning string
	// Model renders right-aligned and is the first thing dropped when narrow.
	Model string
}

// identity renders the right-hand pair — the reasoning level and the model —
// as they appear together, or "" when the session has neither to state.
func (c Cockpit) identity() string {
	switch {
	case c.Reasoning != "" && c.Model != "":
		return sty.Body.Render(c.Reasoning) + sty.Status.Render(" · ") + sty.Body.Render(c.Model)
	case c.Reasoning != "":
		return sty.Body.Render(c.Reasoning)
	case c.Model != "":
		return sty.Body.Render(c.Model)
	}
	return ""
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

// ctxMeter renders the context occupancy bar with its warning colors — the
// shared Meter, so the vitals rail and the inspector rail cannot
// report the same pressure two ways.
func (c Cockpit) ctxMeter() string {
	return Meter{
		Pct:   c.CtxPct,
		Cells: MeterCellsVitals,
		Tone:  MeterPressure,
		Label: "ctx",
		Warn:  c.WarnPct,
		Alert: c.AlertPct,
	}.View()
}

// agentsSegment renders the sub-agent count with the blocked badge, which
// marks children waiting on the user.
func (c Cockpit) agentsSegment() string {
	seg := sty.Info.Render(fmt.Sprintf("◇ %s", plural(c.Agents, "agent")))
	if c.AgentsBlocked > 0 {
		seg += " " + sty.Err.Render(fmt.Sprintf("⚠%d", c.AgentsBlocked))
	}
	return seg
}

// Rail drop ranks (docs/interface/surfaces.md#the-input-frame): when a
// frame rail overflows, the highest rank present is dropped first.
// Model/provider detail goes first, then token counts; context pressure,
// spend, and error/blocked state are never the first fields removed, and the
// mode segment is never dropped.
const (
	RailKeep   = iota // mode — never dropped
	RailVital         // context meter, spend, blocked-agent state
	RailNormal        // round counter, extras, idle agent count
	RailTokens        // token counts — dropped second
	RailDetail        // model/provider detail — dropped first
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
		segs = append(segs, RailSegment{Text: sty.Status.Render(c.Tokens), Drop: RailTokens})
	}
	if c.Spend != "" {
		segs = append(segs, RailSegment{Text: sty.Status.Render(c.Spend), Drop: RailVital})
	}
	for _, e := range c.Extra {
		segs = append(segs, RailSegment{Text: sty.Status.Render(e), Drop: RailNormal})
	}
	if c.Agents > 0 {
		drop := RailNormal
		if c.AgentsBlocked > 0 {
			drop = RailVital
		}
		segs = append(segs, RailSegment{Text: c.agentsSegment(), Drop: drop})
	}
	// The level and the model are separate segments so the rail can drop the
	// model and keep the level: the model is the detail rank the field-drop
	// order names, and the level is the thing the session just changed.
	if c.Reasoning != "" {
		segs = append(segs, RailSegment{Text: sty.Body.Render(c.Reasoning), Drop: RailTokens})
	}
	if c.Model != "" {
		segs = append(segs, RailSegment{Text: sty.Body.Render(c.Model), Drop: RailDetail})
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

// View assembles the rail, dropping the right side first and then trailing
// left segments when the width runs out.
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

	// The right side sheds the model, then the reasoning level, then itself
	//. The stages are a list rather than a chain of conditions because
	// a chain that can re-widen never terminates.
	rights := []string{c.identity(), Cockpit{Reasoning: c.Reasoning}.identity(), ""}
	stage := 0
	right := rights[stage]
	for {
		left := strings.Join(segments, sty.Status.Render(" · "))
		pad := width - lipgloss.Width(left) - lipgloss.Width(right)
		if pad >= 1 || (right == "" && pad >= 0) {
			return left + strings.Repeat(" ", max(pad, 0)) + right
		}
		if stage < len(rights)-1 {
			stage++
			right = rights[stage]
			continue
		}
		if len(segments) > 1 {
			segments = segments[:len(segments)-1]
			continue
		}
		return Clip(left, width)
	}
}
