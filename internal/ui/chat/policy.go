package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/web"
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
		Conversation:     m.wiring.Conversation,
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
	_, refused := m.commandRule(req)
	return refused
}

// commandRule is the rule that refuses a request's command line, asked
// through the one function the policy asks: the deny list, then a destroying
// command pointed at something this session may not destroy. The second is
// answered here with the first for the first's reason — it is not a
// decision, so there is no card, no batch approval and no classifier round.
// See docs/capabilities/approvals-and-safety.md#some-targets-are-never-destroyed.
func (m Model) commandRule(req *approvalRequest) (string, bool) {
	if req == nil || req.command == "" || req.host != "" {
		return "", false
	}
	a := agent.Action{Command: req.command}
	if !req.write {
		a.Irreplaceable = m.irreplaceable(req)
	}
	return agent.RuleRefusal(m.policy.denylist, a)
}

// irreplaceable is what a request's command destroys that this session may
// not, in the words the refusal names it with, or "".
//
// The command is read from the directory it runs in. A process start names a
// directory of its own, which the request does not carry, so its relative
// paths prove nothing and only its absolute ones are read.
func (m Model) irreplaceable(req *approvalRequest) string {
	if req == nil || req.command == "" || req.write || req.host != "" {
		return ""
	}
	return radius.Destroys(req.command, m.destroyWhere(req)).Refusal()
}

// destroyWhere is where a request's command is read for what it destroys:
// the working scope, its root, the directory a command runs in where the
// request says, and the home directory.
func (m Model) destroyWhere(req *approvalRequest) radius.Where {
	where := radius.Where{Scope: m.wiring.Scope}
	where.Root = m.wiring.Workspace
	if m.wiring.Scope != nil {
		where.Root = m.wiring.Scope.Root()
	}
	if where.Root == "" {
		where.Root, _ = os.Getwd()
	}
	if req.kind == approvalExec {
		where.Dir = where.Root
		if m.wiring.Workspace != "" {
			where.Dir = m.wiring.Workspace
		}
	}
	where.Home, _ = os.UserHomeDir()
	return where
}

// scratchDelete reports whether a request's command is a delete of untracked
// scratch inside the workspace, read against the same place irreplaceable
// reads it: every target resolved below the workspace root, none of it
// tracked, nothing else on the line flagged. A process start names a
// directory of its own the request does not carry, so its relative paths
// prove nothing and it is scratch only where every target is absolute.
// See docs/capabilities/approvals-and-safety.md#severity-moves-the-default.
func (m Model) scratchDelete(req *approvalRequest) bool {
	if req == nil || req.command == "" || req.write || req.host != "" {
		return false
	}
	return radius.ScratchDelete(req.command, m.destroyWhere(req))
}

// ruleDenial is what a refusal by one of the rules tells the model, the
// reason the denied row carries beside it, the sentence `/permissions why`
// prints and the code the record files it under. A host and a command are
// refused by the same act and answered in the same place; what the model is
// told differs, because a refused host is refused whatever the URL and a
// retry with another path is the loop the wording exists to stop.
func (m Model) ruleDenial(req *approvalRequest) (result, reason, why, code string) {
	if req != nil && req.host != "" && agent.HostMatches(m.policy.denyHosts, req.host) {
		return agent.DeniedHostResult, agent.DenyReasonHost, denyHostWhy, observe.ReasonDenylist
	}
	if reason, ok := m.commandRule(req); ok && agent.IsIrreplaceable(reason) {
		// Filed with the safety table's refusals: it is that table's
		// destroying rows, read against where they point.
		return agent.IrreplaceableResult(reason), reason, irreplaceableWhy, observe.ReasonSafety
	}
	return agent.DenylistResult, agent.DenyReasonDenylist, denylistWhy, observe.ReasonDenylist
}

// irreplaceableWhy is the sentence `/permissions why` prints under a command
// refused for what it destroys. Unlike the model, the reader is told the way
// through, because it is theirs: a command typed after `!` is their own and
// nothing refuses it.
const irreplaceableWhy = "refused by rule: it destroys something no session may destroy, in any mode, " +
	"and nothing lifts that; run it yourself with ! if you mean it"

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
	if a.Kind == agent.ActionCommand {
		a.Irreplaceable = m.irreplaceable(req)
		a.Scratch = req.scratch
	}
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
	//
	// The host's reading rides along, read from the call's own URL by the
	// function every surface asks, so the policy and the classifier judge
	// the fetch on what the lists say as well as on where it goes.
	if req.host != "" {
		return agent.Action{Kind: agent.ActionFetch, Host: req.host, Command: req.command,
			Reading: web.ReadFetch(json.RawMessage(req.call.Arguments))}
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
	if m.wiring.Conversation {
		return conversationModeNote
	}
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
	sb.WriteString("Set with /permissions <manual|accept-edits|auto|read-only|plan>; /permissions why shows the latest auto-mode denial.\n")
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
		parts = append(parts, "commands")
	case len(live.Commands)+len(live.ExactCommands) > 0:
		parts = append(parts, plural(len(live.Commands)+len(live.ExactCommands), "command"))
	}
	if n := len(live.Hosts); n > 0 {
		parts = append(parts, plural(n, "host"))
	}
	// The role grants are counted from the session's own field rather than
	// from live grants: they have no turn-length half to fold in, which is
	// what liveGrants exists to do (policyState).
	if n := len(m.policy.roles); n > 0 {
		parts = append(parts, plural(n, "role"))
	}
	if len(m.policy.allowlist) > 0 {
		parts = append(parts, "allowlist")
	}
	if len(parts) == 0 {
		return ""
	}
	return "granted: " + strings.Join(parts, ", ")
}

