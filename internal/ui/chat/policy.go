package chat

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/safety"
)

// Session approval policy (S-054). The default is maximally safe: every
// approval-gated tool call prompts. The user can loosen that per category —
// pressing [a] on a confirm prompt auto-allows the rest of that category
// (file edits or shell commands) for the session — and a config allowlist
// (behavior.command_allowlist) can pre-approve specific commands. Commands
// flagged by safety.Check always prompt, regardless of policy.

// WithCommandAllowlist sets the config-provided command allowlist: commands
// whose leading words match an entry run without an approval prompt, unless
// safety-flagged.
func (m Model) WithCommandAllowlist(list []string) Model {
	m.commandAllowlist = list
	return m
}

// policyAllows reports whether an approval request may run without prompting
// under the active session policy, and the reason shown in the transcript.
// Generic gated tools and safety-flagged commands never qualify.
func (m Model) policyAllows(req *approvalRequest) (reason string, ok bool) {
	switch req.kind {
	case approvalExec:
		if len(safety.Check(req.command)) > 0 {
			return "", false
		}
		if m.allowAllCommands {
			return "session policy", true
		}
		if allowlistMatches(m.commandAllowlist, req.command) {
			return "allowlist", true
		}
	case approvalDiff:
		if m.allowAllEdits {
			return "session policy", true
		}
	}
	return "", false
}

// allowlistUnsafe are shell metacharacters that could chain a second command
// onto an allowlisted prefix (e.g. "git status; rm -rf ~" prefix-matching the
// entry "git status"), so commands containing any of them always prompt.
const allowlistUnsafe = ";&|`$()<>\n"

// allowlistMatches reports whether command's leading words exactly match all
// words of some allowlist entry ("go test" matches "go test ./...").
func allowlistMatches(allowlist []string, command string) bool {
	if strings.ContainsAny(command, allowlistUnsafe) {
		return false
	}
	words := strings.Fields(command)
	for _, entry := range allowlist {
		pattern := strings.Fields(entry)
		if len(pattern) == 0 || len(pattern) > len(words) {
			continue
		}
		match := true
		for i, w := range pattern {
			if words[i] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// policyLabel is the status bar segment for the active approval policy;
// empty in the default everything-prompts state.
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
	sb.WriteString("  edits:     " + status(m.allowAllEdits) + "\n")
	sb.WriteString("  commands:  " + status(m.allowAllCommands) + "\n")
	if n := len(m.commandAllowlist); n > 0 {
		fmt.Fprintf(&sb, "  allowlist: %d command pattern(s) from config auto-approve\n", n)
	}
	sb.WriteString("  Safety-flagged commands always ask.")
	return sb.String()
}
