package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/scope"
)

// Session approval policy: the permission mode decides how each
// approval-gated tool call is handled — manual prompts for everything,
// accept-edits auto-allows file edits, auto defers to policy (allowlist
// rules, then the LLM classifier), and plan is read-only. The session-grant
// internals still apply inside the prompting modes: [a] on a confirm prompt
// offers the grants that call can make — for the turn or for the session,
// over the pattern the card printed or over the thing exactly as it stands —
// and a config allowlist (behavior.command_allowlist) pre-approves specific
// commands.
// Commands flagged by safety.Check always prompt, in every mode except plan
// (which refuses them like everything else).

// WithCommandAllowlist sets the config-provided command allowlist: commands
// whose leading words match an entry run without an approval prompt, unless
// safety-flagged.
func (m Model) WithCommandAllowlist(list []string) Model {
	m.policy.allowlist = list
	return m
}

// WithCommandDenylist sets the config-provided command deny list
// (behavior.command_denylist): commands whose leading words match an entry
// are refused before a card is drawn, in every mode.
func (m Model) WithCommandDenylist(list []string) Model {
	m.policy.denylist = list
	return m
}

// WithHostRules sets the config-provided host lists (web.allow_hosts,
// web.deny_hosts): a fetch to an allowed host runs without a card, and a
// fetch to a denied one is refused before a card is drawn, in every mode.
func (m Model) WithHostRules(allow, deny []string) Model {
	m.policy.allowHosts = allow
	m.policy.denyHosts = deny
	return m
}

// WithHostGrants installs the sink the session's reachable hosts are pushed
// to whenever they change. The fetcher is what takes them, and it is what
// answers a redirect: a hop that starts on a granted host and ends on an
// ungranted one is a decision nobody made, and the fetcher is the only place
// that hop is visible.
// See docs/capabilities/approvals-and-safety.md#a-host-is-granted-once.
func (m Model) WithHostGrants(sink func([]string)) Model {
	m.hostGrants = sink
	return m
}

// WithCommandTimeout bounds how long one assistant-run command may take.
// Zero or less removes the ceiling.
//
// A command the reader typed is never bounded by it, here or anywhere: they
// are in front of the session and chose to run the thing, so the key that
// cancels it is the ceiling.
// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.
func (m Model) WithCommandTimeout(d time.Duration) Model {
	m.policy.timeout = d
	return m
}

// WithReadOnlyCommands configures the read-only inspection allowlist: extra
// entries beyond the built-in list, and whether the built-in list auto-runs
// at all (behavior.read_only_commands / behavior.read_only_auto).
func (m Model) WithReadOnlyCommands(extra []string, disabled bool) Model {
	m.policy.readOnlyExtra = extra
	m.policy.readOnlyDisabled = disabled
	return m
}

// WithApprovalMode sets the session's starting permission mode and the
// Shift+Tab cycle order; an empty cycle keeps the default order.
func (m Model) WithApprovalMode(mode agent.Mode, cycle []agent.Mode) Model {
	m.policy.mode = mode
	if len(cycle) > 0 {
		m.policy.cycle = cycle
	}
	return m
}

// modePolicy assembles the agent-level policy state the mode machine decides
// with. The session's own command grants join the config allowlist, because
// they are the same kind of thing — leading words that pre-approve a command
// — and the only difference is that one of them can be revoked.
func (m Model) modePolicy() agent.ModePolicy {
	return agent.ModePolicy{
		Mode:             m.policy.mode,
		AllowEdits:       m.policy.allEdits,
		AllowCommands:    m.policy.allCommands,
		EditDirs:         m.policy.editDirs,
		EditPaths:        m.policy.editPaths,
		ExactCommands:    m.policy.exactCommands,
		TurnGrants:       m.policy.turn,
		CommandAllowlist: m.allowlist(),
		CommandDenylist:  m.policy.denylist,
		AllowHosts:       m.hostAllowlist(),
		DenyHosts:        m.policy.denyHosts,
		ReadOnlyExtra:    m.policy.readOnlyExtra,
		ReadOnlyDisabled: m.policy.readOnlyDisabled,
	}
}

