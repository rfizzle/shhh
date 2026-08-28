package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/scope"
)

// Session approval policy: the permission mode (S-059) decides how each
// approval-gated tool call is handled — manual prompts for everything,
// accept-edits auto-allows file edits, auto defers to policy (allowlist
// rules, then the S-060 LLM classifier), and plan is read-only. The S-054
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

// WithReadOnlyCommands configures the read-only inspection allowlist: extra
// entries beyond the built-in list, and whether the built-in list auto-runs
// at all (behavior.read_only_commands / behavior.read_only_auto).
func (m Model) WithReadOnlyCommands(extra []string, disabled bool) Model {
	m.readOnlyExtra = extra
	m.readOnlyDisabled = disabled
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
// with. The session's own command grants join the config allowlist, because
// they are the same kind of thing — leading words that pre-approve a command
// — and the only difference is that one of them can be revoked.
func (m Model) modePolicy() agent.ModePolicy {
	return agent.ModePolicy{
		Mode:             m.mode,
		AllowEdits:       m.allowAllEdits,
		AllowCommands:    m.allowAllCommands,
		EditDirs:         m.editDirGrants,
		CommandAllowlist: m.allowlist(),
		ReadOnlyExtra:    m.readOnlyExtra,
		ReadOnlyDisabled: m.readOnlyDisabled,
	}
}

// allowlist is the config's command allowlist and the session's own, in that
// order. It allocates only where the session has added something, so the
// common case hands the config slice straight through.
func (m Model) allowlist() []string {
	if len(m.commandGrants) == 0 {
		return m.commandAllowlist
	}
	out := make([]string, 0, len(m.commandAllowlist)+len(m.commandGrants))
	return append(append(out, m.commandAllowlist...), m.commandGrants...)
}

// grants is the session's four grants as one value, for the surfaces that
// carry all of them: the sub-agent supervisor (S-086) and /permissions revoke.
func (m Model) grants() agent.Grants {
	return agent.Grants{
		AllEdits:    m.allowAllEdits,
		AllCommands: m.allowAllCommands,
		EditDirs:    m.editDirGrants,
		Commands:    m.commandGrants,
	}
}

// grantCommand records [a] on a command card: the command's leading words,
// pre-approving the shape of it rather than every command there is. A prefix
// already covered by the allowlist — the config's or an earlier grant's —
// adds nothing, so pressing [a] twice on the same shape of command records it
// once.
func (m *Model) grantCommand(command string) string {
	prefix := agent.GrantPrefix(command)
	if prefix == "" {
		return ""
	}
	if !agent.AllowlistMatches(m.allowlist(), prefix) {
		m.commandGrants = append(m.commandGrants, prefix)
	}
	return prefix
}

// grantEditDir records [a] on an edit card: the directory the file lives in,
// which is the scope a reader approving a file in it has actually looked at.
// It grants the directory and everything under it, and nothing beside it.
func (m *Model) grantEditDir(path string) string {
	dir := filepath.Dir(path)
	if dir == "" {
		return ""
	}
	if !agent.PathUnder(m.editDirGrants, filepath.Join(dir, "x")) {
		m.editDirGrants = append(m.editDirGrants, dir)
	}
	return dir
}

// revokeGrants drops every session grant and reports what went, in the order
// the status lines name them. Config's own allowlist is untouched: it is not
// this session's to take back.
func (m *Model) revokeGrants() []string {
	var gone []string
	if m.allowAllEdits {
		gone = append(gone, "every edit")
	}
	if m.allowAllCommands {
		gone = append(gone, "every command")
	}
	for _, d := range m.editDirGrants {
		gone = append(gone, "edits in "+displayDir(d))
	}
	gone = append(gone, quoteAll(m.commandGrants)...)
	m.allowAllEdits, m.allowAllCommands = false, false
	m.editDirGrants, m.commandGrants = nil, nil
	return gone
}

// scopeSuffix qualifies an "ask" with the scoped grants that already answer
// some of it. "ask" and "ask, except in 2 directories" are different states,
// and the second one is what [a] leaves behind.
func scopeSuffix(n int, one, many string) string {
	if n == 0 {
		return ""
	}
	if n == 1 {
		return ", except in 1 " + one
	}
	return fmt.Sprintf(", except in %d %s", n, many)
}

// noteGrant puts what [a] just granted into the transcript. A grant that is
// not said is a grant nobody can revoke: the card is gone a frame later, and
// the vitals chip can only say that something was granted, not what.
func (m *Model) noteGrant(text string) {
	m.appendEntry(entry{kind: entrySystem, text: text})
}

// quoteAll quotes a run of command grants for a sentence that lists them, so
// a multi-word grant reads as one thing rather than as two.
func quoteAll(cmds []string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = strconv.Quote(c)
	}
	return out
}

