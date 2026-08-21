package chat

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/safety"
)

// Session approval policy: the permission mode (S-059) decides how each
// approval-gated tool call is handled — manual prompts for everything,
// accept-edits auto-allows file edits, auto defers to policy (allowlist rules
// until the S-060 classifier lands), and plan is read-only. The S-054
// internals still apply inside the prompting modes: [a] on a confirm prompt
// auto-allows the rest of that category for the session, and a config
// allowlist (behavior.command_allowlist) pre-approves specific commands.
// Commands flagged by safety.Check always prompt, in every mode except plan
// (which refuses them like everything else).

// WithCommandAllowlist sets the config-provided command allowlist: commands
// whose leading words match an entry run without an approval prompt, unless
// safety-flagged.
func (m Model) WithCommandAllowlist(list []string) Model {
	m.commandAllowlist = list
	return m
}

// WithApprovalMode sets the session's starting permission mode and the
// Shift+Tab cycle order (S-059); an empty cycle keeps the default order.
func (m Model) WithApprovalMode(mode agent.Mode, cycle []agent.Mode) Model {
	m.mode = mode
	if len(cycle) > 0 {
		m.modeCycle = cycle
	}
	return m
}

// modePolicy assembles the agent-level policy state the mode machine decides
// with.
func (m Model) modePolicy() agent.ModePolicy {
	return agent.ModePolicy{
		Mode:             m.mode,
		AllowEdits:       m.allowAllEdits,
		AllowCommands:    m.allowAllCommands,
		CommandAllowlist: m.commandAllowlist,
	}
}

// policyDecision returns the mode verdict for an approval request and, when
// allowed, the reason shown in the transcript.
func (m Model) policyDecision(req *approvalRequest) (agent.Decision, string) {
	action := agent.Action{Kind: agent.ActionOther}
	switch req.kind {
	case approvalExec:
		action = agent.Action{
			Kind:          agent.ActionCommand,
			Command:       req.command,
			SafetyFlagged: len(safety.Check(req.command)) > 0,
		}
	case approvalDiff:
		action = agent.Action{Kind: agent.ActionEdit}
	}
	return m.modePolicy().Decide(action)
}

// modeSegment is the always-present status bar segment for the active
// permission mode (DESIGN-TUI.md §8): permissive modes render ⏵⏵, gated
// modes ⏸.
func (m Model) modeSegment() string {
	name := strings.ReplaceAll(m.mode.String(), "-", " ")
	switch m.mode {
	case agent.ModeAcceptEdits, agent.ModeAuto:
		return modePermissiveStyle.Render("⏵⏵ " + name)
	default:
		return modeGatedStyle.Render("⏸ " + name)
	}
}

// modeStatus describes the active mode and cycle for /mode with no argument.
func (m Model) modeStatus() string {
	cycle := m.modeCycle
	if len(cycle) == 0 {
		cycle = agent.DefaultCycle()
	}
	names := make([]string, len(cycle))
	for i, mode := range cycle {
		names[i] = mode.String()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Mode: %s — %s.\n", m.mode, m.mode.Describe())
	sb.WriteString("Cycle (Shift+Tab): " + strings.Join(names, " → ") + "\n")
	sb.WriteString("Set with /mode <manual|accept-edits|auto|plan>.")
	return sb.String()
}

// policyLabel is the status bar segment for the S-054 session grants; empty
// in the default everything-prompts state.
func (m Model) policyLabel() string {
	var parts []string
	if m.allowAllEdits {
		parts = append(parts, "edits")
	}
	if m.allowAllCommands {
		parts = append(parts, "cmds")
	}
	if len(m.commandAllowlist) > 0 {
		parts = append(parts, "allowlist")
	}
	if len(parts) == 0 {
		return ""
	}
	return "auto: " + strings.Join(parts, "+")
}

// policyHelp describes the active approval policy, appended to /help output.
func (m Model) policyHelp() string {
	status := func(on bool) string {
		if on {
			return "auto-allow (this session)"
		}
		return "ask"
	}
	var sb strings.Builder
	sb.WriteString("Approval policy:\n")
	fmt.Fprintf(&sb, "  mode:      %s (%s)\n", m.mode, m.mode.Describe())
	sb.WriteString("  edits:     " + status(m.allowAllEdits) + "\n")
	sb.WriteString("  commands:  " + status(m.allowAllCommands) + "\n")
	if n := len(m.commandAllowlist); n > 0 {
		fmt.Fprintf(&sb, "  allowlist: %d command pattern(s) from config auto-approve\n", n)
	}
	sb.WriteString("  Safety-flagged commands always ask.")
	return sb.String()
}

// allowlistMatches reports whether command's leading words exactly match all
// words of some allowlist entry ("go test" matches "go test ./..."). The
// matching lives in internal/agent so headless print mode applies the same
// policy.
func allowlistMatches(allowlist []string, command string) bool {
	return agent.AllowlistMatches(allowlist, command)
}