// allowlist is the config's command allowlist and the session's own, in that
// order. It allocates only where the session has added something, so the
// common case hands the config slice straight through.
func (m Model) allowlist() []string {
	if len(m.policy.commands) == 0 {
		return m.policy.allowlist
	}
	out := make([]string, 0, len(m.policy.allowlist)+len(m.policy.commands))
	return append(append(out, m.policy.allowlist...), m.policy.commands...)
}

// hostAllowlist is the config's allowed hosts and the session's own grants,
// in that order and by the same rule the command allowlist follows: it
// allocates only where the session has granted something.
func (m Model) hostAllowlist() []string {
	if len(m.policy.hosts) == 0 {
		return m.policy.allowHosts
	}
	out := make([]string, 0, len(m.policy.allowHosts)+len(m.policy.hosts))
	return append(append(out, m.policy.allowHosts...), m.policy.hosts...)
}

// deniedByRule reports whether the deny list answers this request, which is
// asked before a card is built rather than after: a command the user has
// refused in advance is not a decision, so there is nothing to draw, nothing
// to batch-approve and nothing to send to the classifier.
//
// Two lists answer here, because they are the same act: the command list is
// asked of the actions that run a command — execute_command and a process
// start — and the host list of the one action that leaves the machine. An
// edit is a different question and neither list answers it.
// See docs/capabilities/approvals-and-safety.md#a-deny-list-is-answered-before-anything-can-allow.
func (m Model) deniedByRule(req *approvalRequest) bool {
	if req == nil {
		return false
	}
	if req.host != "" && agent.HostMatches(m.policy.denyHosts, req.host) {
		return true
	}
	return req.command != "" && agent.DenylistMatches(m.policy.denylist, req.command)
}

// ruleDenial is what a refusal by one of the two lists tells the model, and
// the reason the denied row carries beside it. A host and a command are
// refused by the same act and answered in the same place; what the model is
// told differs, because a refused host is refused whatever the URL and a
// retry with another path is the loop the wording exists to stop.
func (m Model) ruleDenial(req *approvalRequest) (result, reason, why string) {
	if req != nil && req.host != "" && agent.HostMatches(m.policy.denyHosts, req.host) {
		return agent.DeniedHostResult, agent.DenyReasonHost, denyHostWhy
	}
	return agent.DenylistResult, agent.DenyReasonDenylist, denylistWhy
}

// denylistWhy is the sentence `/permissions why` prints under a deny-list
// refusal. "A rule said no" is only actionable when the reader is told which
// rule, so it names the list and the key it is written in — the reader is the
// one person who can edit it, and the model is deliberately told neither.
const denylistWhy = "refused by the command deny list (behavior.command_denylist), " +
	"which is read before the allowlist and before the classifier"

// denyHostWhy is the sentence `/permissions why` prints under a refused
// fetch, in denylistWhy's shape and for its reason: the reader is the one
// person who can edit the list, and the model is deliberately told neither
// the key nor the file.
const denyHostWhy = "refused by the host deny list (web.deny_hosts), " +
	"which is read before a grant, before the mode and before the classifier"

// grants is the session's grants as one value, for the surfaces that carry
// all of them: /permissions revoke and the status listing. The turn's own are
// deliberately not in it — what they answer is the same, but how long they
// answer it for is not, and every reader of this value has to say so.
func (m Model) grants() agent.Grants {
	return agent.Grants{
		AllEdits:      m.policy.allEdits,
		AllCommands:   m.policy.allCommands,
		EditDirs:      m.policy.editDirs,
		Commands:      m.policy.commands,
		EditPaths:     m.policy.editPaths,
		ExactCommands: m.policy.exactCommands,
		Hosts:         m.policy.hosts,
	}
}