// displayDir is how a granted directory is named on a card and in the status
// lines: the name a reader would recognise, not the one the tool call
// happened to carry.
//
// A path inside the workspace is shown relative to it, because that is how
// every other path in this product is written and because the absolute form
// pushes the rest of the key line off the card. One outside it keeps its
// absolute form — that it is somewhere else is the fact worth seeing — but is
// abbreviated from the left if it is long, since the tail is what identifies
// a directory and the head is what a reader already knows.
func displayDir(dir string) string {
	if dir == "." || dir == "" {
		return "./"
	}
	dir = strings.TrimSuffix(dir, string(filepath.Separator))
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return "./"
			}
			dir = rel
		}
	}
	return shortenDir(dir) + "/"
}

// maxDirDisplay is how wide a directory may be written before it is
// abbreviated. It is the width that leaves the rest of an 80-column card's
// key line intact, which is the line the name shares.
const maxDirDisplay = 28

func shortenDir(dir string) string {
	if len(dir) <= maxDirDisplay {
		return dir
	}
	parts := strings.Split(dir, string(filepath.Separator))
	for i := 1; i < len(parts); i++ {
		if tail := strings.Join(parts[i:], string(filepath.Separator)); len(tail)+2 <= maxDirDisplay {
			return "…/" + tail
		}
	}
	return "…/" + parts[len(parts)-1]
}

// approvalAction classifies an approval request for mode and classifier
// decisions. The working scope (S-141) rides along: what the action reaches
// outside it is as much a part of the decision as what kind of action it is,
// and resolving it here means every surface that asks the policy a question
// asks it with the same facts.
func (m Model) approvalAction(req *approvalRequest) agent.Action {
	a := baseAction(req)
	reach := m.scopeReachFor(req)
	a.OutOfScope = reach.dirs
	a.ScopeSensitive = reach.class == scope.Sensitive
	a.ScopeRefused = reach.class == scope.Refused
	a.ScopeReason = reach.reason
	return a
}

// baseAction is the action without the scope reading — what the request is,
// on its own terms.
func baseAction(req *approvalRequest) agent.Action {
	switch req.kind {
	case approvalExec:
		return agent.Action{
			Kind:          agent.ActionCommand,
			Command:       req.command,
			SafetyFlagged: len(safety.Check(req.command)) > 0,
		}
	case approvalDiff:
		return agent.Action{Kind: agent.ActionEdit, Path: req.path}
	}
	// A generic approval carrying a command — a process start (S-073) — is
	// judged as a command: allowlist entries apply and safety flags stick.
	if req.command != "" {
		return agent.Action{
			Kind:          agent.ActionCommand,
			Command:       req.command,
			SafetyFlagged: len(safety.Check(req.command)) > 0,
		}
	}
	return agent.Action{Kind: agent.ActionOther}
}

// policyDecision returns the mode verdict for an approval request and, when
// allowed, the reason shown in the transcript.
func (m Model) policyDecision(req *approvalRequest) (agent.Decision, string) {
	return m.modePolicy().Decide(m.approvalAction(req))
}

// modeStatus describes the active mode and cycle for /permissions with no argument.
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
	sb.WriteString("Set with /permissions <manual|accept-edits|auto|plan>; /permissions why shows the latest auto-mode denial.\n")
	sb.WriteString("/permissions grants lists what this session has stopped asking about; /permissions revoke takes it back.")
	return sb.String()
}