// policyHelp is the approval policy as text, the way /help lays it out.
func (m Model) policyHelp() string { return helpSheet{m.policySection()}.text() }

// policySection describes the active approval policy, the last section of
// /help: a field a row, in the head column, with what it is set to beside
// it.
func (m Model) policySection() helpSection {
	status := func(on bool) string {
		if on {
			return "auto-allow (this session)"
		}
		return "ask"
	}
	s := helpSection{title: "approval policy", head: policyHeadWidth}
	row := func(head, text string) {
		r := helpRow{paras: []string{text}}
		if head != "" {
			r.head = []string{head}
		}
		s.rows = append(s.rows, r)
	}
	row("mode", fmt.Sprintf("%s (%s)", m.policy.mode, m.policy.mode.Describe()))
	live := m.liveGrants()
	row("edits", status(live.AllEdits)+scopeSuffix(len(live.EditDirs)+len(live.EditPaths), "place", "places"))
	row("commands", status(live.AllCommands)+scopeSuffix(len(live.Commands)+len(live.ExactCommands), "command shape", "command shapes"))
	if n := len(live.Hosts); n > 0 || len(m.policy.allowHosts) > 0 {
		row("hosts", fmt.Sprintf("%s fetched without asking (%d granted, %d from config)",
			plural(n+len(m.policy.allowHosts), "host"), n, len(m.policy.allowHosts)))
	}
	if n := len(m.policy.allowlist); n > 0 {
		row("allowlist", plural(n, "command pattern")+" from config auto-approve")
	}
	if n := len(m.policy.denylist); n > 0 {
		row("denylist", plural(n, "command pattern")+" from config are refused in every mode")
	}
	if live.Any() {
		row("", "/permissions grants names them; /permissions revoke takes them back")
	}
	if m.policy.readOnlyDisabled {
		row("read-only", "prompts (behavior.read_only_auto = false)")
	} else {
		row("read-only", plural(len(agent.ReadOnlyCommands())+len(m.policy.readOnlyExtra), "inspection command")+
			" run without asking (ls, cat, grep, git status, …)")
	}
	if n := len(m.scopeDirs()); n > 0 {
		row("scope", fmt.Sprintf("the session directory and %d added %s (/add-dir)", n, plural2(n, "directory", "directories")))
	} else if m.wiring.Scope != nil {
		row("scope", "the session directory; anything outside it asks (/add-dir)")
	}
	if n := len(m.writerDirs()); n > 0 {
		row("", fmt.Sprintf("%d %s inside it granted for writers (/add-dir)", n, plural2(n, "directory", "directories")))
	}
	if m.wiring.Subagents != nil {
		row("", "sub-agents inherit this mode, these grants, and the classifier")
	}
	row("", "safety-flagged commands, and anything outside the working scope, always ask")
	return s
}

// policyHeadWidth is the policy's field column: its longest name,
// `allowlist`, and the gap after it.
const policyHeadWidth = 11

// scopeDirs is what the session has added to its working scope, or nothing
// when the session has no scope wired (older tests, `shhh chat` without one).
// It is Beyond, the set the session's containment writes to: a grant of the
// checkout widens only a writer's scope and is writerDirs instead.
// See docs/capabilities/subagents.md#a-child-inherits-its-scope-not-more.
func (m Model) scopeDirs() []string {
	if m.wiring.Scope == nil {
		return nil
	}
	return m.wiring.Scope.Beyond()
}

// writerDirs are the granted directories inside the session's own checkout:
// recorded for writers, and nothing the session did not already have.
func (m Model) writerDirs() []string {
	var out []string
	for _, d := range m.wiring.Scope.Dirs() {
		if m.wiring.Scope.InRoot(d) {
			out = append(out, d)
		}
	}
	return out
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
	// secretIgnore is commit.secret_ignore, the fixtures a commit made from
	// this session may carry a credential shape in (commit.go). It is no
	// grant either: it is the checkout's word on which files are fixtures.
	secretIgnore []string
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
	// roles are the sub-agent profiles [a] has granted on a spawn card:
	// spawning one of these starts without a card. Only a role that changes
	// nothing is ever offered, so there is no blanket counterpart either —
	// "every role" would include the ones that hand back a patch, and a
	// patch is the decision the card exists for
	// (docs/capabilities/subagents.md#spawning-is-a-decision).
	//
	// They are the session's alone and have no turn-length half beside them,
	// which is why they sit here rather than in the turn's Grants: a fan-out
	// happens once in a turn, so a role granted for the turn would cover the
	// card in front of the reader and nothing after it.
	roles []string
	// Read-only inspection commands auto-run in every mode; config can
	// extend the built-in list or turn it off entirely.
	readOnlyExtra    []string
	readOnlyDisabled bool
}