// liveGrants is every grant standing right now, the turn's and the session's
// in one value, for the surfaces that decide on a grant's strength rather
// than on how long it lasts: a child under this session, and the fetcher that
// answers a redirect. They lose a turn grant when the parent pushes its
// grants again at the turn's close, which is the same seam that expires it
// here (subagents.go, close.go).
func (m Model) liveGrants() agent.Grants {
	g, t := m.grants(), m.policy.turn
	if !t.Any() {
		return g
	}
	g.AllEdits, g.AllCommands = g.AllEdits || t.AllEdits, g.AllCommands || t.AllCommands
	g.EditDirs = concatGrants(g.EditDirs, t.EditDirs)
	g.Commands = concatGrants(g.Commands, t.Commands)
	g.EditPaths = concatGrants(g.EditPaths, t.EditPaths)
	g.ExactCommands = concatGrants(g.ExactCommands, t.ExactCommands)
	g.Hosts = concatGrants(g.Hosts, t.Hosts)
	return g
}

// concatGrants joins two grant lists without writing into either: the
// session's own slice is handed out by grants(), and appending to it in place
// would extend the session's grants with the turn's the first time the
// capacity allowed it.
func concatGrants(session, turn []string) []string {
	if len(turn) == 0 {
		return session
	}
	out := make([]string, 0, len(session)+len(turn))
	return append(append(out, session...), turn...)
}

// grantHost records a fetch grant of the length the reader chose: the host
// the card named, exactly. A host already reachable — from the config list or
// an earlier grant — adds nothing, so choosing the same row twice on the same
// site records it once.
//
// It takes no narrow width because a host grant is already the narrowest
// there is: this host and not its parent domain, and not a sibling under it
// (docs/capabilities/approvals-and-safety.md#a-host-is-granted-once).
func (m *Model) grantHost(host string, o grantOffer) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if o.length == forThisTurn {
		if !agent.HostMatches(m.hostAllowlist(), host) && !m.policy.turn.CoversHost(host) {
			m.policy.turn.Hosts = append(m.policy.turn.Hosts, host)
		}
		return host
	}
	// A host the turn already reaches is still recorded for the session:
	// this grant is longer than that one, and skipping it would take the
	// length the reader chose away because a shorter grant happened to
	// cover the same host today.
	if !agent.HostMatches(m.hostAllowlist(), host) {
		m.policy.hosts = append(m.policy.hosts, host)
	}
	return host
}

// grantCommand records a command grant of the length and width the reader
// chose: the command's leading words, which pre-approve the shape of it
// rather than every command there is, or the line exactly as it stands. A
// pattern already covered adds nothing, so choosing the same row twice on the
// same shape of command records it once.
//
// It reports what was granted in the words the row printed, which is what the
// transcript then keeps — quoted for the prefix and the exact line, because a
// multi-word grant read unquoted in a sentence is two grants.
func (m *Model) grantCommand(command string, o grantOffer) string {
	if o.exact {
		line := strings.TrimSpace(command)
		if line == "" {
			return ""
		}
		if o.length == forThisTurn {
			if !m.commandGranted(line) && !m.policy.turn.CoversCommand(line) {
				m.policy.turn.ExactCommands = append(m.policy.turn.ExactCommands, line)
			}
			return strconv.Quote(line)
		}
		if !m.commandGranted(line) {
			m.policy.exactCommands = append(m.policy.exactCommands, line)
		}
		return strconv.Quote(line)
	}
	prefix := agent.GrantPrefix(command)
	if prefix == "" {
		return ""
	}
	if o.length == forThisTurn {
		if !m.commandGranted(prefix) && !m.policy.turn.CoversCommand(prefix) {
			m.policy.turn.Commands = append(m.policy.turn.Commands, prefix)
		}
		return strconv.Quote(prefix)
	}
	if !m.commandGranted(prefix) {
		m.policy.commands = append(m.policy.commands, prefix)
	}
	return strconv.Quote(prefix)
}

