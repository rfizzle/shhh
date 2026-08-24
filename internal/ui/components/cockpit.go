package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// CockpitMode classifies the permission-mode segment's rendering
// (DESIGN-TUI.md §8).
type CockpitMode int

const (
	CockpitPermissive CockpitMode = iota // ⏵⏵ green
	CockpitGated                         // ⏸ amber
	CockpitChecking                      // ✦ classifier deciding
)

// Context-meter warning thresholds, matching S-055's trim warnings.
const (
	ctxWarnPct  = 70
	ctxAlertPct = 90
	ctxBarCells = 8
)

// Cockpit is the status-bar rail of session vitals (§8). It is a passive
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
	// the defaults), so the host can match its own trim warnings (S-055).
	WarnPct  int
	AlertPct int
	// Tokens is the usage segment ("↑41.2k ↓9.8k"); Spend the cost ("$0.14").
	Tokens string
	Spend  string
	// Agents is the running sub-agent count; AgentsBlocked adds the ⚠ badge
	// for children waiting on the user.
	Agents        int
	AgentsBlocked int
	// Extra segments (queued steering, policy label, …) render after the
	// built-ins.
	Extra []string
	// Model renders right-aligned and is the first thing dropped when narrow.
	Model string
}

// modeSegment renders the always-present permission-mode segment.
func (c Cockpit) modeSegment() string {
	switch c.ModeKind {
	case CockpitChecking:
		return spinTextStyle.Render("✦ " + c.Mode)
	case CockpitPermissive:
		return addStyle.Render("⏵⏵ " + c.Mode)
	default:
		return accentStyle.Render("⏸ " + c.Mode)
	}
}

// ctxMeter renders the context occupancy bar with its warning colors.
func (c Cockpit) ctxMeter() string {
	warn, alert := ctxWarnPct, ctxAlertPct
	if c.WarnPct > 0 {
		warn = c.WarnPct
	}
	if c.AlertPct > 0 {
		alert = c.AlertPct
	}
	pct := min(max(c.CtxPct, 0), 100)
	filled := pct * ctxBarCells / 100
	bar := fmt.Sprintf("ctx %s%s %d%%",
		strings.Repeat("▰", filled), strings.Repeat("▱", ctxBarCells-filled), pct)
	switch {
	case pct >= alert:
		return errStyle.Bold(true).Render(bar)
	case pct >= warn:
		return accentStyle.Render(bar)
	default:
		return statusStyle.Render(bar)
	}
}

// agentsSegment renders the sub-agent count with the blocked badge, which
// marks children waiting on the user (§9c).
func (c Cockpit) agentsSegment() string {
	seg := infoStyle.Render(fmt.Sprintf("◇ %s", plural(c.Agents, "agent")))
	if c.AgentsBlocked > 0 {
		seg += " " + errStyle.Render(fmt.Sprintf("⚠%d", c.AgentsBlocked))
	}
	return seg
}

// View assembles the rail, dropping the right side first and then trailing
// left segments when the width runs out.
func (c Cockpit) View(width int) string {
	segments := []string{c.modeSegment()}
	if c.Round != "" {
		segments = append(segments, statusStyle.Render(c.Round))
	}
	if c.CtxPct >= 0 {
		segments = append(segments, c.ctxMeter())
	}
	if c.Tokens != "" {
		segments = append(segments, statusStyle.Render(c.Tokens))
	}
	if c.Spend != "" {
		segments = append(segments, statusStyle.Render(c.Spend))
	}
	for _, e := range c.Extra {
		segments = append(segments, statusStyle.Render(e))
	}
	if c.Agents > 0 {
		segments = append(segments, c.agentsSegment())
	}

	right := ""
	if c.Model != "" {
		right = statusStyle.Render(c.Model)
	}
	for {
		left := strings.Join(segments, statusStyle.Render(" · "))
		pad := width - lipgloss.Width(left) - lipgloss.Width(right)
		if pad >= 1 || (right == "" && pad >= 0) {
			return left + strings.Repeat(" ", max(pad, 0)) + right
		}
		if right != "" {
			right = ""
			continue
		}
		if len(segments) > 1 {
			segments = segments[:len(segments)-1]
			continue
		}
		return clip(left, width)
	}
}