// policyLabel is the status bar segment for the S-054 session grants; empty
// in the default everything-prompts state.
func (m Model) policyLabel() string {
	var parts []string
	switch {
	case m.allowAllEdits:
		parts = append(parts, "edits")
	case len(m.editDirGrants) > 0:
		// A scoped grant counts what it covers rather than claiming the
		// category: "edits" and "2 dirs" are different states, and the chip
		// is the only place the difference is visible at a glance.
		parts = append(parts, plural(len(m.editDirGrants), "dir"))
	}
	switch {
	case m.allowAllCommands:
		parts = append(parts, "cmds")
	case len(m.commandGrants) > 0:
		parts = append(parts, plural(len(m.commandGrants), "cmd"))
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
	sb.WriteString("  edits:     " + status(m.allowAllEdits) + scopeSuffix(len(m.editDirGrants), "directory", "directories") + "\n")
	sb.WriteString("  commands:  " + status(m.allowAllCommands) + scopeSuffix(len(m.commandGrants), "command shape", "command shapes") + "\n")
	if n := len(m.commandAllowlist); n > 0 {
		fmt.Fprintf(&sb, "  allowlist: %d command pattern(s) from config auto-approve\n", n)
	}
	if m.grants().Any() {
		sb.WriteString("  /permissions grants names them; /permissions revoke takes them back.\n")
	}
	if m.readOnlyDisabled {
		sb.WriteString("  read-only: prompts (behavior.read_only_auto = false)\n")
	} else {
		fmt.Fprintf(&sb, "  read-only: %d inspection command(s) run without asking", len(agent.ReadOnlyCommands())+len(m.readOnlyExtra))
		sb.WriteString(" (ls, cat, grep, git status, …)\n")
	}
	if n := len(m.scopeDirs()); n > 0 {
		fmt.Fprintf(&sb, "  scope:     the session directory and %d added %s (/add-dir)\n", n, plural2(n, "directory", "directories"))
	} else if m.scope != nil {
		sb.WriteString("  scope:     the session directory; anything outside it asks (/add-dir)\n")
	}
	if m.subagents != nil {
		sb.WriteString("  sub-agents inherit this mode, these grants, and the classifier.\n")
	}
	sb.WriteString("  Safety-flagged commands, and anything outside the working scope, always ask.")
	return sb.String()
}

// scopeDirs is what the session has added to its working scope, or nothing
// when the session has no scope wired (older tests, `shhh chat` without one).
func (m Model) scopeDirs() []string {
	if m.scope == nil {
		return nil
	}
	return m.scope.Dirs()
}

// plural2 picks between two spellings for a count, where "dir"/"dirs" will
// not do because the word is being read in a sentence.
func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// allowlistMatches reports whether command's leading words exactly match all
// words of some allowlist entry ("go test" matches "go test ./..."). The
// matching lives in internal/agent so headless print mode applies the same
// policy.
func allowlistMatches(allowlist []string, command string) bool {
	return agent.AllowlistMatches(allowlist, command)
}

// grantStatus is `/permissions grants`: everything this session has stopped asking
// about, and the one line that takes it back. It names each grant in the same
// words the card used to record it, so the two can be recognised as the same
// act.
func (m Model) grantStatus() string {
	g := m.grants()
	if !g.Any() && len(m.commandAllowlist) == 0 && len(m.scopeDirs()) == 0 {
		return "Nothing is granted — every gated call asks.\n" +
			"[a] on a confirm prompt grants the one shape of call it is showing; /permissions allow <commands|edits> grants the category."
	}
	var sb strings.Builder
	sb.WriteString("Session grants:\n")
	if g.AllEdits {
		sb.WriteString("  edits      every edit, anywhere (/permissions allow edits)\n")
	}
	for _, d := range g.EditDirs {
		sb.WriteString("  edits      " + displayDir(d) + "\n")
	}
	if g.AllCommands {
		sb.WriteString("  commands   every command (/permissions allow commands)\n")
	}
	for _, c := range g.Commands {
		sb.WriteString("  commands   " + strconv.Quote(c) + "\n")
	}
	if !g.Any() {
		sb.WriteString("  (none — everything below came from config)\n")
	}
	for _, d := range m.scopeDirs() {
		sb.WriteString("  scope      " + displayDir(d) + " — in the working scope (/add-dir drop takes it back)\n")
	}
	if n := len(m.commandAllowlist); n > 0 {
		fmt.Fprintf(&sb, "  config     %d command pattern(s) from behavior.command_allowlist — not this session's to revoke\n", n)
	}
	if g.Any() {
		sb.WriteString("/permissions revoke [edits|commands] takes them back.")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// allowCommand is `/permissions allow <commands|edits>`: the blanket grant, which
// used to be one keystroke on a card. It is a command now because of what it
// is — a decision about every call the rest of the session will make, taken
// once, in front of no particular one of them.
func (m *Model) allowCommand(args []string) string {
	if len(args) != 1 {
		return "Usage: /permissions allow <commands|edits> — the blanket grants. For one shape of call, [a] on its confirm prompt."
	}
	switch args[0] {
	case "commands", "cmds":
		if m.allowAllCommands {
			return "Commands already run without asking. /permissions revoke commands takes it back."
		}
		m.allowAllCommands = true
		m.syncChildGrants()
		return "Every command will now run without asking, except the safety-flagged ones, which always ask.\n/permissions revoke commands takes it back."
	case "edits":
		if m.allowAllEdits {
			return "Edits already apply without asking. /permissions revoke edits takes it back."
		}
		m.allowAllEdits = true
		m.syncChildGrants()
		return "Every edit will now apply without asking, anywhere in the workspace.\n/permissions revoke edits takes it back."
	}
	return "Usage: /permissions allow <commands|edits>"
}

// revokeCommand is `/permissions revoke`: the way back a session grant never had.
// Until it existed, [a] pressed once on one `go test` was the last time the
// session asked about anything, and only restarting it — or plan mode, which
// refuses everything — undid that.
func (m *Model) revokeCommand(args []string) string {
	if len(args) > 1 {
		return "Usage: /permissions revoke [edits|commands]"
	}
	scope := "all"
	if len(args) == 1 {
		scope = args[0]
	}
	var gone []string
	switch scope {
	case "all":
		gone = m.revokeGrants()
	case "edits":
		if m.allowAllEdits {
			gone = append(gone, "every edit")
		}
		for _, d := range m.editDirGrants {
			gone = append(gone, "edits in "+displayDir(d))
		}
		m.allowAllEdits, m.editDirGrants = false, nil
	case "commands", "cmds":
		if m.allowAllCommands {
			gone = append(gone, "every command")
		}
		gone = append(gone, quoteAll(m.commandGrants)...)
		m.allowAllCommands, m.commandGrants = false, nil
	default:
		return "Usage: /permissions revoke [edits|commands]"
	}
	m.syncChildGrants()
	if len(gone) == 0 {
		return "Nothing was granted; everything already asks."
	}
	return "Revoked, and asking again: " + strings.Join(gone, ", ") + "."
}