// commandGranted reports whether the session already runs this line without
// asking — by config, by a prefix grant, or by an exact one. It is the one
// question every command grant asks before recording itself, so the two
// widths cannot disagree about what is already covered.
//
// It is the session's and not the turn's, and the asymmetry is the point. A
// grant the session already covers is nothing to record at either length. A
// grant the *turn* covers still records at session length, because that grant
// is the longer of the two and dropping it would take away the length the
// reader chose. So the turn's recorders ask both sets and the session's ask
// only this one.
func (m Model) commandGranted(command string) bool {
	return agent.AllowlistMatches(m.allowlist(), command) ||
		agent.ExactMatches(m.policy.exactCommands, command)
}

// grantEdit records an edit grant of the length and width the reader chose:
// the directory the file lives in, which is the scope a reader approving a
// file in it has actually looked at, or that one file alone.
func (m *Model) grantEdit(path string, o grantOffer) string {
	if o.exact {
		file := strings.TrimSpace(path)
		if file == "" {
			return ""
		}
		if o.length == forThisTurn {
			if !m.editGranted(file) && !m.policy.turn.CoversEdit(file) {
				m.policy.turn.EditPaths = append(m.policy.turn.EditPaths, file)
			}
			return file
		}
		if !m.editGranted(file) {
			m.policy.editPaths = append(m.policy.editPaths, file)
		}
		return file
	}
	dir := filepath.Dir(path)
	if dir == "" {
		return ""
	}
	// A directory is tested by a path inside it, because the grant covers
	// what is under a directory rather than the directory itself.
	inside := filepath.Join(dir, "x")
	if o.length == forThisTurn {
		if !m.editGranted(inside) && !m.policy.turn.CoversEdit(inside) {
			m.policy.turn.EditDirs = append(m.policy.turn.EditDirs, dir)
		}
		return displayDir(dir)
	}
	if !agent.PathUnder(m.policy.editDirs, inside) {
		m.policy.editDirs = append(m.policy.editDirs, dir)
	}
	return displayDir(dir)
}

// editGranted reports whether this file already applies without asking, by a
// directory grant or by a grant of the file itself. It reads the session's
// grants alone, for the reason commandGranted does.
func (m Model) editGranted(path string) bool {
	return agent.PathUnder(m.policy.editDirs, path) || agent.PathIs(m.policy.editPaths, path)
}

// revokeGrants drops every grant this session has made — the standing ones
// and the ones that were going to end with the turn — and reports what went,
// in the order the status lines name them. A turn grant goes with the rest
// because /permissions revoke is the way back for every grant, and one that
// survived it on the grounds that it was going to expire anyway would be a
// grant the reader could not take back. Config's own allowlist is untouched:
// it is not this session's to take back.
func (m *Model) revokeGrants() []string {
	var gone []string
	if m.policy.allEdits {
		gone = append(gone, "every edit")
	}
	if m.policy.allCommands {
		gone = append(gone, "every command")
	}
	for _, d := range m.policy.editDirs {
		gone = append(gone, "edits in "+displayDir(d))
	}
	for _, p := range m.policy.editPaths {
		gone = append(gone, "edits to "+p)
	}
	gone = append(gone, quoteAll(m.policy.commands)...)
	gone = append(gone, quoteAll(m.policy.exactCommands)...)
	for _, h := range m.policy.hosts {
		gone = append(gone, "fetches from "+h)
	}
	m.policy.allEdits, m.policy.allCommands = false, false
	m.policy.editDirs, m.policy.commands, m.policy.hosts = nil, nil, nil
	m.policy.editPaths, m.policy.exactCommands = nil, nil
	return append(gone, m.revokeTurnGrants()...)
}

