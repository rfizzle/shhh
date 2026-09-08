package agent

// Permission modes: a session-level state machine that decides how
// approval-gated tool calls are handled. Read-only tools never reach this
// layer — they auto-run in every mode — and safety-flagged commands always
// ask the human regardless of mode.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/logs"
)

// Mode is the session's permission mode.
type Mode int

const (
	// ModeManual prompts for every consequential tool call (the maximally
	// safe default). Session grants ([a] on a prompt) and the config command
	// allowlist can loosen it per category.
	ModeManual Mode = iota
	// ModeAcceptEdits auto-allows file edits; commands and other external
	// actions still prompt.
	ModeAcceptEdits
	// ModeAuto defers to policy: edits apply and allowlisted commands run;
	// anything else is judged by the LLM classifier when configured,
	// else asks. Classifier failures fall back to asking, never allowing.
	ModeAuto
	// ModePlan is read-only research: file edits and non-inspection
	// commands are refused with a result telling the model it is in plan
	// mode; shell access is restricted to the inspection allowlist.
	ModePlan
)

func (m Mode) String() string {
	switch m {
	case ModeAcceptEdits:
		return "accept-edits"
	case ModeAuto:
		return "auto"
	case ModePlan:
		return "plan"
	default:
		return "manual"
	}
}

// Describe is the one-line explanation of a mode shown by /permissions and
// /help.
func (m Mode) Describe() string {
	switch m {
	case ModeAcceptEdits:
		return "file edits apply without prompts; commands and other actions ask"
	case ModeAuto:
		return "edits apply; allowlisted commands run; the classifier judges the rest (or asks)"
	case ModePlan:
		return "read-only research — edits and non-inspection commands are refused"
	default:
		return "every consequential tool call asks"
	}
}

// ParseMode maps a config or /permissions name to its Mode.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "manual":
		return ModeManual, nil
	case "accept-edits", "accept_edits", "accept edits":
		return ModeAcceptEdits, nil
	case "auto":
		return ModeAuto, nil
	case "plan":
		return ModePlan, nil
	}
	return ModeManual, fmt.Errorf("unknown mode %q (valid: manual, accept-edits, auto, plan)", s)
}

// DefaultCycle is the Shift+Tab mode order when the config does not override
// it (behavior.mode_cycle).
func DefaultCycle() []Mode {
	return []Mode{ModeManual, ModeAcceptEdits, ModeAuto, ModePlan}
}

// ParseCycle parses behavior.mode_cycle entries; an empty list means the
// default cycle.
func ParseCycle(names []string) ([]Mode, error) {
	if len(names) == 0 {
		return nil, nil
	}
	cycle := make([]Mode, 0, len(names))
	for _, name := range names {
		mode, err := ParseMode(name)
		if err != nil {
			return nil, err
		}
		cycle = append(cycle, mode)
	}
	return cycle, nil
}

// NextMode returns the mode after current in cycle, wrapping around; a nil
// cycle uses DefaultCycle, and a current mode outside the cycle enters it at
// the start.
func NextMode(cycle []Mode, current Mode) Mode {
	if len(cycle) == 0 {
		cycle = DefaultCycle()
	}
	for i, m := range cycle {
		if m == current {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

// permissiveness ranks modes for ClampMode: plan is the most restrictive,
// auto the most permissive.
func permissiveness(m Mode) int {
	switch m {
	case ModePlan:
		return 0
	case ModeAcceptEdits:
		return 2
	case ModeAuto:
		return 3
	default: // ModeManual
		return 1
	}
}

// ClampMode caps mode at ceiling: a sub-agent can never run in a more
// permissive mode than its parent.
func ClampMode(mode, ceiling Mode) Mode {
	if permissiveness(mode) > permissiveness(ceiling) {
		return ceiling
	}
	return mode
}

// ActionKind classifies an approval-gated tool call for mode decisions.
type ActionKind int

const (
	// ActionEdit is a file write/edit.
	ActionEdit ActionKind = iota
	// ActionCommand is a shell command.
	ActionCommand
	// ActionFetch is an outbound request for one page, which carries the
	// host it leaves for. It is its own kind because the host is the unit a
	// person answers for — the twentieth page from a documentation site is
	// the same decision as the first, and no other kind's grant means
	// anything about it.
	// See docs/capabilities/approvals-and-safety.md#a-host-is-granted-once.
	ActionFetch
	// ActionOther is any other gated tool call (registered gated tools).
	ActionOther
)

// String is the kind in the word a refusal is written down under, and the
// word a person greps the log for.
func (k ActionKind) String() string {
	switch k {
	case ActionEdit:
		return "edit"
	case ActionCommand:
		return "command"
	case ActionFetch:
		return "fetch"
	}
	return "action"
}

// Action is one approval-gated tool call as the mode policy sees it.
type Action struct {
	Kind ActionKind
	// Command is the command text for ActionCommand (allowlist matching),
	// and for an action of any other kind the line the deny list is matched
	// against. A tool with a closed verb set stands for a command line —
	// the git writer's commit verb stands for `git commit` — and a person
	// who put that line on the deny list meant the act, not the spelling, so
	// the tool cannot be the way around it.
	Command string
	// Path is the file for ActionEdit, which is what a directory-scoped edit
	// grant is matched against (GrantPrefix's counterpart).
	Path string
	// Host is the host an ActionFetch leaves for, exactly as the card names
	// it. It is what a host grant and the host deny list are matched
	// against, and it carries no port and no path: the person answered for
	// the site, not for the page.
	Host string
	// SafetyFlagged marks commands flagged by safety.Check; they always ask
	// the human, in every mode except plan (which refuses them outright).
	SafetyFlagged bool
	// OutOfScope names the directories this action reaches that are outside
	// the session's working scope. It is resolved by the front-end,
	// which holds the scope; here it is one more thing that stops a
	// permissive mode answering on the user's behalf, because a mode that
	// says "edits apply" was granted over the work, not over the whole disk.
	OutOfScope []string
	// ScopeSensitive marks an out-of-scope directory that only a person may
	// grant — a home directory, a system root, another tool's credential
	// store. Like SafetyFlagged it always asks, in every mode.
	ScopeSensitive bool
	// ScopeRefused marks a path behind the containment deny mask, which no
	// grant can reach. The call is refused rather than asked about: the
	// answer would not have been honoured.
	ScopeRefused bool
	// ScopeReason is why the scope fields say what they do, in the words the
	// card and the tool result print after a dash.
	ScopeReason string
}

// Decision is a mode policy verdict for one gated tool call.
type Decision int

const (
	// Ask prompts the user (the fallback whenever nothing allows or denies).
	Ask Decision = iota
	// Allow runs the call without a prompt.
	Allow
	// Deny refuses the call with an error tool result, without prompting.
	Deny
)

// scopeRefusedReason states why a call was refused for what it reaches, in
// the words the transcript and the tool result both use.
func scopeRefusedReason(a Action) string {
	if a.ScopeReason != "" {
		return "outside the working scope — " + a.ScopeReason
	}
	return "outside the working scope, behind the containment deny mask"
}

// ScopeRefusedResult is the tool result recorded for a call refused for the
// paths it reaches, so the model learns the boundary instead of retrying it.
func ScopeRefusedResult(reason string) string {
	return "error: this path is outside the session's working scope and cannot be granted (" + reason +
		"). Work inside the session's directories, or ask the user to run /add-dir for a directory that can be granted."
}

// DenyReasonDenylist is the rule name a command refused for the deny list
// carries. It is short because it is printed on the denial row beside who
// decided, and `/permissions why` is where the key it came from is named.
const DenyReasonDenylist = "deny list"

// DenylistResult is the tool result recorded for a command the deny list
// refused, so the model spends no more rounds on a command that has no
// spelling that would run.
//
// It names no key and points at no file. The list is the user's, and a
// refusal that came with instructions for editing it would be handing the
// model the way around it.
// See docs/capabilities/approvals-and-safety.md#a-deny-list-is-answered-before-anything-can-allow.
const DenylistResult = "error: this command is on the deny list for this session. It is refused in every " +
	"permission mode and no approval can allow it, so retrying it or rephrasing it will not run it. " +
	"Say what you were trying to do and let the user decide."

// DenyReasonHost is the rule name a fetch refused for the host deny list
// carries. It is a different word from the command list's so the row says
// which of the two answered, and so the model gets the result that is true
// of it — a host is refused whatever the URL, which is not what
// DenylistResult says.
const DenyReasonHost = "host deny list"

// DeniedHostResult is the tool result recorded for a fetch the host deny
// list refused. Like DenylistResult it names no key and points at no file:
// the list is the user's, and a refusal that came with editing instructions
// would be handing the model the way around it.
// See docs/capabilities/approvals-and-safety.md#a-host-is-granted-once.
const DeniedHostResult = "error: this host is refused for this session. No URL on it will be fetched, in any " +
	"permission mode, and no approval can allow one, so another path on the same host will not work either. " +
	"Find the answer on another source, or say what you were looking for and let the user decide."

// UnattendedRefusedResult is the tool result recorded for a gated call an
// unattended run refused: a run behind --print, or a served session with no
// client to draw a card at. It carries the reason the policy reached, which
// is the half a model can act on — a call refused for what it reaches is a
// different next round from one the classifier judged unrelated.
//
// What it does not offer is a way to ask. Every other refusal here ends by
// sending the model back to the user; there is no user, and a result that
// told it to wait for one would cost the run the rest of its rounds waiting.
// See docs/capabilities/headless.md#auto-mode-fails-closed.
func UnattendedRefusedResult(what, reason string) string {
	out := "error: " + what + " not approved"
	if strings.TrimSpace(reason) != "" {
		out += ": " + strings.TrimSpace(reason)
	}
	return out + ". This run has nobody to ask; say what you were trying to do and finish with what you can."
}

// PlanModeResult is the tool result recorded for a gated call refused in
// plan mode, so the model learns why nothing ran instead of the call being
// silently dropped.
const PlanModeResult = "error: this session is in plan mode (read-only); the call was not executed. Present your plan as a message, or ask the user to switch modes (Shift+Tab or /permissions)."

// ModePolicy is the session approval-policy state: the active mode plus the
// Session-grant internals (per-category grants and the config command
// allowlist) that manual and accept-edits build on.
type ModePolicy struct {
	Mode Mode
	// AllowEdits and AllowCommands are the blanket session grants, which
	// `/permissions allow` sets: every edit, every command, until they are
	// revoked.
	AllowEdits    bool
	AllowCommands bool
	// EditDirs are the scoped edit grants [a] records on an edit card: edits
	// under these directories run without asking, and nothing else does.
	EditDirs []string
	// CommandAllowlist entries pre-approve matching commands — the config
	// list, plus whatever [a] has recorded on a command card this session.
	CommandAllowlist []string
	// CommandDenylist entries refuse matching commands outright
	// (behavior.command_denylist). It is read before the allowlist and
	// before the classifier, and nothing a session can grant reaches past
	// it: it is the answer a person gave once, for every mode, so that a
	// command they never want run is not a card they have to keep refusing.
	CommandDenylist []string
	// AllowHosts are the hosts a fetch reaches without asking: the config
	// list (web.allow_hosts) and whatever [a] has granted this session, in
	// one field because they answer the same question and the reason a row
	// carries is the same either way.
	AllowHosts []string
	// DenyHosts are hosts no fetch reaches (web.deny_hosts). Like the
	// command deny list it is read before anything that can allow, and
	// nothing a session can grant reaches past it.
	DenyHosts []string
	// ReadOnlyExtra extends the built-in read-only command allowlist
	// (behavior.read_only_commands).
	ReadOnlyExtra []string
	// ReadOnlyDisabled turns off the built-in read-only allowlist, so
	// inspection commands prompt like anything else
	// (behavior.read_only_auto = false).
	ReadOnlyDisabled bool
}

// readOnly reports whether a command auto-runs as pure inspection.
func (p ModePolicy) readOnly(a Action) bool {
	if p.ReadOnlyDisabled || a.Kind != ActionCommand || a.SafetyFlagged {
		return false
	}
	return ReadOnlyAllowed(a.Command, p.ReadOnlyExtra)
}

// ReadOnlyCommands is the built-in allowlist of inspection commands that run
// without a prompt in every mode: pure reads, nothing that can write, delete,
// or execute further commands or code. Matching goes through
// AllowlistMatches, so chained or redirected commands never qualify, and a
// safety-flagged command is never matched against it at all.
//
// Entries are deliberately conservative. Anything that compiles or runs
// project code (go build, go test, go vet, make, npm run) stays out: it
// executes the repository's own code, which is not a read.
func ReadOnlyCommands() []string {
	return []string{
		// Filesystem inspection.
		"ls", "pwd", "cat", "head", "tail", "wc", "file", "stat", "tree", "du",
		"realpath", "basename", "dirname", "readlink",
		// Search.
		"grep", "rg", "find", "fd", "which", "type",
		// Text shaping over piped-in content is out (it needs a pipe, which
		// AllowlistMatches rejects anyway); these are read-only on their own.
		"diff", "cmp", "sort", "uniq", "cut", "column",
		// Git inspection.
		"git status", "git log", "git diff", "git show", "git blame",
		"git ls-files", "git branch", "git remote -v", "git describe",
		"git rev-parse", "git shortlog", "git tag -l", "git stash list",
		// Toolchain inspection (no compilation, no execution).
		"go version", "go env", "go list", "go doc", "go mod graph", "go mod why",
		"node --version", "npm ls", "python --version", "python3 --version",
		"cargo --version", "rustc --version",
		// Environment.
		"whoami", "hostname", "uname", "date", "env", "printenv", "id",
	}
}

// PlanInspectionCommands is the read-only allowlist under its plan-mode name
// ; plan mode grants exactly the same set.
func PlanInspectionCommands() []string { return ReadOnlyCommands() }

// readOnlyGuards names the flags that turn an otherwise read-only command
// into one that writes, deletes, or executes something else. A command whose
// prefix matches a key and that carries any of its flags is not read-only,
// however innocent the rest of it looks ("find . -delete").
var readOnlyGuards = map[string][]string{
	"find":       {"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fls", "-fprint", "-fprint0", "-fprintf"},
	"fd":         {"-x", "-X", "--exec", "--exec-batch"},
	"sort":       {"-o", "--output"},
	"tree":       {"-o"},
	"git branch": {"-d", "-D", "-m", "-M", "-c", "-C", "--delete", "--move", "--copy", "--set-upstream-to", "-u", "--unset-upstream", "--edit-description"},
	// env with operands runs a command; bare env just prints the environment.
	"env": {},
}

// guardedReadOnly applies readOnlyGuards to a command already known to match
// the built-in allowlist.
func guardedReadOnly(command string) bool {
	words := strings.Fields(command)
	for prefix, banned := range readOnlyGuards {
		pattern := strings.Fields(prefix)
		if len(pattern) > len(words) {
			continue
		}
		match := true
		for i, w := range pattern {
			if words[i] != w {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		rest := words[len(pattern):]
		// A guard with no banned flags allows only the bare command.
		if len(banned) == 0 && len(rest) > 0 {
			return false
		}
		for _, w := range rest {
			for _, b := range banned {
				if w == b || strings.HasPrefix(w, b+"=") {
					return false
				}
			}
		}
	}
	return true
}

// ReadOnlyAllowed reports whether a command is a built-in read-only
// inspection command, or matches one of the caller's extra entries. Extra
// entries are the user's own call and skip the built-in flag guards.
func ReadOnlyAllowed(command string, extra []string) bool {
	if AllowlistMatches(ReadOnlyCommands(), command) && guardedReadOnly(command) {
		return true
	}
	return len(extra) > 0 && AllowlistMatches(extra, command)
}

// PlanInspectionAllowed reports whether a command is on plan mode's
// inspection allowlist.
func PlanInspectionAllowed(command string) bool {
	return ReadOnlyAllowed(command, nil)
}

// Decide returns the verdict for one gated action and, for Allow, the reason
// shown in the transcript ("session policy", "allowlist", "auto mode", …).
//
// A refusal is written to the diagnostic log on its way out, because a
// refusal is the one verdict with no surface of its own that lasts: an allow
// ran and its result is in the transcript, an ask drew a card somebody
// answered, and a deny is a tool result the model reads and the person never
// sees — which is how a run that did nothing comes to have no explanation
// anywhere by the morning.
// See docs/capabilities/configuration.md#a-failure-is-written-down.
func (p ModePolicy) Decide(a Action) (Decision, string) {
	decision, reason := p.decide(a)
	if decision == Deny {
		LogRefusal(a.Kind.String(), a.Command, reason, 0, 0)
	}
	return decision, reason
}

// LogRefusal writes the one line a refused call leaves behind: what was
// asked for, the first word of the command it stands for, the rule that
// answered, and where the run had got to. It is exported because the
// unattended surfaces refuse a call before this policy is consulted — a deny
// list, the safety table, a run with nobody to ask — and a second wording for
// the same fact is a second thing for a person grepping at 3 a.m. to know
// about.
//
// What is written is what the caller knows: an unattended run names the tool
// the model called, and the policy — which is asked about an action and not
// about a call — names the tier the action sat at. Both go in one field
// rather than two, because the reader is grepping for the refusal and a
// second field that is empty on half the lines is a column of blanks.
//
// The line is as content-free as the record is, and for the same reason: the
// file is shared between sessions and outlives all of them. The first word
// of a command is the program, which is the half that says which refusal
// this was; the rest of the line, the path an edit named and the URL a fetch
// wanted are not written down.
func LogRefusal(what, command, rule string, turn, round int64) {
	args := []any{"what", what, "rule", rule}
	// A call with no command line behind it — an edit, a fetch, a tool with
	// a closed verb set — leaves the field off rather than carrying an empty
	// one: a key with nothing under it reads as a command that was blank.
	if word := firstWord(command); word != "" {
		args = append(args, "command", word)
	}
	// A zero position is a surface that has none to give rather than the
	// first round of the first turn: the session's policy is asked about a
	// call without being told where the conversation had got to, and a line
	// claiming turn 1 would be worse than a line that says nothing.
	if turn > 0 || round > 0 {
		args = append(args, "turn", turn, "round", round)
	}
	logs.Logger().Info("call refused", args...)
}

// firstWord is the program a command line names, and nothing else. An empty
// command stays empty rather than becoming a quoted nothing.
func firstWord(command string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(command), " ", 2)[0])
}

// decide is the policy itself. Decide wraps it so that every way of reaching
// a refusal writes one line and only one — a log call at each of the returns
// below is a log call the next return added to this function forgets.
func (p ModePolicy) decide(a Action) (Decision, string) {
	// The deny list is read before anything else, including the mode: it is
	// the one answer no mode changes, and it is read first so that the
	// reason the row carries names the list rather than whichever rule
	// happened to refuse the call second. It is read off the line rather
	// than off the kind, so an action that stands for a command line is
	// answered by the same match the command path uses whatever tier it
	// sits at.
	if a.Command != "" && DenylistMatches(p.CommandDenylist, a.Command) {
		return Deny, DenyReasonDenylist
	}
	// The host deny list is read in the same breath and for the same reason:
	// a host the person has refused is not a decision, so it is answered
	// before the grant, before the mode and before the classifier is paid to
	// think about it.
	if a.Kind == ActionFetch && HostMatches(p.DenyHosts, a.Host) {
		return Deny, DenyReasonHost
	}
	if p.Mode == ModePlan {
		// Plan mode grants inspection even with the read-only allowlist
		// disabled: read-only is the whole point of the mode.
		if a.Kind == ActionCommand && !a.SafetyFlagged && ReadOnlyAllowed(a.Command, p.ReadOnlyExtra) {
			return Allow, "plan mode inspection"
		}
		return Deny, "plan mode"
	}
	if a.SafetyFlagged {
		return Ask, ""
	}
	// The working scope is checked before the mode is: a path behind
	// the deny mask is refused whatever the mode says, and a directory
	// outside the scope is a decision the session has not been given — the
	// permissive modes were granted over the work, and this is the question
	// of what the work is.
	if a.ScopeRefused {
		return Deny, scopeRefusedReason(a)
	}
	if len(a.OutOfScope) > 0 {
		return Ask, ""
	}
	// Inspection commands never prompt, in any mode: they cannot change
	// anything, and prompting for them is the bulk of the noise.
	if p.readOnly(a) {
		return Allow, "read-only"
	}
	switch a.Kind {
	case ActionEdit:
		switch {
		case p.Mode == ModeAcceptEdits || p.Mode == ModeAuto:
			return Allow, p.Mode.String() + " mode"
		case p.AllowEdits:
			return Allow, "session policy"
		case PathUnder(p.EditDirs, a.Path):
			return Allow, "session grant"
		}
	case ActionCommand:
		switch {
		case p.AllowCommands:
			return Allow, "session policy"
		case AllowlistMatches(p.CommandAllowlist, a.Command):
			return Allow, "allowlist"
		}
	case ActionFetch:
		// A granted host is allowed here, before auto mode's classifier is
		// asked: the classifier's job is to judge whether a URL is an
		// outbound channel worth stopping for, and the grant is the person
		// having already said this host is not.
		if HostMatches(p.AllowHosts, a.Host) {
			return Allow, "session grant"
		}
	}
	return Ask, ""
}