// revokeTurnGrants drops what this turn granted and names it, for the two
// callers that take a grant back on purpose. The expiry at the turn's close
// is the other route in and says nothing (expireTurnGrants).
func (m *Model) revokeTurnGrants() []string {
	t := m.policy.turn
	if !t.Any() {
		return nil
	}
	var gone []string
	for _, d := range t.EditDirs {
		gone = append(gone, "edits in "+displayDir(d)+" this turn")
	}
	for _, p := range t.EditPaths {
		gone = append(gone, "edits to "+p+" this turn")
	}
	for _, c := range append(append([]string(nil), t.Commands...), t.ExactCommands...) {
		gone = append(gone, strconv.Quote(c)+" this turn")
	}
	for _, h := range t.Hosts {
		gone = append(gone, "fetches from "+h+" this turn")
	}
	m.policy.turn = agent.Grants{}
	return gone
}

// expireTurnGrants ends every grant made for the length of this turn.
//
// It says nothing. The grant named its end when it was made — that is the
// whole point of the list the key opens — so a row announcing the expiry
// would be the session telling the reader something they were told at the
// moment they chose it, in the middle of the block that closes the turn.
// See docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends.
func (m *Model) expireTurnGrants() {
	if !m.policy.turn.Any() {
		return
	}
	m.policy.turn = agent.Grants{}
	// The children hold their own copy, so the expiry has to reach them the
	// way the grant did (subagents.go).
	m.syncGrants()
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
// decisions. The working scope rides along: what the action reaches
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
	// A generic approval that said it sits at the write tier is judged as an
	// edit, and carries its deny line so a person's refusal of the command
	// spelling still answers first. It is read before the command fallback
	// below: the line is what the deny list matches, not what the tier is.
	if req.write {
		return agent.Action{Kind: agent.ActionEdit, Path: req.path, Command: req.command}
	}
	// A generic approval that named a host is a fetch: the host is what the
	// two host lists and a session grant are matched against, and it is read
	// before the command fallback for the reason the write tier is — what
	// the call is, not what tier it sits at, decides which rule answers it.
	if req.host != "" {
		return agent.Action{Kind: agent.ActionFetch, Host: req.host, Command: req.command}
	}
	// A generic approval carrying a command — a process start — is
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

// modeStatus describes the active mode and cycle for /permissions with no
// argument.
func (m Model) modeStatus() string {
	cycle := m.policy.cycle
	if len(cycle) == 0 {
		cycle = agent.DefaultCycle()
	}
	names := make([]string, len(cycle))
	for i, mode := range cycle {
		names[i] = mode.String()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Mode: %s — %s.\n", m.policy.mode, m.policy.mode.Describe())
	sb.WriteString("Cycle (Shift+Tab): " + strings.Join(names, " → ") + "\n")
	sb.WriteString("Set with /permissions <manual|accept-edits|auto|plan>; /permissions why shows the latest auto-mode denial.\n")
	sb.WriteString("/permissions grants lists what this session has stopped asking about; /permissions revoke takes it back.")
	return sb.String()
}

// policyLabel is the status bar segment for the session grants; empty
// in the default everything-prompts state.
// The turn's grants are counted here with the session's rather than in a
// segment of their own. The chip is read at a glance and answers one question
// — is anything running without asking right now — and a grant that ends with
// the turn is running without asking right now. When it ends it leaves the
// count, which is the chip saying the same thing again.
func (m Model) policyLabel() string {
	var parts []string
	live := m.liveGrants()
	switch {
	case live.AllEdits:
		parts = append(parts, "edits")
	default:
		// A scoped grant counts what it covers rather than claiming the
		// category: "edits" and "2 dirs" are different states, and the chip
		// is the only place the difference is visible at a glance. A file and
		// a directory are counted apart for the same reason — the narrow
		// grant is the one whose whole point is that it is not the directory.
		if n := len(live.EditDirs); n > 0 {
			parts = append(parts, plural(n, "dir"))
		}
		if n := len(live.EditPaths); n > 0 {
			parts = append(parts, plural(n, "file"))
		}
	}
	switch {
	case live.AllCommands:
		parts = append(parts, "cmds")
	case len(live.Commands)+len(live.ExactCommands) > 0:
		parts = append(parts, plural(len(live.Commands)+len(live.ExactCommands), "cmd"))
	}
	if n := len(live.Hosts); n > 0 {
		parts = append(parts, plural(n, "host"))
	}
	if len(m.policy.allowlist) > 0 {
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
	fmt.Fprintf(&sb, "  mode:      %s (%s)\n", m.policy.mode, m.policy.mode.Describe())
	live := m.liveGrants()
	sb.WriteString("  edits:     " + status(live.AllEdits) + scopeSuffix(len(live.EditDirs)+len(live.EditPaths), "place", "places") + "\n")
	sb.WriteString("  commands:  " + status(live.AllCommands) + scopeSuffix(len(live.Commands)+len(live.ExactCommands), "command shape", "command shapes") + "\n")
	if n := len(live.Hosts); n > 0 || len(m.policy.allowHosts) > 0 {
		fmt.Fprintf(&sb, "  hosts:     %s fetched without asking (%d granted, %d from config)\n",
			plural(n+len(m.policy.allowHosts), "host"), n, len(m.policy.allowHosts))
	}
	if n := len(m.policy.allowlist); n > 0 {
		fmt.Fprintf(&sb, "  allowlist: %d command pattern(s) from config auto-approve\n", n)
	}
	if n := len(m.policy.denylist); n > 0 {
		fmt.Fprintf(&sb, "  denylist:  %d command pattern(s) from config are refused in every mode\n", n)
	}
	if live.Any() {
		sb.WriteString("  /permissions grants names them; /permissions revoke takes them back.\n")
	}
	if m.policy.readOnlyDisabled {
		sb.WriteString("  read-only: prompts (behavior.read_only_auto = false)\n")
	} else {
		fmt.Fprintf(&sb, "  read-only: %d inspection command(s) run without asking", len(agent.ReadOnlyCommands())+len(m.policy.readOnlyExtra))
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

// grantStatus is `/permissions grants`: everything this session has stopped
// asking about, and the one line that takes it back. It names each grant in
// the same words the card used to record it, so the two can be recognised as
// the same act.
func (m Model) grantStatus() string {
	g := m.grants()
	if !g.Any() && !m.policy.turn.Any() && len(m.policy.allowlist) == 0 && len(m.policy.denylist) == 0 &&
		len(m.policy.allowHosts) == 0 && len(m.policy.denyHosts) == 0 && len(m.scopeDirs()) == 0 {
		return "Nothing is granted — every gated call asks.\n" +
			"[a] on a confirm prompt offers the grants that call can make, each with when it ends; /permissions allow <commands|edits> grants the category."
	}
	var sb strings.Builder
	sb.WriteString("Grants:\n")
	if g.AllEdits {
		sb.WriteString("  edits      every edit, anywhere (/permissions allow edits) — " + endsWithSession + "\n")
	}
	if g.AllCommands {
		sb.WriteString("  commands   every command (/permissions allow commands) — " + endsWithSession + "\n")
	}
	// Every grant is listed with when it ends, in the words the row that made
	// it printed: a listing that named a grant differently from the card
	// would read as a second grant rather than as the same one, and a grant
	// whose end is not stated is one the reader has to remember (grant.go).
	writeGrants(&sb, g, endsWithSession)
	writeGrants(&sb, m.policy.turn, endsWithTurn)
	if !g.Any() && !m.policy.turn.Any() {
		sb.WriteString("  (none — everything below came from config)\n")
	}
	for _, d := range m.scopeDirs() {
		sb.WriteString("  scope      " + displayDir(d) + " — in the working scope (/add-dir drop takes it back)\n")
	}
	if n := len(m.policy.allowlist); n > 0 {
		fmt.Fprintf(&sb, "  config     %d command pattern(s) from behavior.command_allowlist — not this session's to revoke\n", n)
	}
	if n := len(m.policy.denylist); n > 0 {
		fmt.Fprintf(&sb, "  config     %d command pattern(s) from behavior.command_denylist — refused before anything here can allow them\n", n)
	}
	for _, h := range m.policy.allowHosts {
		sb.WriteString("  config     " + h + " from web.allow_hosts — not this session's to revoke\n")
	}
	if n := len(m.policy.denyHosts); n > 0 {
		fmt.Fprintf(&sb, "  config     %s from web.deny_hosts — refused before anything here can allow them\n", plural(n, "host"))
	}
	if g.Any() {
		sb.WriteString("/permissions revoke [edits|commands|hosts] takes them back.")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// writeGrants lists one set of scoped grants, each row ending in the words
// its own end was granted under. It is one function over both sets rather
// than a loop per kind inside the listing because every one of these rows
// says the same thing about when it ends, and a kind that acquired a
// different phrase for it would be a second answer to the one question this
// listing exists to answer.
//
// The blanket grants are not here: they have no pattern to print and a
// sentence of their own naming the command that set them.
func writeGrants(sb *strings.Builder, g agent.Grants, ends string) {
	end := " — " + ends + "\n"
	for _, d := range g.EditDirs {
		sb.WriteString("  edits      " + displayDir(d) + end)
	}
	for _, p := range g.EditPaths {
		sb.WriteString("  edits      " + p + ", that file alone" + end)
	}
	for _, c := range g.Commands {
		sb.WriteString("  commands   " + strconv.Quote(c) + end)
	}
	for _, c := range g.ExactCommands {
		sb.WriteString("  commands   " + strconv.Quote(c) + ", that line alone" + end)
	}
	for _, h := range g.Hosts {
		sb.WriteString("  hosts      " + h + ", that host alone" + end)
	}
}

// allowCommand is `/permissions allow <commands|edits>`: the blanket grant,
// which used to be one keystroke on a card. It is a command now because of
// what it is — a decision about every call the rest of the session will make,
// taken once, in front of no particular one of them.
func (m *Model) allowCommand(args []string) string {
	if len(args) != 1 {
		return "Usage: /permissions allow <commands|edits> — the blanket grants. For one shape of call, [a] on its confirm prompt."
	}
	switch args[0] {
	case "commands", "cmds":
		if m.policy.allCommands {
			return "Commands already run without asking. /permissions revoke commands takes it back."
		}
		m.policy.allCommands = true
		m.syncGrants()
		return "Every command will now run without asking, except the safety-flagged ones, which always ask.\n/permissions revoke commands takes it back."
	case "edits":
		if m.policy.allEdits {
			return "Edits already apply without asking. /permissions revoke edits takes it back."
		}
		m.policy.allEdits = true
		m.syncGrants()
		return "Every edit will now apply without asking, anywhere in the workspace.\n/permissions revoke edits takes it back."
	}
	return "Usage: /permissions allow <commands|edits>"
}

// revokeCommand is `/permissions revoke`: the way back a session grant never
// had. Until it existed, [a] pressed once on one `go test` was the last time
// the session asked about anything, and only restarting it — or plan mode,
// which refuses everything — undid that.
func (m *Model) revokeCommand(args []string) string {
	if len(args) > 1 {
		return "Usage: /permissions revoke [edits|commands|hosts]"
	}
	scope := "all"
	if len(args) == 1 {
		scope = args[0]
	}
	var gone []string
	switch scope {
	case "all":
		gone = m.revokeGrants()
	// Each of the three takes the turn's grants of that kind with the
	// session's: what the reader named is a kind, and a grant of that kind
	// left standing because it was going to expire on its own is a grant the
	// revoke they typed did not take back.
	case "edits":
		if m.policy.allEdits {
			gone = append(gone, "every edit")
		}
		for _, d := range m.policy.editDirs {
			gone = append(gone, "edits in "+displayDir(d))
		}
		for _, p := range m.policy.editPaths {
			gone = append(gone, "edits to "+p)
		}
		m.policy.allEdits, m.policy.editDirs, m.policy.editPaths = false, nil, nil
		for _, d := range m.policy.turn.EditDirs {
			gone = append(gone, "edits in "+displayDir(d)+" this turn")
		}
		for _, p := range m.policy.turn.EditPaths {
			gone = append(gone, "edits to "+p+" this turn")
		}
		m.policy.turn.AllEdits, m.policy.turn.EditDirs, m.policy.turn.EditPaths = false, nil, nil
	case "commands", "cmds":
		if m.policy.allCommands {
			gone = append(gone, "every command")
		}
		gone = append(gone, quoteAll(m.policy.commands)...)
		gone = append(gone, quoteAll(m.policy.exactCommands)...)
		m.policy.allCommands, m.policy.commands, m.policy.exactCommands = false, nil, nil
		for _, c := range append(append([]string(nil), m.policy.turn.Commands...), m.policy.turn.ExactCommands...) {
			gone = append(gone, strconv.Quote(c)+" this turn")
		}
		m.policy.turn.AllCommands, m.policy.turn.Commands, m.policy.turn.ExactCommands = false, nil, nil
	case "hosts", "host":
		for _, h := range m.policy.hosts {
			gone = append(gone, "fetches from "+h)
		}
		m.policy.hosts = nil
		for _, h := range m.policy.turn.Hosts {
			gone = append(gone, "fetches from "+h+" this turn")
		}
		m.policy.turn.Hosts = nil
	default:
		return "Usage: /permissions revoke [edits|commands|hosts]"
	}
	m.syncGrants()
	if len(gone) == 0 {
		return "Nothing was granted; everything already asks."
	}
	return "Revoked, and asking again: " + strings.Join(gone, ", ") + "."
}

// policyState is what the session has stopped asking about, and the mode that
// frames it. allowlist comes from config; everything below it is what [a] and
// /permissions allow have granted this session. The zero value is manual mode
// with nothing granted, which is the default: everything prompts.
//
// It is one struct because a grant is only ever read against the mode it was
// made under — a directory edits are allowed in says nothing on its own — and
// because /permissions revoke has to be able to clear the whole of it.
type policyState struct {
	mode      agent.Mode
	cycle     []agent.Mode
	allowlist []string
	// denylist is the config's refusals. It is not a grant and nothing in
	// this struct can revoke it: it sits here because it is read against the
	// mode in the same breath the allowlist is, and a reader looking for
	// what answers a command should find both in one place.
	denylist []string
	// allowHosts and denyHosts are the config's two host lists
	// (web.allow_hosts, web.deny_hosts), which stand to a fetch as the two
	// command lists stand to a command.
	allowHosts []string
	denyHosts  []string
	// timeout bounds one assistant-run command; zero means no ceiling.
	timeout time.Duration
	// The blanket grants: every edit, every command, until revoked. They are
	// what /permissions allow sets, and nothing else — [a] used to set them,
	// which made one keystroke on one `go test` the last time the session
	// asked about anything.
	allEdits    bool
	allCommands bool
	// The scoped grants [a] records instead: directories edits are allowed
	// under, and allowlist entries in agent.GrantPrefix's shape. /permissions
	// revoke clears all four, which is the way back that a session grant
	// never had.
	editDirs []string
	commands []string
	// The narrow widths the always-allow list offers beside those two: this
	// one file, and this one command line exactly as it stands.
	editPaths     []string
	exactCommands []string
	// turn is every grant made for the length of the open turn, which is a
	// value of its own beside the fields above rather than five more fields
	// beside them: it is read before all of them and cleared whole at the
	// turn's close, and a grant that outlived its turn because one of five
	// resets was forgotten is the failure the whole thing exists to prevent
	// (grant.go, close.go).
	// See docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends.
	turn agent.Grants
	// hosts are the hosts [a] has granted on a fetch card, exact and never a
	// suffix. There is no blanket counterpart: "every host" is the whole of
	// the outbound channel.
	hosts []string
	// Read-only inspection commands auto-run in every mode; config can
	// extend the built-in list or turn it off entirely.
	readOnlyExtra    []string
	readOnlyDisabled bool
}
